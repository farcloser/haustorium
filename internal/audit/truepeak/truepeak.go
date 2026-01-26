package truepeak

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/farcloser/haustorium/internal/types"
	"github.com/farcloser/primordium/fault"
)

const (
	oversample   = 4  // 4x oversampling per ITU-R BS.1770
	tapsPerPhase = 12 // filter taps per phase
	totalTaps    = oversample * tapsPerPhase
)

// Polyphase filter coefficients for 4x oversampling
// Generated from windowed sinc with Kaiser window (beta=5)
var polyphaseCoeffs [oversample][tapsPerPhase]float64

func init() {
	// Generate polyphase filter coefficients
	// Lowpass at 0.25 normalized frequency (Nyquist of original signal)
	beta := 5.0 // Kaiser window parameter

	for phase := 0; phase < oversample; phase++ {
		for tap := 0; tap < tapsPerPhase; tap++ {
			// Filter index in the full filter
			n := tap*oversample + phase
			center := float64(totalTaps-1) / 2.0

			// Sinc function
			x := float64(n) - center
			var sinc float64
			if math.Abs(x) < 1e-10 {
				sinc = 1.0
			} else {
				sinc = math.Sin(math.Pi*x/float64(oversample)) / (math.Pi * x / float64(oversample))
			}

			// Kaiser window
			alpha := (float64(n) - center) / center
			if math.Abs(alpha) <= 1.0 {
				window := bessel0(beta*math.Sqrt(1-alpha*alpha)) / bessel0(beta)
				polyphaseCoeffs[phase][tap] = sinc * window * float64(oversample)
			}
		}
	}

	// Normalize each phase
	for phase := 0; phase < oversample; phase++ {
		var sum float64
		for tap := 0; tap < tapsPerPhase; tap++ {
			sum += polyphaseCoeffs[phase][tap]
		}
		for tap := 0; tap < tapsPerPhase; tap++ {
			polyphaseCoeffs[phase][tap] /= sum
		}
	}
}

// Bessel function I0 (modified Bessel function of the first kind, order 0)
func bessel0(x float64) float64 {
	var sum float64 = 1.0
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
	bytesPerSample := int(format.BitDepth / 8)
	numChannels := int(format.Channels)
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
	}

	// History buffers for each channel (for polyphase filter)
	history := make([][]float64, numChannels)
	for ch := range history {
		history[ch] = make([]float64, tapsPerPhase)
	}

	var samplePeak float64
	var truePeak float64
	var ispCount uint64
	var ispMax float64
	var totalFrames uint64

	for {
		n, err := r.Read(buf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := buf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += frameSize {
					for ch := 0; ch < numChannels; ch++ {
						sample := float64(int16(binary.LittleEndian.Uint16(data[i+ch*2:]))) / maxVal

						// Track sample peak
						absSample := math.Abs(sample)
						if absSample > samplePeak {
							samplePeak = absSample
						}

						// Shift history and add new sample
						copy(history[ch][0:], history[ch][1:])
						history[ch][tapsPerPhase-1] = sample

						// Compute interpolated samples at each phase
						for phase := 0; phase < oversample; phase++ {
							var interp float64
							for tap := 0; tap < tapsPerPhase; tap++ {
								interp += history[ch][tap] * polyphaseCoeffs[phase][tap]
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
					for ch := 0; ch < numChannels; ch++ {
						offset := i + ch*3
						raw := int32(data[offset]) | int32(data[offset+1])<<8 | int32(data[offset+2])<<16
						if raw&0x800000 != 0 {
							raw |= ^0xFFFFFF
						}
						sample := float64(raw) / maxVal

						absSample := math.Abs(sample)
						if absSample > samplePeak {
							samplePeak = absSample
						}

						copy(history[ch][0:], history[ch][1:])
						history[ch][tapsPerPhase-1] = sample

						for phase := 0; phase < oversample; phase++ {
							var interp float64
							for tap := 0; tap < tapsPerPhase; tap++ {
								interp += history[ch][tap] * polyphaseCoeffs[phase][tap]
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
					for ch := 0; ch < numChannels; ch++ {
						sample := float64(int32(binary.LittleEndian.Uint32(data[i+ch*4:]))) / maxVal

						absSample := math.Abs(sample)
						if absSample > samplePeak {
							samplePeak = absSample
						}

						copy(history[ch][0:], history[ch][1:])
						history[ch][tapsPerPhase-1] = sample

						for phase := 0; phase < oversample; phase++ {
							var interp float64
							for tap := 0; tap < tapsPerPhase; tap++ {
								interp += history[ch][tap] * polyphaseCoeffs[phase][tap]
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
