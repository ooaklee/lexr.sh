//go:build linux

package bootidentity

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Test injection only, never selected by a flag, environment or source input.
var btrfsCommand = exec.CommandContext

// attestBtrfsDefaultRoot is optional read-only evidence, never an override.
// Pin the selected directory through fd 3, not a second path lookup by btrfs.
// A missing tool/permission, timeout, output drift or non-default ID is unknown.
func attestBtrfsDefaultRoot(ctx context.Context, path string, expected unix.Stat_t) bool {
	if ctx.Err() != nil {
		return false
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	directory := os.NewFile(uintptr(fd), "btrfs-root-evidence")
	defer directory.Close()
	var actual unix.Stat_t
	if unix.Fstat(fd, &actual) != nil || actual.Dev != expected.Dev || actual.Ino != expected.Ino || actual.Mode != expected.Mode {
		return false
	}
	// Do not search caller-controlled PATH or execute a per-user wrapper.
	tool := ""
	for _, candidate := range []string{"/usr/bin/btrfs", "/usr/sbin/btrfs", "/sbin/btrfs", "/bin/btrfs"} {
		if unix.Access(candidate, unix.X_OK) == nil {
			tool = candidate
			break
		}
	}
	if tool == "" {
		return false
	}
	return runBtrfsDefaultProbe(ctx, tool, directory)
}

// runBtrfsDefaultProbe is a closed read-only command boundary. The private
// command dependency lets Linux fixtures exercise it without a Btrfs mount.
func runBtrfsDefaultProbe(ctx context.Context, tool string, directory *os.File) bool {
	switch tool {
	case "/usr/bin/btrfs", "/usr/sbin/btrfs", "/sbin/btrfs", "/bin/btrfs":
	default:
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := btrfsCommand(bounded, tool, "subvolume", "get-default", "/proc/self/fd/3")
	command.Env = []string{"LC_ALL=C", "LANG=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	command.ExtraFiles = []*os.File{directory}
	command.WaitDelay = 250 * time.Millisecond
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := unix.Kill(-command.Process.Pid, unix.SIGKILL)
		if err == unix.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	stdout, stderr := &btrfsOutput{limit: 128}, &btrfsOutput{limit: 1024}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Start(); err != nil {
		return false
	}
	err := command.Wait()
	// A direct child exit must not leave a pipe-holding helper behind. Never
	// accept that state; signal only this command's independently created group.
	clean, settled := settleBtrfsProcessGroup(command.Process.Pid)
	if !clean || !settled {
		return false
	}
	return bounded.Err() == nil && btrfsDefaultRootOutput(stdout.Bytes(), stderr.Bytes(), err)
}

// settleBtrfsProcessGroup checks the independently created group even if its
// direct child already exited. Cleanup, even successful, never grants proof.
func settleBtrfsProcessGroup(pid int) (clean, settled bool) {
	err := unix.Kill(-pid, 0)
	if err == unix.ESRCH {
		return true, true
	}
	if err != nil {
		return false, false
	}
	if err := unix.Kill(-pid, unix.SIGKILL); err != nil && err != unix.ESRCH {
		return false, false
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		err := unix.Kill(-pid, 0)
		if err == unix.ESRCH {
			return false, true
		}
		if err != nil {
			return false, false
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false, false
}
