//nolint:wrapcheck
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

// Custom bands
opts := haustorium.DefaultOptions()
opts.Truncation = haustorium.Bands{Mild: -35, Moderate: -25, Severe: -15}
opts.ChannelImbalance = haustorium.Bands{Mild: 1.5, Moderate: 3.0, Severe: 5.0}
result, err := haustorium.Analyze(factory, format, opts)

// Source-aware (adjusts bands for vinyl/live characteristics)
opts := haustorium.OptionsForSource(haustorium.SourceVinyl)
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

// Check represents a high-level audio quality check.
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

	// Presets.
	ChecksDefects = CheckClipping | CheckTruncation | CheckFakeBitDepth |
		CheckFakeSampleRate | CheckLossyTranscode | CheckDCOffset |
		CheckFakeStereo | CheckPhaseIssues | CheckInvertedPhase |
		CheckChannelImbalance | CheckSilencePadding | CheckHum |
		CheckNoiseFloor | CheckInterSamplePeaks | CheckDropouts

	ChecksLoudness = CheckLoudness | CheckDynamicRange | CheckInterSamplePeaks

	ChecksAll = ChecksDefects | ChecksLoudness
)

func (c Check) String() string {
	switch c {
	case CheckClipping:
		return "clipping"
	case CheckTruncation:
		return "truncation"
	case CheckFakeBitDepth:
		return "fake-bit-depth"
	case CheckFakeSampleRate:
		return "fake-sample-rate"
	case CheckLossyTranscode:
		return "lossy-transcode"
	case CheckDCOffset:
		return "dc-offset"
	case CheckFakeStereo:
		return "fake-stereo"
	case CheckPhaseIssues:
		return "phase-issues"
	case CheckInvertedPhase:
		return "inverted-phase"
	case CheckChannelImbalance:
		return "channel-imbalance"
	case CheckSilencePadding:
		return "silence-padding"
	case CheckHum:
		return "hum"
	case CheckNoiseFloor:
		return "noise-floor"
	case CheckInterSamplePeaks:
		return "inter-sample-peaks"
	case CheckLoudness:
		return "loudness"
	case CheckDynamicRange:
		return "dynamic-range"
	case CheckDropouts:
		return "dropouts"
	}

	return "unknown"
}

// Severity indicates how bad a detected issue is.
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
		return "no issue"
	case SeverityMild:
		return "mild"
	case SeverityModerate:
		return "moderate"
	case SeveritySevere:
		return "severe"
	}

	return "unknown"
}

// Issue represents a detected problem.
type Issue struct {
	Check      Check
	Detected   bool
	Severity   Severity
	Summary    string  // human-readable summary
	Confidence float64 // 0.0-1.0
}

// Bands defines severity thresholds for a check. Direction is implicit:
// if Mild < Severe, higher values are worse (ascending, e.g. dB offset).
// If Mild > Severe, lower values are worse (descending, e.g. DR score).
type Bands struct {
	Mild     float64
	Moderate float64
	Severe   float64
}

// Match returns the severity for a value.
// Returns (SeverityNone, false) when the value is below detection (the Mild threshold).
func (b Bands) Match(value float64) (Severity, bool) {
	if b.Mild <= b.Severe {
		// Ascending: higher = worse.
		if value >= b.Severe {
			return SeveritySevere, true
		}

		if value >= b.Moderate {
			return SeverityModerate, true
		}

		if value >= b.Mild {
			return SeverityMild, true
		}
	} else {
		// Descending: lower = worse (e.g. DR score).
		if value <= b.Severe {
			return SeveritySevere, true
		}

		if value <= b.Moderate {
			return SeverityModerate, true
		}

		if value <= b.Mild {
			return SeverityMild, true
		}
	}

	return SeverityNone, false
}

// Options configures the analysis.
type Options struct {
	Checks Check // which checks to run (default: ChecksAll)

	// Severity bands per check (zero value = use defaults).
	Clipping         Bands
	Truncation       Bands
	DCOffset         Bands
	ChannelImbalance Bands
	PhaseIssues      Bands
	SilencePadding   Bands
	Hum              Bands
	NoiseFloor       Bands
	ISP              Bands
	DynamicRange     Bands
	Dropouts         Bands

	// Analyzer thresholds (not severity bands).
	TranscodeSharpnessDb  float64 // default 30
	UpsampleSharpnessDb   float64 // default 40
	DropoutDeltaThreshold float64 // default 0.5
}

// DefaultOptions returns DefaultDigitalOptions.
func DefaultOptions() Options {
	return DefaultDigitalOptions()
}

// DefaultDigitalOptions returns options for clean digital recordings.
func DefaultDigitalOptions() Options {
	return Options{
		Checks:           ChecksAll,
		Clipping:         Bands{Mild: 1, Moderate: 10, Severe: 100},
		Truncation:       Bands{Mild: -40, Moderate: -30, Severe: -20},
		DCOffset:         Bands{Mild: -40, Moderate: -26, Severe: -13},
		ChannelImbalance: Bands{Mild: 1, Moderate: 2, Severe: 3},
		PhaseIssues:      Bands{Mild: 3, Moderate: 6, Severe: 10},
		SilencePadding:   Bands{Mild: 2, Moderate: 5, Severe: 10},
		Hum:              Bands{Mild: 10, Moderate: 20, Severe: 30},
		NoiseFloor:       Bands{Mild: -30, Moderate: -20, Severe: -10},
		ISP:              Bands{Mild: 1, Moderate: 100, Severe: 1000},
		DynamicRange:     Bands{Mild: 8, Moderate: 6, Severe: 4},
		Dropouts:         Bands{Mild: 1, Moderate: 5, Severe: 20},

		TranscodeSharpnessDb:  30,
		UpsampleSharpnessDb:   40,
		DropoutDeltaThreshold: 0.5,
	}
}

// DefaultVinylOptions returns options for vinyl rips.
// Higher tolerance for noise, hum, DC offset, silence padding, dropouts,
// and channel imbalance (early stereo mixes used hard panning).
func DefaultVinylOptions() Options {
	opts := DefaultDigitalOptions()
	opts.Truncation = Bands{Mild: -30, Moderate: -20, Severe: -10}
	opts.DCOffset = Bands{Mild: -26, Moderate: -13, Severe: 0}
	opts.ChannelImbalance = Bands{Mild: 3, Moderate: 6, Severe: 10}
	opts.SilencePadding = Bands{Mild: 5, Moderate: 10, Severe: 20}
	opts.Hum = Bands{Mild: 20, Moderate: 30, Severe: 40}
	opts.NoiseFloor = Bands{Mild: -20, Moderate: -10, Severe: 0}
	opts.Dropouts = Bands{Mild: 5, Moderate: 15, Severe: 40}
	opts.DropoutDeltaThreshold = 0.7

	return opts
}

// DefaultLiveOptions returns options for live recordings.
// Higher tolerance for ambient noise, PA hum, silence padding, and DC offset.
func DefaultLiveOptions() Options {
	opts := DefaultDigitalOptions()
	opts.Truncation = Bands{Mild: -30, Moderate: -20, Severe: -10}
	opts.DCOffset = Bands{Mild: -30, Moderate: -20, Severe: -10}
	opts.SilencePadding = Bands{Mild: 5, Moderate: 10, Severe: 20}
	opts.Hum = Bands{Mild: 15, Moderate: 25, Severe: 35}
	opts.NoiseFloor = Bands{Mild: -20, Moderate: -10, Severe: 0}

	return opts
}

// Source represents the audio source type, which adjusts detection thresholds
// to account for characteristics inherent to the medium.
type Source int

const (
	SourceDigital Source = iota // Clean digital recording (default).
	SourceVinyl                 // Vinyl rip. Higher noise, hum, DC offset tolerance.
	SourceLive                  // Live recording. Ambient noise, PA hum tolerance.
)

func (s Source) String() string {
	switch s {
	case SourceDigital:
		return "digital"
	case SourceVinyl:
		return "vinyl"
	case SourceLive:
		return "live"
	}

	return "unknown"
}

// ParseSource converts a string to a Source value.
func ParseSource(s string) (Source, error) {
	switch s {
	case "digital", "":
		return SourceDigital, nil
	case "vinyl":
		return SourceVinyl, nil
	case "live":
		return SourceLive, nil
	default:
		return 0, fmt.Errorf("unknown source %q (valid: digital, vinyl, live)", s)
	}
}

// OptionsForSource returns the default Options for the given source type.
func OptionsForSource(source Source) Options {
	switch source {
	case SourceVinyl:
		return DefaultVinylOptions()
	case SourceLive:
		return DefaultLiveOptions()
	default:
		return DefaultDigitalOptions()
	}
}

// Result contains all analysis results.
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

// ReaderFactory provides fresh readers for multiple passes.
type ReaderFactory func() (io.Reader, error)

// Analyze performs comprehensive audio analysis.
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

		result.Spectral, err = spectral.AnalyzeV2(r, format, spectral.DefaultOptions())
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

		result.Dropout, err = dropout.DetectV2(r, format, dropout.Options{
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
	defaults := DefaultOptions()
	zeroBands := Bands{}

	if opts.Clipping == zeroBands {
		opts.Clipping = defaults.Clipping
	}

	if opts.Truncation == zeroBands {
		opts.Truncation = defaults.Truncation
	}

	if opts.DCOffset == zeroBands {
		opts.DCOffset = defaults.DCOffset
	}

	if opts.ChannelImbalance == zeroBands {
		opts.ChannelImbalance = defaults.ChannelImbalance
	}

	if opts.PhaseIssues == zeroBands {
		opts.PhaseIssues = defaults.PhaseIssues
	}

	if opts.SilencePadding == zeroBands {
		opts.SilencePadding = defaults.SilencePadding
	}

	if opts.Hum == zeroBands {
		opts.Hum = defaults.Hum
	}

	if opts.NoiseFloor == zeroBands {
		opts.NoiseFloor = defaults.NoiseFloor
	}

	if opts.ISP == zeroBands {
		opts.ISP = defaults.ISP
	}

	if opts.DynamicRange == zeroBands {
		opts.DynamicRange = defaults.DynamicRange
	}

	if opts.Dropouts == zeroBands {
		opts.Dropouts = defaults.Dropouts
	}

	if opts.TranscodeSharpnessDb == 0 {
		opts.TranscodeSharpnessDb = defaults.TranscodeSharpnessDb
	}

	if opts.UpsampleSharpnessDb == 0 {
		opts.UpsampleSharpnessDb = defaults.UpsampleSharpnessDb
	}

	if opts.DropoutDeltaThreshold == 0 {
		opts.DropoutDeltaThreshold = defaults.DropoutDeltaThreshold
	}
}

func interpretResults(result *Result, opts Options) {
	// Clipping
	if result.Clipping != nil && opts.Checks&CheckClipping != 0 {
		events := float64(result.Clipping.Events)
		severity, detected := opts.Clipping.Match(events)

		var summary string

		switch severity {
		case SeverityNone:
			summary = "No clipping detected"
		case SeverityMild, SeverityModerate:
			summary = fmt.Sprintf("%d clipping events", result.Clipping.Events)
		case SeveritySevere:
			summary = fmt.Sprintf(
				"%d clipping events, longest run %d samples",
				result.Clipping.Events,
				result.Clipping.LongestRun,
			)
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
		severity, detected := opts.Truncation.Match(result.Truncation.FinalRmsDb)

		var summary string

		switch severity {
		case SeverityNone:
			summary = "Clean ending"
		case SeverityMild:
			summary = fmt.Sprintf("Possibly truncated (%.1f dB at end)", result.Truncation.FinalRmsDb)
		case SeverityModerate:
			summary = fmt.Sprintf("Likely truncated (%.1f dB at end)", result.Truncation.FinalRmsDb)
		case SeveritySevere:
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

	// Fake Bit Depth (binary detection, no bands)
	if result.BitDepth != nil && opts.Checks&CheckFakeBitDepth != 0 {
		detected := int(result.BitDepth.Effective) < int(result.BitDepth.Claimed)

		var (
			severity Severity
			summary  string
		)

		if detected {
			severity = SeveritySevere
			summary = fmt.Sprintf(
				"Fake %d-bit: actually %d-bit (zero-padded)",
				result.BitDepth.Claimed,
				result.BitDepth.Effective,
			)
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

	// Fake Sample Rate (binary detection, no bands)
	if result.Spectral != nil && opts.Checks&CheckFakeSampleRate != 0 {
		detected := result.Spectral.IsUpsampled

		var (
			severity Severity
			summary  string
		)

		if detected {
			severity = SeveritySevere
			summary = fmt.Sprintf(
				"Fake %d Hz: upsampled from %d Hz",
				result.Spectral.ClaimedRate,
				result.Spectral.EffectiveRate,
			)
		} else {
			severity = SeverityNone
			summary = fmt.Sprintf("Genuine %d Hz", result.Spectral.ClaimedRate)
		}

		// Base sample rates (44100, 48000) have no standard lower rate to upsample from,
		// so the check is not applicable and we report 100% confidence in "genuine".
		confidence := boolToConfidence(result.Spectral.UpsampleSharpness > opts.UpsampleSharpnessDb)
		if !detected && result.Spectral.ClaimedRate <= 48000 {
			confidence = 1.0
		}

		result.HasFakeSampleRate = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckFakeSampleRate,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: confidence,
		})
	}

	// Lossy Transcode (binary detection, no bands)
	if result.Spectral != nil && opts.Checks&CheckLossyTranscode != 0 {
		detected := result.Spectral.IsTranscode

		var (
			severity Severity
			summary  string
		)

		if detected {
			severity = SeveritySevere
			summary = fmt.Sprintf(
				"Lossy transcode detected: likely %s (cutoff %.0f Hz)",
				result.Spectral.LikelyCodec,
				result.Spectral.TranscodeCutoff,
			)
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
		severity, detected := opts.DCOffset.Match(result.DCOffset.OffsetDb)

		var summary string

		switch severity {
		case SeverityNone:
			summary = "No DC offset"
		case SeverityMild:
			summary = fmt.Sprintf("Minor DC offset (%.1f dB)", result.DCOffset.OffsetDb)
		case SeverityModerate:
			summary = fmt.Sprintf("DC offset present (%.1f dB)", result.DCOffset.OffsetDb)
		case SeveritySevere:
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
		// Fake Stereo (binary detection, no bands)
		if opts.Checks&CheckFakeStereo != 0 {
			detected := result.Stereo.Correlation > 0.98 && result.Stereo.DifferenceDb < -60

			var (
				severity Severity
				summary  string
			)

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

		// Phase Issues (binary detection from cancellation threshold, bands for severity)
		if opts.Checks&CheckPhaseIssues != 0 {
			severity, detected := opts.PhaseIssues.Match(result.Stereo.CancellationDb)

			var summary string

			switch severity {
			case SeverityNone:
				summary = "Mono-compatible"
			case SeverityMild:
				summary = fmt.Sprintf("Minor phase issues (%.1f dB cancellation)", result.Stereo.CancellationDb)
			case SeverityModerate:
				summary = fmt.Sprintf("Phase issues: %.1f dB lost in mono", result.Stereo.CancellationDb)
			case SeveritySevere:
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

		// Inverted Phase (binary detection, no bands)
		if opts.Checks&CheckInvertedPhase != 0 {
			detected := result.Stereo.Correlation < -0.95

			var (
				severity Severity
				summary  string
			)

			if detected {
				severity = SeveritySevere
				summary = fmt.Sprintf(
					"Inverted phase: one channel polarity flipped (correlation %.3f)",
					result.Stereo.Correlation,
				)
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
			severity, detected := opts.ChannelImbalance.Match(imbalance)

			var summary string

			side := "left"
			if result.Stereo.ImbalanceDb < 0 {
				side = "right"
			}

			switch severity {
			case SeverityNone:
				summary = "Channels balanced"
			case SeverityMild:
				summary = fmt.Sprintf("Slight imbalance: %s louder by %.1f dB", side, imbalance)
			case SeverityModerate:
				summary = fmt.Sprintf("Channel imbalance: %s louder by %.1f dB", side, imbalance)
			case SeveritySevere:
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
		worst := result.Silence.LeadingSec
		if result.Silence.TrailingSec > worst {
			worst = result.Silence.TrailingSec
		}

		severity, detected := opts.SilencePadding.Match(worst)

		var summary string

		switch severity {
		case SeverityNone:
			summary = "No excessive silence padding"
		default:
			summary = fmt.Sprintf(
				"Silence padding: %.1fs leading, %.1fs trailing",
				result.Silence.LeadingSec,
				result.Silence.TrailingSec,
			)
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

	// Hum (binary detection from spectral flags, bands for severity)
	if result.Spectral != nil && opts.Checks&CheckHum != 0 {
		detected := result.Spectral.Has50HzHum || result.Spectral.Has60HzHum

		var (
			severity Severity
			summary  string
		)

		if detected {
			var freqs string
			if result.Spectral.Has50HzHum && result.Spectral.Has60HzHum {
				freqs = "50Hz and 60Hz"
			} else if result.Spectral.Has50HzHum {
				freqs = "50Hz"
			} else {
				freqs = "60Hz"
			}

			severity, _ = opts.Hum.Match(result.Spectral.HumLevelDb)
			if severity == SeverityNone {
				// Detected but below band thresholds: default to mild.
				severity = SeverityMild
			}

			if severity == SeveritySevere {
				summary = fmt.Sprintf("Severe %s hum (%.1f dB)", freqs, result.Spectral.HumLevelDb)
			} else {
				summary = fmt.Sprintf("%s hum detected (%.1f dB)", freqs, result.Spectral.HumLevelDb)
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
		severity, detected := opts.NoiseFloor.Match(result.Spectral.NoiseFloorDb)

		var summary string

		switch severity {
		case SeverityNone:
			summary = fmt.Sprintf("Clean recording (noise floor %.1f dB)", result.Spectral.NoiseFloorDb)
		case SeverityMild:
			summary = fmt.Sprintf("Slightly elevated noise floor (%.1f dB)", result.Spectral.NoiseFloorDb)
		case SeverityModerate:
			summary = fmt.Sprintf("Elevated noise floor (%.1f dB)", result.Spectral.NoiseFloorDb)
		case SeveritySevere:
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
		ispCount := float64(result.TruePeak.ISPCount)
		severity, detected := opts.ISP.Match(ispCount)

		var summary string

		switch severity {
		case SeverityNone:
			summary = fmt.Sprintf("No inter-sample peaks (true peak %.1f dBTP)", result.TruePeak.TruePeakDb)
		case SeverityMild, SeverityModerate:
			summary = fmt.Sprintf("%d ISPs, max overshoot %.2f dB", result.TruePeak.ISPCount, result.TruePeak.ISPMaxDb)
		case SeveritySevere:
			summary = fmt.Sprintf(
				"Pervasive ISPs: %d events, max overshoot %.2f dB",
				result.TruePeak.ISPCount,
				result.TruePeak.ISPMaxDb,
			)
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

	// Loudness (informational, no bands)
	if result.Loudness != nil && opts.Checks&CheckLoudness != 0 {
		result.Issues = append(result.Issues, Issue{
			Check:    CheckLoudness,
			Detected: false, // informational
			Severity: SeverityNone,
			Summary: fmt.Sprintf(
				"Loudness: %.1f LUFS, range %.1f LU",
				result.Loudness.IntegratedLUFS,
				result.Loudness.LoudnessRange,
			),
			Confidence: 1.0,
		})
	}

	// Dynamic Range (descending bands: lower DR = worse)
	if result.Loudness != nil && opts.Checks&CheckDynamicRange != 0 {
		drScore := float64(result.Loudness.DRScore)
		severity, detected := opts.DynamicRange.Match(drScore)

		var summary string

		switch severity {
		case SeverityNone:
			if result.Loudness.DRScore >= 12 {
				summary = fmt.Sprintf("Excellent dynamics (DR%d)", result.Loudness.DRScore)
			} else {
				summary = fmt.Sprintf("Good dynamics (DR%d)", result.Loudness.DRScore)
			}
		case SeverityMild:
			summary = fmt.Sprintf("Compressed (DR%d)", result.Loudness.DRScore)
		case SeverityModerate:
			summary = fmt.Sprintf("Heavily compressed (DR%d)", result.Loudness.DRScore)
		case SeveritySevere:
			summary = fmt.Sprintf("Brickwalled (DR%d)", result.Loudness.DRScore)
		}

		result.IsBrickwalled = detected
		result.Issues = append(result.Issues, Issue{
			Check:      CheckDynamicRange,
			Detected:   detected,
			Severity:   severity,
			Summary:    summary,
			Confidence: 1.0,
		})
	}

	// Dropouts
	if result.Dropout != nil && opts.Checks&CheckDropouts != 0 {
		total := float64(result.Dropout.DeltaCount + result.Dropout.ZeroRunCount + result.Dropout.DCJumpCount)
		severity, detected := opts.Dropouts.Match(total)

		var summary string

		switch severity {
		case SeverityNone:
			summary = "No dropouts or glitches"
		case SeverityMild:
			summary = fmt.Sprintf(
				"%d discontinuities (%d jumps, %d zero runs, %d DC shifts; worst: %.1f dB)",
				int(
					total,
				),
				result.Dropout.DeltaCount,
				result.Dropout.ZeroRunCount,
				result.Dropout.DCJumpCount,
				result.Dropout.WorstDb,
			)
		case SeverityModerate:
			summary = fmt.Sprintf(
				"%d discontinuities (%d jumps, %d zero runs, %d DC shifts; worst: %.1f dB)",
				int(
					total,
				),
				result.Dropout.DeltaCount,
				result.Dropout.ZeroRunCount,
				result.Dropout.DCJumpCount,
				result.Dropout.WorstDb,
			)
		case SeveritySevere:
			summary = fmt.Sprintf(
				"%d discontinuities (%d jumps, %d zero runs, %d DC shifts; worst: %.1f dB)",
				int(
					total,
				),
				result.Dropout.DeltaCount,
				result.Dropout.ZeroRunCount,
				result.Dropout.DCJumpCount,
				result.Dropout.WorstDb,
			)
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
