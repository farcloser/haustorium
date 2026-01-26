package spectral

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"gonum.org/v1/gonum/dsp/fourier"

	"github.com/farcloser/haustorium/internal/types"
	"github.com/farcloser/primordium/fault"
)

type Options struct {
	FFTSize    int // default 8192
	WindowsMax int // max windows to analyze; 0 = all (default 100)
}

func DefaultOptions() Options {
	return Options{
		FFTSize:    8192,
		WindowsMax: 100,
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

func Analyze(r io.Reader, format types.PCMFormat, opts Options) (*types.SpectralResult, error) {
	if opts.FFTSize == 0 {
		opts.FFTSize = 8192
	}
	if opts.WindowsMax == 0 {
		opts.WindowsMax = 100
	}

	bytesPerSample := int(format.BitDepth / 8)
	numChannels := int(format.Channels)
	frameSize := bytesPerSample * numChannels

	fftSize := opts.FFTSize
	hopSize := fftSize / 2

	window := makeHannWindow(fftSize)

	binCount := fftSize/2 + 1
	magnitudeSum := make([]float64, binCount)

	var maxVal float64
	switch format.BitDepth {
	case types.Depth16:
		maxVal = 32768.0
	case types.Depth24:
		maxVal = 8388608.0
	case types.Depth32:
		maxVal = 2147483648.0
	}

	sampleBuf := make([]float64, fftSize)
	bufPos := 0
	bufFilled := 0

	fft := fourier.NewFFT(fftSize)
	fftIn := make([]float64, fftSize)
	windowsProcessed := 0
	var totalFrames uint64

	readBuf := make([]byte, frameSize*4096)

	for {
		n, err := r.Read(readBuf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := readBuf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += frameSize {
					var sum float64
					for ch := 0; ch < numChannels; ch++ {
						sample := float64(int16(binary.LittleEndian.Uint16(data[i+ch*2:]))) / maxVal
						sum += sample
					}
					sampleBuf[bufPos] = sum / float64(numChannels)
					bufPos = (bufPos + 1) % fftSize
					if bufFilled < fftSize {
						bufFilled++
					}
					totalFrames++

					if bufFilled == fftSize && (totalFrames%uint64(hopSize)) == 0 {
						if opts.WindowsMax > 0 && windowsProcessed >= opts.WindowsMax {
							continue
						}
						processWindow(sampleBuf, bufPos, window, fftIn, fft, magnitudeSum)
						windowsProcessed++
					}
				}
			case types.Depth24:
				for i := 0; i < len(data); i += frameSize {
					var sum float64
					for ch := 0; ch < numChannels; ch++ {
						offset := i + ch*3
						raw := int32(data[offset]) | int32(data[offset+1])<<8 | int32(data[offset+2])<<16
						if raw&0x800000 != 0 {
							raw |= ^0xFFFFFF
						}
						sum += float64(raw) / maxVal
					}
					sampleBuf[bufPos] = sum / float64(numChannels)
					bufPos = (bufPos + 1) % fftSize
					if bufFilled < fftSize {
						bufFilled++
					}
					totalFrames++

					if bufFilled == fftSize && (totalFrames%uint64(hopSize)) == 0 {
						if opts.WindowsMax > 0 && windowsProcessed >= opts.WindowsMax {
							continue
						}
						processWindow(sampleBuf, bufPos, window, fftIn, fft, magnitudeSum)
						windowsProcessed++
					}
				}
			case types.Depth32:
				for i := 0; i < len(data); i += frameSize {
					var sum float64
					for ch := 0; ch < numChannels; ch++ {
						sample := float64(int32(binary.LittleEndian.Uint32(data[i+ch*4:]))) / maxVal
						sum += sample
					}
					sampleBuf[bufPos] = sum / float64(numChannels)
					bufPos = (bufPos + 1) % fftSize
					if bufFilled < fftSize {
						bufFilled++
					}
					totalFrames++

					if bufFilled == fftSize && (totalFrames%uint64(hopSize)) == 0 {
						if opts.WindowsMax > 0 && windowsProcessed >= opts.WindowsMax {
							continue
						}
						processWindow(sampleBuf, bufPos, window, fftIn, fft, magnitudeSum)
						windowsProcessed++
					}
				}
			}
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("%w: %w", fault.ErrReadFailure, err)
		}
	}

	if windowsProcessed == 0 {
		return &types.SpectralResult{
			ClaimedRate: int(format.SampleRate),
			Frames:      totalFrames,
		}, nil
	}

	// Average magnitude spectrum
	avgMagnitude := make([]float64, binCount)
	for i := range avgMagnitude {
		avgMagnitude[i] = magnitudeSum[i] / float64(windowsProcessed)
	}

	binHz := float64(format.SampleRate) / float64(fftSize)
	nyquist := float64(format.SampleRate) / 2

	// Convert to dB
	magDb := toDb(avgMagnitude)

	// Reference level: 1-10 kHz average
	refLevel := bandAverage(magDb, 1000, 10000, binHz)

	result := &types.SpectralResult{
		ClaimedRate: int(format.SampleRate),
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

func makeHannWindow(size int) []float64 {
	window := make([]float64, size)
	for i := range window {
		window[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(size-1)))
	}
	return window
}

func processWindow(ringBuf []float64, pos int, window, fftIn []float64, fft *fourier.FFT, magnitudeSum []float64) {
	n := len(fftIn)

	for i := 0; i < n; i++ {
		idx := (pos + i) % n
		fftIn[i] = ringBuf[idx] * window[i]
	}

	coeffs := fft.Coefficients(nil, fftIn)

	for i, c := range coeffs {
		mag := math.Sqrt(real(c)*real(c) + imag(c)*imag(c))
		magnitudeSum[i] += mag
	}
}

func toDb(magnitude []float64) []float64 {
	db := make([]float64, len(magnitude))
	for i, m := range magnitude {
		if m > 0 {
			db[i] = 20 * math.Log10(m)
		} else {
			db[i] = -120
		}
	}
	return db
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
	var bestSharpness float64
	var bestCutoff float64
	var bestRate int

	for _, sr := range upsampleNyquists {
		if sr.nyquist >= nyquist {
			continue
		}

		drop, sharpness := detectBrickWall(magDb, sr.nyquist, binHz)

		if drop > 20 && sharpness > bestSharpness {
			bestSharpness = sharpness
			bestCutoff = sr.nyquist
			bestRate = sr.rate
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

	var bestSharpness float64
	var bestCutoff float64
	var bestCodec string

	for _, tc := range transcodeCutoffs {
		if tc.freq >= nyquist {
			continue
		}
		// Don't flag upsample cutoff as transcode
		if result.IsUpsampled && math.Abs(tc.freq-result.UpsampleCutoff) < 2000 {
			continue
		}

		drop, sharpness := detectBrickWall(magDb, tc.freq, binHz)

		if drop > 15 && sharpness > bestSharpness {
			bestSharpness = sharpness
			bestCutoff = tc.freq
			bestCodec = tc.codec
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

	for _, h := range harmonics {
		freq := fundamental * h
		bin := int(freq / binHz)

		if bin <= 2 || bin >= len(magDb)-2 {
			continue
		}

		// Peak at harmonic
		peakLevel := magDb[bin]
		// Average of surrounding bins (±3 bins, excluding center)
		var surroundSum float64
		surroundCount := 0
		for i := bin - 5; i <= bin+5; i++ {
			if i >= 0 && i < len(magDb) && (i < bin-1 || i > bin+1) {
				surroundSum += magDb[i]
				surroundCount++
			}
		}
		surroundAvg := surroundSum / float64(surroundCount)

		spike := peakLevel - surroundAvg
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
	var weightedSum float64
	var totalMag float64

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

	var energy []float64
	var freqs []float64

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
