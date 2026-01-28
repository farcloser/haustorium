//nolint:staticcheck // too dumb
package loudness

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"

	"github.com/farcloser/primordium/fault"

	"github.com/farcloser/haustorium/internal/audit/shared"
	"github.com/farcloser/haustorium/internal/types"
)

// Biquad filter coefficients.
type biquad struct {
	b0, b1, b2 float64
	a1, a2     float64
}

// Biquad filter state.
type biquadState struct {
	z1, z2 float64
}

func (s *biquadState) process(b *biquad, in float64) float64 {
	out := b.b0*in + s.z1
	s.z1 = b.b1*in - b.a1*out + s.z2
	s.z2 = b.b2*in - b.a2*out

	return out
}

// K-weighting filter coefficients for common sample rates
// Pre-filter (high shelf) + RLB weighting (high pass).
func getKWeightingFilters(rate int) (pre, rlb biquad) {
	// Coefficients from ITU-R BS.1770-4
	// These are computed from the analog prototype transfer functions
	sampleRate := float64(rate)

	// Pre-filter (high shelf)
	// Models the acoustic effects of the head
	centerFreq := 1681.974450955533
	G := 3.999843853973347
	qualityFactor := 0.7071752369554196

	bilinearK := math.Tan(math.Pi * centerFreq / sampleRate)
	headGainV := math.Pow(10, G/20)
	Vb := math.Pow(headGainV, 0.4996667741545416)

	gain := 1 + bilinearK/qualityFactor + bilinearK*bilinearK
	pre.b0 = (headGainV + Vb*bilinearK/qualityFactor + bilinearK*bilinearK) / gain
	pre.b1 = 2 * (bilinearK*bilinearK - headGainV) / gain
	pre.b2 = (headGainV - Vb*bilinearK/qualityFactor + bilinearK*bilinearK) / gain
	pre.a1 = 2 * (bilinearK*bilinearK - 1) / gain
	pre.a2 = (1 - bilinearK/qualityFactor + bilinearK*bilinearK) / gain

	// RLB weighting (high pass)
	centerFreq = 38.13547087602444
	qualityFactor = 0.5003270373238773

	bilinearK = math.Tan(math.Pi * centerFreq / sampleRate)

	gain = 1 + bilinearK/qualityFactor + bilinearK*bilinearK
	rlb.b0 = 1 / gain
	rlb.b1 = -2 / gain
	rlb.b2 = 1 / gain
	rlb.a1 = 2 * (bilinearK*bilinearK - 1) / gain
	rlb.a2 = (1 - bilinearK/qualityFactor + bilinearK*bilinearK) / gain

	return pre, rlb
}

// Channel weights for surround (we only handle stereo for now).
func getChannelWeight(channel, numChannels int) float64 {
	if numChannels <= 2 {
		return 1.0
	}
	// For surround: L, R, C = 1.0; Ls, Rs = 1.41 (~+1.5dB)
	// LFE is excluded
	if channel >= 3 && channel <= 4 && numChannels > 4 {
		return 1.41
	}

	return 1.0
}

// drBlock holds peak and RMS for a 3-second analysis block.
type drBlock struct {
	peak float64
	rms  float64
}

// meter holds all state for the loudness/DR measurement.
type meter struct {
	numChannels int
	sampleRate  int
	pre, rlb    biquad
	preState    []biquadState
	rlbState    []biquadState

	// Window sizes in samples.
	momentarySize int
	shortTermSize int
	blockSize     int
	hopSize       int

	// Ring buffers for windowed measurements.
	momentaryBuf    []float64
	shortTermBuf    []float64
	momentaryPos    int
	shortTermPos    int
	momentarySum    float64
	shortTermSum    float64
	momentaryFilled int
	shortTermFilled int

	// DR calculation: 3s blocks.
	drBlocks     []drBlock
	blockSum     float64
	blockPeak    float64
	blockSamples int

	// Loudness windows.
	momentaryPowers []float64
	shortTermPowers []float64
	momentaryMax    float64
	shortTermMax    float64

	// Counters.
	sampleCount int
	totalFrames uint64

	// Per-frame scratch buffer (reused, avoids allocation).
	frameSamples []float64
}

func newMeter(sampleRate, numChannels int) *meter {
	pre, rlb := getKWeightingFilters(sampleRate)

	return &meter{
		numChannels:   numChannels,
		sampleRate:    sampleRate,
		pre:           pre,
		rlb:           rlb,
		preState:      make([]biquadState, numChannels),
		rlbState:      make([]biquadState, numChannels),
		momentarySize: sampleRate * 400 / 1000,
		shortTermSize: sampleRate * 3,
		blockSize:     sampleRate * 3,
		hopSize:       sampleRate * 100 / 1000,
		momentaryBuf:  make([]float64, sampleRate*400/1000),
		shortTermBuf:  make([]float64, sampleRate*3),
		momentaryMax:  -120,
		shortTermMax:  -120,
		frameSamples:  make([]float64, numChannels),
	}
}

// processFrame applies K-weighting, accumulates loudness and DR data for one frame.
// The caller must fill m.frameSamples before calling this.
func (m *meter) processFrame() {
	var framePower, framePeak float64

	for channel, sample := range m.frameSamples {
		if abs := math.Abs(sample); abs > framePeak {
			framePeak = abs
		}

		filtered := m.preState[channel].process(&m.pre, sample)
		filtered = m.rlbState[channel].process(&m.rlb, filtered)

		weight := getChannelWeight(channel, m.numChannels)
		framePower += weight * filtered * filtered
	}

	// Update DR block.
	m.blockSum += framePower / float64(m.numChannels)

	if framePeak > m.blockPeak {
		m.blockPeak = framePeak
	}

	m.blockSamples++

	if m.blockSamples >= m.blockSize {
		rms := math.Sqrt(m.blockSum / float64(m.blockSamples))
		m.drBlocks = append(m.drBlocks, drBlock{m.blockPeak, rms})
		m.blockSum = 0
		m.blockPeak = 0
		m.blockSamples = 0
	}

	// Update momentary window (ring buffer).
	old := m.momentaryBuf[m.momentaryPos]
	m.momentaryBuf[m.momentaryPos] = framePower
	m.momentarySum = m.momentarySum - old + framePower

	m.momentaryPos = (m.momentaryPos + 1) % m.momentarySize
	if m.momentaryFilled < m.momentarySize {
		m.momentaryFilled++
	}

	// Update short-term window (ring buffer).
	old = m.shortTermBuf[m.shortTermPos]
	m.shortTermBuf[m.shortTermPos] = framePower
	m.shortTermSum = m.shortTermSum - old + framePower

	m.shortTermPos = (m.shortTermPos + 1) % m.shortTermSize
	if m.shortTermFilled < m.shortTermSize {
		m.shortTermFilled++
	}

	m.sampleCount++
	m.totalFrames++

	// Every hop, calculate windowed loudness.
	if m.sampleCount%m.hopSize == 0 {
		if m.momentaryFilled == m.momentarySize {
			momentaryLoudness := -0.691 + 10*math.Log10(m.momentarySum/float64(m.momentarySize))
			m.momentaryPowers = append(m.momentaryPowers, m.momentarySum/float64(m.momentarySize))

			if momentaryLoudness > m.momentaryMax {
				m.momentaryMax = momentaryLoudness
			}
		}

		if m.shortTermFilled == m.shortTermSize {
			shortTermLoudness := -0.691 + 10*math.Log10(m.shortTermSum/float64(m.shortTermSize))
			m.shortTermPowers = append(m.shortTermPowers, m.shortTermSum/float64(m.shortTermSize))

			if shortTermLoudness > m.shortTermMax {
				m.shortTermMax = shortTermLoudness
			}
		}
	}
}

// finalize handles the final partial DR block and computes all results.
func (m *meter) finalize() *types.LoudnessResult {
	// Handle final partial DR block.
	if m.blockSamples > m.sampleRate { // at least 1 second
		rms := math.Sqrt(m.blockSum / float64(m.blockSamples))
		m.drBlocks = append(m.drBlocks, drBlock{m.blockPeak, rms})
	}

	integratedLUFS := calculateIntegratedLoudness(m.momentaryPowers)
	lra := calculateLoudnessRange(m.shortTermPowers)
	drScore, drValue, peakDb, rmsDb := calculateDR(m.drBlocks)

	return &types.LoudnessResult{
		IntegratedLUFS: integratedLUFS,
		ShortTermMax:   m.shortTermMax,
		MomentaryMax:   m.momentaryMax,
		LoudnessRange:  lra,
		DRScore:        drScore,
		DRValue:        drValue,
		PeakDb:         peakDb,
		RmsDb:          rmsDb,
		Frames:         m.totalFrames,
	}
}

func Analyze(reader io.Reader, format types.PCMFormat) (*types.LoudnessResult, error) {
	bytesPerSample := int(format.BitDepth / 8) //nolint:gosec // bit depth and channel count are small constants
	numChannels := int(format.Channels)        //nolint:gosec // bit depth and channel count are small constants
	frameSize := bytesPerSample * numChannels
	sampleRate := format.SampleRate

	buf := make([]byte, frameSize*4096)

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

	measurement := newMeter(sampleRate, numChannels)

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := buf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += frameSize {
					for ch := range numChannels {
						measurement.frameSamples[ch] = float64(int16(binary.LittleEndian.Uint16(data[i+ch*2:]))) / maxVal //nolint:gosec // two's complement conversion for signed PCM samples
					}

					measurement.processFrame()
				}
			case types.Depth24:
				for i := 0; i < len(data); i += frameSize {
					for channel := range numChannels {
						offset := i + channel*3

						raw := int32(data[offset]) | int32(data[offset+1])<<8 | int32(data[offset+2])<<16
						if raw&0x800000 != 0 {
							raw |= ^0xFFFFFF
						}

						measurement.frameSamples[channel] = float64(raw) / maxVal
					}

					measurement.processFrame()
				}
			case types.Depth32:
				for i := 0; i < len(data); i += frameSize {
					for ch := range numChannels {
						measurement.frameSamples[ch] = float64(int32(binary.LittleEndian.Uint32(data[i+ch*4:]))) / maxVal //nolint:gosec // two's complement conversion for signed PCM samples
					}

					measurement.processFrame()
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

	return measurement.finalize(), nil
}

func calculateIntegratedLoudness(powers []float64) float64 {
	if len(powers) == 0 {
		return -120
	}

	// First pass: absolute gate at -70 LUFS
	var (
		sum   float64
		count int
	)

	for _, p := range powers {
		lufs := -0.691 + 10*math.Log10(p)
		if lufs > -70 {
			sum += p
			count++
		}
	}

	if count == 0 {
		return -120
	}

	// Relative threshold: -10 LU below ungated mean
	ungatedMean := sum / float64(count)
	relativeThreshold := -0.691 + 10*math.Log10(ungatedMean) - 10

	// Second pass: relative gate
	sum = 0
	count = 0

	for _, p := range powers {
		lufs := -0.691 + 10*math.Log10(p)
		if lufs > relativeThreshold {
			sum += p
			count++
		}
	}

	if count == 0 {
		return -120
	}

	return -0.691 + 10*math.Log10(sum/float64(count))
}

func calculateLoudnessRange(powers []float64) float64 {
	if len(powers) < 2 {
		return 0
	}

	// Convert to LUFS and filter by absolute gate
	var lufsValues []float64

	for _, p := range powers {
		lufs := -0.691 + 10*math.Log10(p)
		if lufs > -70 {
			lufsValues = append(lufsValues, lufs)
		}
	}

	if len(lufsValues) < 2 {
		return 0
	}

	// Relative gate at -20 LU below ungated mean
	var sum float64
	for _, l := range lufsValues {
		sum += l
	}

	mean := sum / float64(len(lufsValues))
	relativeThreshold := mean - 20

	var gated []float64

	for _, l := range lufsValues {
		if l > relativeThreshold {
			gated = append(gated, l)
		}
	}

	if len(gated) < 2 {
		return 0
	}

	// LRA = difference between 95th and 10th percentile
	sort.Float64s(gated)
	low := gated[int(float64(len(gated))*0.10)]
	high := gated[int(float64(len(gated))*0.95)]

	return high - low
}

func calculateDR(blocks []drBlock) (score int, value, peakDb, rmsDb float64) {
	if len(blocks) == 0 {
		return 0, 0, -120, -120
	}

	// Sort blocks by peak (descending)
	peaksSorted := make([]float64, len(blocks))
	for i, b := range blocks {
		peaksSorted[i] = b.peak
	}

	sort.Sort(sort.Reverse(sort.Float64Slice(peaksSorted)))

	// Use second-highest peak (avoid outliers)
	peakIdx := 1
	if len(peaksSorted) == 1 {
		peakIdx = 0
	}

	peak := peaksSorted[peakIdx]

	// Sort blocks by RMS (descending)
	rmsSorted := make([]float64, len(blocks))
	for i, b := range blocks {
		rmsSorted[i] = b.rms
	}

	sort.Sort(sort.Reverse(sort.Float64Slice(rmsSorted)))

	// Average top 20% of RMS values
	top20Count := max(len(rmsSorted)/5, 1)

	var rmsSum float64
	for i := range top20Count {
		rmsSum += rmsSorted[i]
	}

	rms := rmsSum / float64(top20Count)

	if rms == 0 {
		return 0, 0, -120, -120
	}

	// DR = 20 * log10(peak / rms)
	dynamicRange := 20 * math.Log10(peak/rms)

	// Clamp to DR1-DR20
	score = min(max(int(math.Round(dynamicRange)), 1), 20)

	peakDb = 20 * math.Log10(peak)
	rmsDb = 20 * math.Log10(rms)

	return score, dynamicRange, peakDb, rmsDb
}
