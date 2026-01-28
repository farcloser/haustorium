package truepeak

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/farcloser/primordium/fault"

	"github.com/farcloser/haustorium/internal/audit/shared"
	"github.com/farcloser/haustorium/internal/types"
)

const (
	oversample   = 4  // 4x oversampling per ITU-R BS.1770
	tapsPerPhase = 12 // filter taps per phase
	totalTaps    = oversample * tapsPerPhase
)

// Polyphase filter coefficients for 4x oversampling
// Generated from windowed sinc with Kaiser window (beta=5).
var polyphaseCoeffs [oversample][tapsPerPhase]float64

func init() {
	// Generate polyphase filter coefficients
	// Lowpass at 0.25 normalized frequency (Nyquist of original signal)
	beta := 5.0 // Kaiser window parameter

	for phase := range oversample {
		for tap := range tapsPerPhase {
			// Filter index in the full filter
			count := tap*oversample + phase
			center := float64(totalTaps-1) / 2.0

			// Sinc function
			sample := float64(count) - center

			var sinc float64
			if math.Abs(sample) < 1e-10 {
				sinc = 1.0
			} else {
				sinc = math.Sin(math.Pi*sample/float64(oversample)) / (math.Pi * sample / float64(oversample))
			}

			// Kaiser window
			alpha := (float64(count) - center) / center
			if math.Abs(alpha) <= 1.0 {
				window := bessel0(beta*math.Sqrt(1-alpha*alpha)) / bessel0(beta)
				polyphaseCoeffs[phase][tap] = sinc * window * float64(oversample)
			}
		}
	}

	// Normalize each phase
	for phase := range oversample {
		var sum float64
		for tap := range tapsPerPhase {
			sum += polyphaseCoeffs[phase][tap]
		}

		for tap := range tapsPerPhase {
			polyphaseCoeffs[phase][tap] /= sum
		}
	}
}

// Bessel function I0 (modified Bessel function of the first kind, order 0).
func bessel0(x float64) float64 {
	sum := 1.0

	term := 1.0
	for k := 1; k <= 25; k++ {
		term *= (x * x) / (4.0 * float64(k) * float64(k))

		sum += term
		if term < 1e-12 {
			break
		}
	}

	return sum
}

func Detect(r io.Reader, format types.PCMFormat) (*types.TruePeakResult, error) {
	bytesPerSample := int(format.BitDepth / 8) //nolint:gosec // bit depth and channel count are small constants
	numChannels := int(format.Channels)        //nolint:gosec // bit depth and channel count are small constants
	frameSize := bytesPerSample * numChannels

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

	// History buffers for each channel (for polyphase filter)
	history := make([][]float64, numChannels)
	for channel := range history {
		history[channel] = make([]float64, tapsPerPhase)
	}

	var (
		samplePeak  float64
		truePeak    float64
		ispCount    uint64
		ispMax      float64
		totalFrames uint64

		// Enhanced ISP tracking
		ispsAboveHalfdB uint64
		ispsAbove1dB    uint64
		ispsAbove2dB    uint64
	)

	// Density tracking: count ISPs per 1-second window
	samplesPerSecond := format.SampleRate
	windowISPCounts := []uint64{0}   // ISP count for each 1-second window
	currentWindowISPs := uint64(0)   // ISPs in current window
	currentWindowStart := uint64(0)  // frame where current window started

	for {
		n, err := r.Read(buf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := buf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += frameSize {
					for channel := range numChannels {
						sample := float64(int16(binary.LittleEndian.Uint16(data[i+channel*2:]))) / maxVal //nolint:gosec // two's complement conversion for signed PCM samples

						// Track sample peak
						absSample := math.Abs(sample)
						if absSample > samplePeak {
							samplePeak = absSample
						}

						// Shift history and add new sample
						copy(history[channel][0:], history[channel][1:])
						history[channel][tapsPerPhase-1] = sample

						// Compute interpolated samples at each phase
						for phase := range oversample {
							var interp float64
							for tap := range tapsPerPhase {
								interp += history[channel][tap] * polyphaseCoeffs[phase][tap]
							}

							absInterp := math.Abs(interp)
							if absInterp > truePeak {
								truePeak = absInterp
							}

							// Count ISPs (peaks exceeding 0 dBFS)
							if absInterp > 1.0 {
								ispCount++
								currentWindowISPs++

								overshoot := 20 * math.Log10(absInterp)
								if overshoot > ispMax {
									ispMax = overshoot
								}

								// Track by magnitude threshold
								if overshoot > 0.5 {
									ispsAboveHalfdB++
								}
								if overshoot > 1.0 {
									ispsAbove1dB++
								}
								if overshoot > 2.0 {
									ispsAbove2dB++
								}
							}
						}
					}

					totalFrames++

					// Check if we've completed a 1-second window
					if totalFrames-currentWindowStart >= uint64(samplesPerSecond) {
						windowISPCounts = append(windowISPCounts, currentWindowISPs)
						currentWindowISPs = 0
						currentWindowStart = totalFrames
					}
				}
			case types.Depth24:
				for i := 0; i < len(data); i += frameSize {
					for channel := range numChannels {
						offset := i + channel*3

						raw := int32(data[offset]) | int32(data[offset+1])<<8 | int32(data[offset+2])<<16
						if raw&0x800000 != 0 {
							raw |= ^0xFFFFFF
						}

						sample := float64(raw) / maxVal

						absSample := math.Abs(sample)
						if absSample > samplePeak {
							samplePeak = absSample
						}

						copy(history[channel][0:], history[channel][1:])
						history[channel][tapsPerPhase-1] = sample

						for phase := range oversample {
							var interp float64
							for tap := range tapsPerPhase {
								interp += history[channel][tap] * polyphaseCoeffs[phase][tap]
							}

							absInterp := math.Abs(interp)
							if absInterp > truePeak {
								truePeak = absInterp
							}

							if absInterp > 1.0 {
								ispCount++
								currentWindowISPs++

								overshoot := 20 * math.Log10(absInterp)
								if overshoot > ispMax {
									ispMax = overshoot
								}

								if overshoot > 0.5 {
									ispsAboveHalfdB++
								}
								if overshoot > 1.0 {
									ispsAbove1dB++
								}
								if overshoot > 2.0 {
									ispsAbove2dB++
								}
							}
						}
					}

					totalFrames++

					if totalFrames-currentWindowStart >= uint64(samplesPerSecond) {
						windowISPCounts = append(windowISPCounts, currentWindowISPs)
						currentWindowISPs = 0
						currentWindowStart = totalFrames
					}
				}
			case types.Depth32:
				for i := 0; i < len(data); i += frameSize {
					for channel := range numChannels {
						sample := float64(int32(binary.LittleEndian.Uint32(data[i+channel*4:]))) / maxVal //nolint:gosec // two's complement conversion for signed PCM samples

						absSample := math.Abs(sample)
						if absSample > samplePeak {
							samplePeak = absSample
						}

						copy(history[channel][0:], history[channel][1:])
						history[channel][tapsPerPhase-1] = sample

						for phase := range oversample {
							var interp float64
							for tap := range tapsPerPhase {
								interp += history[channel][tap] * polyphaseCoeffs[phase][tap]
							}

							absInterp := math.Abs(interp)
							if absInterp > truePeak {
								truePeak = absInterp
							}

							if absInterp > 1.0 {
								ispCount++
								currentWindowISPs++

								overshoot := 20 * math.Log10(absInterp)
								if overshoot > ispMax {
									ispMax = overshoot
								}

								if overshoot > 0.5 {
									ispsAboveHalfdB++
								}
								if overshoot > 1.0 {
									ispsAbove1dB++
								}
								if overshoot > 2.0 {
									ispsAbove2dB++
								}
							}
						}
					}

					totalFrames++

					if totalFrames-currentWindowStart >= uint64(samplesPerSecond) {
						windowISPCounts = append(windowISPCounts, currentWindowISPs)
						currentWindowISPs = 0
						currentWindowStart = totalFrames
					}
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

	samplePeakDb := -120.0
	if samplePeak > 0 {
		samplePeakDb = 20 * math.Log10(samplePeak)
	}

	truePeakDb := -120.0
	if truePeak > 0 {
		truePeakDb = 20 * math.Log10(truePeak)
	}

	// Add any remaining ISPs from the last partial window
	if currentWindowISPs > 0 {
		windowISPCounts = append(windowISPCounts, currentWindowISPs)
	}

	// Calculate density metrics
	var ispDensityPeak float64
	var worstDensitySec float64

	for i, count := range windowISPCounts {
		density := float64(count) // ISPs per second (window is 1 second)
		if density > ispDensityPeak {
			ispDensityPeak = density
			worstDensitySec = float64(i) // window index = second
		}
	}

	var ispDensityAvg float64
	if totalFrames > 0 {
		durationSec := float64(totalFrames) / float64(samplesPerSecond)
		if durationSec > 0 {
			ispDensityAvg = float64(ispCount) / durationSec
		}
	}

	return &types.TruePeakResult{
		TruePeakDb:   truePeakDb,
		SamplePeakDb: samplePeakDb,
		ISPCount:     ispCount,
		ISPMaxDb:     ispMax,
		Frames:       totalFrames,

		ISPDensityPeak:  ispDensityPeak,
		ISPDensityAvg:   ispDensityAvg,
		ISPsAboveHalfdB: ispsAboveHalfdB,
		ISPsAbove1dB:    ispsAbove1dB,
		ISPsAbove2dB:    ispsAbove2dB,
		WorstDensitySec: worstDensitySec,
	}, nil
}
