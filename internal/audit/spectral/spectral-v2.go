package spectral

import (
	"io"
	"math"

	"github.com/farcloser/haustorium/internal/types"
	"gonum.org/v1/gonum/dsp/fourier"
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

	// === Noise floor V2 (quiet passages + flatness) ===
	detectNoiseFloorV2(result, windowMagnitudes, windowRMS, binHz, nyquist, opts)

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

// detectNoiseFloorV2 measures noise floor during quiet passages and checks spectral flatness.
// Both the HF level and the reference level are computed from the same quiet windows
// to ensure a self-consistent comparison.
func detectNoiseFloorV2(result *types.SpectralResult, windowMagnitudes [][]float64, windowRMS []float64, binHz, nyquist float64, opts Options) {
	if len(windowMagnitudes) == 0 {
		result.NoiseFloorDb = -120
		return
	}

	// Find the quietest 20% of windows (or at least 1).
	quietCount := max(len(windowRMS)/5, 1)
	quietIndices := findQuietestWindows(windowRMS, quietCount)

	if len(quietIndices) == 0 {
		result.NoiseFloorDb = -120
		return
	}

	// Compute band boundaries.
	binCount := len(windowMagnitudes[0])
	refStart := int(1000 / binHz)
	refEnd := int(10000 / binHz)
	hfStart := int(14000 / binHz)
	hfEnd := int(min(18000, nyquist-500) / binHz)

	if hfStart >= binCount || hfEnd <= hfStart || refEnd <= refStart {
		result.NoiseFloorDb = -120
		return
	}

	var hfSum float64
	var refSum float64
	var flatnessSum float64
	hfBins := hfEnd - hfStart
	refBins := refEnd - refStart

	for _, wi := range quietIndices {
		mag := windowMagnitudes[wi]

		// Average HF level (14-18 kHz).
		var bandSum float64
		for i := hfStart; i < hfEnd && i < len(mag); i++ {
			bandSum += mag[i]
		}

		hfSum += bandSum / float64(hfBins)

		// Reference level (1-10 kHz) from the same quiet windows.
		var rBandSum float64
		for i := refStart; i < refEnd && i < len(mag); i++ {
			rBandSum += mag[i]
		}

		refSum += rBandSum / float64(refBins)

		// Spectral flatness in HF band: geometric mean / arithmetic mean.
		// Approaches 1.0 for noise (flat), lower for tonal content.
		flatnessSum += spectralFlatness(mag[hfStart:min(hfEnd, len(mag))])
	}

	quietWindowCount := float64(len(quietIndices))
	avgHF := hfSum / quietWindowCount
	avgRef := refSum / quietWindowCount
	avgFlatness := flatnessSum / quietWindowCount

	// Convert both to dB and compute relative level.
	hfDb := -120.0
	if avgHF > 0 && avgRef > 0 {
		hfDb = 20*math.Log10(avgHF) - 20*math.Log10(avgRef)
	}

	result.NoiseFloorDb = hfDb

	// Only flag as problematic if both elevated AND spectrally flat (actual noise).
	// Musical content that's just dark gets a pass.
	flatnessCutoff := opts.NoiseFlatnessCutoff
	if flatnessCutoff == 0 {
		flatnessCutoff = 0.4
	}

	if avgFlatness < flatnessCutoff {
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
	for i := 0; i < n; i++ {
		minIdx := i
		for j := i + 1; j < len(indexed); j++ {
			if indexed[j].rms < indexed[minIdx].rms {
				minIdx = j
			}
		}
		indexed[i], indexed[minIdx] = indexed[minIdx], indexed[i]
	}

	result := make([]int, n)
	for i := 0; i < n; i++ {
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

	var arithmeticSum float64
	var logSum float64
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
