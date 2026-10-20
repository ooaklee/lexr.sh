//go:build linux

package build

import (
	"errors"
	"io"
	"os"
)

func verifyBuildMountTopology(paths []string) error {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, maximumBuildMountInfoBytes+1))
	closeErr := f.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	return validateBuildMountTopology(data, paths)
}
