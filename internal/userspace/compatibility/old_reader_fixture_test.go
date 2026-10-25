package compatibility

import (
	"os"
	"testing"
)

// TestUnknownKernelEvidenceFixtureIsCanonical keeps a standalone synthetic input
// usable by both the new reader and a separately compiled pre-extension reader.
// This fixture is not a source declaration or sensor qualification evidence.
func TestUnknownKernelEvidenceFixtureIsCanonical(t *testing.T) {
	data, err := os.ReadFile("testdata/unknown-kernel-evidence.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	decision := Evaluate(manifest, Target{LexrVersion: "0.7.0", Architecture: "arm64", DeviceProfile: "x1e80100-microsoft-denali-oled", OSID: "ubuntu", OSVersion: "26.04", KernelABI: "7.2.0-jg-0sp11v19-qcom-x1e"})
	if decision.Status != Unverified || len(decision.Reasons) != 1 || decision.Reasons[0] != "kernel generation has no recorded tested-through evidence" {
		t.Fatalf("standalone synthetic fixture gained qualification: %+v", decision)
	}
}
