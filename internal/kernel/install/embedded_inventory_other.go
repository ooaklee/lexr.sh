//go:build !linux && !darwin

package install

import (
	"errors"
	"os"
)

// supportsInstalledDTBDiscovery preserves the prior bounded Surface-only path
// where component-wise no-follow discovery is not implemented.
const supportsInstalledDTBDiscovery = false

// openInstalledDTBDirectory fails closed without component-wise no-follow reads.
func openInstalledDTBDirectory(string, string) (*os.File, error) {
	return nil, errors.New("installed device-tree discovery requires Linux or macOS")
}

// openInstalledDTBChild fails closed on platforms without this read boundary.
func openInstalledDTBChild(*os.File, string, bool) (*os.File, error) {
	return nil, errors.New("installed device-tree discovery requires Linux or macOS")
}
