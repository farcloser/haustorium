package tests_test

import (
	"testing"

	"github.com/containerd/nerdctl/mod/tigron/expect"
	"github.com/containerd/nerdctl/mod/tigron/test"

	"github.com/farcloser/agar/pkg/agar"

	"github.com/farcloser/haustorium/tests/testutils"
)

func TestChannelImbalance(t *testing.T) {
	testCase := testutils.Setup()

	testCase.SubTests = []*test.Case{
		{
			Description: "left-heavy imbalance detected",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.ChannelImbalanceLeft(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "channel-imbalance", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectIssueDetected("channel-imbalance"),
				}
			},
		},
		{
			Description: "balanced stereo not flagged",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.TrueStereoDifferentChannels(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "channel-imbalance", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectNoIssue("channel-imbalance"),
				}
			},
		},
	}

	testCase.Run(t)
}
