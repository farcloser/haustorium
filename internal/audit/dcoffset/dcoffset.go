package dcoffset

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/farcloser/primordium/fault"

	"github.com/farcloser/haustorium/internal/audit/shared"
	"github.com/farcloser/haustorium/internal/types"
)

func Detect(reader io.Reader, format types.PCMFormat) (*types.DCOffsetResult, error) {
	bytesPerSample := int(format.BitDepth / 8) //nolint:gosec // bit depth and channel count are small constants
	frameSize := bytesPerSample * int(format.Channels) //nolint:gosec // bit depth and channel count are small constants
	buf := make([]byte, frameSize*4096)

	numChannels := int(format.Channels) //nolint:gosec // channel count is small
	channelSums := make([]float64, numChannels)

	var samples uint64

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

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			completeFrames := (n / frameSize) * frameSize
			data := buf[:completeFrames]

			switch format.BitDepth {
			case types.Depth16:
				for i := 0; i < len(data); i += 2 {
					channel := (i / 2) % numChannels
					sample := float64(int16(binary.LittleEndian.Uint16(data[i:]))) / maxVal //nolint:gosec // two's complement conversion for signed PCM samples
					channelSums[channel] += sample
					samples++
				}
			case types.Depth24:
				for i := 0; i < len(data); i += 3 {
					channel := (i / 3) % numChannels

					raw := int32(data[i]) | int32(data[i+1])<<8 | int32(data[i+2])<<16
					if raw&0x800000 != 0 {
						raw |= ^0xFFFFFF
					}

					sample := float64(raw) / maxVal
					channelSums[channel] += sample
					samples++
				}
			case types.Depth32:
				for i := 0; i < len(data); i += 4 {
					channel := (i / 4) % numChannels
					sample := float64(int32(binary.LittleEndian.Uint32(data[i:]))) / maxVal //nolint:gosec // two's complement conversion for signed PCM samples
					channelSums[channel] += sample
					samples++
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

	if samples == 0 {
		return &types.DCOffsetResult{
			Offset:   0,
			OffsetDb: -120.0,
			Channels: make([]float64, numChannels),
			Samples:  0,
		}, nil
	}

	samplesPerChannel := float64(samples) / float64(numChannels)
	channelOffsets := make([]float64, numChannels)

	var totalOffset float64

	for channel := range numChannels {
		channelOffsets[channel] = channelSums[channel] / samplesPerChannel
		totalOffset += math.Abs(channelOffsets[channel])
	}

	totalOffset /= float64(numChannels)

	offsetDb := 20 * math.Log10(totalOffset)
	if math.IsInf(offsetDb, -1) {
		offsetDb = -120.0
	}

	return &types.DCOffsetResult{
		Offset:   totalOffset,
		OffsetDb: offsetDb,
		Channels: channelOffsets,
		Samples:  samples,
	}, nil
}
