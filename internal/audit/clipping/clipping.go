package clipping

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/farcloser/primordium/fault"

	"github.com/farcloser/haustorium/internal/types"
)

const (
	max16 = 1<<15 - 1 // 32767
	min16 = -1 << 15  // -32768
	max24 = 1<<23 - 1 // 8388607
	min24 = -1 << 23  // -8388608
	max32 = 1<<31 - 1 // 2147483647
	min32 = -1 << 31  // -2147483648
)

func Detect(r io.Reader, format types.PCMFormat) (*types.ClippingDetection, error) {
	bytesPerSample := int(format.BitDepth / 8) //nolint:gosec // bit depth and channel count are small constants
	frameSize := bytesPerSample * int(format.Channels) //nolint:gosec // bit depth and channel count are small constants
	buf := make([]byte, frameSize*4096)

	numChannels := int(format.Channels) //nolint:gosec // channel count is small
	result := &types.ClippingDetection{
		Channels: make([]types.ChannelClipping, numChannels),
	}
	consecutive := make([]uint64, numChannels)

	var sampleIndex int

	for {
		n, err := r.Read(buf)
		if n > 0 {
			completeSamples := (n / bytesPerSample) * bytesPerSample
			data := buf[:completeSamples]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += 2 {
					channel := sampleIndex % numChannels
					sample := int16(binary.LittleEndian.Uint16(data[i:])) //nolint:gosec // two's complement conversion for signed PCM samples
					result.Samples++
					sampleIndex++

					if sample == max16 || sample == min16 {
						consecutive[channel]++
					} else {
						if consecutive[channel] >= 2 {
							result.Channels[channel].Events++

							result.Channels[channel].ClippedSamples += consecutive[channel]
							if consecutive[channel] > result.Channels[channel].LongestRun {
								result.Channels[channel].LongestRun = consecutive[channel]
							}

							result.Events++

							result.ClippedSamples += consecutive[channel]
							if consecutive[channel] > result.LongestRun {
								result.LongestRun = consecutive[channel]
							}
						}

						consecutive[channel] = 0
					}
				}
			case types.Depth24:
				for i := 0; i < len(data); i += 3 {
					channel := sampleIndex % numChannels

					sample := int32(data[i]) | int32(data[i+1])<<8 | int32(data[i+2])<<16
					if sample&0x800000 != 0 {
						sample |= ^0xFFFFFF
					}

					result.Samples++
					sampleIndex++

					if sample == max24 || sample == min24 {
						consecutive[channel]++
					} else {
						if consecutive[channel] >= 2 {
							result.Channels[channel].Events++

							result.Channels[channel].ClippedSamples += consecutive[channel]
							if consecutive[channel] > result.Channels[channel].LongestRun {
								result.Channels[channel].LongestRun = consecutive[channel]
							}

							result.Events++

							result.ClippedSamples += consecutive[channel]
							if consecutive[channel] > result.LongestRun {
								result.LongestRun = consecutive[channel]
							}
						}

						consecutive[channel] = 0
					}
				}
			case types.Depth32:
				for i := 0; i < len(data); i += 4 {
					channel := sampleIndex % numChannels
					sample := int32(binary.LittleEndian.Uint32(data[i:])) //nolint:gosec // two's complement conversion for signed PCM samples
					result.Samples++
					sampleIndex++

					if sample == max32 || sample == min32 {
						consecutive[channel]++
					} else {
						if consecutive[channel] >= 2 {
							result.Channels[channel].Events++

							result.Channels[channel].ClippedSamples += consecutive[channel]
							if consecutive[channel] > result.Channels[channel].LongestRun {
								result.Channels[channel].LongestRun = consecutive[channel]
							}

							result.Events++

							result.ClippedSamples += consecutive[channel]
							if consecutive[channel] > result.LongestRun {
								result.LongestRun = consecutive[channel]
							}
						}

						consecutive[channel] = 0
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

	// Flush trailing clips for all channels
	for channel := range numChannels {
		if consecutive[channel] >= 2 {
			result.Channels[channel].Events++

			result.Channels[channel].ClippedSamples += consecutive[channel]
			if consecutive[channel] > result.Channels[channel].LongestRun {
				result.Channels[channel].LongestRun = consecutive[channel]
			}

			result.Events++

			result.ClippedSamples += consecutive[channel]
			if consecutive[channel] > result.LongestRun {
				result.LongestRun = consecutive[channel]
			}
		}
	}

	return result, nil
}
