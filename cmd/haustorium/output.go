//nolint:wrapcheck
package main

import (
	"fmt"
	"os"

	"github.com/farcloser/primordium/format"

	haustorium "github.com/farcloser/haustorium"
	"github.com/farcloser/haustorium/internal/types"
)

func outputResult(filePath string, result *haustorium.Result, formatName string, verbose bool) error {
	if formatName == "console" {
		printResult(filePath, result, verbose)

		return nil
	}

	formatter, err := format.GetFormatter(formatName)
	if err != nil {
		return err
	}

	data := resultToData(filePath, result)

	return formatter.PrintAll([]*format.Data{data}, os.Stdout)
}

func resultToData(filePath string, result *haustorium.Result) *format.Data {
	meta := map[string]any{
		"summary": map[string]any{
			"issue_count":    result.IssueCount,
			"worst_severity": result.WorstSeverity.String(),
		},
	}

	// Issues.
	issues := make([]any, 0, len(result.Issues))
	for _, issue := range result.Issues {
		issues = append(issues, map[string]any{
			"check":      issue.Check.String(),
			"detected":   issue.Detected,
			"severity":   issue.Severity.String(),
			"summary":    issue.Summary,
			"confidence": issue.Confidence,
		})
	}

	meta["issues"] = issues

	// Raw analyzer results.
	if r := result.Clipping; r != nil {
		meta["clipping"] = clippingToMap(r)
	}

	if r := result.Truncation; r != nil {
		meta["truncation"] = map[string]any{
			"final_rms_db":    r.FinalRmsDb,
			"final_peak_db":   r.FinalPeakDb,
			"samples_in_tail": r.SamplesInTail,
		}
	}

	if r := result.BitDepth; r != nil {
		meta["bit_depth"] = map[string]any{
			"claimed":   int(r.Claimed),
			"effective": int(r.Effective),
			"is_padded": r.IsPadded,
			"samples":   r.Samples,
		}
	}

	if r := result.Spectral; r != nil {
		meta["spectral"] = spectralToMap(r)
	}

	if r := result.DCOffset; r != nil {
		meta["dc_offset"] = map[string]any{
			"offset":    r.Offset,
			"offset_db": r.OffsetDb,
			"channels":  r.Channels,
			"samples":   r.Samples,
		}
	}

	if r := result.Stereo; r != nil {
		meta["stereo"] = map[string]any{
			"correlation":     r.Correlation,
			"difference_db":   r.DifferenceDb,
			"mono_sum_db":     r.MonoSumDb,
			"stereo_rms_db":   r.StereoRmsDb,
			"cancellation_db": r.CancellationDb,
			"left_rms_db":     r.LeftRmsDb,
			"right_rms_db":    r.RightRmsDb,
			"imbalance_db":    r.ImbalanceDb,
			"frames":          r.Frames,
		}
	}

	if r := result.Silence; r != nil {
		meta["silence"] = silenceToMap(r)
	}

	if r := result.TruePeak; r != nil {
		meta["true_peak"] = map[string]any{
			"true_peak_db":   r.TruePeakDb,
			"sample_peak_db": r.SamplePeakDb,
			"isp_count":      r.ISPCount,
			"isp_max_db":     r.ISPMaxDb,
			"frames":         r.Frames,
		}
	}

	if r := result.Loudness; r != nil {
		meta["loudness"] = map[string]any{
			"integrated_lufs": r.IntegratedLUFS,
			"short_term_max":  r.ShortTermMax,
			"momentary_max":   r.MomentaryMax,
			"loudness_range":  r.LoudnessRange,
			"dr_score":        r.DRScore,
			"dr_value":        r.DRValue,
			"peak_db":         r.PeakDb,
			"rms_db":          r.RmsDb,
			"frames":          r.Frames,
		}
	}

	if r := result.Dropout; r != nil {
		meta["dropouts"] = dropoutToMap(r)
	}

	return &format.Data{
		Object: filePath,
		Meta:   meta,
	}
}

func clippingToMap(r *types.ClippingDetection) map[string]any {
	channels := make([]any, 0, len(r.Channels))
	for i, ch := range r.Channels {
		channels = append(channels, map[string]any{
			"channel":         i,
			"events":          ch.Events,
			"clipped_samples": ch.ClippedSamples,
			"longest_run":     ch.LongestRun,
		})
	}

	return map[string]any{
		"events":          r.Events,
		"clipped_samples": r.ClippedSamples,
		"longest_run":     r.LongestRun,
		"samples":         r.Samples,
		"channels":        channels,
	}
}

func spectralToMap(r *types.SpectralResult) map[string]any {
	m := map[string]any{
		"claimed_rate":      r.ClaimedRate,
		"is_upsampled":      r.IsUpsampled,
		"is_transcode":      r.IsTranscode,
		"has_50hz_hum":      r.Has50HzHum,
		"has_60hz_hum":      r.Has60HzHum,
		"hum_level_db":      r.HumLevelDb,
		"noise_floor_db":    r.NoiseFloorDb,
		"spectral_centroid": r.SpectralCentroid,
		"frames":            r.Frames,
	}

	if r.IsUpsampled {
		m["effective_rate"] = r.EffectiveRate
		m["upsample_cutoff"] = r.UpsampleCutoff
		m["upsample_sharpness"] = r.UpsampleSharpness
	}

	if r.IsTranscode {
		m["transcode_cutoff"] = r.TranscodeCutoff
		m["transcode_sharpness"] = r.TranscodeSharpness
		m["likely_codec"] = r.LikelyCodec
	}

	if len(r.BandEnergy) > 0 {
		bands := make([]any, 0, len(r.BandEnergy))
		for i, e := range r.BandEnergy {
			entry := map[string]any{
				"energy_db": e,
			}
			if i < len(r.BandFreqs) {
				entry["freq_hz"] = r.BandFreqs[i]
			}

			bands = append(bands, entry)
		}

		m["band_energy"] = bands
	}

	return m
}

func silenceToMap(r *types.SilenceResult) map[string]any {
	segments := make([]any, 0, len(r.Segments))
	for _, seg := range r.Segments {
		segments = append(segments, map[string]any{
			"start_sec":    seg.StartSec,
			"end_sec":      seg.EndSec,
			"duration_sec": seg.DurationSec,
			"rms_db":       seg.RmsDb,
		})
	}

	return map[string]any{
		"total_duration": r.TotalDuration,
		"leading_sec":    r.LeadingSec,
		"trailing_sec":   r.TrailingSec,
		"total_silence":  r.TotalSilence,
		"frames":         r.Frames,
		"segments":       segments,
	}
}

func dropoutToMap(r *types.DropoutResult) map[string]any {
	events := make([]any, 0, len(r.Events))
	for _, e := range r.Events {
		ev := map[string]any{
			"time_sec": e.TimeSec,
			"channel":  e.Channel,
			"type":     e.Type.String(),
			"severity": fmt.Sprintf("%.4f", e.Severity),
		}
		if e.Type == types.EventZeroRun {
			ev["duration_ms"] = e.DurationMs
		}

		events = append(events, ev)
	}

	return map[string]any{
		"delta_count":    r.DeltaCount,
		"zero_run_count": r.ZeroRunCount,
		"dc_jump_count":  r.DCJumpCount,
		"worst_db":       r.WorstDb,
		"frames":         r.Frames,
		"events":         events,
	}
}
