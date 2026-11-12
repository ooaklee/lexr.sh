//go:build linux || darwin

package offline

import (
	"errors"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// supported admits only platforms with the reviewed exclusive-rename primitive.
func supported() error { return nil }

// currentOwner requires the current effective user, not a foreign object.
func currentOwner(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Geteuid())
}

// singleLink rejects additional hard-link names for files and fixed aliases.
func singleLink(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink == 1
}

// privateParent validates the explicitly supplied current-user 0700 directory.
func privateParent(name string) error {
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 || !currentOwner(info) {
		return errors.New("offline parent must be a current-user-owned private 0700 directory")
	}
	return nil
}

// openDirectory opens a real directory without following its final component.
func openDirectory(parent *os.File, name string) (*os.File, error) {
	fdBase := unix.AT_FDCWD
	if parent != nil {
		fdBase = int(parent.Fd())
	}
	fd, err := unix.Openat(fdBase, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

// openRegular excludes symlinks, blocking special files and hard-linked input.
func openRegular(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 {
		f.Close()
		return nil, errors.New("offline member is not a singly linked regular file")
	}
	return f, nil
}

// removeFile unlinks only one named non-directory entry under a retained parent.
func removeFile(parent *os.File, name string) error {
	if !component(name) {
		return errors.New("unsafe offline unlink")
	}
	return unix.Unlinkat(int(parent.Fd()), name, 0)
}

// removeDirectory removes one empty directory, never descendants recursively.
func removeDirectory(parent *os.File, name string) error {
	if !component(name) {
		return errors.New("unsafe offline rmdir")
	}
	return unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
}

// availableSpace observes the selected fresh root filesystem, not a host path.
func availableSpace(directory *os.File) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(directory.Fd()), &st); err != nil {
		return 0, err
	}
	if st.Bsize <= 0 {
		return 0, errors.New("offline filesystem block size is invalid")
	}
	if st.Bavail > uint64((1<<63-1)/int64(st.Bsize)) {
		return 0, errors.New("offline free-space observation overflows")
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
