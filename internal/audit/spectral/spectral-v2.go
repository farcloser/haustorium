//nolint:staticcheck // too dumb with Db
package spectral

import (
	"io"
	"math"

	"gonum.org/v1/gonum/dsp/fourier"

	"github.com/farcloser/haustorium/internal/types"
)

// AnalyzeV2 adds temporal variance analysis to reduce false positives for hum
// and noise floor detection on legitimately dark or bass-heavy recordings.
func AnalyzeV2(reader io.Reader, format types.PCMFormat, opts Options) (*types.SpectralResult, error) {
	if opts.FFTSize == 0 {
		opts.FFTSize = 8192
	}

	if opts.WindowsMax == 0 {
		opts.WindowsMax = 100
	}

	fftSize := opts.FFTSize

	// Phase 1: Read entire stream into mono-mixed samples.
	samples, err := readMonoMixed(reader, format)
	if err != nil {
		return nil, err
	}

	totalFrames := uint64(len(samples))

	if len(samples) < fftSize {
		return &types.SpectralResult{
			ClaimedRate: format.SampleRate,
			Frames:      totalFrames,
		}, nil
	}

	// Phase 2: Compute evenly spaced window positions.
	positions := windowPositions(len(samples), fftSize, opts.WindowsMax)

	if len(positions) == 0 {
		return &types.SpectralResult{
			ClaimedRate: format.SampleRate,
			Frames:      totalFrames,
		}, nil
	}

	// Phase 3: Process FFT windows, keeping per-window data for variance analysis.
	window := makeHannWindow(fftSize)
	binCount := fftSize/2 + 1
	magnitudeSum := make([]float64, binCount)
	fft := fourier.NewFFT(fftSize)
	fftIn := make([]float64, fftSize)

	// Per-window storage for variance analysis.
	windowMagnitudes := make([][]float64, len(positions))
	windowRMS := make([]float64, len(positions)) // overall RMS per window for quiet detection

	for windowIdx, pos := range positions {
		var rmsSum float64

		for i := range fftSize {
			fftIn[i] = samples[pos+i] * window[i]
			rmsSum += samples[pos+i] * samples[pos+i]
		}

		windowRMS[windowIdx] = math.Sqrt(rmsSum / float64(fftSize))

		coeffs := fft.Coefficients(nil, fftIn)

		windowMagnitudes[windowIdx] = make([]float64, binCount)

		for i, c := range coeffs {
			mag := math.Sqrt(real(c)*real(c) + imag(c)*imag(c))
			windowMagnitudes[windowIdx][i] = mag
			magnitudeSum[i] += mag
		}
	}

	windowsProcessed := len(positions)

	// Average magnitude spectrum.
	avgMagnitude := make([]float64, binCount)
	for i := range avgMagnitude {
		avgMagnitude[i] = magnitudeSum[i] / float64(windowsProcessed)
	}

	binHz := float64(format.SampleRate) / float64(fftSize)
	nyquist := float64(format.SampleRate) / 2

	magDb := toDb(avgMagnitude)

	// Reference level: 1-10 kHz average.
	refLevel := bandAverage(magDb, 1000, 10000, binHz)

	result := &types.SpectralResult{
		ClaimedRate: format.SampleRate,
		Frames:      totalFrames,
	}

	// === Sample rate authenticity ===
	if format.SampleRate > 44100 {
		detectUpsampling(result, magDb, binHz, nyquist, refLevel)
	}

	// === Lossy transcode detection V2 (with consistency analysis) ===
	detectTranscodeV2(result, windowMagnitudes, magDb, binHz, nyquist, refLevel)

	// === Hum detection V2 (with variance) ===
	detectHumV2(result, windowMagnitudes, binHz, refLevel)

	// === Noise floor V2 (quiet-window HF + full-track reference + RMS gate) ===
	detectNoiseFloorV2(result, windowMagnitudes, windowRMS, magDb, binHz, nyquist, refLevel, opts)

	// === Spectral centroid ===
	result.SpectralCentroid = calculateCentroid(avgMagnitude, binHz)

	// === Band energy for debugging ===
	result.BandEnergy, result.BandFreqs = calculateBandEnergy(magDb, binHz, nyquist, refLevel)

	return result, nil
}

// detectHumV2 checks for hum by analyzing temporal variance.
// Real hum is constant; musical content at 50/60 Hz varies with the performance.
func detectHumV2(result *types.SpectralResult, windowMagnitudes [][]float64, binHz, refLevel float64) {
	hum50, variance50 := detectHumFrequencyV2(windowMagnitudes, 50, binHz)
	hum60, variance60 := detectHumFrequencyV2(windowMagnitudes, 60, binHz)

	// Hum = high level + low variance (coefficient of variation < 0.3)
	// Music = high level + high variance
	const maxVarianceForHum = 0.3

	if hum50 > 15 && variance50 < maxVarianceForHum {
		result.Has50HzHum = true
		result.HumLevelDb = hum50
	}

	if hum60 > 15 && variance60 < maxVarianceForHum {
		result.Has60HzHum = true
		if hum60 > result.HumLevelDb {
			result.HumLevelDb = hum60
		}
	}
}

// detectHumFrequencyV2 returns the spike level and coefficient of variation across windows.
func detectHumFrequencyV2(windowMagnitudes [][]float64, fundamental, binHz float64) (spike, coeffVar float64) {
	if len(windowMagnitudes) == 0 {
		return 0, 1
	}

	harmonics := []float64{1, 2, 3, 4, 5, 6}

	// For each window, compute the max spike across harmonics.
	windowSpikes := make([]float64, len(windowMagnitudes))

	for windowIdx, mag := range windowMagnitudes {
		magDb := toDb(mag)

		var maxSpike float64

		for _, harmonic := range harmonics {
			freq := fundamental * harmonic
			bin := int(freq / binHz)

			if bin <= 5 || bin >= len(magDb)-5 {
				continue
			}

			peakLevel := magDb[bin]

			var surroundSum float64

			surroundCount := 0

			for idx := bin - 5; idx <= bin+5; idx++ {
				if idx >= 0 && idx < len(magDb) && (idx < bin-1 || idx > bin+1) {
					surroundSum += magDb[idx]
					surroundCount++
				}
			}

			if surroundCount > 0 {
				surroundAvg := surroundSum / float64(surroundCount)
				spikeLevel := peakLevel - surroundAvg

				// Peak sharpness: reject broad spectral bumps (synth bass, kick)
				// that are not genuine tonal spikes.
				if bin >= 1 && bin < len(magDb)-1 {
					adjacentAvg := (magDb[bin-1] + magDb[bin+1]) / 2
					if peakLevel-adjacentAvg < 6 {
						continue
					}
				}

				if spikeLevel > maxSpike {
					maxSpike = spikeLevel
				}
			}
		}

		windowSpikes[windowIdx] = maxSpike
	}

	// Compute mean and standard deviation of spikes across windows.
	var sum float64
	for _, s := range windowSpikes {
		sum += s
	}

	mean := sum / float64(len(windowSpikes))

	var varianceSum float64

	for _, s := range windowSpikes {
		d := s - mean
		varianceSum += d * d
	}

	stdDev := math.Sqrt(varianceSum / float64(len(windowSpikes)))

	// Coefficient of variation (stdDev / mean).
	// Low CV = consistent level = hum.
	// High CV = varying level = music.
	cv := 1.0
	if mean > 0 {
		cv = stdDev / mean
	}

	return mean, cv
}

// detectNoiseFloorV2 measures noise floor using quiet-window HF with full-track reference,
// gated by an absolute RMS threshold on the quiet windows.
//
// Strategy:
//   - HF energy (14-18 kHz) is measured from the quietest 20% of windows to expose the true
//     noise floor without signal masking.
//   - Reference level (1-10 kHz) comes from the full-track average for a stable baseline.
//   - RMS gate: if the quiet windows are not actually quiet (above -40 dBFS), they contain
//     signal, not noise. In that case, fall back to full-track HF measurement.
//   - Spectral flatness guard: suppress detection when HF energy is tonal (music, not noise).
func detectNoiseFloorV2(
	result *types.SpectralResult,
	windowMagnitudes [][]float64,
	windowRMS []float64,
	magDb []float64,
	binHz, nyquist, refLevel float64,
	opts Options,
) {
	if len(windowMagnitudes) == 0 {
		result.NoiseFloorDb = -120

		return
	}

	// HF band boundaries.
	binCount := len(windowMagnitudes[0])
	hfStart := int(14000 / binHz)
	hfEnd := int(min(18000, nyquist-500) / binHz)

	if hfStart >= binCount || hfEnd <= hfStart {
		result.NoiseFloorDb = -120

		return
	}

	// Find the quietest 20% of windows (or at least 1).
	quietCount := max(len(windowRMS)/5, 1)
	quietIndices := findQuietestWindows(windowRMS, quietCount)

	// RMS gate: check if quiet windows have enough signal for meaningful measurement.
	// Below -50 dBFS, we're in recording-medium noise territory (dither, ADC noise)
	// where the HF measurement reflects the medium, not a quality problem.
	// Above -50 dBFS, quiet passages still contain enough musical signal for the
	// quiet-window HF measurement to be more accurate than the full-track average.
	const quietGateDbFS = -50.0

	var quietRMSSum float64
	for _, wi := range quietIndices {
		quietRMSSum += windowRMS[wi]
	}

	avgQuietRMS := quietRMSSum / float64(len(quietIndices))

	quietRMSDb := -120.0
	if avgQuietRMS > 0 {
		quietRMSDb = 20 * math.Log10(avgQuietRMS)
	}

	useQuietWindows := quietRMSDb > quietGateDbFS

	var hfDb float64

	if useQuietWindows {
		// Quiet windows are genuinely quiet — measure HF from them.
		var hfSum float64

		hfBins := hfEnd - hfStart

		for _, wi := range quietIndices {
			mag := windowMagnitudes[wi]

			var bandSum float64
			for i := hfStart; i < hfEnd && i < len(mag); i++ {
				bandSum += mag[i]
			}

			hfSum += bandSum / float64(hfBins)
		}

		avgHF := hfSum / float64(len(quietIndices))

		hfDb = -120.0
		if avgHF > 0 {
			hfDb = 20*math.Log10(avgHF) - refLevel
		}
	} else {
		// Quiet windows still contain signal — fall back to full-track HF.
		hfLevel := bandAverage(magDb, 14000, 18000, binHz)
		hfDb = hfLevel - refLevel
	}

	result.NoiseFloorDb = hfDb

	// Spectral flatness guard: only flag if HF energy is spectrally flat (actual noise).
	// Computed from quiet windows when available, full-track magnitudes otherwise.
	var flatness float64

	if useQuietWindows {
		var flatnessSum float64

		for _, wi := range quietIndices {
			mag := windowMagnitudes[wi]
			flatnessSum += spectralFlatness(mag[hfStart:min(hfEnd, len(mag))])
		}

		flatness = flatnessSum / float64(len(quietIndices))
	} else {
		// Full-track average magnitude for flatness.
		avgMag := make([]float64, binCount)

		for _, wm := range windowMagnitudes {
			for i := hfStart; i < hfEnd && i < len(wm); i++ {
				avgMag[i] += wm[i]
			}
		}

		wc := float64(len(windowMagnitudes))
		for i := hfStart; i < hfEnd && i < len(avgMag); i++ {
			avgMag[i] /= wc
		}

		flatness = spectralFlatness(avgMag[hfStart:min(hfEnd, binCount)])
	}

	flatnessCutoff := opts.NoiseFlatnessCutoff
	if flatnessCutoff == 0 {
		flatnessCutoff = 0.4
	}

	if flatness < flatnessCutoff {
		// Not flat enough to be noise; likely just dark recording.
		// Cap the reported level below the mild threshold to avoid false positive.
		result.NoiseFloorDb = min(hfDb, -40)
	}
}

// findQuietestWindows returns indices of the N quietest windows by RMS.
func findQuietestWindows(windowRMS []float64, count int) []int {
	if count >= len(windowRMS) {
		indices := make([]int, len(windowRMS))
		for i := range indices {
			indices[i] = i
		}

		return indices
	}

	// Simple selection: copy and sort indices by RMS.
	type indexedRMS struct {
		index int
		rms   float64
	}

	indexed := make([]indexedRMS, len(windowRMS))
	for i, rms := range windowRMS {
		indexed[i] = indexedRMS{i, rms}
	}

	// Partial sort: find count smallest.
	// For simplicity, full sort then take first count.
	for idx := range count {
		minIdx := idx
		for j := idx + 1; j < len(indexed); j++ {
			if indexed[j].rms < indexed[minIdx].rms {
				minIdx = j
			}
		}

		indexed[idx], indexed[minIdx] = indexed[minIdx], indexed[idx]
	}

	result := make([]int, count)
	for idx := range count {
		result[idx] = indexed[idx].index
	}

	return result
}

// spectralFlatness computes the Wiener entropy: geometric mean / arithmetic mean.
// Returns 1.0 for white noise (flat spectrum), lower for tonal content.
func spectralFlatness(magnitudes []float64) float64 {
	if len(magnitudes) == 0 {
		return 0
	}

	var (
		arithmeticSum float64
		logSum        float64
	)

	count := 0

	for _, m := range magnitudes {
		if m > 0 {
			arithmeticSum += m
			logSum += math.Log(m)
			count++
		}
	}

	if count == 0 || arithmeticSum == 0 {
		return 0
	}

	arithmeticMean := arithmeticSum / float64(count)
	geometricMean := math.Exp(logSum / float64(count))

	return geometricMean / arithmeticMean
}

// detectTranscodeV2 detects lossy transcodes with enhanced analysis to reduce false positives.
//
// Key improvements over V1:
//   - Measures cutoff consistency across windows (mastering LPFs are rock-solid, codecs may vary)
//   - Checks for ultrasonic content above the cutoff (mastering may leave some, codecs don't)
//   - Adjusts confidence based on these factors
//
// A 20-21 kHz cutoff on 44.1kHz content is ambiguous: it could be a legitimate mastering
// low-pass filter (common practice) or a high-bitrate lossy codec (Opus 128, AAC 256).
// This function attempts to distinguish them.
func detectTranscodeV2(
	result *types.SpectralResult,
	windowMagnitudes [][]float64,
	magDb []float64,
	binHz, nyquist, refLevel float64,
) {
	// First, run the basic detection to find candidate cutoffs.
	detectTranscode(result, magDb, binHz, nyquist, refLevel)

	// If no transcode detected, nothing more to do.
	if !result.IsTranscode {
		result.TranscodeConfidence = 0
		return
	}

	// Start with high confidence, reduce based on evidence.
	confidence := 0.95
	cutoffFreq := result.TranscodeCutoff

	// === Check 1: Cutoff consistency across windows ===
	// A mastering LPF creates identical cutoffs in every window.
	// A codec's psychoacoustic model may cause slight variations.
	cutoffStdDev := measureCutoffConsistency(windowMagnitudes, cutoffFreq, binHz)
	result.CutoffConsistency = cutoffStdDev

	// Very low stddev (< 50 Hz) suggests mastering filter, not codec.
	// Reduce confidence proportionally.
	if cutoffStdDev < 50 {
		// Linear reduction: 0 Hz stddev -> -0.20 confidence, 50 Hz -> 0
		reduction := 0.20 * (1 - cutoffStdDev/50)
		confidence -= reduction
	}

	// === Check 2: Ultrasonic content above cutoff ===
	// Legitimate mastering often leaves faint ultrasonic content (harmonics, dither).
	// Lossy codecs completely eliminate everything above their cutoff.
	// This is the strongest indicator: codecs create a hard wall with NOTHING above.
	hasUltrasonic := checkUltrasonicContent(magDb, cutoffFreq, binHz, nyquist, refLevel)
	result.HasUltrasonicContent = hasUltrasonic

	if hasUltrasonic {
		// Content above cutoff is definitive evidence against a codec.
		// Codecs cannot leave ultrasonic content - they completely eliminate it.
		// This is the strongest signal we have.
		confidence -= 0.40
	}

	// === Check 3: Cutoff frequency penalty for high frequencies ===
	// Cutoffs at 20+ kHz are more likely to be mastering decisions.
	// Lower cutoffs (15-18 kHz) are more clearly codec-related.
	if cutoffFreq >= 20000 {
		// 20 kHz: -0.10, 20.5 kHz: -0.15, 21 kHz: -0.20
		reduction := 0.10 + (cutoffFreq-20000)/5000*0.10
		confidence -= min(reduction, 0.20)
	}

	// === Check 4: Sharpness analysis ===
	// Mastering filters: typically 24-48 dB/octave (gentle to moderate)
	// Codec brick walls: often 60+ dB/octave (very steep)
	// But some mastering filters can also be steep, so this is a weak signal.
	sharpness := result.TranscodeSharpness
	if sharpness < 40 {
		// Moderate slope is more consistent with mastering filter.
		confidence -= 0.10
	}

	// Clamp confidence to valid range.
	confidence = max(0.0, min(1.0, confidence))

	// If confidence drops below threshold, un-flag as transcode.
	const minConfidenceThreshold = 0.50

	if confidence < minConfidenceThreshold {
		result.IsTranscode = false
		result.LikelyCodec = ""
	}

	result.TranscodeConfidence = confidence
}

// measureCutoffConsistency measures how consistent the cutoff frequency is across windows.
// Returns the standard deviation of detected cutoff frequencies.
// Low stddev = consistent (mastering filter), high stddev = variable (possibly codec).
func measureCutoffConsistency(windowMagnitudes [][]float64, targetCutoff, binHz float64) float64 {
	if len(windowMagnitudes) < 3 {
		return 0 // not enough windows to measure consistency
	}

	// For each window, find the frequency where energy drops most sharply
	// in the vicinity of the target cutoff.
	searchStart := targetCutoff - 2000 // search ±2 kHz around target
	searchEnd := targetCutoff + 2000
	startBin := max(1, int(searchStart/binHz))

	var cutoffs []float64

	for _, mag := range windowMagnitudes {
		magDb := toDb(mag)
		endBin := min(len(magDb)-2, int(searchEnd/binHz))

		if startBin >= endBin {
			continue
		}

		// Find the bin with the steepest drop.
		var (
			maxDrop    float64
			maxDropBin int
		)

		for bin := startBin; bin < endBin; bin++ {
			// Measure drop from bin to bin+2 (smoothed gradient).
			drop := magDb[bin] - magDb[bin+2]
			if drop > maxDrop {
				maxDrop = drop
				maxDropBin = bin
			}
		}

		if maxDrop > 5 { // only count if there's a meaningful drop
			cutoffs = append(cutoffs, float64(maxDropBin)*binHz)
		}
	}

	if len(cutoffs) < 3 {
		return 0
	}

	// Calculate standard deviation.
	var sum float64
	for _, c := range cutoffs {
		sum += c
	}

	mean := sum / float64(len(cutoffs))

	var varianceSum float64
	for _, c := range cutoffs {
		d := c - mean
		varianceSum += d * d
	}

	return math.Sqrt(varianceSum / float64(len(cutoffs)))
}

// checkUltrasonicContent checks if there's any meaningful content above the cutoff.
// Legitimate mastering may leave faint harmonics, dither, or room noise above 20 kHz.
// Lossy codecs create a hard wall with nothing above.
func checkUltrasonicContent(magDb []float64, cutoffFreq, binHz, nyquist, refLevel float64) bool {
	// Check energy in the band from cutoff+500 Hz to nyquist-500 Hz.
	checkStart := cutoffFreq + 500
	checkEnd := nyquist - 500

	if checkEnd <= checkStart {
		return false // no room to check
	}

	startBin := int(checkStart / binHz)
	endBin := int(checkEnd / binHz)

	if startBin >= len(magDb) || endBin <= startBin {
		return false
	}

	endBin = min(endBin, len(magDb)-1)

	// Calculate average energy above cutoff.
	var sum float64

	count := 0

	for i := startBin; i <= endBin; i++ {
		sum += magDb[i]
		count++
	}

	if count == 0 {
		return false
	}

	avgAboveCutoff := sum / float64(count)

	// Compare to reference level.
	// If ultrasonic energy is within 50 dB of reference, there's content.
	// (Pure silence/noise floor would be 60-80 dB below reference.)
	relativeLevel := avgAboveCutoff - refLevel

	// If there's meaningful content (not just noise floor), return true.
	// Threshold: -50 dB relative to reference indicates some content.
	return relativeLevel > -50
}
