//go:build darwin

package build

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func publicationSupported() error { return nil }

func renamePublicationNoReplace(parent *os.File, source, destination string) error {
	return unix.RenameatxNp(int(parent.Fd()), source, int(parent.Fd()), destination, unix.RENAME_EXCL)
}

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
