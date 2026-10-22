//go:build linux || darwin

package source

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureGitProcess gives this invocation its own process group. Cancelling
// Git also cancels transport/helpers holding pipes, never another caller's job.
func configureGitProcess(command *exec.Cmd) error {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}
