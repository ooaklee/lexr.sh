//go:build !windows

package install

import (
	"fmt"
	"os"
	"syscall"
)

// openRegularNoFollow pins a regular inode without following a final symbolic
// link on Unix-like hosts.
func openRegularNoFollow(path string) (*os.File, os.FileInfo, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, fmt.Errorf("source is not a regular file: %s", path)
	}
	return file, info, nil
}
