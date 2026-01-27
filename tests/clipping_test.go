package tests_test

import (
	"testing"

	"github.com/containerd/nerdctl/mod/tigron/expect"
	"github.com/containerd/nerdctl/mod/tigron/test"

	"github.com/farcloser/agar/pkg/agar"

	"github.com/farcloser/haustorium/tests/testutils"
)

func TestClipping(t *testing.T) {
	testCase := testutils.Setup()

	testCase.SubTests = []*test.Case{
		{
			Description: "hard clipped audio is detected",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.ClippedHard(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "clipping", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectIssueDetected("clipping"),
				}
			},
		},
		{
			Description: "clean audio has no clipping",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.Genuine16bit44k(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "clipping", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectNoIssue("clipping"),
				}
			},
		},
		{
			Description: "limited audio does not clip",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.ClippedLimited(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "clipping", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectNoIssue("clipping"),
				}
			},
		},
	}

	testCase.Run(t)
}
