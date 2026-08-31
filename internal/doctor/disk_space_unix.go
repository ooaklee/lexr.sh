//go:build !windows

package doctor

import "syscall"

// availableBytes reports the filesystem space available to an unprivileged
// process at path on Unix-like hosts.
func availableBytes(path string) (uint64, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return 0, err
	}
	return stats.Bavail * uint64(stats.Bsize), nil
}
