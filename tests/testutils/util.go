// Package testutils provides test infrastructure for haustorium integration tests.
package testutils

import (
	"path/filepath"
	"runtime"

	"github.com/containerd/nerdctl/mod/tigron/test"

	"github.com/farcloser/agar/pkg/agar"
)

// Setup creates a test case configured to run the haustorium binary.
func Setup() *test.Case {
	_, thisFile, _, _ := runtime.Caller(0) //nolint:dogsled // runtime.Caller returns 4 values, only file is needed
	projectRoot := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
	binaryPath := filepath.Join(projectRoot, "bin", "haustorium")

	return agar.Setup(binaryPath)
}
