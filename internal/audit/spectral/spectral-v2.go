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
func AnalyzeV2(r io.Reader, format types.PCMFormat, opts Options) (*types.SpectralResult, error) {
	if opts.FFTSize == 0 {
		opts.FFTSize = 8192
	}

	if opts.WindowsMax == 0 {
		opts.WindowsMax = 100
	}

	fftSize := opts.FFTSize

	// Phase 1: Read entire stream into mono-mixed samples.
	samples, err := readMonoMixed(r, format)
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

	for wi, pos := range positions {
		var rmsSum float64

		for i := range fftSize {
			fftIn[i] = samples[pos+i] * window[i]
			rmsSum += samples[pos+i] * samples[pos+i]
		}

		windowRMS[wi] = math.Sqrt(rmsSum / float64(fftSize))

		coeffs := fft.Coefficients(nil, fftIn)

		windowMagnitudes[wi] = make([]float64, binCount)

		for i, c := range coeffs {
			mag := math.Sqrt(real(c)*real(c) + imag(c)*imag(c))
			windowMagnitudes[wi][i] = mag
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

	// === Lossy transcode detection ===
	detectTranscode(result, magDb, binHz, nyquist, refLevel)

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

	for wi, mag := range windowMagnitudes {
		magDb := toDb(mag)

		var maxSpike float64

		for _, h := range harmonics {
			freq := fundamental * h
			bin := int(freq / binHz)

			if bin <= 5 || bin >= len(magDb)-5 {
				continue
			}

			peakLevel := magDb[bin]

			var surroundSum float64

			surroundCount := 0

			for i := bin - 5; i <= bin+5; i++ {
				if i >= 0 && i < len(magDb) && (i < bin-1 || i > bin+1) {
					surroundSum += magDb[i]
					surroundCount++
				}
			}

			if surroundCount > 0 {
				surroundAvg := surroundSum / float64(surroundCount)

				s := peakLevel - surroundAvg
				if s > maxSpike {
					maxSpike = s
				}
			}
		}

		windowSpikes[wi] = maxSpike
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
func findQuietestWindows(windowRMS []float64, n int) []int {
	if n >= len(windowRMS) {
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

	// Partial sort: find n smallest.
	// For simplicity, full sort then take first n.
	for i := range n {
		minIdx := i
		for j := i + 1; j < len(indexed); j++ {
			if indexed[j].rms < indexed[minIdx].rms {
				minIdx = j
			}
		}

		indexed[i], indexed[minIdx] = indexed[minIdx], indexed[i]
	}

	result := make([]int, n)
	for i := range n {
		result[i] = indexed[i].index
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
