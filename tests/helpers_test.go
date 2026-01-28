package tests_test

import (
	"fmt"
	"strings"

	"github.com/containerd/nerdctl/mod/tigron/test"
	"github.com/containerd/nerdctl/mod/tigron/tig"
)

// expectIssue returns a comparator verifying that the given check was detected with the given severity.
// It looks for an issue block containing: check: <check>, detected: true, severity: <severity>.
func expectIssue(check, severity string) test.Comparator {
	return func(stdout string, testing tig.T) {
		testing.Helper()

		checkLine := fmt.Sprintf("check: %s", check)
		detectedLine := "detected: true"
		severityLine := fmt.Sprintf("severity: %s", severity)

		if strings.Contains(stdout, checkLine) &&
			strings.Contains(stdout, detectedLine) &&
			strings.Contains(stdout, severityLine) {
			return
		}

		testing.Log(
			fmt.Sprintf("expected issue %q with severity %q not found in output:\n%s", check, severity, stdout),
		)
		testing.Fail()
	}
}

// expectIssueDetected returns a comparator verifying that the given check was detected (any severity).
// It looks for an issue block containing: check: <check>, detected: true.
func expectIssueDetected(check string) test.Comparator {
	return func(stdout string, testing tig.T) {
		testing.Helper()

		checkLine := fmt.Sprintf("check: %s", check)
		detectedLine := "detected: true"

		if strings.Contains(stdout, checkLine) && strings.Contains(stdout, detectedLine) {
			return
		}

		testing.Log(fmt.Sprintf("expected issue %q to be detected but was not found in output:\n%s", check, stdout))
		testing.Fail()
	}
}

// expectNoIssue returns a comparator verifying that the given check was NOT detected.
// It looks for check: <check> paired with detected: false, or absence of the check entirely.
func expectNoIssue(check string) test.Comparator {
	return func(stdout string, testing tig.T) {
		testing.Helper()

		checkLine := fmt.Sprintf("check: %s", check)

		if !strings.Contains(stdout, checkLine) {
			// Check not present at all — that's fine, means it wasn't run.
			return
		}

		detectedLine := "detected: true"
		if strings.Contains(stdout, detectedLine) {
			// Need to verify this "detected: true" belongs to the same check.
			// Parse issue blocks to find the right one.
			if issueBlockContains(stdout, check, "detected: true") {
				testing.Log(fmt.Sprintf("expected no issue for %q but it was detected in output:\n%s", check, stdout))
				testing.Fail()
			}
		}
	}
}

// issueBlockContains checks whether an issue block for the given check contains the target string.
// It scans for "check: <check>" and then looks in adjacent lines for the target.
func issueBlockContains(stdout, check, target string) bool {
	lines := strings.Split(stdout, "\n")
	checkLine := fmt.Sprintf("check: %s", check)

	for i, line := range lines {
		if !strings.Contains(line, checkLine) {
			continue
		}

		// Search nearby lines (within the same issue block).
		for j := max(0, i-5); j < min(len(lines), i+5); j++ {
			if strings.Contains(lines[j], target) {
				return true
			}
		}
	}

	return false
}

// expectWorstSeverity returns a comparator verifying the worst severity in the summary.
func expectWorstSeverity(severity string) test.Comparator {
	return func(stdout string, testing tig.T) {
		testing.Helper()

		expected := fmt.Sprintf("worst_severity: %s", severity)

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
