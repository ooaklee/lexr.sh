//go:build linux || darwin

package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// supportsInstalledDTBDiscovery identifies platforms with anchored no-follow IO.
const supportsInstalledDTBDiscovery = true

// openInstalledDTBDirectory pins every directory component without following
// links. The caller additionally checks route and descriptor identities.
func openInstalledDTBDirectory(root, relative string) (*os.File, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(fd), root)
	for _, component := range strings.Split(relative, "/") {
		next, err := openInstalledDTBChild(current, component, true)
		closeErr := current.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			_ = next.Close()
			return nil, closeErr
		}
		current = next
	}
	return current, nil
}

// openInstalledDTBChild refuses symlinks, special files and component races at
// the descriptor boundary; O_NONBLOCK also prevents a raced FIFO from blocking.
func openInstalledDTBChild(parent *os.File, name string, directory bool) (*os.File, error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return nil, errors.New("unsafe installed device-tree component")
	}
	var before unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	want := uint32(unix.S_IFREG)
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if directory {
		want = unix.S_IFDIR
		flags |= unix.O_DIRECTORY
	}
	if uint32(before.Mode)&unix.S_IFMT != want {
		return nil, errors.New("installed device-tree component has an unexpected type")
	}
	fd, err := unix.Openat(int(parent.Fd()), name, flags, 0)
	if err != nil {
		return nil, err
	}
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if before.Dev != after.Dev || before.Ino != after.Ino || uint32(after.Mode)&unix.S_IFMT != want {
		_ = unix.Close(fd)
		return nil, errors.New("installed device-tree component changed while opening")
	}
	file := os.NewFile(uintptr(fd), filepath.Join(parent.Name(), name))
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("invalid installed device-tree descriptor")
	}
	return file, nil
}
