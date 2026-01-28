package truncation

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
	defaultWindowMs uint = 50
)

func Detect(r io.ReadSeeker, format types.PCMFormat, windowMs uint) (*types.TruncationDetection, error) {
	if windowMs == 0 {
		windowMs = defaultWindowMs
	}

	bytesPerSample := int(format.BitDepth / 8) //nolint:gosec // bit depth and channel count are small constants
	tailSamples := format.SampleRate * int(windowMs) / 1000 * int(format.Channels) //nolint:gosec // bit depth and channel count are small constants
	tailBytes := int64(tailSamples * bytesPerSample)

	// Seek to end minus tail size
	_, err := r.Seek(-tailBytes, io.SeekEnd)
	if err != nil {
		// File shorter than tail window, seek to start
		_, err = r.Seek(0, io.SeekStart)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", fault.ErrReadFailure, err)
		}
	}

	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", fault.ErrReadFailure, err)
	}

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

	var (
		sumSquares float64
		peak       float64
		count      uint64
	)

	completeSamples := (len(buf) / bytesPerSample) * bytesPerSample
	data := buf[:completeSamples]

	switch format.BitDepth {
	case types.Depth16:
		for i := 0; i < len(data); i += 2 {
			sample := int16(binary.LittleEndian.Uint16(data[i:])) //nolint:gosec // two's complement conversion for signed PCM samples
			normalized := float64(sample) / maxVal

			sumSquares += normalized * normalized
			if abs := math.Abs(normalized); abs > peak {
				peak = abs
			}

			count++
		}
	case types.Depth24:
		for i := 0; i < len(data); i += 3 {
			sample := int32(data[i]) | int32(data[i+1])<<8 | int32(data[i+2])<<16
			if sample&0x800000 != 0 {
				sample |= ^0xFFFFFF
			}

			normalized := float64(sample) / maxVal

			sumSquares += normalized * normalized
			if abs := math.Abs(normalized); abs > peak {
				peak = abs
			}

			count++
		}
	case types.Depth32:
		for i := 0; i < len(data); i += 4 {
			sample := int32(binary.LittleEndian.Uint32(data[i:])) //nolint:gosec // two's complement conversion for signed PCM samples
			normalized := float64(sample) / maxVal

			sumSquares += normalized * normalized
			if abs := math.Abs(normalized); abs > peak {
				peak = abs
			}

			count++
		}
	default:
	}

	if count == 0 {
		return &types.TruncationDetection{
			IsTruncated:   false,
			FinalRmsDb:    -120.0,
			FinalPeakDb:   -120.0,
			SamplesInTail: 0,
		}, nil
	}

	rms := math.Sqrt(sumSquares / float64(count))
	rmsDb := 20 * math.Log10(rms)
	peakDb := 20 * math.Log10(peak)

	if math.IsInf(rmsDb, -1) {
		rmsDb = -120.0
	}

	if math.IsInf(peakDb, -1) {
		peakDb = -120.0
	}

	return &types.TruncationDetection{
		FinalRmsDb:    rmsDb,
		FinalPeakDb:   peakDb,
		SamplesInTail: count,
	}, nil
}
