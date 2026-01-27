package ffprobe

import "time"

const (
	name = "ffprobe"
	// Slow hard-drives spinning up or network retrieved resources may cause timeouts if too aggressive.
	timeout = 60 * time.Second
)
