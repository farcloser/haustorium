package tests_test

import (
	"fmt"
	"strings"

	"github.com/containerd/nerdctl/mod/tigron/test"
	"github.com/containerd/nerdctl/mod/tigron/tig"
)

// expectIssue returns a comparator verifying that the given check was detected with the given severity.
// It looks for a line matching: !! [<severity>] [<check>].
func expectIssue(check, severity string) test.Comparator {
	return func(stdout string, testing tig.T) {
		testing.Helper()

		pattern := fmt.Sprintf("!! [%s] [%s]", severity, check)

		if !strings.Contains(stdout, pattern) {
			testing.Log(
				fmt.Sprintf("expected issue %q with severity %q not found in output:\n%s", check, severity, stdout),
			)
			testing.Fail()
		}
	}
}

// expectIssueDetected returns a comparator verifying that the given check was detected (any severity).
// It looks for a line matching: !! [*] [<check>].
func expectIssueDetected(check string) test.Comparator {
	return func(stdout string, testing tig.T) {
		testing.Helper()

		marker := fmt.Sprintf("] [%s]", check)

		for _, line := range strings.Split(stdout, "\n") {
			if strings.Contains(line, marker) && strings.HasPrefix(strings.TrimSpace(line), "!!") {
				return
			}
		}

		testing.Log(fmt.Sprintf("expected issue %q to be detected but was not found in output:\n%s", check, stdout))
		testing.Fail()
	}
}

// expectNoIssue returns a comparator verifying that the given check was NOT detected.
// The check line should exist but NOT start with "!!".
func expectNoIssue(check string) test.Comparator {
	return func(stdout string, testing tig.T) {
		testing.Helper()

		marker := fmt.Sprintf("] [%s]", check)

		for _, line := range strings.Split(stdout, "\n") {
			if strings.Contains(line, marker) {
				if strings.HasPrefix(strings.TrimSpace(line), "!!") {
					testing.Log(fmt.Sprintf("expected no issue for %q but found: %s", check, line))
					testing.Fail()
				}

				return
			}
		}

		// Check line not present at all — that's fine, means it wasn't run.
	}
}

// expectWorstSeverity returns a comparator verifying the worst severity in the header line.
func expectWorstSeverity(severity string) test.Comparator {
	return func(stdout string, testing tig.T) {
		testing.Helper()

		expected := fmt.Sprintf("worst severity: %s", severity)

		if !strings.Contains(stdout, expected) {
			testing.Log(fmt.Sprintf("expected worst severity %q not found in output:\n%s", severity, stdout))
			testing.Fail()
		}
	}
}

// expectContains returns a comparator verifying the output contains a substring.
func expectContains(substr string) test.Comparator {
	return func(stdout string, testing tig.T) {
		testing.Helper()

		if !strings.Contains(stdout, substr) {
			testing.Log(fmt.Sprintf("expected substring %q not found in output:\n%s", substr, stdout))
			testing.Fail()
		}
	}
}
