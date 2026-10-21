//go:build linux || darwin

package install

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// TestEmbeddedDiscoveryRejectsFIFO proves a non-regular leaf is rejected
// from metadata, without a potentially blocking read of the named pipe.
func TestEmbeddedDiscoveryRejectsFIFO(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	target := filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/fifo.dtb")
	if err := unix.Mkfifo(target, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverInstalledDeviceTrees(fixtureContext(), root, fixtureFallbackABI, defaultInstalledDTBLimits()); err == nil || !strings.Contains(err.Error(), "non-regular member") {
		t.Fatalf("FIFO discovery error = %v", err)
	}
	directory, err := openInstalledDTBDirectory(root, "usr/lib/firmware/"+fixtureFallbackABI+"/device-tree/qcom")
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	if file, err := openInstalledDTBChild(directory, "fifo.dtb", false); err == nil {
		file.Close()
		t.Fatal("no-follow opener accepted a named pipe")
	}
}
