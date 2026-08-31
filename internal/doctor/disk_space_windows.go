//go:build windows

package doctor

import "errors"

// availableBytes reports that the current host-space diagnostic does not yet
// have a Windows implementation.
func availableBytes(string) (uint64, error) {
	return 0, errors.New("free-space inspection is unsupported on Windows")
}
