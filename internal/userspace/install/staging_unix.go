//go:build !windows

package install

import (
	"errors"
	"os"
	"syscall"
)

// stagingOwner returns the numeric Unix owner of one staging filesystem
// object.
func stagingOwner(info os.FileInfo) (uint32, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("install staging metadata has no Unix owner")
	}
	return stat.Uid, nil
}
