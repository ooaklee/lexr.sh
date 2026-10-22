//go:build linux || darwin

package compatibility

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// OpenRegular opens a rooted regular file without blocking on a swapped FIFO
// or escaping through a symlink. The caller owns the returned descriptor.
func OpenRegular(root *os.Root, path string) (*os.File, error) {
	file, err := root.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("target evidence is not a regular file")
	}
	return file, nil
}
