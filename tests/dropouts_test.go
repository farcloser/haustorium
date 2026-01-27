package tests_test

import (
	"testing"

	"github.com/containerd/nerdctl/mod/tigron/expect"
	"github.com/containerd/nerdctl/mod/tigron/test"

	"github.com/farcloser/agar/pkg/agar"

	"github.com/farcloser/haustorium/tests/testutils"
)

func TestDropouts(t *testing.T) {
	testCase := testutils.Setup()

	testCase.SubTests = []*test.Case{
		{
			Description: "clean audio has no dropouts",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.Genuine16bit44k(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "dropouts", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectNoIssue("dropouts"),
				}
			},
		},
	}

	testCase.Run(t)
}

// TestDropoutsPositive is a placeholder for dropout positive-detection tests.
//
// Dropout detection looks for zero-sample runs (>= 1ms) and sample-level
// discontinuities (delta jumps where one side is near zero). Generating audio
// with actual glitches using ffmpeg is not possible: ffmpeg's lavfi synthesis
// sources produce clean, continuous waveforms with no sample-level artifacts.
//
// A positive test requires either a pre-recorded file with known dropout events
// or programmatic construction of raw PCM with injected zero runs and delta
// discontinuities. This needs a dedicated agar fixture that writes raw samples
// directly rather than using ffmpeg.
func TestDropoutsPositive(t *testing.T) {
	t.Skip("blocked: no agar fixture can generate audio with dropout glitches using ffmpeg")
}
