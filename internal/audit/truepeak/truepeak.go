package truepeak

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/farcloser/primordium/fault"

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
	numChannels := int(format.Channels)       //nolint:gosec // bit depth and channel count are small constants
	frameSize := bytesPerSample * numChannels

	buf := make([]byte, frameSize*4096)

	var maxVal float64

	switch format.BitDepth {
	case types.Depth16:
		maxVal = 32768.0
	case types.Depth24:
		maxVal = 8388608.0
	case types.Depth32:
		maxVal = 2147483648.0
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
	)

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

								overshoot := 20 * math.Log10(absInterp)
								if overshoot > ispMax {
									ispMax = overshoot
								}
							}
						}
					}

					totalFrames++
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

								overshoot := 20 * math.Log10(absInterp)
								if overshoot > ispMax {
									ispMax = overshoot
								}
							}
						}
					}

					totalFrames++
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

								overshoot := 20 * math.Log10(absInterp)
								if overshoot > ispMax {
									ispMax = overshoot
								}
							}
						}
					}

					totalFrames++
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

	return &types.TruePeakResult{
		TruePeakDb:   truePeakDb,
		SamplePeakDb: samplePeakDb,
		ISPCount:     ispCount,
		ISPMaxDb:     ispMax,
		Frames:       totalFrames,
	}, nil
}
