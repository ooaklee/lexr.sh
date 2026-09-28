package manager

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	lexr "github.com/ooaklee/lexr.sh"
	"github.com/ooaklee/lexr.sh/internal/userspace/catalog"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	userspacestatus "github.com/ooaklee/lexr.sh/internal/userspace/status"
)

// TestOfflineStatusReportsAuthenticatedManifestEvidence exercises the public
// manager path without a downloader or any host target identity.
func TestOfflineStatusReportsAuthenticatedManifestEvidence(t *testing.T) {
	manifest := &compatibility.Manifest{SchemaVersion: 1, Kind: "lexr.userspace-component-compatibility", ComponentID: "audio-fullio-v19c", Release: "sp11-audio-next", Capabilities: []string{"audio"}, Lexr: compatibility.VersionRange{MinimumInclusive: "0.5.0", TestedThroughInclusive: "0.5.0"}, Targets: []compatibility.TargetRule{{Architectures: []string{"arm64"}, DeviceProfiles: []string{"surface-pro-11-x1e-oled"}, OperatingSystems: []compatibility.OperatingSystem{{ID: "ubuntu", VersionRanges: []compatibility.OSRange{{MinimumInclusive: "24.04", MaximumExclusive: "26.10"}}, TestedVersions: []string{"24.04"}}}, Kernels: []compatibility.KernelRule{{PatchLine: "7.2.0", PlatformFlavour: "qcom-x1e", Scope: "sp11", ABIGeneration: compatibility.GenerationRange{MinimumInclusive: 19, TestedThroughInclusive: 19}}}}}}
	data, err := compatibility.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	ref := compatibility.ReferenceFor(data)
	catalogueData, err := fs.ReadFile(lexr.UserspaceCatalogFS(), "supported-userspace.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(catalogueData, &document); err != nil {
		t.Fatal(err)
	}
	for _, raw := range document["components"].([]any) {
		component := raw.(map[string]any)
		if component["id"] != manifest.ComponentID {
			continue
		}
		delete(component, "legacy_profile")
		delete(component, "kernel_compatibility")
		component["compatibility"] = ref
		release := component["release"].(map[string]any)
		release["tag"] = manifest.Release
		release["url"] = "https://github.com/ooaklee/linux-surface-pro-11-oe/releases/tag/" + manifest.Release
		release["asset_allowlist"] = append(release["asset_allowlist"].([]any), compatibility.Filename)
	}
	catalogueData, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	manager := New(catalog.NewLoader(fstest.MapFS{"supported-userspace.json": {Data: catalogueData}}, "supported-userspace.json"), nil, nil)
	root, bundle := t.TempDir(), t.TempDir()
	abi := "7.2.0-jg-0sp11v19-qcom-x1e"
	for _, path := range []string{"etc", "lib/modules/" + abi} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc/os-release"), []byte("ID=ubuntu\nVERSION_ID=24.04\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, compatibility.Filename), data, 0o644); err != nil {
		t.Fatal(err)
	}
	options := userspacestatus.Options{Root: root, KernelABI: abi, Features: []userspacestatus.Feature{userspacestatus.FeatureAudio}, BundleDirectory: bundle, CompatibilityTarget: compatibility.Target{Architecture: "arm64", DeviceProfile: "surface-pro-11-x1e-oled"}}
	for _, test := range []struct {
		name   string
		state  userspacestatus.State
		mutate func()
	}{
		{"development CLI", userspacestatus.StateWarn, func() {}},
		{"missing metadata", userspacestatus.StateUnavailable, func() {
			if err := os.Remove(filepath.Join(bundle, compatibility.Filename)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.mutate()
			report, err := manager.Status(options)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, check := range report.Checks {
				if check.ID == "component-compatibility-"+manifest.ComponentID {
					found = true
					if check.State != test.state {
						t.Fatalf("check=%+v", check)
					}
				}
				if strings.HasPrefix(check.ID, "kernel-compatibility-") {
					t.Fatal("legacy compatibility result survived replacement")
				}
			}
			if !found {
				t.Fatal("missing compatibility assessment")
			}
		})
	}
}

// TestUnsupportedCatalogueStatusIsUnavailable preserves a structured diagnostic
// instead of projecting future metadata through the old schema.
func TestUnsupportedCatalogueStatusIsUnavailable(t *testing.T) {
	data, err := fs.ReadFile(lexr.UserspaceCatalogFS(), "supported-userspace.json")
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"schema_version": 3`, `"schema_version": 999`, 1))
	manager := New(catalog.NewLoader(fstest.MapFS{"supported-userspace.json": {Data: data}}, "supported-userspace.json"), nil, nil)
	report, err := manager.Status(userspacestatus.Options{Root: t.TempDir()})
	if err != nil || report.Ready || len(report.Checks) != 1 || report.Checks[0].State != userspacestatus.StateUnavailable {
		t.Fatalf("report=%+v error=%v", report, err)
	}
}

// TestNativeCompatibilityStatusUsesCompiledPin diagnoses a locally prepared
// component offline before the embedded download catalogue adopts that release.
func TestNativeCompatibilityStatusUsesCompiledPin(t *testing.T) {
	data, err := os.ReadFile("../producer/testdata/declarations/imx681-libcamera-v1/lexr-component-compatibility.json")
	if err != nil {
		t.Fatal(err)
	}
	root, bundle := t.TempDir(), t.TempDir()
	abi := "7.2.0-jg-0sp11v19-qcom-x1e"
	for _, path := range []string{"etc", "lib/modules/" + abi} {
		if err := os.MkdirAll(filepath.Join(root, path), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc/os-release"), []byte("ID=ubuntu\nVERSION_ID=26.04\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, compatibility.Filename), data, 0644); err != nil {
		t.Fatal(err)
	}
	manager := New(catalog.NewLoader(lexr.UserspaceCatalogFS(), "supported-userspace.json"), nil, nil)
	options := userspacestatus.Options{Root: root, KernelABI: abi, Features: []userspacestatus.Feature{userspacestatus.FeatureCamera}, BundleDirectory: bundle, CompatibilityTarget: compatibility.Target{Architecture: "arm64", DeviceProfile: "surface-pro-11-x1e-oled"}}
	for _, state := range []userspacestatus.State{userspacestatus.StateWarn, userspacestatus.StateUnavailable} {
		report, err := manager.Status(options)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, check := range report.Checks {
			if check.ID == "component-compatibility-imx681-libcamera-v1" {
				found = true
				if check.State != state {
					t.Fatalf("check=%+v", check)
				}
			}
		}
		if !found {
			t.Fatal("missing native compatibility check")
		}
		if err := os.WriteFile(filepath.Join(bundle, compatibility.Filename), []byte("{}\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
