package manager

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lexr "github.com/ooaklee/lexr.sh"
	"github.com/ooaklee/lexr.sh/internal/userspace/catalog"
	userspacestatus "github.com/ooaklee/lexr.sh/internal/userspace/status"
)

// privateSensorCatalogue describes only the compiled experimental source
// capability, never a public catalogue entry or a device support declaration.
const privateSensorCatalogue = `{"schema_version":3,"description":"Private source-only sensor build fixture.","components":[{"id":"native-sensors","name":"Native sensor development sources","level":"experimental","capability":"sensors","redistribution":"source-required","support_actions":{"status":false,"pull":false,"build":true,"install":false},"compatibility_evidence":"unqualified_source","notes":["No installation or hardware qualification."],"remediation":"Leave distribution services unchanged."}]}`

// writePrivateCatalogue persists untrusted test input in a fresh temporary path.
func writePrivateCatalogue(t *testing.T, data []byte) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "supported-userspace.json")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// TestPrivateSensorCatalogueSelectsOnlyCompiledSourceCeiling permits the hidden
// build capability without adding sensors to the shipped catalogue or granting
// status, download or installation authority.
func TestPrivateSensorCatalogueSelectsOnlyCompiledSourceCeiling(t *testing.T) {
	for _, schema := range []int{2, 3} {
		document := map[string]any{}
		if err := json.Unmarshal([]byte(privateSensorCatalogue), &document); err != nil {
			t.Fatal(err)
		}
		document["schema_version"] = schema
		data, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		downloader, installer := &fakeDownloader{}, &fakeInstaller{}
		manager := New(catalog.NewLoader(lexr.UserspaceCatalogFS(), "supported-userspace.json"), downloader, nil)
		manager.Installer = installer
		shipped, err := manager.LoadCatalog("")
		if err != nil {
			t.Fatal(err)
		}
		if _, found := shipped.Get(SensorsComponent); found {
			t.Fatal("sensor source capability was added to the shipped catalogue")
		}
		override := writePrivateCatalogue(t, data)
		selected, err := manager.LoadCatalog(override)
		if err != nil {
			t.Fatal(err)
		}
		component, found := selected.Get(SensorsComponent)
		if !found || component.SupportActions != (catalog.SupportActions{Build: true}) || component.Release != nil || component.Compatibility != nil {
			t.Fatalf("private sensor selection = %+v", component)
		}
		if _, err := manager.Pull(context.Background(), override, SensorsComponent, t.TempDir()); err == nil {
			t.Fatal("source-only component became downloadable")
		}
		if _, err := manager.Install(context.Background(), InstallRequest{CatalogPath: override, Selector: SensorsComponent, Root: t.TempDir()}); err == nil {
			t.Fatal("source-only component became installable")
		}
		if _, err := manager.StatusWithCatalog(override, userspacestatus.Options{Root: t.TempDir()}); err == nil {
			t.Fatal("source-only catalogue became a deployed sensor status policy")
		}
		if len(downloader.specs) != 0 || len(installer.calls) != 0 {
			t.Fatal("source-only metadata reached a release downloader or installer")
		}
	}
}

// TestPrivateSensorCatalogueCannotWidenCompiledCeiling rejects valid-looking
// alternative identities and redistribution or qualification claims, including
// changes the general catalogue schema alone would otherwise permit.
func TestPrivateSensorCatalogueCannotWidenCompiledCeiling(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"other-id", func(c map[string]any) { c["id"] = "other-sensors" }},
		{"other-capability", func(c map[string]any) { c["capability"] = "power" }},
		{"redistribution", func(c map[string]any) { c["redistribution"] = "allowed" }},
		{"qualified-evidence", func(c map[string]any) { c["compatibility_evidence"] = "source_integrated_prior_validation" }},
		{"mature-support", func(c map[string]any) {
			c["compatibility_evidence"] = "source_integrated_prior_validation"
			c["level"] = "supported"
		}},
		{"status", func(c map[string]any) { c["support_actions"].(map[string]any)["status"] = true }},
		{"pull", func(c map[string]any) { c["support_actions"].(map[string]any)["pull"] = true }},
		{"install", func(c map[string]any) { c["support_actions"].(map[string]any)["install"] = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := map[string]any{}
			if err := json.Unmarshal([]byte(privateSensorCatalogue), &document); err != nil {
				t.Fatal(err)
			}
			test.mutate(document["components"].([]any)[0].(map[string]any))
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			manager := New(catalog.NewLoader(lexr.UserspaceCatalogFS(), "supported-userspace.json"), &fakeDownloader{}, nil)
			if _, err := manager.LoadCatalog(writePrivateCatalogue(t, data)); err == nil {
				t.Fatal("private catalogue widened the compiled source-only ceiling")
			}
		})
	}
}

// TestPrivateSensorCatalogueDoesNotAddStatusFeature isolates the source-only
// refusal from missing ordinary entries: even a complete private catalogue
// cannot add a sensor inspector or physical-support claim.
func TestPrivateSensorCatalogueDoesNotAddStatusFeature(t *testing.T) {
	data, err := fs.ReadFile(lexr.UserspaceCatalogFS(), "supported-userspace.json")
	if err != nil {
		t.Fatal(err)
	}
	var document, source map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(privateSensorCatalogue), &source); err != nil {
		t.Fatal(err)
	}
	document["components"] = append(document["components"].([]any), source["components"].([]any)[0])
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	manager := New(catalog.NewLoader(lexr.UserspaceCatalogFS(), "supported-userspace.json"), &fakeDownloader{}, nil)
	override := writePrivateCatalogue(t, data)
	if _, err := manager.LoadCatalog(override); err != nil {
		t.Fatal(err)
	}
	for _, feature := range []userspacestatus.Feature{"sensors", "native-sensors"} {
		_, err := manager.StatusWithCatalog(override, userspacestatus.Options{Root: t.TempDir(), Features: []userspacestatus.Feature{feature}})
		if err == nil || !strings.Contains(err.Error(), "unsupported userspace feature") {
			t.Fatalf("source-only status feature %q was accepted: %v", feature, err)
		}
	}
}

// TestLegacyCatalogueProfilesIntersectAcrossSchemas keeps schema-2 omission of
// an explicit legacy profile equivalent only to the exact same known release.
func TestLegacyCatalogueProfilesIntersectAcrossSchemas(t *testing.T) {
	data, err := fs.ReadFile(lexr.UserspaceCatalogFS(), "supported-userspace.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["schema_version"] = 2
	for _, raw := range document["components"].([]any) {
		delete(raw.(map[string]any), "legacy_profile")
	}
	legacyBytes, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := catalog.LoadBytes(legacyBytes)
	if err != nil {
		t.Fatal(err)
	}
	document["schema_version"] = 3
	for _, raw := range document["components"].([]any) {
		component := raw.(map[string]any)
		if release, ok := component["release"].(map[string]any); ok {
			component["legacy_profile"] = release["tag"]
		}
	}
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	current, err := catalog.LoadBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]*catalog.Catalog{{current, legacy}, {legacy, current}} {
		if err := intersectCatalogue(pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
}
