package archlinux

import (
	"os/exec"
	"testing"
)

// TestInstallerIntegration runs Surface boot and offline-payload regressions.
// API tests additionally run against the real pinned Archinstall in ARM64 validation.
func TestInstallerIntegration(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is needed for installer integration tests")
	}
	command := exec.Command(python, "-B", "-m", "unittest", "discover", "-s", "installer", "-v")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("installer integration tests failed: %v\n%s", err, output)
	}
}
