package tests_test

import (
	"testing"

	"github.com/containerd/nerdctl/mod/tigron/expect"
	"github.com/containerd/nerdctl/mod/tigron/test"

	"github.com/farcloser/agar/pkg/agar"

	"github.com/farcloser/haustorium/tests/testutils"
)

func TestLossyTranscode(t *testing.T) {
	testCase := testutils.Setup()

	testCase.SubTests = []*test.Case{
		{
			Description: "MP3 128k transcode detected",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.LossyTranscodeMP3128k(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "lossy-transcode", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectIssueDetected("lossy-transcode"),
				}
			},
		},
		{
			Description: "genuine lossless not flagged",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.Genuine24bit96k(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "lossy-transcode", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectNoIssue("lossy-transcode"),
				}
			},
		},
	}

	testCase.Run(t)
}
