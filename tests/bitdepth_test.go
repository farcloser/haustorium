package tests_test

import (
	"testing"

	"github.com/containerd/nerdctl/mod/tigron/expect"
	"github.com/containerd/nerdctl/mod/tigron/test"

	"github.com/farcloser/agar/pkg/agar"

	"github.com/farcloser/haustorium/tests/testutils"
)

func TestFakeBitDepth(t *testing.T) {
	testCase := testutils.Setup()

	testCase.SubTests = []*test.Case{
		{
			Description: "padded 24-bit detected as fake",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.FakeHiresPadded24bit(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "fake-bit-depth", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectIssue("fake-bit-depth", "severe"),
				}
			},
		},
		{
			Description: "genuine 24-bit not flagged",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.Genuine24bit96k(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "fake-bit-depth", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectNoIssue("fake-bit-depth"),
				}
			},
		},
	}

	testCase.Run(t)
}
