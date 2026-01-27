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
	bytesPerSample := int(format.BitDepth / 8)
	frameSize := bytesPerSample * int(format.Channels)
	buf := make([]byte, frameSize*4096)

	numChannels := int(format.Channels)
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
					ch := sampleIndex % numChannels
					sample := int16(binary.LittleEndian.Uint16(data[i:]))
					result.Samples++
					sampleIndex++

					if sample == max16 || sample == min16 {
						consecutive[ch]++
					} else {
						if consecutive[ch] >= 2 {
							result.Channels[ch].Events++

							result.Channels[ch].ClippedSamples += consecutive[ch]
							if consecutive[ch] > result.Channels[ch].LongestRun {
								result.Channels[ch].LongestRun = consecutive[ch]
							}

							result.Events++

							result.ClippedSamples += consecutive[ch]
							if consecutive[ch] > result.LongestRun {
								result.LongestRun = consecutive[ch]
							}
						}

						consecutive[ch] = 0
					}
				}
			case types.Depth24:
				for i := 0; i < len(data); i += 3 {
					ch := sampleIndex % numChannels

					sample := int32(data[i]) | int32(data[i+1])<<8 | int32(data[i+2])<<16
					if sample&0x800000 != 0 {
						sample |= ^0xFFFFFF
					}

					result.Samples++
					sampleIndex++

					if sample == max24 || sample == min24 {
						consecutive[ch]++
					} else {
						if consecutive[ch] >= 2 {
							result.Channels[ch].Events++

							result.Channels[ch].ClippedSamples += consecutive[ch]
							if consecutive[ch] > result.Channels[ch].LongestRun {
								result.Channels[ch].LongestRun = consecutive[ch]
							}

							result.Events++

							result.ClippedSamples += consecutive[ch]
							if consecutive[ch] > result.LongestRun {
								result.LongestRun = consecutive[ch]
							}
						}

						consecutive[ch] = 0
					}
				}
			case types.Depth32:
				for i := 0; i < len(data); i += 4 {
					ch := sampleIndex % numChannels
					sample := int32(binary.LittleEndian.Uint32(data[i:]))
					result.Samples++
					sampleIndex++

					if sample == max32 || sample == min32 {
						consecutive[ch]++
					} else {
						if consecutive[ch] >= 2 {
							result.Channels[ch].Events++

							result.Channels[ch].ClippedSamples += consecutive[ch]
							if consecutive[ch] > result.Channels[ch].LongestRun {
								result.Channels[ch].LongestRun = consecutive[ch]
							}

							result.Events++

							result.ClippedSamples += consecutive[ch]
							if consecutive[ch] > result.LongestRun {
								result.LongestRun = consecutive[ch]
							}
						}

						consecutive[ch] = 0
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
	for ch := range numChannels {
		if consecutive[ch] >= 2 {
			result.Channels[ch].Events++

			result.Channels[ch].ClippedSamples += consecutive[ch]
			if consecutive[ch] > result.Channels[ch].LongestRun {
				result.Channels[ch].LongestRun = consecutive[ch]
			}

			result.Events++

			result.ClippedSamples += consecutive[ch]
			if consecutive[ch] > result.LongestRun {
				result.LongestRun = consecutive[ch]
			}
		}
	}

	return result, nil
}
