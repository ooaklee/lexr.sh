//go:build windows

package doctor

import (
	"strings"
	"testing"
)

// TestAvailableBytesExplainsWindowsLimitation verifies that the unsupported
// diagnostic reports the affected capability and platform.
func TestAvailableBytesExplainsWindowsLimitation(t *testing.T) {
	_, err := availableBytes(".")
	if err == nil || !strings.Contains(err.Error(), "free-space inspection") || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("availableBytes error = %v, want a clear Windows limitation", err)
	}
}
