package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	haustorium "github.com/farcloser/haustorium"
	"github.com/farcloser/haustorium/internal/types"
)

var errInvalidArgCount = errors.New("expected exactly one argument: file path or \"-\" for stdin")

func analyzeCommand() *cli.Command {
	return &cli.Command{
		Name:      "analyze",
		Usage:     "Analyze raw PCM audio for quality issues",
		ArgsUsage: "<file | ->",
		Flags: []cli.Flag{
			// PCMFormat flags.
			&cli.IntFlag{
				Name:     "sample-rate",
				Aliases:  []string{"s"},
				Usage:    "Sample rate in Hz (e.g., 44100, 48000, 96000)",
				Required: true,
			},
			&cli.IntFlag{
				Name:    "bit-depth",
				Aliases: []string{"b"},
				Usage:   "Bit depth (16, 24, or 32)",
				Value:   32,
			},
			&cli.IntFlag{
				Name:    "channels",
				Aliases: []string{"c"},
				Usage:   "Number of channels (1 = mono, 2 = stereo)",
				Value:   2,
			},
			&cli.IntFlag{
				Name:  "expected-bit-depth",
				Usage: "Expected bit depth for authenticity check (defaults to --bit-depth value)",
			},

			// Check selection.
			&cli.StringFlag{
				Name:    "checks",
				Aliases: []string{"C"},
				Usage:   "Comma-separated checks or presets: all, defects, loudness, clipping, truncation, fake-bit-depth, fake-sample-rate, lossy-transcode, dc-offset, fake-stereo, phase-issues, inverted-phase, channel-imbalance, silence-padding, hum, noise-floor, inter-sample-peaks, dynamic-range, dropouts",
				Value:   "all",
			},

			// Source type.
			&cli.StringFlag{
				Name:    "source",
				Aliases: []string{"S"},
				Usage:   "Audio source type adjusting detection thresholds: digital, vinyl, live",
				Value:   "digital",
			},

			// Output verbosity.
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"V"},
				Usage:   "Print all raw analyzer data alongside the summary",
			},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return fmt.Errorf("%w: got %d", errInvalidArgCount, cmd.NArg())
			}

			// Parse PCM format.
			format, err := parsePCMFormat(cmd)
			if err != nil {
				return err
			}

			// Parse checks.
			checks, err := parseChecks(cmd.String("checks"))
			if err != nil {
				return err
			}

			source, err := haustorium.ParseSource(cmd.String("source"))
			if err != nil {
				return err
			}

			opts := haustorium.OptionsForSource(source)
			opts.Checks = checks

			// Build reader factory.
			inputPath := cmd.Args().First()

			factory, cleanup, err := readerFactory(inputPath)
			if err != nil {
				return err
			}
			defer cleanup()

			// Run analysis.
			result, err := haustorium.Analyze(factory, format, opts)
			if err != nil {
				return fmt.Errorf("analysis failed: %w", err)
			}

			printResult(inputPath, result, cmd.Bool("verbose"))

			return nil
		},
	}
}

func parsePCMFormat(cmd *cli.Command) (types.PCMFormat, error) {
	sampleRate := cmd.Int("sample-rate")
	bitDepth := cmd.Int("bit-depth")
	channels := cmd.Int("channels")
	expectedBitDepth := cmd.Int("expected-bit-depth")

	bd, err := toBitDepth(int(bitDepth))
	if err != nil {
		return types.PCMFormat{}, fmt.Errorf("--bit-depth: %w", err)
	}

	ebd := bd
	if expectedBitDepth > 0 {
		ebd, err = toBitDepth(int(expectedBitDepth))
		if err != nil {
			return types.PCMFormat{}, fmt.Errorf("--expected-bit-depth: %w", err)
		}
	}

	return types.PCMFormat{
		SampleRate:       int(sampleRate),
		BitDepth:         bd,
		Channels:         uint(channels),
		ExpectedBitDepth: ebd,
	}, nil
}

var errInvalidBitDepth = errors.New("must be 16, 24, or 32")

func toBitDepth(v int) (types.BitDepth, error) {
	switch v {
	case 16:
		return types.Depth16, nil
	case 24:
		return types.Depth24, nil
	case 32:
		return types.Depth32, nil
	default:
		return 0, errInvalidBitDepth
	}
}

//nolint:gochecknoglobals
var checkNames = map[string]haustorium.Check{
	"clipping":           haustorium.CheckClipping,
	"truncation":         haustorium.CheckTruncation,
	"fake-bit-depth":     haustorium.CheckFakeBitDepth,
	"fake-sample-rate":   haustorium.CheckFakeSampleRate,
	"lossy-transcode":    haustorium.CheckLossyTranscode,
	"dc-offset":          haustorium.CheckDCOffset,
	"fake-stereo":        haustorium.CheckFakeStereo,
	"phase-issues":       haustorium.CheckPhaseIssues,
	"inverted-phase":     haustorium.CheckInvertedPhase,
	"channel-imbalance":  haustorium.CheckChannelImbalance,
	"silence-padding":    haustorium.CheckSilencePadding,
	"hum":                haustorium.CheckHum,
	"noise-floor":        haustorium.CheckNoiseFloor,
	"inter-sample-peaks": haustorium.CheckInterSamplePeaks,
	"loudness":           haustorium.CheckLoudness,
	"dynamic-range":      haustorium.CheckDynamicRange,
	"dropouts":           haustorium.CheckDropouts,
	// Presets.
	"all":     haustorium.ChecksAll,
	"defects": haustorium.ChecksDefects,
}

func parseChecks(raw string) (haustorium.Check, error) {
	var result haustorium.Check

	for name := range strings.SplitSeq(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		check, ok := checkNames[name]
		if !ok {
			return 0, fmt.Errorf("unknown check %q", name)
		}

		result |= check
	}

	if result == 0 {
		return haustorium.ChecksAll, nil
	}

	return result, nil
}

// readerFactory returns a factory that produces fresh readers for multi-pass analysis.
// For files, it re-opens the file each time. For stdin, it buffers the entire input.
func readerFactory(source string) (haustorium.ReaderFactory, func(), error) {
	if source == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, func() {}, fmt.Errorf("reading stdin: %w", err)
		}

		factory := func() (io.Reader, error) {
			return bytes.NewReader(data), nil
		}

		return factory, func() {}, nil
	}

	// Verify the file exists upfront.
	if _, err := os.Stat(source); err != nil {
		return nil, func() {}, fmt.Errorf("cannot access %s: %w", source, err)
	}

	factory := func() (io.Reader, error) {
		return os.Open(source)
	}

	return factory, func() {}, nil
}

func printResult(filePath string, result *haustorium.Result, verbose bool) {
	if filePath != "" && filePath != "-" {
		fmt.Printf("File: %s\n", filePath)
	}

	fmt.Printf("Issues found: %d (worst severity: %s)\n\n", result.IssueCount, result.WorstSeverity)

	for _, issue := range result.Issues {
		marker := "  "
		if issue.Detected {
			marker = "!!"
		}

		fmt.Printf("%s [%s] [%s] %s (confidence: %.0f%%)\n",
			marker, issue.Severity, issue.Check, issue.Summary, issue.Confidence*100)
	}

	printProperties(result)

	if verbose {
		printVerbose(result)
	}
}

func printProperties(result *haustorium.Result) {
	var props []string

	if r := result.Loudness; r != nil {
		props = append(props,
			fmt.Sprintf("  Loudness:          %.1f LUFS (range: %.1f LU)", r.IntegratedLUFS, r.LoudnessRange),
			fmt.Sprintf("  Dynamic Range:     DR%d", r.DRScore),
		)
	}

	if r := result.TruePeak; r != nil {
		props = append(props,
			fmt.Sprintf("  True Peak:         %.1f dBTP", r.TruePeakDb),
		)
	}

	if r := result.Spectral; r != nil {
		props = append(props,
			fmt.Sprintf("  Spectral Centroid: %.0f Hz", r.SpectralCentroid),
		)
	}

	if r := result.Stereo; r != nil {
		props = append(props,
			fmt.Sprintf("  Stereo Width:      %s (correlation: %.2f)", stereoWidthLabel(r.Correlation), r.Correlation),
		)
	}

	if len(props) == 0 {
		return
	}

	fmt.Printf("\nProperties:\n")

	for _, p := range props {
		fmt.Println(p)
	}
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

func printVerbose(result *haustorium.Result) {
	fmt.Println("\n--- Verbose Output ---")

	if r := result.Clipping; r != nil {
		fmt.Println("\n[Clipping]")
		fmt.Printf("  Events:          %d\n", r.Events)
		fmt.Printf("  Clipped samples: %d\n", r.ClippedSamples)
		fmt.Printf("  Longest run:     %d samples\n", r.LongestRun)
		fmt.Printf("  Total samples:   %d\n", r.Samples)

		for i, ch := range r.Channels {
			fmt.Printf("  Channel %d:       %d events, %d clipped, longest run %d\n",
				i, ch.Events, ch.ClippedSamples, ch.LongestRun)
		}
	}

	if r := result.Truncation; r != nil {
		fmt.Println("\n[Truncation]")
		fmt.Printf("  Final RMS:       %.1f dB\n", r.FinalRmsDb)
		fmt.Printf("  Final peak:      %.1f dB\n", r.FinalPeakDb)
		fmt.Printf("  Samples in tail: %d\n", r.SamplesInTail)
	}

	if r := result.BitDepth; r != nil {
		fmt.Println("\n[Bit Depth]")
		fmt.Printf("  Claimed:         %d-bit\n", r.Claimed)
		fmt.Printf("  Effective:       %d-bit\n", r.Effective)
		fmt.Printf("  Padded:          %v\n", r.IsPadded)
		fmt.Printf("  Samples:         %d\n", r.Samples)
	}

	if r := result.Spectral; r != nil {
		fmt.Println("\n[Spectral]")
		fmt.Printf("  Claimed rate:    %d Hz\n", r.ClaimedRate)

		if r.IsUpsampled {
			fmt.Printf("  Upsampled:       yes (from %d Hz)\n", r.EffectiveRate)
			fmt.Printf("  Cutoff:          %.0f Hz\n", r.UpsampleCutoff)
			fmt.Printf("  Sharpness:       %.1f dB/oct\n", r.UpsampleSharpness)
		} else {
			fmt.Printf("  Upsampled:       no\n")
		}

		if r.IsTranscode {
			fmt.Printf("  Transcode:       yes (%s)\n", r.LikelyCodec)
			fmt.Printf("  Cutoff:          %.0f Hz\n", r.TranscodeCutoff)
			fmt.Printf("  Sharpness:       %.1f dB/oct\n", r.TranscodeSharpness)
		} else {
			fmt.Printf("  Transcode:       no\n")
		}

		fmt.Printf("  50 Hz hum:       %v\n", r.Has50HzHum)
		fmt.Printf("  60 Hz hum:       %v\n", r.Has60HzHum)

		if r.Has50HzHum || r.Has60HzHum {
			fmt.Printf("  Hum level:       %.1f dB\n", r.HumLevelDb)
		}

		fmt.Printf("  Noise floor:     %.1f dB\n", r.NoiseFloorDb)
		fmt.Printf("  Centroid:        %.0f Hz\n", r.SpectralCentroid)
		fmt.Printf("  Frames:          %d\n", r.Frames)
	}

	if r := result.DCOffset; r != nil {
		fmt.Println("\n[DC Offset]")
		fmt.Printf("  Overall:         %.6f (%.1f dB)\n", r.Offset, r.OffsetDb)

		for i, ch := range r.Channels {
			fmt.Printf("  Channel %d:       %.6f\n", i, ch)
		}

		fmt.Printf("  Samples:         %d\n", r.Samples)
	}

	if r := result.Stereo; r != nil {
		fmt.Println("\n[Stereo]")
		fmt.Printf("  Correlation:     %.4f\n", r.Correlation)
		fmt.Printf("  L-R difference:  %.1f dB\n", r.DifferenceDb)
		fmt.Printf("  Mono sum:        %.1f dB\n", r.MonoSumDb)
		fmt.Printf("  Stereo RMS:      %.1f dB\n", r.StereoRmsDb)
		fmt.Printf("  Cancellation:    %.1f dB\n", r.CancellationDb)
		fmt.Printf("  Left RMS:        %.1f dB\n", r.LeftRmsDb)
		fmt.Printf("  Right RMS:       %.1f dB\n", r.RightRmsDb)
		fmt.Printf("  Imbalance:       %.1f dB (%s louder)\n", math.Abs(r.ImbalanceDb), imbalanceSide(r.ImbalanceDb))
		fmt.Printf("  Frames:          %d\n", r.Frames)
	}

	if r := result.Silence; r != nil {
		fmt.Println("\n[Silence]")
		fmt.Printf("  Total duration:  %.1fs\n", r.TotalDuration)
		fmt.Printf("  Leading:         %.2fs\n", r.LeadingSec)
		fmt.Printf("  Trailing:        %.2fs\n", r.TrailingSec)
		fmt.Printf("  Total silence:   %.2fs\n", r.TotalSilence)
		fmt.Printf("  Segments:        %d\n", len(r.Segments))

		for i, seg := range r.Segments {
			fmt.Printf("    %d. %.2f-%.2fs (%.2fs) at %.1f dB\n",
				i+1, seg.StartSec, seg.EndSec, seg.DurationSec, seg.RmsDb)
		}
	}

	if r := result.TruePeak; r != nil {
		fmt.Println("\n[True Peak]")
		fmt.Printf("  True peak:       %.2f dBTP\n", r.TruePeakDb)
		fmt.Printf("  Sample peak:     %.2f dB\n", r.SamplePeakDb)
		fmt.Printf("  ISP count:       %d\n", r.ISPCount)
		fmt.Printf("  ISP max:         %.2f dB\n", r.ISPMaxDb)
		fmt.Printf("  Frames:          %d\n", r.Frames)
	}

	if r := result.Loudness; r != nil {
		fmt.Println("\n[Loudness]")
		fmt.Printf("  Integrated:      %.1f LUFS\n", r.IntegratedLUFS)
		fmt.Printf("  Short-term max:  %.1f LUFS\n", r.ShortTermMax)
		fmt.Printf("  Momentary max:   %.1f LUFS\n", r.MomentaryMax)
		fmt.Printf("  Loudness range:  %.1f LU\n", r.LoudnessRange)
		fmt.Printf("  DR score:        DR%d\n", r.DRScore)
		fmt.Printf("  DR value:        %.1f\n", r.DRValue)
		fmt.Printf("  Peak:            %.1f dB\n", r.PeakDb)
		fmt.Printf("  RMS:             %.1f dB\n", r.RmsDb)
		fmt.Printf("  Frames:          %d\n", r.Frames)
	}

	if r := result.Dropout; r != nil {
		fmt.Println("\n[Dropouts]")
		fmt.Printf("  Delta count:     %d\n", r.DeltaCount)
		fmt.Printf("  Zero run count:  %d\n", r.ZeroRunCount)
		fmt.Printf("  DC jump count:   %d\n", r.DCJumpCount)
		fmt.Printf("  Worst:           %.1f dB\n", r.WorstDb)
		fmt.Printf("  Frames:          %d\n", r.Frames)

		if len(r.Events) > 0 {
			fmt.Printf("  Events:\n")

			for i, e := range r.Events {
				switch e.Type {
				case types.EventZeroRun:
					fmt.Printf("    %d. %s @ %.3fs ch%d duration=%.1fms\n",
						i+1, e.Type, e.TimeSec, e.Channel, e.DurationMs)
				default:
					fmt.Printf("    %d. %s @ %.3fs ch%d severity=%.4f\n",
						i+1, e.Type, e.TimeSec, e.Channel, e.Severity)
				}
			}
		}
	}
}

func imbalanceSide(imbalanceDb float64) string {
	if imbalanceDb >= 0 {
		return "left"
	}

	return "right"
}
