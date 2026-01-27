package tests_test

import (
	"testing"

	"github.com/containerd/nerdctl/mod/tigron/expect"
	"github.com/containerd/nerdctl/mod/tigron/test"

	"github.com/farcloser/agar/pkg/agar"

	"github.com/farcloser/haustorium/tests/testutils"
)

func TestNoiseFloor(t *testing.T) {
	testCase := testutils.Setup()

	testCase.SubTests = []*test.Case{
		{
			Description: "white noise has elevated noise floor",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.NoiseFloorHigh(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "noise-floor", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectIssueDetected("noise-floor"),
				}
			},
		},
		{
			Description: "band-limited audio has clean noise floor",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.NoiseFloorClean(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "noise-floor", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectNoIssue("noise-floor"),
				}
			},
		},
	}

	testCase.Run(t)
}
