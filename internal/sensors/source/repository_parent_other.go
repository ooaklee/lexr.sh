//go:build !linux && !darwin

package source

import "os"

func requirePrivateSnapshotParent(*os.Root) error { return regularReadSupported() }
