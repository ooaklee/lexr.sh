//go:build linux || darwin

package build

import (
	"errors"
	"os"
	"syscall"
)

// sourceTransportCurrentUID returns the current real user's ownership identity.
func sourceTransportCurrentUID() int { return os.Getuid() }

// sourceTransportLstat captures type, owner and inode identity without following
// the final filesystem entry when establishing a local Docker transport.
func sourceTransportLstat(name string) (sourceTransportPathInfo, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return sourceTransportPathInfo{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return sourceTransportPathInfo{}, errors.New("Unix socket/file ownership metadata is unavailable")
	}
	return sourceTransportPathInfo{mode: info.Mode(), uid: stat.Uid, device: uint64(stat.Dev), inode: uint64(stat.Ino)}, nil
}
