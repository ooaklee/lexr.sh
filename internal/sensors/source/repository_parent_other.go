//go:build !linux && !darwin

package source

import "os"

// requirePrivateSnapshotParent refuses unsupported directory ownership checks.
func requirePrivateSnapshotParent(*os.Root) error { return regularReadSupported() }
