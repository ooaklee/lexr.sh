package archlinux

import (
	"os/exec"
	"testing"
)

// TestInstallerPolicy runs filesystem-preservation and offline-payload regressions.
// API tests additionally run against the real pinned Archinstall in ARM64 validation.
func TestInstallerPolicy(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is needed for installer policy tests")
	}
	command := exec.Command(python, "-B", "-m", "unittest", "discover", "-s", "installer", "-v")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("installer policy tests failed: %v\n%s", err, output)
	}
}
