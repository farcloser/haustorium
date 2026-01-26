package ffmpeg

import "time"

const (
	name    = "ffmpeg"
	timeout = 60 * time.Second
	codec   = "pcm_s32le"
)
