package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	camerabuild "github.com/ooaklee/lexr.sh/internal/camera/build"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// legacyReleaseFixture reconstructs the old eleven-file metadata contract in a
// temporary software fixture. Build proof is injected exactly as in other
// release tests; the build package separately tests actual static validation.
func legacyReleaseFixture(t *testing.T) (*Manager, ValidationRequest, Manifest) {
	t.Helper()
	fixture := makeReleaseFixture(t)
	manager := executableReleaseManager(fixture.bundle)
	prepared, err := manager.Prepare(context.Background(), fixtureRequest(fixture))
	if err != nil || prepared.Manifest == nil {
		t.Fatalf("fixture preparation: %v", err)
	}
	legacy := *prepared.Manifest
	legacy.SchemaVersion, legacy.Compatibility = LegacySchemaVersion, nil
	legacy.Build.SchemaVersion, legacy.Build.Compatibility = camerabuild.LegacySchemaVersion, nil
	directory := prepared.Plan.ReleaseDirectory
	if err := os.Remove(filepath.Join(directory, compatibility.Filename)); err != nil {
		t.Fatal(err)
	}
	buildData, err := json.MarshalIndent(legacy.Build, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	buildData = append(buildData, '\n')
	if err := os.WriteFile(filepath.Join(directory, camerabuild.ReceiptName), buildData, 0o644); err != nil {
		t.Fatal(err)
	}
	var files []GeneratedFile
	for _, file := range legacy.BuildArtifacts {
		if file.Name == compatibility.Filename {
			continue
		}
		if file.Name == camerabuild.ReceiptName {
			file, err = inspectFile(filepath.Join(directory, file.Name))
			if err != nil {
				t.Fatal(err)
			}
		}
		files = append(files, file)
	}
	legacy.BuildArtifacts = files
	if err := os.WriteFile(filepath.Join(directory, ChecksumName), renderChecksums(files), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, NotesName), []byte("Legacy software fixture; not installation guidance.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for index, file := range legacy.GeneratedFiles {
		legacy.GeneratedFiles[index], err = inspectFile(filepath.Join(directory, file.Name))
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(directory, ManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	manager.validate = executableReleaseManager(legacy.Build).validate
	digest := sha256.Sum256(data)
	return manager, ValidationRequest{RepositoryRoot: fixture.root, Directory: directory, ExpectedAuthoritySHA256: hex.EncodeToString(digest[:])}, legacy
}

// TestLegacyNativeReleaseStaticKeepsPriorContract retains the old closed shape
// without a declaration, producer target or re-labelled compatibility record.
func TestLegacyNativeReleaseStaticKeepsPriorContract(t *testing.T) {
	manager, request, _ := legacyReleaseFixture(t)
	got, err := manager.ValidateStatic(context.Background(), request)
	if err != nil || got.Manifest.SchemaVersion != LegacySchemaVersion || got.Manifest.Compatibility != nil || len(got.Manifest.BuildArtifacts) != 8 {
		t.Fatalf("legacy release validation: %+v %v", got, err)
	}
	request.ExpectedAuthoritySHA256 = strings.Repeat("0", 64)
	if _, err := manager.ValidateStatic(context.Background(), request); err == nil {
		t.Fatal("legacy release ignored independent authority")
	}
}

// TestLegacyNativeReleaseRejectsMixedAuthority retains closed membership and
// rejects explicit compatibility metadata, including null, on the old schema.
func TestLegacyNativeReleaseRejectsMixedAuthority(t *testing.T) {
	for _, name := range []string{"extra-manifest", "package", "null-metadata", "nested-null", "unknown-schema", "mixed-build"} {
		t.Run(name, func(t *testing.T) {
			manager, request, legacy := legacyReleaseFixture(t)
			switch name {
			case "extra-manifest":
				if err := os.WriteFile(filepath.Join(request.Directory, compatibility.Filename), []byte("{}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "package":
				if err := os.WriteFile(filepath.Join(request.Directory, legacy.Build.Artifacts[0].Name), []byte("tampered"), 0o644); err != nil {
					t.Fatal(err)
				}
			default:
				if name == "unknown-schema" {
					legacy.SchemaVersion = 3
				} else if name == "mixed-build" {
					legacy.Build.SchemaVersion = camerabuild.SchemaVersion
				}
				data, err := json.MarshalIndent(legacy, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if name == "null-metadata" {
					data = []byte(strings.Replace(string(data), "{", "{\n  \"compatibility\": null,", 1))
				} else if name == "nested-null" {
					data = []byte(strings.Replace(string(data), "\"build\": {", "\"build\": {\n    \"compatibility\": null,", 1))
				}
				data = append(data, '\n')
				if err := os.WriteFile(filepath.Join(request.Directory, ManifestName), data, 0o644); err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(data)
				request.ExpectedAuthoritySHA256 = hex.EncodeToString(digest[:])
			}
			if _, err := manager.ValidateStatic(context.Background(), request); err == nil {
				t.Fatal("legacy release accepted mixed or altered authority")
			}
		})
	}
}
