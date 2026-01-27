package ffmpeg

import (
	"strconv"

	"github.com/farcloser/haustorium/internal/types"
)

func bitDepthToSpec(bitDepth types.BitDepth) string {
	// BitDepth 32 = s32le, 24 = s24le, 16 = s16le
	//nolint:gosec // we fine, gosec
	return "s" + strconv.Itoa(int(bitDepth)) + "le"
}
