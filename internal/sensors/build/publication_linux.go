//go:build linux

package build

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// publicationSupported permits Linux's exclusive directory-rename boundary.
func publicationSupported() error { return nil }

// renamePublicationNoReplace moves within the retained parent without replacing
// an existing destination, even when another process creates it concurrently.
func renamePublicationNoReplace(parent *os.File, source, destination string) error {
	return unix.Renameat2(int(parent.Fd()), source, int(parent.Fd()), destination, unix.RENAME_NOREPLACE)
}

// openPublicationRegular requires one singly linked regular member beneath the
// retained directory and refuses symlink or blocking special-file substitution.
func openPublicationRegular(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 {
		_ = f.Close()
		return nil, errors.New("publication member is not a singly linked regular file")
	}
	return f, nil
}
