package haustorium

import (
	"fmt"
	"io"

	"github.com/farcloser/haustorium/internal/audit/bitdepth"
	"github.com/farcloser/haustorium/internal/audit/clipping"
	"github.com/farcloser/haustorium/internal/audit/dcoffset"
	"github.com/farcloser/haustorium/internal/audit/dropout"
	"github.com/farcloser/haustorium/internal/audit/loudness"
	"github.com/farcloser/haustorium/internal/audit/silence"
	"github.com/farcloser/haustorium/internal/audit/spectral"
	"github.com/farcloser/haustorium/internal/audit/stereo"
	"github.com/farcloser/haustorium/internal/audit/truepeak"
	"github.com/farcloser/haustorium/internal/audit/truncation"
	"github.com/farcloser/haustorium/internal/types"
)

/*
Usage:

result, err := haustorium.Analyze(factory, format, haustorium.DefaultOptions())
if result.HasClipping {
    fmt.Println("Clipping detected!")
}

// Defects only
opts := haustorium.DefaultOptions()
opts.Checks = haustorium.ChecksDefects
result, err := haustorium.Analyze(factory, format, opts)

// Custom thresholds
opts := haustorium.DefaultOptions()
opts.TruncationThresholdDb = -35
opts.ChannelImbalanceDb = 3.0
result, err := haustorium.Analyze(factory, format, opts)

// Iterate issues
for _, issue := range result.Issues {
    if issue.Detected {
        fmt.Printf("[%s] %s\n", issue.Severity, issue.Summary)
    }
}

// Inspect raw data
if result.Stereo != nil {
    fmt.Printf("Correlation: %.3f\n", result.Stereo.Correlation)
}

*/

// Check represents a high-level audio quality check
type Check int

const (
	CheckClipping Check = 1 << iota
	CheckTruncation
	CheckFakeBitDepth
	CheckFakeSampleRate
	CheckLossyTranscode
	CheckDCOffset
	CheckFakeStereo
	CheckPhaseIssues
	CheckInvertedPhase
	CheckChannelImbalance
	CheckSilencePadding
	CheckHum
	CheckNoiseFloor
	CheckInterSamplePeaks
	CheckLoudness
	CheckDynamicRange
	CheckDropouts

	// Presets
	ChecksDefects = CheckClipping | CheckTruncation | CheckFakeBitDepth |
		CheckFakeSampleRate | CheckLossyTranscode | CheckDCOffset |
		CheckFakeStereo | CheckPhaseIssues | CheckInvertedPhase |
		CheckChannelImbalance | CheckSilencePadding | CheckHum |
		CheckNoiseFloor | CheckInterSamplePeaks | CheckDropouts

	ChecksLoudness = CheckLoudness | CheckDynamicRange | CheckInterSamplePeaks

	ChecksAll = ChecksDefects | ChecksLoudness
)

// Severity indicates how bad a detected issue is
type Severity int

const (
	SeverityNone Severity = iota
	SeverityMild
	SeverityModerate
	SeveritySevere
)

func (s Severity) String() string {
	switch s {
	case SeverityNone:
		return "none"
	case SeverityMild:
		return "mild"
	case SeverityModerate:
		return "moderate"
	case SeveritySevere:
		return "severe"
	}
	return "unknown"
}

// Issue represents a detected problem
type Issue struct {
	Check      Check
	Detected   bool
	Severity   Severity
	Summary    string  // human-readable summary
	Confidence float64 // 0.0-1.0
}

// Options configures the analysis
type Options struct {
	Checks Check // which checks to run (default: ChecksAll)

	// Thresholds (zero = use defaults)
	TruncationThresholdDb float64 // default -40
	DCOffsetThresholdDb   float64 // default -40
	ChannelImbalanceDb    float64 // default 2.0
	SilencePaddingMinSec  float64 // default 2.0
	NoiseFloorThresholdDb float64 // default -20
	HumThresholdDb        float64 // default 15
	TranscodeSharpnessDb  float64 // default 30
	UpsampleSharpnessDb   float64 // default 40
	DRBrickwallThreshold  int     // default 6
	ISPCountThreshold     uint64  // default 100
	DropoutDeltaThreshold float64 // default 0.5
}

// DefaultOptions returns sensible defaults
func DefaultOptions() Options {
	return Options{
		Checks:                ChecksAll,
		TruncationThresholdDb: -40,
		DCOffsetThresholdDb:   -40,
		ChannelImbalanceDb:    2.0,
		SilencePaddingMinSec:  2.0,
		NoiseFloorThresholdDb: -20,
		HumThresholdDb:        15,
		TranscodeSharpnessDb:  30,
		UpsampleSharpnessDb:   40,
		DRBrickwallThreshold:  6,
		ISPCountThreshold:     100,
		DropoutDeltaThreshold: 0.5,
	}
}

// Result contains all analysis results
type Result struct {
	// High-level issues (what the user asked for)
	Issues []Issue

	// Quick access booleans
	HasClipping         bool
	HasTruncation       bool
	HasFakeBitDepth     bool
	HasFakeSampleRate   bool
	HasLossyTranscode   bool
	HasDCOffset         bool
	HasFakeStereo       bool
	HasPhaseIssues      bool
	HasInvertedPhase    bool
	HasChannelImbalance bool
	HasSilencePadding   bool
	HasHum              bool
	HasHighNoiseFloor   bool
	HasInterSamplePeaks bool
	HasDropouts         bool
	IsBrickwalled       bool

	// Summary
	IssueCount    int
	WorstSeverity Severity

	// Raw analysis results (for inspection, nil if not requested)
	Clipping   *types.ClippingDetection
	Truncation *types.TruncationDetection
	BitDepth   *types.BitDepthAuthenticity
	Spectral   *types.SpectralResult
	DCOffset   *types.DCOffsetResult
	Stereo     *types.StereoResult
	Silence    *types.SilenceResult
	TruePeak   *types.TruePeakResult
	Loudness   *types.LoudnessResult
	Dropout    *types.DropoutResult
}

// ReaderFactory provides fresh readers for multiple passes
type ReaderFactory func() (io.Reader, error)

// Analyze performs comprehensive audio analysis
func Analyze(factory ReaderFactory, format types.PCMFormat, opts Options) (*Result, error) {
	if opts.Checks == 0 {
		opts = DefaultOptions()
	}
	applyDefaults(&opts)

	result := &Result{}

	// Determine which low-level analyzers we need
	needClipping := opts.Checks&CheckClipping != 0
	needTruncation := opts.Checks&CheckTruncation != 0
	needBitDepth := opts.Checks&CheckFakeBitDepth != 0
	needSpectral := opts.Checks&(CheckFakeSampleRate|CheckLossyTranscode|CheckHum|CheckNoiseFloor) != 0
	needDCOffset := opts.Checks&CheckDCOffset != 0
	needStereo := opts.Checks&(CheckFakeStereo|CheckPhaseIssues|CheckInvertedPhase|CheckChannelImbalance) != 0
	needSilence := opts.Checks&CheckSilencePadding != 0
	needTruePeak := opts.Checks&CheckInterSamplePeaks != 0
	needLoudness := opts.Checks&(CheckLoudness|CheckDynamicRange) != 0
	needDropout := opts.Checks&CheckDropouts != 0

	// Run analyzers
	if needClipping {
		r, err := factory()
		if err != nil {
			return nil, err
		}
		result.Clipping, err = clipping.Detect(r, format)
		if err != nil {
			return nil, err
		}
	}

	if needTruncation {
		r, err := factory()
		if err != nil {
			return nil, err
		}
		if rs, ok := r.(io.ReadSeeker); ok {
			result.Truncation, err = truncation.Detect(rs, format, 50)
			if err != nil {
				return nil, err
			}
		}
	}

	if needBitDepth {
		r, err := factory()
		if err != nil {
			return nil, err
		}
		result.BitDepth, err = bitdepth.Authenticity(r, format)
		if err != nil {
			return nil, err
		}
	}

	if needSpectral {
		r, err := factory()
		if err != nil {
			return nil, err
		}
		result.Spectral, err = spectral.Analyze(r, format, spectral.DefaultOptions())
		if err != nil {
			return nil, err
		}
	}

	if needDCOffset {
		r, err := factory()
		if err != nil {
			return nil, err
		}
		result.DCOffset, err = dcoffset.Detect(r, format)
		if err != nil {
			return nil, err
		}
	}

	if needStereo && format.Channels == 2 {
		r, err := factory()
		if err != nil {
			return nil, err
		}
		result.Stereo, err = stereo.Analyze(r, format)
		if err != nil {
			return nil, err
		}
	}

	if needSilence {
		r, err := factory()
		if err != nil {
			return nil, err
		}
		result.Silence, err = silence.Detect(r, format, silence.DefaultOptions())
		if err != nil {
			return nil, err
		}
	}

	if needTruePeak {
		r, err := factory()
		if err != nil {
			return nil, err
		}
		result.TruePeak, err = truepeak.Detect(r, format)
		if err != nil {
			return nil, err
		}
	}

	if needLoudness {
		r, err := factory()
		if err != nil {
			return nil, err
		}
		result.Loudness, err = loudness.Analyze(r, format)
		if err != nil {
			return nil, err
		}
	}

	if needDropout {
		r, err := factory()
		if err != nil {
			return nil, err
		}
		result.Dropout, err = dropout.Detect(r, format, dropout.Options{
			DeltaThreshold: opts.DropoutDeltaThreshold,
		})
		if err != nil {
			return nil, err
		}
	}

	// Interpret results
	interpretResults(result, opts)

	return result, nil
}

func applyDefaults(opts *Options) {
	if opts.TruncationThresholdDb == 0 {
		opts.TruncationThresholdDb = -40
	}
	if opts.DCOffsetThresholdDb == 0 {
		opts.DCOffsetThresholdDb = -40
	}
	if opts.ChannelImbalanceDb == 0 {
		opts.ChannelImbalanceDb = 2.0
	}
	if opts.SilencePaddingMinSec == 0 {
		opts.SilencePaddingMinSec = 2.0
	}
	if opts.NoiseFloorThresholdDb == 0 {
		opts.NoiseFloorThresholdDb = -20
	}
	if opts.HumThresholdDb == 0 {
		opts.HumThresholdDb = 15
	}
	if opts.TranscodeSharpnessDb == 0 {
		opts.TranscodeSharpnessDb = 30
	}
	if opts.UpsampleSharpnessDb == 0 {
		opts.UpsampleSharpnessDb = 40
	}
	if opts.DRBrickwallThreshold == 0 {
		opts.DRBrickwallThreshold = 6
	}
	if opts.ISPCountThreshold == 0 {
		opts.ISPCountThreshold = 100
	}
	if opts.DropoutDeltaThreshold == 0 {
		opts.DropoutDeltaThreshold = 0.5
	}
}

func interpretResults(result *Result, opts Options) {
	// Clipping
	if result.Clipping != nil && opts.Checks&CheckClipping != 0 {
		detected := result.Clipping.Events > 0
		var severity Severity
		var summary string

		switch {
		case result.Clipping.Events == 0:
			severity = SeverityNone
			summary = "No clipping detected"
		case result.Clipping.Events < 10:
			severity = SeverityMild
			summary = fmt.Sprintf("%d clipping events", result.Clipping.Events)
		case result.Clipping.Events < 100:
			severity = SeverityModerate
			summary = fmt.Sprintf("%d clipping events", result.Clipping.Events)
		default:
			severity = SeveritySevere
			summary = fmt.Sprintf("%d clipping events, longest run %d samples", result.Clipping.Events, result.Clipping.LongestRun)
		}

		result.HasClipping = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckClipping,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: 1.0,
		})
	}

	// Truncation
	if result.Truncation != nil && opts.Checks&CheckTruncation != 0 {
		detected := result.Truncation.FinalRmsDb > opts.TruncationThresholdDb
		var severity Severity
		var summary string

		switch {
		case result.Truncation.FinalRmsDb < -50:
			severity = SeverityNone
			summary = "Clean ending"
		case result.Truncation.FinalRmsDb < -40:
			severity = SeverityMild
			summary = fmt.Sprintf("Possibly truncated (%.1f dB at end)", result.Truncation.FinalRmsDb)
		case result.Truncation.FinalRmsDb < -30:
			severity = SeverityModerate
			summary = fmt.Sprintf("Likely truncated (%.1f dB at end)", result.Truncation.FinalRmsDb)
		default:
			severity = SeveritySevere
			summary = fmt.Sprintf("Truncated mid-audio (%.1f dB at end)", result.Truncation.FinalRmsDb)
		}

		result.HasTruncation = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckTruncation,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: 0.8,
		})
	}

	// Fake Bit Depth
	if result.BitDepth != nil && opts.Checks&CheckFakeBitDepth != 0 {
		detected := int(result.BitDepth.Effective) < int(result.BitDepth.Claimed)
		var severity Severity
		var summary string

		if detected {
			severity = SeveritySevere
			summary = fmt.Sprintf("Fake %d-bit: actually %d-bit (zero-padded)", result.BitDepth.Claimed, result.BitDepth.Effective)
		} else {
			severity = SeverityNone
			summary = fmt.Sprintf("Genuine %d-bit", result.BitDepth.Claimed)
		}

		result.HasFakeBitDepth = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckFakeBitDepth,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: 1.0,
		})
	}

	// Fake Sample Rate
	if result.Spectral != nil && opts.Checks&CheckFakeSampleRate != 0 {
		detected := result.Spectral.IsUpsampled
		var severity Severity
		var summary string

		if detected {
			severity = SeveritySevere
			summary = fmt.Sprintf("Fake %d Hz: upsampled from %d Hz", result.Spectral.ClaimedRate, result.Spectral.EffectiveRate)
		} else {
			severity = SeverityNone
			summary = fmt.Sprintf("Genuine %d Hz", result.Spectral.ClaimedRate)
		}

		result.HasFakeSampleRate = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckFakeSampleRate,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: boolToConfidence(result.Spectral.UpsampleSharpness > opts.UpsampleSharpnessDb),
		})
	}

	// Lossy Transcode
	if result.Spectral != nil && opts.Checks&CheckLossyTranscode != 0 {
		detected := result.Spectral.IsTranscode
		var severity Severity
		var summary string

		if detected {
			severity = SeveritySevere
			summary = fmt.Sprintf("Lossy transcode detected: likely %s (cutoff %.0f Hz)", result.Spectral.LikelyCodec, result.Spectral.TranscodeCutoff)
		} else {
			severity = SeverityNone
			summary = "No lossy transcode detected"
		}

		result.HasLossyTranscode = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckLossyTranscode,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: boolToConfidence(result.Spectral.TranscodeSharpness > opts.TranscodeSharpnessDb),
		})
	}

	// DC Offset
	if result.DCOffset != nil && opts.Checks&CheckDCOffset != 0 {
		detected := result.DCOffset.OffsetDb > opts.DCOffsetThresholdDb
		var severity Severity
		var summary string

		switch {
		case result.DCOffset.OffsetDb < -60:
			severity = SeverityNone
			summary = "No DC offset"
		case result.DCOffset.OffsetDb < -40:
			severity = SeverityMild
			summary = fmt.Sprintf("Minor DC offset (%.1f dB)", result.DCOffset.OffsetDb)
		case result.DCOffset.OffsetDb < -26:
			severity = SeverityModerate
			summary = fmt.Sprintf("DC offset present (%.1f dB)", result.DCOffset.OffsetDb)
		default:
			severity = SeveritySevere
			summary = fmt.Sprintf("Severe DC offset (%.1f dB)", result.DCOffset.OffsetDb)
		}

		result.HasDCOffset = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckDCOffset,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: 1.0,
		})
	}

	// Stereo checks
	if result.Stereo != nil {
		// Fake Stereo
		if opts.Checks&CheckFakeStereo != 0 {
			detected := result.Stereo.Correlation > 0.98 && result.Stereo.DifferenceDb < -60
			var severity Severity
			var summary string

			if detected {
				severity = SeverityModerate
				summary = fmt.Sprintf("Fake stereo: channels identical (correlation %.3f)", result.Stereo.Correlation)
			} else {
				severity = SeverityNone
				summary = "Real stereo content"
			}

			result.HasFakeStereo = detected
			result.Issues = append(result.Issues, Issue{
				Check:      CheckFakeStereo,
				Detected:   detected,
				Severity:   severity,
				Summary:    summary,
				Confidence: 1.0,
			})
		}

		// Phase Issues
		if opts.Checks&CheckPhaseIssues != 0 {
			detected := result.Stereo.CancellationDb > 3
			var severity Severity
			var summary string

			switch {
			case result.Stereo.CancellationDb < 1:
				severity = SeverityNone
				summary = "Mono-compatible"
			case result.Stereo.CancellationDb < 3:
				severity = SeverityMild
				summary = fmt.Sprintf("Minor phase issues (%.1f dB cancellation)", result.Stereo.CancellationDb)
			case result.Stereo.CancellationDb < 6:
				severity = SeverityModerate
				summary = fmt.Sprintf("Phase issues: %.1f dB lost in mono", result.Stereo.CancellationDb)
			default:
				severity = SeveritySevere
				summary = fmt.Sprintf("Severe phase issues: %.1f dB cancellation in mono", result.Stereo.CancellationDb)
			}

			result.HasPhaseIssues = detected
			result.Issues = append(result.Issues, Issue{
				Check:      CheckPhaseIssues,
				Detected:   detected,
				Severity:   severity,
				Summary:    summary,
				Confidence: 1.0,
			})
		}

		// Inverted Phase
		if opts.Checks&CheckInvertedPhase != 0 {
			detected := result.Stereo.Correlation < -0.95
			var severity Severity
			var summary string

			if detected {
				severity = SeveritySevere
				summary = fmt.Sprintf("Inverted phase: one channel polarity flipped (correlation %.3f)", result.Stereo.Correlation)
			} else {
				severity = SeverityNone
				summary = "Phase polarity OK"
			}

			result.HasInvertedPhase = detected
			result.Issues = append(result.Issues, Issue{
				Check:      CheckInvertedPhase,
				Detected:   detected,
				Severity:   severity,
				Summary:    summary,
				Confidence: 1.0,
			})
		}

		// Channel Imbalance
		if opts.Checks&CheckChannelImbalance != 0 {
			imbalance := abs(result.Stereo.ImbalanceDb)
			detected := imbalance > opts.ChannelImbalanceDb
			var severity Severity
			var summary string

			side := "left"
			if result.Stereo.ImbalanceDb < 0 {
				side = "right"
			}

			switch {
			case imbalance < 1:
				severity = SeverityNone
				summary = "Channels balanced"
			case imbalance < 2:
				severity = SeverityMild
				summary = fmt.Sprintf("Slight imbalance: %s louder by %.1f dB", side, imbalance)
			case imbalance < 3:
				severity = SeverityModerate
				summary = fmt.Sprintf("Channel imbalance: %s louder by %.1f dB", side, imbalance)
			default:
				severity = SeveritySevere
				summary = fmt.Sprintf("Severe imbalance: %s louder by %.1f dB", side, imbalance)
			}

			result.HasChannelImbalance = detected
			result.Issues = append(result.Issues, Issue{
				Check:      CheckChannelImbalance,
				Detected:   detected,
				Severity:   severity,
				Summary:    summary,
				Confidence: 1.0,
			})
		}
	}

	// Silence Padding
	if result.Silence != nil && opts.Checks&CheckSilencePadding != 0 {
		leading := result.Silence.LeadingSec > opts.SilencePaddingMinSec
		trailing := result.Silence.TrailingSec > opts.SilencePaddingMinSec
		detected := leading || trailing
		var severity Severity
		var summary string

		switch {
		case !detected:
			severity = SeverityNone
			summary = "No excessive silence padding"
		case result.Silence.LeadingSec > 5 || result.Silence.TrailingSec > 10:
			severity = SeverityModerate
			summary = fmt.Sprintf("Silence padding: %.1fs leading, %.1fs trailing", result.Silence.LeadingSec, result.Silence.TrailingSec)
		default:
			severity = SeverityMild
			summary = fmt.Sprintf("Silence padding: %.1fs leading, %.1fs trailing", result.Silence.LeadingSec, result.Silence.TrailingSec)
		}

		result.HasSilencePadding = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckSilencePadding,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: 1.0,
		})
	}

	// Hum
	if result.Spectral != nil && opts.Checks&CheckHum != 0 {
		detected := result.Spectral.Has50HzHum || result.Spectral.Has60HzHum
		var severity Severity
		var summary string

		if detected {
			freqs := ""
			if result.Spectral.Has50HzHum && result.Spectral.Has60HzHum {
				freqs = "50Hz and 60Hz"
			} else if result.Spectral.Has50HzHum {
				freqs = "50Hz"
			} else {
				freqs = "60Hz"
			}

			switch {
			case result.Spectral.HumLevelDb < 20:
				severity = SeverityMild
				summary = fmt.Sprintf("%s hum detected (%.1f dB)", freqs, result.Spectral.HumLevelDb)
			case result.Spectral.HumLevelDb < 30:
				severity = SeverityModerate
				summary = fmt.Sprintf("%s hum detected (%.1f dB)", freqs, result.Spectral.HumLevelDb)
			default:
				severity = SeveritySevere
				summary = fmt.Sprintf("Severe %s hum (%.1f dB)", freqs, result.Spectral.HumLevelDb)
			}
		} else {
			severity = SeverityNone
			summary = "No mains hum detected"
		}

		result.HasHum = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckHum,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: 0.9,
		})
	}

	// Noise Floor
	if result.Spectral != nil && opts.Checks&CheckNoiseFloor != 0 {
		detected := result.Spectral.NoiseFloorDb > opts.NoiseFloorThresholdDb
		var severity Severity
		var summary string

		switch {
		case result.Spectral.NoiseFloorDb < -40:
			severity = SeverityNone
			summary = fmt.Sprintf("Clean recording (noise floor %.1f dB)", result.Spectral.NoiseFloorDb)
		case result.Spectral.NoiseFloorDb < -30:
			severity = SeverityMild
			summary = fmt.Sprintf("Good noise floor (%.1f dB)", result.Spectral.NoiseFloorDb)
		case result.Spectral.NoiseFloorDb < -20:
			severity = SeverityModerate
			summary = fmt.Sprintf("Elevated noise floor (%.1f dB)", result.Spectral.NoiseFloorDb)
		default:
			severity = SeveritySevere
			summary = fmt.Sprintf("High noise floor (%.1f dB)", result.Spectral.NoiseFloorDb)
		}

		result.HasHighNoiseFloor = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckNoiseFloor,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: 0.85,
		})
	}

	// Inter-Sample Peaks
	if result.TruePeak != nil && opts.Checks&CheckInterSamplePeaks != 0 {
		detected := result.TruePeak.ISPCount > opts.ISPCountThreshold
		var severity Severity
		var summary string

		switch {
		case result.TruePeak.ISPCount == 0:
			severity = SeverityNone
			summary = fmt.Sprintf("No inter-sample peaks (true peak %.1f dBTP)", result.TruePeak.TruePeakDb)
		case result.TruePeak.ISPCount < 100:
			severity = SeverityMild
			summary = fmt.Sprintf("%d ISPs, max overshoot %.2f dB", result.TruePeak.ISPCount, result.TruePeak.ISPMaxDb)
		case result.TruePeak.ISPCount < 1000:
			severity = SeverityModerate
			summary = fmt.Sprintf("%d ISPs, max overshoot %.2f dB", result.TruePeak.ISPCount, result.TruePeak.ISPMaxDb)
		default:
			severity = SeveritySevere
			summary = fmt.Sprintf("Pervasive ISPs: %d events, max overshoot %.2f dB", result.TruePeak.ISPCount, result.TruePeak.ISPMaxDb)
		}

		result.HasInterSamplePeaks = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckInterSamplePeaks,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: 1.0,
		})
	}

	// Loudness (informational, not a defect)
	if result.Loudness != nil && opts.Checks&CheckLoudness != 0 {
		result.Issues = append(result.Issues, Issue{
			Check:      CheckLoudness,
			Detected:   false, // informational
			Severity:   SeverityNone,
			Summary:    fmt.Sprintf("Loudness: %.1f LUFS, range %.1f LU", result.Loudness.IntegratedLUFS, result.Loudness.LoudnessRange),
			Confidence: 1.0,
		})
	}

	// Dynamic Range
	if result.Loudness != nil && opts.Checks&CheckDynamicRange != 0 {
		brickwalled := result.Loudness.DRScore <= opts.DRBrickwallThreshold
		var severity Severity
		var summary string

		switch {
		case result.Loudness.DRScore >= 12:
			severity = SeverityNone
			summary = fmt.Sprintf("Excellent dynamics (DR%d)", result.Loudness.DRScore)
		case result.Loudness.DRScore >= 8:
			severity = SeverityNone
			summary = fmt.Sprintf("Good dynamics (DR%d)", result.Loudness.DRScore)
		case result.Loudness.DRScore >= 6:
			severity = SeverityMild
			summary = fmt.Sprintf("Compressed (DR%d)", result.Loudness.DRScore)
		default:
			severity = SeveritySevere
			summary = fmt.Sprintf("Brickwalled (DR%d)", result.Loudness.DRScore)
		}

		result.IsBrickwalled = brickwalled
		result.Issues = append(result.Issues, Issue{
			Check:      CheckDynamicRange,
			Detected:   brickwalled,
			Severity:   severity,
			Summary:    summary,
			Confidence: 1.0,
		})
	}

	// Dropouts
	if result.Dropout != nil && opts.Checks&CheckDropouts != 0 {
		total := result.Dropout.DeltaCount + result.Dropout.ZeroRunCount + result.Dropout.DCJumpCount
		detected := total > 0
		var severity Severity
		var summary string

		switch {
		case total == 0:
			severity = SeverityNone
			summary = "No dropouts or glitches"
		case total < 5:
			severity = SeverityMild
			summary = fmt.Sprintf("%d discontinuities (%d jumps, %d zero runs, %d DC shifts)", total, result.Dropout.DeltaCount, result.Dropout.ZeroRunCount, result.Dropout.DCJumpCount)
		case total < 20:
			severity = SeverityModerate
			summary = fmt.Sprintf("%d discontinuities detected", total)
		default:
			severity = SeveritySevere
			summary = fmt.Sprintf("%d discontinuities (worst: %.1f dB)", total, result.Dropout.WorstDb)
		}

		result.HasDropouts = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckDropouts,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: 0.9,
		})
	}

	// Calculate summary stats
	for _, issue := range result.Issues {
		if issue.Detected {
			result.IssueCount++
		}
		if issue.Severity > result.WorstSeverity {
			result.WorstSeverity = issue.Severity
		}
	}
}

func boolToConfidence(b bool) float64 {
	if b {
		return 0.95
	}
	return 0.5
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
