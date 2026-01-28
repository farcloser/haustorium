//nolint:wrapcheck
package main

import (
	"fmt"
	"math"
	"os"

	"github.com/farcloser/primordium/format"

	"github.com/farcloser/haustorium"
	"github.com/farcloser/haustorium/internal/output"
)

func outputResult(filePath string, result *haustorium.Result, formatName string, debug bool) error {
	formatter, err := format.GetFormatter(formatName)
	if err != nil {
		return err
	}

	var meta map[string]any
	if debug {
		meta = output.ResultToMap(result)
	} else {
		meta = buildFriendlyOutput(result)
	}

	data := &format.Data{
		Object: filePath,
		Meta:   meta,
	}

	return formatter.PrintAll([]*format.Data{data}, os.Stdout)
}

// buildFriendlyOutput creates a user-friendly summary of the analysis results.
func buildFriendlyOutput(result *haustorium.Result) map[string]any {
	meta := map[string]any{
		"summary": fmt.Sprintf("%d issues found (worst: %s)", result.IssueCount, result.WorstSeverity),
	}

	// Issues list.
	if len(result.Issues) > 0 {
		issues := make([]any, 0, len(result.Issues))
		for _, issue := range result.Issues {
			marker := "  "
			if issue.Detected {
				marker = "!!"
			}

			issues = append(issues, fmt.Sprintf("%s [%s] %s: %s (%.0f%% confidence)",
				marker, issue.Severity, issue.Check, issue.Summary, issue.Confidence*100))
		}

		meta["issues"] = issues
	}

	// Key properties.
	props := buildProperties(result)
	if len(props) > 0 {
		meta["properties"] = props
	}

	return meta
}

func buildProperties(result *haustorium.Result) map[string]any {
	props := make(map[string]any)

	if r := result.Loudness; r != nil {
		props["loudness"] = fmt.Sprintf("%.1f LUFS (range: %.1f LU)", r.IntegratedLUFS, r.LoudnessRange)
		props["dynamic_range"] = fmt.Sprintf("DR%d", r.DRScore)
	}

	if r := result.TruePeak; r != nil {
		props["true_peak"] = fmt.Sprintf("%.1f dBTP", r.TruePeakDb)
	}

	if r := result.Spectral; r != nil {
		props["spectral_centroid"] = fmt.Sprintf("%.0f Hz", r.SpectralCentroid)
		props["noise_floor"] = fmt.Sprintf("%.1f dB", r.NoiseFloorDb)
	}

	if r := result.Stereo; r != nil {
		props["stereo_width"] = fmt.Sprintf("%s (correlation: %.2f)", stereoWidthLabel(r.Correlation), r.Correlation)
		if math.Abs(r.ImbalanceDb) > 0.5 {
			props["channel_imbalance"] = fmt.Sprintf("%.1f dB (%s louder)", math.Abs(r.ImbalanceDb), imbalanceSide(r.ImbalanceDb))
		}
	}

	if r := result.BitDepth; r != nil {
		if r.Claimed != r.Effective {
			props["bit_depth"] = fmt.Sprintf("%d-bit (effective: %d-bit)", r.Claimed, r.Effective)
		} else {
			props["bit_depth"] = fmt.Sprintf("%d-bit", r.Claimed)
		}
	}

	return props
}

func stereoWidthLabel(correlation float64) string {
	switch {
	case correlation > 0.95:
		return "Mono/Narrow"
	case correlation > 0.75:
		return "Narrow"
	case correlation > 0.5:
		return "Normal"
	case correlation > 0.2:
		return "Wide"
	default:
		return "Very Wide"
	}
}

func imbalanceSide(imbalanceDb float64) string {
	if imbalanceDb > 0 {
		return "left"
	}

	return "right"
}
