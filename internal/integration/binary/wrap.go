package binary

import (
	"os/exec"
)

// Available checks if a binary is available in the system PATH.
func Available(binName string) (string, bool) {
	path, err := exec.LookPath(binName)

	return path, err == nil
}
