//go:build linux || darwin

package source

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// requirePrivateSnapshotParent requires a current-user-owned private directory.
func requirePrivateSnapshotParent(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	var info unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &info); err != nil {
		return err
	}
	if info.Mode&unix.S_IFMT != unix.S_IFDIR || info.Mode&0o7777 != 0o700 || info.Uid != uint32(os.Geteuid()) {
		return errors.New("snapshot parent must be a current-user-owned directory with mode 0700")
	}
	return nil
}
