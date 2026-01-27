package loudness

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"

	"github.com/farcloser/primordium/fault"

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
func getKWeightingFilters(sampleRate int) (pre, rlb biquad) {
	// Coefficients from ITU-R BS.1770-4
	// These are computed from the analog prototype transfer functions
	fs := float64(sampleRate)

	// Pre-filter (high shelf)
	// Models the acoustic effects of the head
	f0 := 1681.974450955533
	G := 3.999843853973347
	Q := 0.7071752369554196

	K := math.Tan(math.Pi * f0 / fs)
	Vh := math.Pow(10, G/20)
	Vb := math.Pow(Vh, 0.4996667741545416)

	a0 := 1 + K/Q + K*K
	pre.b0 = (Vh + Vb*K/Q + K*K) / a0
	pre.b1 = 2 * (K*K - Vh) / a0
	pre.b2 = (Vh - Vb*K/Q + K*K) / a0
	pre.a1 = 2 * (K*K - 1) / a0
	pre.a2 = (1 - K/Q + K*K) / a0

	// RLB weighting (high pass)
	f0 = 38.13547087602444
	Q = 0.5003270373238773

	K = math.Tan(math.Pi * f0 / fs)

	a0 = 1 + K/Q + K*K
	rlb.b0 = 1 / a0
	rlb.b1 = -2 / a0
	rlb.b2 = 1 / a0
	rlb.a1 = 2 * (K*K - 1) / a0
	rlb.a2 = (1 - K/Q + K*K) / a0

	return pre, rlb
}

// Channel weights for surround (we only handle stereo for now).
func getChannelWeight(ch, numChannels int) float64 {
	if numChannels <= 2 {
		return 1.0
	}
	// For surround: L, R, C = 1.0; Ls, Rs = 1.41 (~+1.5dB)
	// LFE is excluded
	if ch >= 3 && ch <= 4 && numChannels > 4 {
		return 1.41
	}

	return 1.0
}

func Analyze(r io.Reader, format types.PCMFormat) (*types.LoudnessResult, error) {
	bytesPerSample := int(format.BitDepth / 8)
	numChannels := int(format.Channels)
	frameSize := bytesPerSample * numChannels
	sampleRate := int(format.SampleRate)

	buf := make([]byte, frameSize*4096)

	var maxVal float64

	switch format.BitDepth {
	case types.Depth16:
		maxVal = 32768.0
	case types.Depth24:
		maxVal = 8388608.0
	case types.Depth32:
		maxVal = 2147483648.0
	}

	// K-weighting filters per channel
	pre, rlb := getKWeightingFilters(sampleRate)
	preState := make([]biquadState, numChannels)
	rlbState := make([]biquadState, numChannels)

	// Window sizes in samples
	momentarySize := sampleRate * 400 / 1000 // 400ms
	shortTermSize := sampleRate * 3          // 3s
	blockSize := sampleRate * 3              // 3s blocks for DR calculation
	hopSize := sampleRate * 100 / 1000       // 100ms hop for LUFS windows

	// Accumulators for gated loudness
	var momentaryPowers []float64 // for integrated calculation

	var shortTermPowers []float64 // for LRA calculation

	var (
		momentaryMax float64 = -120
		shortTermMax float64 = -120
	)

	// Ring buffers for windowed measurements
	momentaryBuf := make([]float64, momentarySize)
	shortTermBuf := make([]float64, shortTermSize)

	var (
		momentaryPos, shortTermPos       int
		momentarySum, shortTermSum       float64
		momentaryFilled, shortTermFilled int
	)

	// DR calculation: 3s blocks
	var (
		drBlocks []struct {
			peak float64
			rms  float64
		}
		blockSum     float64
		blockPeak    float64
		blockSamples int
	)

	var (
		sampleCount int
		totalFrames uint64
	)

	for {
		n, err := r.Read(buf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := buf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += frameSize {
					var (
						framePower float64
						framePeak  float64
					)

					for ch := range numChannels {
						sample := float64(int16(binary.LittleEndian.Uint16(data[i+ch*2:]))) / maxVal

						// Track peak for DR
						if abs := math.Abs(sample); abs > framePeak {
							framePeak = abs
						}

						// Apply K-weighting
						filtered := preState[ch].process(&pre, sample)
						filtered = rlbState[ch].process(&rlb, filtered)

						// Accumulate weighted power
						weight := getChannelWeight(ch, numChannels)
						framePower += weight * filtered * filtered
					}

					// Update DR block
					blockSum += framePower / float64(numChannels)

					if framePeak > blockPeak {
						blockPeak = framePeak
					}

					blockSamples++

					if blockSamples >= blockSize {
						rms := math.Sqrt(blockSum / float64(blockSamples))
						drBlocks = append(drBlocks, struct{ peak, rms float64 }{blockPeak, rms})
						blockSum = 0
						blockPeak = 0
						blockSamples = 0
					}

					// Update momentary window (ring buffer)
					old := momentaryBuf[momentaryPos]
					momentaryBuf[momentaryPos] = framePower
					momentarySum = momentarySum - old + framePower

					momentaryPos = (momentaryPos + 1) % momentarySize
					if momentaryFilled < momentarySize {
						momentaryFilled++
					}

					// Update short-term window (ring buffer)
					old = shortTermBuf[shortTermPos]
					shortTermBuf[shortTermPos] = framePower
					shortTermSum = shortTermSum - old + framePower

					shortTermPos = (shortTermPos + 1) % shortTermSize
					if shortTermFilled < shortTermSize {
						shortTermFilled++
					}

					sampleCount++
					totalFrames++

					// Every hop, calculate windowed loudness
					if sampleCount%hopSize == 0 {
						if momentaryFilled == momentarySize {
							momentaryLoudness := -0.691 + 10*math.Log10(momentarySum/float64(momentarySize))
							momentaryPowers = append(momentaryPowers, momentarySum/float64(momentarySize))

							if momentaryLoudness > momentaryMax {
								momentaryMax = momentaryLoudness
							}
						}

						if shortTermFilled == shortTermSize {
							shortTermLoudness := -0.691 + 10*math.Log10(shortTermSum/float64(shortTermSize))
							shortTermPowers = append(shortTermPowers, shortTermSum/float64(shortTermSize))

							if shortTermLoudness > shortTermMax {
								shortTermMax = shortTermLoudness
							}
						}
					}
				}
			case types.Depth24:
				for i := 0; i < len(data); i += frameSize {
					var (
						framePower float64
						framePeak  float64
					)

					for ch := range numChannels {
						offset := i + ch*3

						raw := int32(data[offset]) | int32(data[offset+1])<<8 | int32(data[offset+2])<<16
						if raw&0x800000 != 0 {
							raw |= ^0xFFFFFF
						}

						sample := float64(raw) / maxVal

						if abs := math.Abs(sample); abs > framePeak {
							framePeak = abs
						}

						filtered := preState[ch].process(&pre, sample)
						filtered = rlbState[ch].process(&rlb, filtered)

						weight := getChannelWeight(ch, numChannels)
						framePower += weight * filtered * filtered
					}

					blockSum += framePower / float64(numChannels)

					if framePeak > blockPeak {
						blockPeak = framePeak
					}

					blockSamples++

					if blockSamples >= blockSize {
						rms := math.Sqrt(blockSum / float64(blockSamples))
						drBlocks = append(drBlocks, struct{ peak, rms float64 }{blockPeak, rms})
						blockSum = 0
						blockPeak = 0
						blockSamples = 0
					}

					old := momentaryBuf[momentaryPos]
					momentaryBuf[momentaryPos] = framePower
					momentarySum = momentarySum - old + framePower

					momentaryPos = (momentaryPos + 1) % momentarySize
					if momentaryFilled < momentarySize {
						momentaryFilled++
					}

					old = shortTermBuf[shortTermPos]
					shortTermBuf[shortTermPos] = framePower
					shortTermSum = shortTermSum - old + framePower

					shortTermPos = (shortTermPos + 1) % shortTermSize
					if shortTermFilled < shortTermSize {
						shortTermFilled++
					}

					sampleCount++
					totalFrames++

					if sampleCount%hopSize == 0 {
						if momentaryFilled == momentarySize {
							momentaryLoudness := -0.691 + 10*math.Log10(momentarySum/float64(momentarySize))
							momentaryPowers = append(momentaryPowers, momentarySum/float64(momentarySize))

							if momentaryLoudness > momentaryMax {
								momentaryMax = momentaryLoudness
							}
						}

						if shortTermFilled == shortTermSize {
							shortTermLoudness := -0.691 + 10*math.Log10(shortTermSum/float64(shortTermSize))
							shortTermPowers = append(shortTermPowers, shortTermSum/float64(shortTermSize))

							if shortTermLoudness > shortTermMax {
								shortTermMax = shortTermLoudness
							}
						}
					}
				}
			case types.Depth32:
				for i := 0; i < len(data); i += frameSize {
					var (
						framePower float64
						framePeak  float64
					)

					for ch := range numChannels {
						sample := float64(int32(binary.LittleEndian.Uint32(data[i+ch*4:]))) / maxVal

						if abs := math.Abs(sample); abs > framePeak {
							framePeak = abs
						}

						filtered := preState[ch].process(&pre, sample)
						filtered = rlbState[ch].process(&rlb, filtered)

						weight := getChannelWeight(ch, numChannels)
						framePower += weight * filtered * filtered
					}

					blockSum += framePower / float64(numChannels)

					if framePeak > blockPeak {
						blockPeak = framePeak
					}

					blockSamples++

					if blockSamples >= blockSize {
						rms := math.Sqrt(blockSum / float64(blockSamples))
						drBlocks = append(drBlocks, struct{ peak, rms float64 }{blockPeak, rms})
						blockSum = 0
						blockPeak = 0
						blockSamples = 0
					}

					old := momentaryBuf[momentaryPos]
					momentaryBuf[momentaryPos] = framePower
					momentarySum = momentarySum - old + framePower

					momentaryPos = (momentaryPos + 1) % momentarySize
					if momentaryFilled < momentarySize {
						momentaryFilled++
					}

					old = shortTermBuf[shortTermPos]
					shortTermBuf[shortTermPos] = framePower
					shortTermSum = shortTermSum - old + framePower

					shortTermPos = (shortTermPos + 1) % shortTermSize
					if shortTermFilled < shortTermSize {
						shortTermFilled++
					}

					sampleCount++
					totalFrames++

					if sampleCount%hopSize == 0 {
						if momentaryFilled == momentarySize {
							momentaryLoudness := -0.691 + 10*math.Log10(momentarySum/float64(momentarySize))
							momentaryPowers = append(momentaryPowers, momentarySum/float64(momentarySize))

							if momentaryLoudness > momentaryMax {
								momentaryMax = momentaryLoudness
							}
						}

						if shortTermFilled == shortTermSize {
							shortTermLoudness := -0.691 + 10*math.Log10(shortTermSum/float64(shortTermSize))
							shortTermPowers = append(shortTermPowers, shortTermSum/float64(shortTermSize))

							if shortTermLoudness > shortTermMax {
								shortTermMax = shortTermLoudness
							}
						}
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

	// Handle final partial DR block
	if blockSamples > sampleRate { // at least 1 second
		rms := math.Sqrt(blockSum / float64(blockSamples))
		drBlocks = append(drBlocks, struct{ peak, rms float64 }{blockPeak, rms})
	}

	// Calculate integrated loudness (EBU R128 gating)
	integratedLUFS := calculateIntegratedLoudness(momentaryPowers)

	// Calculate loudness range (LRA)
	lra := calculateLoudnessRange(shortTermPowers)

	// Calculate DR score
	drScore, drValue, peakDb, rmsDb := calculateDR(drBlocks)

	return &types.LoudnessResult{
		IntegratedLUFS: integratedLUFS,
		ShortTermMax:   shortTermMax,
		MomentaryMax:   momentaryMax,
		LoudnessRange:  lra,
		DRScore:        drScore,
		DRValue:        drValue,
		PeakDb:         peakDb,
		RmsDb:          rmsDb,
		Frames:         totalFrames,
	}, nil
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

func calculateDR(blocks []struct{ peak, rms float64 }) (score int, value, peakDb, rmsDb float64) {
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
	for i := 0; i < top20Count; i++ {
		rmsSum += rmsSorted[i]
	}

	rms := rmsSum / float64(top20Count)

	if rms == 0 {
		return 0, 0, -120, -120
	}

	// DR = 20 * log10(peak / rms)
	dr := 20 * math.Log10(peak/rms)

	// Clamp to DR1-DR20
	score = max(int(math.Round(dr)), 1)

	if score > 20 {
		score = 20
	}

	peakDb = 20 * math.Log10(peak)
	rmsDb = 20 * math.Log10(rms)

	return score, dr, peakDb, rmsDb
}
