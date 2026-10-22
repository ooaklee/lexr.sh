//go:build linux || darwin

package build

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// inspectWorkParent requires an owned, private directory opened without symlinks.
func inspectWorkParent(name string) error {
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil {
		return err
	}
	if info.Uid != uint32(os.Geteuid()) || info.Mode&0o7777 != 0o700 {
		return errors.New("sensor work parent must be current-user-owned with mode 0700")
	}
	return nil
}

// Compare directory identities and namespace ancestry. Linux mount topology
// needs its separate coordinate check for descendant bind-mount aliases.
// Only directory metadata is read; no file payload outside selected roots is opened.
func buildDirectoryContains(ancestor, descendant string) (bool, error) {
	open := func(name string) (*os.File, error) {
		fd, err := unix.Open(name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(fd), "sensor directory"), nil
	}
	a, err := open(ancestor)
	if err != nil {
		return false, err
	}
	defer a.Close()
	wanted, err := a.Stat()
	if err != nil {
		return false, err
	}
	current, err := open(descendant)
	if err != nil {
		return false, err
	}
	defer func() { _ = current.Close() }()
	for depth := 0; depth < 4096; depth++ {
		info, err := current.Stat()
		if err != nil {
			return false, err
		}
		if os.SameFile(wanted, info) {
			return true, nil
		}
		fd, err := unix.Openat(int(current.Fd()), "..", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return false, err
		}
		parent := os.NewFile(uintptr(fd), "sensor ancestor")
		pinfo, err := parent.Stat()
		if err != nil {
			_ = parent.Close()
			return false, err
		}
		if os.SameFile(info, pinfo) {
			_ = parent.Close()
			return false, nil
		}
		_ = current.Close()
		current = parent
	}
	return false, errors.New("sensor directory ancestry exceeds fixed bound")
}
