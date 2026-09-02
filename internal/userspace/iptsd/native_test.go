package iptsd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestFedoraNativeRPMContractIsClosed verifies the shared runtime detector
// consumes a canonical, unique, immutable copy of the intended package layout.
func TestFedoraNativeRPMContractIsClosed(t *testing.T) {
	static := FedoraNativeRPMStaticFiles()
	binaries := FedoraNativeRPMBinaries()
	if len(static) != 6 || len(binaries) != 2 {
		t.Fatalf("native contract has %d static files and %d binaries", len(static), len(binaries))
	}
	seen := make(map[string]bool, len(static)+len(binaries))
	digestPattern := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, file := range append(static, binaries...) {
		if file.Path == "" || filepath.IsAbs(file.Path) || filepath.ToSlash(filepath.Clean(file.Path)) != file.Path || strings.HasPrefix(file.Path, "../") {
			t.Fatalf("unsafe native RPM path: %+v", file)
		}
		if seen[file.Path] {
			t.Fatalf("duplicate native RPM path %q", file.Path)
		}
		seen[file.Path] = true
	}
	for _, file := range static {
		if !digestPattern.MatchString(file.SHA256) || file.Size < 1 {
			t.Fatalf("invalid static native RPM identity: %+v", file)
		}
	}
	for _, file := range binaries {
		if file.SHA256 != "" || file.Size != 0 || !file.Executable {
			t.Fatalf("native RPM binary incorrectly byte-pinned: %+v", file)
		}
	}
	static[0].Path = "mutated"
	if FedoraNativeRPMStaticFiles()[0].Path == "mutated" {
		t.Fatal("caller mutated the shared native RPM contract")
	}
}

// TestFedoraNativeRPMSourceMarkerPinsTheRelease proves the compact package
// marker is the exact source identity already compiled into this IPTSD domain.
func TestFedoraNativeRPMSourceMarkerPinsTheRelease(t *testing.T) {
	marker := []byte("IPTSD_VERSION=3.1.0\n" +
		"IPTSD_REPOSITORY=https://github.com/linux-surface/iptsd.git\n" +
		"IPTSD_COMMIT=a83bc1232f7096f8b33b50fdbda249cd640de670\n" +
		"IPTSD_TREE=06c6e812873e117930eca60b8a32cec40fd13281\n" +
		"IPTSD_INTEGRATION_REPOSITORY=https://github.com/turbineBMW/surface-pro-11-linux.git\n" +
		"IPTSD_INTEGRATION_COMMIT=05e5335bc72476d44390336701cf03efa5fd0165\n")
	var source NativeRPMFile
	for _, file := range FedoraNativeRPMStaticFiles() {
		if file.Path == "usr/share/doc/lexr-sp11-iptsd/SOURCE.env" {
			source = file
		}
	}
	if int64(len(marker)) != source.Size || digestBytes(marker) != source.SHA256 {
		t.Fatalf("Fedora-native SOURCE.env identity = %d/%s, contract = %d/%s", len(marker), digestBytes(marker), source.Size, source.SHA256)
	}
}

// TestOptionalFedoraNativeRPMRoot validates an extracted final RPM when an
// integration environment supplies one, without making unit tests invoke RPM.
func TestOptionalFedoraNativeRPMRoot(t *testing.T) {
	root := strings.TrimSpace(os.Getenv("LEXR_TEST_FEDORA_IPTSD_ROOT"))
	if root == "" {
		t.Skip("set LEXR_TEST_FEDORA_IPTSD_ROOT to an extracted lexr-sp11-iptsd RPM root")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("LEXR_TEST_FEDORA_IPTSD_ROOT must be absolute")
	}
	for _, file := range FedoraNativeRPMStaticFiles() {
		if err := validateFile(filepath.Join(root, filepath.FromSlash(file.Path)), fileSpec{path: file.Path, sha256: file.SHA256, size: file.Size}); err != nil {
			t.Fatalf("validate Fedora-native static file /%s: %v", file.Path, err)
		}
	}
	for _, file := range FedoraNativeRPMBinaries() {
		if err := validateAArch64ELF(filepath.Join(root, filepath.FromSlash(file.Path))); err != nil {
			t.Fatalf("validate Fedora-native binary /%s: %v", file.Path, err)
		}
	}
}
