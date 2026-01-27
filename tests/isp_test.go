package tests_test

import (
	"testing"
)

// TestInterSamplePeaks is a placeholder for inter-sample peak detection tests.
//
// ISP detection counts events where the reconstructed analog signal exceeds 0 dBTP
// (digital full scale). Generating such audio with ffmpeg is not currently possible:
// ffmpeg's synthesis sources (sine, noise) and limiters produce waveforms whose
// true peak stays below 0 dBTP. ISP events require specific inter-sample relationships
// (e.g. two near-full-scale samples with opposite polarity at near-Nyquist frequencies)
// that ffmpeg does not produce.
//
// Attempts that failed:
//   - Near-Nyquist sine wave at 0 dBFS: true peak -18.5 dBTP
//   - Multi-sine high amplitude: true peak -12.0 dBTP
//   - Limited audio at 0 dBFS (alimiter): true peak -8.1 dBTP
//
// A positive test requires either a pre-recorded file with known ISP events
// or programmatic sample-level construction of a WAV/FLAC with the right
// inter-sample relationships. This needs a dedicated agar fixture that writes
// raw PCM samples directly rather than using ffmpeg.
func TestInterSamplePeaks(t *testing.T) {
	t.Skip("blocked: no agar fixture can generate audio with true peak > 0 dBTP using ffmpeg")
}
