package tests_test

import (
	"testing"

	"github.com/containerd/nerdctl/mod/tigron/expect"
	"github.com/containerd/nerdctl/mod/tigron/test"

	"github.com/farcloser/agar/pkg/agar"

	"github.com/farcloser/haustorium/tests/testutils"
)

func TestDynamicRange(t *testing.T) {
	testCase := testutils.Setup()

	testCase.SubTests = []*test.Case{
		{
			Description: "excellent dynamics not flagged",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.DynamicsExcellent(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "dynamic-range", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectNoIssue("dynamic-range"),
				}
			},
		},
		{
			Description: "brickwalled audio detected as severe",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.DynamicsFucked(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "dynamic-range", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectIssue("dynamic-range", "severe"),
				}
			},
		},
		{
			Description: "mediocre dynamics detected as moderate",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.DynamicsMediocre(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "dynamic-range", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectIssue("dynamic-range", "moderate"),
				}
			},
		},
		{
			Description: "OK dynamics flagged as mild",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.DynamicsOK(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "dynamic-range", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectIssue("dynamic-range", "mild"),
				}
			},
		},
	}

	testCase.Run(t)
}
