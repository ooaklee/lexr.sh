package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveProfiles keeps the public names and bundle aliases in agreement.
func TestResolveProfiles(t *testing.T) {
	for _, candidate := range List() {
		for _, value := range []string{candidate.ID, candidate.Platform} {
			got, err := Resolve(value)
			if err != nil || got != candidate {
				t.Fatalf("Resolve(%q) = %#v, %v", value, got, err)
			}
		}
	}
	for _, value := range []string{"", "auto", "x1e-oled", "unknown", " x1p64100-microsoft-denali"} {
		if _, err := Resolve(value); err == nil {
			t.Fatalf("Resolve(%q) accepted an invalid profile", value)
		}
	}
	copy := List()
	copy[0].ID = "changed"
	if List()[0].ID == "changed" {
		t.Fatal("List exposed mutable registry storage")
	}
}

// TestHardwareDetectionRequiresExactIdentity rejects SoC guesses, ambiguous
// evidence, oversized files and symlink escapes from an alternate root.
func TestHardwareDetectionRequiresExactIdentity(t *testing.T) {
	for _, test := range []struct{ name, evidence, want string }{
		{"OLED", "microsoft,denali-oled\x00microsoft,denali\x00qcom,x1e80100\x00", "x1e80100-microsoft-denali-oled"},
		{"LCD", "microsoft,denali-lcd\x00qcom,x1p64100\x00", "x1p64100-microsoft-denali"},
		{"older OLED", "microsoft,denali\x00qcom,x1e80100\x00", "x1e80100-microsoft-denali-oled"},
		{"older LCD", "microsoft,denali-x1p\x00qcom,x1p64100\x00", "x1p64100-microsoft-denali"},
		{"SoC alone", "qcom,x1e80100\x00", ""},
		{"substring", "other,microsoft,denali-oled-unknown\x00", ""},
		{"ambiguous", "microsoft,denali-oled\x00microsoft,denali-lcd\x00", ""},
		{"oversized", strings.Repeat("x", 4097), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "sys/firmware/devicetree/base/compatible")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(test.evidence), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Detect(root)
			if test.want == "" {
				if err == nil {
					t.Fatalf("accepted unproven identity: %#v", got)
				}
			} else if err != nil || got.ID != test.want {
				t.Fatalf("Detect = %#v, %v; want %s", got, err, test.want)
			}
		})
	}
	if _, err := Detect(t.TempDir()); err == nil {
		t.Fatal("missing hardware defaulted to a profile")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "sys")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Detect(root); err == nil {
		t.Fatal("hardware identity escaped target root")
	}
}
