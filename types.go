package haustorium

import "github.com/farcloser/haustorium/internal/types"

// AnalysisOptions configures audio analysis parameters.
type AnalysisOptions struct {
	// SilenceThresholdDB is the noise floor for silence detection (default: -60).
	// Audio below this level is considered silence.
	SilenceThresholdDB int

	// SilenceDurationSec is the minimum silence duration to report (default: 2).
	// Silence periods shorter than this are ignored.
	SilenceDurationSec int
}

// DefaultAnalysisOptions returns sensible defaults for audio analysis.
func DefaultAnalysisOptions() AnalysisOptions {
	return AnalysisOptions{
		SilenceThresholdDB: -60,
		SilenceDurationSec: 5,
	}
}

// SilenceInterval represents a detected silence period.
type SilenceInterval struct {
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Duration float64 `json:"duration"`
}

// AnalysisResult contains comprehensive audio analysis.
type AnalysisResult struct {
	// EBU R128 loudness
	IntegratedLoudness float64 `json:"integrated_loudness"` // LUFS
	LoudnessRange      float64 `json:"loudness_range"`      // LU
	LRALow             float64 `json:"lra_low"`             // LUFS
	LRAHigh            float64 `json:"lra_high"`            // LUFS
	TruePeak           float64 `json:"true_peak"`           // dBFS
	SamplePeak         float64 `json:"sample_peak"`         // dBFS

	// Clipping indicators
	PeakCount   int64   `json:"peak_count"`    // samples at 0dBFS
	FlatFactor  float64 `json:"flat_factor"`   // per-channel max flat factor (>0 suggests clipping)
	DCOffset    float64 `json:"dc_offset"`     // DC offset (should be near 0)
	DCOffsetMax float64 `json:"dc_offset_max"` // max DC offset across channels

	// Silence detection
	SilenceIntervals []SilenceInterval `json:"silence_intervals,omitempty"`
	HasSilence       bool              `json:"has_silence"`

	// Truncation (abrupt ending)
	TruncationDetected bool    `json:"truncation_detected"`
	EndSilenceDuration float64 `json:"end_silence_duration"` // silence at end (0 = abrupt)

	// Stereo issues
	IsFakeStereo         bool    `json:"is_fake_stereo"`       // mono masquerading as stereo
	FakeStereoRMSDiff    float64 `json:"fake_stereo_rms_diff"` // RMS of L-R difference
	HasPhaseCancellation bool    `json:"has_phase_cancellation"`
	PhaseSumRMS          float64 `json:"phase_sum_rms"` // RMS when summed to mono

	// Cheats
	BitDepthAuthenticity     *types.BitDepthAuthenticity
	SampleRateAuthentic      bool `json:"sample_rate_authentic"`
	SampleRateAuthenticError bool `json:"sample_rate_authentic_error"`

	Spectrogram      []byte
	SpectrogramError bool
}
