//nolint:staticcheck // too dumb
package spectral

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"gonum.org/v1/gonum/dsp/fourier"

	"github.com/farcloser/primordium/fault"

	"github.com/farcloser/haustorium/internal/audit/shared"
	"github.com/farcloser/haustorium/internal/types"
)

type Options struct {
	FFTSize    int // default 8192
	WindowsMax int // max windows to analyze; 0 = all (default 100)

	// NoiseFlatnessCutoff is the spectral flatness threshold below which HF energy
	// is considered tonal content rather than noise. Flatness is the Wiener entropy
	// (geometric mean / arithmetic mean): 1.0 = white noise (flat), 0.0 = pure tone.
	// Below this cutoff, the noise floor level is capped at -40 dB to avoid false
	// positives on dark recordings. Default 0.4. Used only by AnalyzeV2.
	NoiseFlatnessCutoff float64
}

func DefaultOptions() Options {
	return Options{
		FFTSize:             8192,
		WindowsMax:          100,
		NoiseFlatnessCutoff: 0.4,
	}
}

var transcodeCutoffs = []struct {
	freq  float64
	codec string
}{
	{15500, "AAC 128"},
	{16000, "MP3 128"},
	{17500, "MP3 160"},
	{18000, "MP3 192 / AAC 192"},
	{19000, "MP3 256 / AAC 256"},
	{20000, "MP3 320"},
	{20500, "Opus 128"},
}

var upsampleNyquists = []struct {
	rate    int
	nyquist float64
}{
	{44100, 22050},
	{48000, 24000},
	{88200, 44100},
	{96000, 48000},
}

func Analyze(reader io.Reader, format types.PCMFormat, opts Options) (*types.SpectralResult, error) {
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

	// Phase 3: Process FFT windows.
	window := makeHannWindow(fftSize)
	binCount := fftSize/2 + 1
	magnitudeSum := make([]float64, binCount)
	fft := fourier.NewFFT(fftSize)
	fftIn := make([]float64, fftSize)

	for _, pos := range positions {
		for i := range fftSize {
			fftIn[i] = samples[pos+i] * window[i]
		}

		coeffs := fft.Coefficients(nil, fftIn)

		for i, c := range coeffs {
			magnitudeSum[i] += math.Sqrt(real(c)*real(c) + imag(c)*imag(c))
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

	// === Hum detection ===
	detectHum(result, magDb, binHz, refLevel)

	// === Noise floor ===
	detectNoiseFloor(result, magDb, binHz, nyquist, refLevel)

	// === Spectral centroid ===
	result.SpectralCentroid = calculateCentroid(avgMagnitude, binHz)

	// === Band energy for debugging ===
	result.BandEnergy, result.BandFreqs = calculateBandEnergy(magDb, binHz, nyquist, refLevel)

	return result, nil
}

// readMonoMixed reads the entire PCM stream and returns mono-mixed samples.
func readMonoMixed(reader io.Reader, format types.PCMFormat) ([]float64, error) {
	bytesPerSample := int(format.BitDepth / 8) //nolint:gosec // bit depth and channel count are small constants
	numChannels := int(format.Channels)        //nolint:gosec // bit depth and channel count are small constants
	frameSize := bytesPerSample * numChannels

	var maxVal float64

	switch format.BitDepth {
	case types.Depth16:
		maxVal = shared.MaxValue16
	case types.Depth24:
		maxVal = shared.MaxValue24
	case types.Depth32:
		maxVal = shared.MaxValue32
	default:
	}

	readBuf := make([]byte, frameSize*4096)

	var samples []float64

	for {
		n, err := reader.Read(readBuf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := readBuf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += frameSize {
					var sum float64
					for ch := range numChannels {
						sum += float64(
							int16(binary.LittleEndian.Uint16(data[i+ch*2:])),
						) / maxVal
					}

					samples = append(samples, sum/float64(numChannels))
				}
			case types.Depth24:
				for i := 0; i < len(data); i += frameSize {
					var sum float64

					for ch := range numChannels {
						offset := i + ch*3

						raw := int32(data[offset]) | int32(data[offset+1])<<8 | int32(data[offset+2])<<16
						if raw&0x800000 != 0 {
							raw |= ^0xFFFFFF
						}

						sum += float64(raw) / maxVal
					}

					samples = append(samples, sum/float64(numChannels))
				}
			case types.Depth32:
				for i := 0; i < len(data); i += frameSize {
					var sum float64
					for ch := range numChannels {
						sum += float64(
							int32(binary.LittleEndian.Uint32(data[i+ch*4:])),
						) / maxVal
					}

					samples = append(samples, sum/float64(numChannels))
				}
			default:
			}
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("%w: %w", fault.ErrReadFailure, err)
		}
	}

	return samples, nil
}

// windowPositions returns evenly spaced FFT window start positions.
// If the track has fewer possible windows than maxWindows, all are returned.
// Otherwise, maxWindows positions are distributed evenly across the track.
func windowPositions(totalSamples, fftSize, maxWindows int) []int {
	available := totalSamples - fftSize
	if available < 0 {
		return nil
	}

	hopSize := fftSize / 2
	totalPossible := available/hopSize + 1

	if totalPossible <= maxWindows {
		positions := make([]int, 0, totalPossible)
		for pos := 0; pos+fftSize <= totalSamples; pos += hopSize {
			positions = append(positions, pos)
		}

		return positions
	}

	positions := make([]int, maxWindows)
	if maxWindows == 1 {
		positions[0] = available / 2

		return positions
	}

	for i := range maxWindows {
		positions[i] = available * i / (maxWindows - 1)
	}

	return positions
}

func makeHannWindow(size int) []float64 {
	window := make([]float64, size)
	for i := range window {
		window[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(size-1)))
	}

	return window
}

func toDb(magnitude []float64) []float64 {
	decibels := make([]float64, len(magnitude))
	for i, m := range magnitude {
		if m > 0 {
			decibels[i] = 20 * math.Log10(m)
		} else {
			decibels[i] = -120
		}
	}

	return decibels
}

func bandAverage(magDb []float64, startHz, endHz, binHz float64) float64 {
	startBin := int(startHz / binHz)
	endBin := int(endHz / binHz)

	if startBin < 0 {
		startBin = 0
	}

	if endBin >= len(magDb) {
		endBin = len(magDb) - 1
	}

	if startBin > endBin {
		return -120
	}

	var sum float64
	for i := startBin; i <= endBin; i++ {
		sum += magDb[i]
	}

	return sum / float64(endBin-startBin+1)
}

func detectBrickWall(magDb []float64, checkFreq, binHz float64) (drop, sharpness float64) {
	belowLevel := bandAverage(magDb, checkFreq-1500, checkFreq-500, binHz)
	aboveLevel := bandAverage(magDb, checkFreq+500, checkFreq+1500, binHz)

	drop = belowLevel - aboveLevel

	if drop > 10 {
		freqBelow := checkFreq - 1000
		freqAbove := checkFreq + 1000
		octaves := math.Log2(freqAbove / freqBelow)
		sharpness = drop / octaves
	}

	return drop, sharpness
}

func detectUpsampling(result *types.SpectralResult, magDb []float64, binHz, nyquist, refLevel float64) {
	var (
		bestSharpness float64
		bestCutoff    float64
		bestRate      int
	)

	for _, sampleRate := range upsampleNyquists {
		if sampleRate.nyquist >= nyquist {
			continue
		}

		drop, sharpness := detectBrickWall(magDb, sampleRate.nyquist, binHz)

		if drop > 20 && sharpness > bestSharpness {
			bestSharpness = sharpness
			bestCutoff = sampleRate.nyquist
			bestRate = sampleRate.rate
		}
	}

	if bestSharpness > 40 {
		result.IsUpsampled = true
		result.EffectiveRate = bestRate
		result.UpsampleCutoff = bestCutoff
		result.UpsampleSharpness = bestSharpness
	}
}

func detectTranscode(result *types.SpectralResult, magDb []float64, binHz, nyquist, refLevel float64) {
	// Only check if claimed sample rate is 44.1/48k (or if upsampled from there)
	// Transcode detection looks for cutoffs below 22kHz
	var (
		bestSharpness float64
		bestCutoff    float64
		bestCodec     string
	)

	for _, transcodeInfo := range transcodeCutoffs {
		if transcodeInfo.freq >= nyquist {
			continue
		}
		// Don't flag upsample cutoff as transcode
		if result.IsUpsampled && math.Abs(transcodeInfo.freq-result.UpsampleCutoff) < 2000 {
			continue
		}

		drop, sharpness := detectBrickWall(magDb, transcodeInfo.freq, binHz)

		if drop > 15 && sharpness > bestSharpness {
			bestSharpness = sharpness
			bestCutoff = transcodeInfo.freq
			bestCodec = transcodeInfo.codec
		}
	}

	if bestSharpness > 30 {
		result.IsTranscode = true
		result.TranscodeCutoff = bestCutoff
		result.TranscodeSharpness = bestSharpness
		result.LikelyCodec = bestCodec
	}
}

func detectHum(result *types.SpectralResult, magDb []float64, binHz, refLevel float64) {
	// Check 50Hz and harmonics (100, 150, 200, 250, 300 Hz)
	hum50 := detectHumFrequency(magDb, 50, binHz, refLevel)
	// Check 60Hz and harmonics (120, 180, 240, 300, 360 Hz)
	hum60 := detectHumFrequency(magDb, 60, binHz, refLevel)

	if hum50 > 15 {
		result.Has50HzHum = true
		result.HumLevelDb = hum50
	}

	if hum60 > 15 {
		result.Has60HzHum = true
		if hum60 > result.HumLevelDb {
			result.HumLevelDb = hum60
		}
	}
}

func detectHumFrequency(magDb []float64, fundamental, binHz, refLevel float64) float64 {
	harmonics := []float64{1, 2, 3, 4, 5, 6}

	var maxSpike float64

	for _, harmonic := range harmonics {
		freq := fundamental * harmonic
		bin := int(freq / binHz)

		if bin <= 2 || bin >= len(magDb)-2 {
			continue
		}

		// Peak at harmonic.
		peakLevel := magDb[bin]

		// Average of surrounding bins (±5 bins, excluding ±1).
		var surroundSum float64

		surroundCount := 0

		for idx := bin - 5; idx <= bin+5; idx++ {
			if idx >= 0 && idx < len(magDb) && (idx < bin-1 || idx > bin+1) {
				surroundSum += magDb[idx]
				surroundCount++
			}
		}

		surroundAvg := surroundSum / float64(surroundCount)

		spike := peakLevel - surroundAvg

		// Peak sharpness check: real mains hum is a razor-sharp spectral line
		// concentrated in 1-2 FFT bins, while musical bass content (synth, kick)
		// spreads energy across many bins. Require the peak bin to stand at least
		// 6 dB above the average of its immediate neighbors (±1 bin) to qualify
		// as a genuine tonal spike rather than a broad spectral bump.
		if bin >= 1 && bin < len(magDb)-1 {
			adjacentAvg := (magDb[bin-1] + magDb[bin+1]) / 2
			sharpness := peakLevel - adjacentAvg

			if sharpness < 6 {
				continue
			}
		}

		if spike > maxSpike {
			maxSpike = spike
		}
	}

	return maxSpike
}

func detectNoiseFloor(result *types.SpectralResult, magDb []float64, binHz, nyquist, refLevel float64) {
	// Measure energy in 14-18 kHz band relative to reference
	// Real music has content here; pure noise floor is flat
	// We're looking for elevated flat noise, not natural rolloff
	hfLevel := bandAverage(magDb, 14000, 18000, binHz)
	result.NoiseFloorDb = hfLevel - refLevel
}

func calculateCentroid(magnitude []float64, binHz float64) float64 {
	var (
		weightedSum float64
		totalMag    float64
	)

	for i, mag := range magnitude {
		freq := float64(i) * binHz
		weightedSum += freq * mag
		totalMag += mag
	}

	if totalMag == 0 {
		return 0
	}

	return weightedSum / totalMag
}

func calculateBandEnergy(magDb []float64, binHz, nyquist, refLevel float64) ([]float64, []float64) {
	bands := []float64{100, 500, 1000, 2000, 4000, 8000, 12000, 16000, 20000, 22050, 24000, 30000, 40000}

	var (
		energy []float64
		freqs  []float64
	)

	for _, freq := range bands {
		if freq >= nyquist {
			break
		}

		level := bandAverage(magDb, freq*0.9, freq*1.1, binHz)
		energy = append(energy, level-refLevel)
		freqs = append(freqs, freq)
	}

	return energy, freqs
}
