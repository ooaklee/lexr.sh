//go:build windows

package install

import (
	"os"
)

// stagingOwner rejects userspace installation because Unix ownership metadata
// is unavailable on Windows.
func stagingOwner(os.FileInfo) (uint32, error) {
	return 0, errUnsupportedWindowsInstall
}
