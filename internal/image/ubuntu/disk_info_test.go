package ubuntu

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// testUbuntuDiskInfo is a source-format identity without a trailing newline.
const testUbuntuDiskInfo = `Ubuntu 26.04 "Resolute Raccoon" - Daily arm64+x1e (20260326-014937)`

// TestValidateInstallerProductInfo covers Ubuntu's first-non-empty-line product
// parsing contract and the unquoted Lexr label that crashed the live installer.
func TestValidateInstallerProductInfo(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		data  string
		valid bool
	}{
		{"concept", testUbuntuDiskInfo, true},
		{"point-release", "Ubuntu 24.04.2 LTS \"Noble Numbat\" - Release arm64 (20250215)\n", true},
		{"blank-lines", "\n \r\n" + testUbuntuDiskInfo + "\r\n", true},
		{"lexr-regression", "Lexr Ubuntu arm64 for Surface Pro 11 (test-abi)\n", false},
		{"empty", "\n \n", false},
		{"missing-product", `"Resolute Raccoon"`, false},
		{"missing-space", `Ubuntu 26.04"Resolute Raccoon"`, false},
		{"missing-closing-quote", `Ubuntu 26.04 "Resolute Raccoon`, false},
		{"empty-codename", `Ubuntu 26.04 " "`, false},
		{"first-line-invalid", "invalid\n" + testUbuntuDiskInfo, false},
		{"invalid-utf8", testUbuntuDiskInfo + "\xff", false},
		{"nul", testUbuntuDiskInfo + "\x00", false},
		{"oversize", testUbuntuDiskInfo + strings.Repeat(" ", int(maximumValidationTextBytes)), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, "disk-info"), []byte(test.data), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := validateInstallerProductInfo(workspace); (err == nil) != test.valid {
				t.Fatalf("validateInstallerProductInfo() error = %v, want valid=%v", err, test.valid)
			}
		})
	}
}

// TestWriteSupportFilesPreservesUbuntuIdentity ensures Lexr branding cannot
// replace the original source bytes consumed by Ubuntu Desktop Bootstrap.
func TestWriteSupportFilesPreservesUbuntuIdentity(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	identity := []byte(testUbuntuDiskInfo)
	if err := os.WriteFile(filepath.Join(workspace, "disk-info"), identity, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := imagecontract.Manifest{
		SchemaVersion:   imagecontract.ManifestSchemaVersion,
		Layout:          "hybrid-iso",
		Adapter:         AdapterID,
		KernelBundle:    kernel.Bundle{ABI: "test-abi"},
		CompanionBundle: companion.Absent(companion.OmissionReasonNotRequested),
	}
	manifestBytes, err := serialiseManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeSupportFiles(workspace, manifest, manifestBytes, "test-abi"); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(workspace, "disk-info"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, identity) {
		t.Fatalf("Ubuntu identity changed: got %q, want %q", actual, identity)
	}
}
