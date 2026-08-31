//go:build windows

package install

import (
	"errors"
	"testing"
)

// TestWindowsFilesystemBoundariesRejectInstallation verifies that Unix-only
// userspace installation primitives fail clearly when reached on Windows.
func TestWindowsFilesystemBoundariesRejectInstallation(t *testing.T) {
	if _, _, err := openRegularNoFollow("fixture"); !errors.Is(err, errUnsupportedWindowsInstall) {
		t.Fatalf("no-follow error = %v, want %v", err, errUnsupportedWindowsInstall)
	}
	if _, err := stagingOwner(nil); !errors.Is(err, errUnsupportedWindowsInstall) {
		t.Fatalf("staging ownership error = %v, want %v", err, errUnsupportedWindowsInstall)
	}
}
