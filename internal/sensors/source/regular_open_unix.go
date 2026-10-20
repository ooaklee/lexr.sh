//go:build linux || darwin

package source

import (
	"errors"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

// regularReadSupported admits platforms with the required confined-read APIs.
func regularReadSupported() error { return nil }

// Root.OpenFile resolves confined symlinks, even with O_NOFOLLOW supplied.
// Retain a confined parent handle, then apply true no-follow/nonblocking flags
// to only the final leaf. FDStat precedes any read, including after a type swap.
func openRegularLeaf(root *os.Root, name string) (*os.File, error) {
	parent, err := root.OpenRoot(path.Dir(name))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	directory, err := parent.Open(".")
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	fd, err := unix.Openat(int(directory.Fd()), path.Base(name), unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if err := validateRegularFD(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// validateRegularFD requires an ordinary singly linked file without special bits.
func validateRegularFD(file *os.File) error {
	var info unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &info); err != nil {
		return err
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 || info.Mode&(unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) != 0 {
		return errors.New("opened source leaf must be regular, singly linked and without special mode bits")
	}
	return nil
}

// directoryContains checks actual directory ancestry, not pathname spelling.
// Only directory metadata is inspected outside the retained roots. Errors or
// unexpectedly deep ancestry fail closed; no file payload is opened here.
func directoryContains(ancestor, descendant *os.Root) (bool, error) {
	wanted, err := ancestor.Stat(".")
	if err != nil {
		return false, err
	}
	current, err := descendant.Open(".")
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
		parent := os.NewFile(uintptr(fd), "directory ancestor")
		parentInfo, err := parent.Stat()
		if err != nil {
			_ = parent.Close()
			return false, err
		}
		if os.SameFile(info, parentInfo) {
			_ = parent.Close()
			return false, nil
		}
		_ = current.Close()
		current = parent
	}
	return false, errors.New("directory ancestry exceeds its fixed safety bound")
}

// directoryPathMatches checks that a pathname still names its retained directory.
func directoryPathMatches(name string, root *os.Root) error {
	// O_DIRECTORY makes a concurrent FIFO/type substitution nonblocking too.
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	current := os.NewFile(uintptr(fd), "directory pathname identity")
	defer current.Close()
	observed, err := current.Stat()
	if err != nil {
		return err
	}
	expected, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !observed.IsDir() || !os.SameFile(observed, expected) {
		return errors.New("directory pathname no longer identifies the retained root")
	}
	return nil
}
