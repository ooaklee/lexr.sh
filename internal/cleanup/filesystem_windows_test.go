//go:build windows

package cleanup

import (
	"errors"
	"testing"
)

// TestWindowsFilesystemBoundariesRejectCleanup verifies that each Unix-only
// mutation boundary fails clearly when it is reached on Windows.
func TestWindowsFilesystemBoundariesRejectCleanup(t *testing.T) {
	if _, err := directoryIdentity(nil); !errors.Is(err, errUnsupportedWindows) {
		t.Fatalf("directory identity error = %v, want %v", err, errUnsupportedWindows)
	}
	if _, _, err := fileOwnership(nil); !errors.Is(err, errUnsupportedWindows) {
		t.Fatalf("ownership error = %v, want %v", err, errUnsupportedWindows)
	}
	if _, err := anchoredExtendedAttributeNames(nil); !errors.Is(err, errUnsupportedWindows) {
		t.Fatalf("extended-attribute error = %v, want %v", err, errUnsupportedWindows)
	}
	if err := renameAnchoredDirectories(nil, "source", nil, "destination"); !errors.Is(err, errUnsupportedWindows) {
		t.Fatalf("rename error = %v, want %v", err, errUnsupportedWindows)
	}
	if err := linkAnchoredDirectories(nil, "source", nil, "destination"); !errors.Is(err, errUnsupportedWindows) {
		t.Fatalf("link error = %v, want %v", err, errUnsupportedWindows)
	}
}
