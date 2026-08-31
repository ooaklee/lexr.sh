//go:build windows

package install

import (
	"errors"
	"os"
)

// errUnsupportedWindowsInstall reports that the Linux userspace installation
// boundary is unavailable on Windows.
var errUnsupportedWindowsInstall = errors.New("userspace installation is unsupported on Windows")

// openRegularNoFollow rejects userspace installation because its no-follow
// filesystem boundary is not implemented on Windows.
func openRegularNoFollow(string) (*os.File, os.FileInfo, error) {
	return nil, nil, errUnsupportedWindowsInstall
}
