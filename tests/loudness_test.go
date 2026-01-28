package tests_test

import (
	"testing"

	"github.com/containerd/nerdctl/mod/tigron/expect"
	"github.com/containerd/nerdctl/mod/tigron/test"

	"github.com/farcloser/agar/pkg/agar"

	"github.com/farcloser/haustorium/tests/testutils"
)

func TestLoudness(t *testing.T) {
	testCase := testutils.Setup()

	testCase.SubTests = []*test.Case{
		{
			Description: "quiet audio reports loudness info",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.LowLoudnessQuiet(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "loudness", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output: expect.All(
						expectContains("integrated_lufs"),
						expectNoIssue("loudness"),
					),
				}
			},
		},
		{
			Description: "normal audio reports loudness info",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.Genuine16bit44k(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "loudness", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output: expect.All(
						expectContains("integrated_lufs"),
						expectNoIssue("loudness"),
					),
				}
			},
		},
	}

	testCase.Run(t)
}
