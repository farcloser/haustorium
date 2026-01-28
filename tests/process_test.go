package tests_test

import (
	"testing"

	"github.com/containerd/nerdctl/mod/tigron/expect"
	"github.com/containerd/nerdctl/mod/tigron/test"

	"github.com/farcloser/agar/pkg/agar"

	"github.com/farcloser/haustorium/tests/testutils"
)

func TestProcessCLI(t *testing.T) {
	testCase := testutils.Setup()

	testCase.SubTests = []*test.Case{
		{
			Description: "process without arguments fails",
			Command:     test.Command("process"),
			Expected:    test.Expects(expect.ExitCodeGenericFail, nil, nil),
		},
		{
			Description: "process nonexistent file fails",
			Command:     test.Command("process", "/nonexistent/path/file.flac"),
			Expected:    test.Expects(expect.ExitCodeGenericFail, nil, nil),
		},
		{
			Description: "process with single check runs only that check",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.Genuine16bit44k(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", "--checks", "clipping", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output: expect.All(
						expectContains("clipping:"),
						expectWorstSeverity("no issue"),
					),
				}
			},
		},
		{
			Description: "process with --source vinyl adjusts thresholds",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.Genuine16bit44k(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command(
					"process",
					"--source",
					"vinyl",
					"--checks",
					"clipping",
					data.Labels().Get("file"),
				)
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectContains("issues:"),
				}
			},
		},
		{
			Description: "process all checks on clean file",
			Setup: func(data test.Data, helpers test.Helpers) {
				data.Labels().Set("file", agar.Genuine16bit44k(data, helpers))
			},
			Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
				return helpers.Command("process", data.Labels().Get("file"))
			},
			Expected: func(_ test.Data, _ test.Helpers) *test.Expected {
				return &test.Expected{
					ExitCode: expect.ExitCodeSuccess,
					Output:   expectContains("issues:"),
				}
			},
		},
	}

	testCase.Run(t)
}
