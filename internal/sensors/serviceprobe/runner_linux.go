//go:build linux

package serviceprobe

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// nativeBootReader reads only the compiled kernel boot-ID path on procfs.
type nativeBootReader struct{}

// read validates the opened procfs leaf and bounds its complete read without
// returning filesystem contents or operating-system diagnostics in errors.
func (nativeBootReader) read(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fd, err := unix.Open("/proc/sys/kernel/random/boot_id", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("kernel boot identifier is unavailable")
	}
	f := os.NewFile(uintptr(fd), "kernel-boot-id")
	defer f.Close()
	var stat unix.Stat_t
	var filesystem unix.Statfs_t
	if unix.Fstat(fd, &stat) != nil || unix.Fstatfs(fd, &filesystem) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != 0 || stat.Mode&0o022 != 0 || filesystem.Type != unix.PROC_SUPER_MAGIC {
		return "", errors.New("kernel boot identifier does not have the required procfs identity")
	}
	data, err := io.ReadAll(io.LimitReader(f, 129))
	if err != nil || len(data) > 128 || len(data) == 0 {
		return "", errors.New("kernel boot identifier is empty, oversized or unreadable")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value := strings.TrimSuffix(string(data), "\n")
	if !bootIDText(value) {
		return "", errors.New("kernel boot identifier is malformed")
	}
	return value, nil
}

// Observe performs only the fixed passive Linux bus queries. It accepts no
// target, path, root, bus address, unit, executable or mutation option.
func Observe(ctx context.Context) (*Snapshot, error) {
	return collect(ctx, fixedRunner{}, nativeBootReader{})
}
