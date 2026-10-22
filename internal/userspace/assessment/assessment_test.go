package assessment

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// testManifest produces canonical compatibility bytes for a synthetic release.
func testManifest(t *testing.T) []byte {
	t.Helper()
	const source = `{"schema_version":1,"kind":"lexr.userspace-component-compatibility","component_id":"iptsd-v1","release":"sp11-iptsd-v3","capabilities":["pen"],"lexr":{"minimum_inclusive":"0.3.0","tested_through_inclusive":"0.5.0"},"targets":[{"architectures":["arm64"],"device_profiles":["x1e80100-microsoft-denali-oled"],"operating_systems":[{"id":"ubuntu","version_ranges":[{"minimum_inclusive":"24.04","maximum_exclusive":"26.10"}],"tested_versions":["24.04"]}],"kernels":[{"patch_line":"7.2.0","platform_flavour":"qcom-x1e","scope":"sp11","abi_generation":{"minimum_inclusive":19,"tested_through_inclusive":19}}]}]}`
	var manifest compatibility.Manifest
	if err := json.Unmarshal([]byte(source), &manifest); err != nil {
		t.Fatal(err)
	}
	data, err := compatibility.Marshal(&manifest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestObserveRootDoesNotBorrowHostIdentity protects alternate roots from host
// distribution, hardware and kernel evidence, including ambiguous board tokens.
func TestObserveRootDoesNotBorrowHostIdentity(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{"etc/os-release": "ID=fedora\nVERSION_ID=44\nID_LIKE=ubuntu\n", "sys/firmware/devicetree/base/compatible": "microsoft,denali\x00"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	target, err := ObserveRoot(root, compatibility.Target{})
	if err == nil || !strings.Contains(err.Error(), "target device profile is unavailable") {
		t.Fatalf("missing profile diagnostic: %v", err)
	}
	if target.OSID != "fedora" || target.OSVersion != "44" || target.DeviceProfile != "" || target.Architecture != "" || target.KernelABI != "" {
		t.Fatalf("borrowed identity: %+v", target)
	}
}

// TestManifestIndependentPin rejects a substituted manifest even when a receipt
// or the manifest itself would claim a compatible target.
func TestManifestIndependentPin(t *testing.T) {
	root := t.TempDir()
	data := testManifest(t)
	ref := compatibility.ReferenceFor(data)
	if err := os.WriteFile(filepath.Join(root, compatibility.Filename), data, 0o644); err != nil {
		t.Fatal(err)
	}
	target := compatibility.Target{LexrVersion: "0.5.0", Architecture: "arm64", DeviceProfile: "x1e80100-microsoft-denali-oled", OSID: "ubuntu", OSVersion: "24.04", KernelABI: "7.2.0-jg-0sp11v19-qcom-x1e"}
	record, err := Evaluate(root, ref, "iptsd-v1", "sp11-iptsd-v3", target, false)
	if err != nil {
		t.Fatal(err)
	}
	if record.Decision.Status != "tested" {
		t.Fatalf("decision=%+v", record.Decision)
	}
	ref.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := Evaluate(root, ref, "iptsd-v1", "sp11-iptsd-v3", target, true); err == nil {
		t.Fatal("substituted manifest accepted")
	}
}

// TestReadManifestRejectsLinksAndOversize bounds untrusted offline metadata.
func TestReadManifestRejectsLinksAndOversize(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "manifest")
	if err := os.WriteFile(target, testManifest(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, compatibility.Filename)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(root); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Remove(filepath.Join(root, compatibility.Filename)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, compatibility.Filename), make([]byte, compatibility.MaxBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(root); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}
