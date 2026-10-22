//go:build !linux && !darwin

package source

import (
	"errors"
	"os/exec"
)

// configureGitProcess refuses hosts without the reviewed process-group boundary.
func configureGitProcess(*exec.Cmd) error {
	return errors.New("isolated userspace source preparation requires Linux or macOS")
}
