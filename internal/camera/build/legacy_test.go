package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// legacyBuildFixture retains the old eight-file contract in a fresh fixture.
// Its package effects are fake; the production static validation still executes.
func legacyBuildFixture(t *testing.T) (string, *fakeCameraRunner, ValidationRequest, BundleReceipt) {
	t.Helper()
	root, tuning := makeCameraRepository(t)
	runner := &fakeCameraRunner{tuning: tuning}
	produced, err := newExecutableTestManager(runner).Run(context.Background(), Request{PayloadTarget: cameraPayloadTarget(), AllowUnverifiedCompatibility: true, RepositoryRoot: root, MinimumFreeGiB: 1})
	if err != nil {
		t.Fatal(err)
	}
	if produced.Bundle == nil {
		t.Fatal("fixture producer omitted its bundle")
	}
	legacy := *produced.Bundle
	legacy.SchemaVersion, legacy.Compatibility = LegacySchemaVersion, nil
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(produced.OutputDirectory, ReceiptName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(produced.OutputDirectory, compatibility.Filename)); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	runner.commands = nil
	return root, runner, ValidationRequest{RepositoryRoot: root, Directory: produced.OutputDirectory, ExpectedAuthoritySHA256: hex.EncodeToString(digest[:])}, legacy
}

// TestLegacyNativeBuildStaticKeepsPriorContract preserves exact old source,
// package and independent authority checks without requiring new metadata.
func TestLegacyNativeBuildStaticKeepsPriorContract(t *testing.T) {
	_, runner, request, legacy := legacyBuildFixture(t)
	got, err := ValidateBundleStatic(context.Background(), runner, request)
	if err != nil || got.SchemaVersion != LegacySchemaVersion || got.Compatibility != nil || got.PackageVersion != legacy.PackageVersion {
		t.Fatalf("legacy native proof: %+v %v", got, err)
	}
	for _, command := range runner.commands {
		if command.Name != "git" && (command.Name != "dpkg-deb" || len(command.Args) == 0 || command.Args[0] != "--field" && command.Args[0] != "--fsys-tarfile") {
			t.Fatalf("legacy static proof executed payload: %+v", command)
		}
	}
}

// TestLegacyNativeBuildRejectsTampering keeps schema families, receipt authority
// and closed file membership distinct rather than granting a downgrade flag.
func TestLegacyNativeBuildRejectsTampering(t *testing.T) {
	for _, name := range []string{"digest", "extra-manifest", "additional-manifest", "null-metadata", "unknown-schema", "missing-current-metadata", "package"} {
		t.Run(name, func(t *testing.T) {
			_, runner, request, legacy := legacyBuildFixture(t)
			switch name {
			case "digest":
				request.ExpectedAuthoritySHA256 = strings.Repeat("0", 64)
			case "extra-manifest", "additional-manifest":
				if err := os.WriteFile(filepath.Join(request.Directory, compatibility.Filename), []byte("{}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if name == "additional-manifest" {
					request.AdditionalFiles = []string{compatibility.Filename}
				}
			case "null-metadata", "unknown-schema", "missing-current-metadata":
				if name == "unknown-schema" {
					legacy.SchemaVersion = 3
				} else if name == "missing-current-metadata" {
					legacy.SchemaVersion = SchemaVersion
				}
				data, err := json.MarshalIndent(legacy, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if name == "null-metadata" {
					data = []byte(strings.Replace(string(data), "{", "{\n  \"compatibility\": null,", 1))
				}
				data = append(data, '\n')
				if err := os.WriteFile(filepath.Join(request.Directory, ReceiptName), data, 0o644); err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(data)
				request.ExpectedAuthoritySHA256 = hex.EncodeToString(digest[:])
			case "package":
				if err := os.WriteFile(filepath.Join(request.Directory, legacy.Artifacts[0].Name), []byte("tampered"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ValidateBundleStatic(context.Background(), runner, request); err == nil {
				t.Fatal("tampered legacy or relabelled schema accepted")
			}
		})
	}
}
