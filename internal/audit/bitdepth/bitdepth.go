package bitdepth

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/farcloser/primordium/fault"

	"github.com/farcloser/haustorium/internal/types"
)

const (
	genuineMask24 = 0xFF
	genuineMask32 = 0xFFFF
)

// Authenticity detects if audio is zero-padded to a higher bit depth.
// A "24-bit" file that's really 16-bit will have lower 8 bits always zero.
func Authenticity(r io.Reader, format types.PCMFormat) (*types.BitDepthAuthenticity, error) {
	if format.BitDepth == types.Depth16 {
		return &types.BitDepthAuthenticity{
			Claimed:   format.BitDepth,
			Effective: format.BitDepth,
			IsPadded:  false,
			Samples:   0,
		}, nil
	}

	bytesPerSample := int(format.BitDepth / 8)
	frameSize := bytesPerSample * int(format.Channels)
	buf := make([]byte, frameSize*4096)

	var usedBits uint32
	var samples uint64

	// Mask for early exit: all lower bits set = genuine
	var genuineMask uint32
	if format.BitDepth == types.Depth24 {
		genuineMask = genuineMask24
	} else {
		genuineMask = genuineMask32
	}

	for {
		n, err := r.Read(buf)
		if n > 0 {
			completeSamples := (n / bytesPerSample) * bytesPerSample
			data := buf[:completeSamples]

			switch format.BitDepth {
			case types.Depth24:
				for i := 0; i < len(data); i += 3 {
					sample := uint32(data[i]) | uint32(data[i+1])<<8 | uint32(data[i+2])<<16
					usedBits |= sample
					samples++
				}
			case types.Depth32:
				for i := 0; i < len(data); i += 4 {
					usedBits |= binary.LittleEndian.Uint32(data[i:])
					samples++
				}
			}

			if usedBits&genuineMask == genuineMask {
				return &types.BitDepthAuthenticity{
					Claimed:   format.BitDepth,
					Effective: format.BitDepth,
					IsPadded:  false,
					Samples:   samples,
				}, nil
			}
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("%w: %w", fault.ErrReadFailure, err)
		}
	}

	effective := effectiveBitDepth(usedBits, format.BitDepth)

	return &types.BitDepthAuthenticity{
		Claimed:   format.BitDepth,
		Effective: effective,
		IsPadded:  effective < format.BitDepth,
		Samples:   samples,
	}, nil
}

func effectiveBitDepth(usedBits uint32, claimed types.BitDepth) types.BitDepth {
	switch claimed {
	case types.Depth24:
		if usedBits&genuineMask24 == 0 {
			return types.Depth16
		}
	case types.Depth32:
		if usedBits&genuineMask32 == 0 {
			return types.Depth16
		}
		if usedBits&genuineMask24 == 0 {
			return types.Depth24
		}
	default:
	}

	return claimed
}
