package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestManifestCatalogueMigration enforces explicit v3 pins and prohibits
// reclassifying an arbitrary new release as an immutable legacy profile.
func TestManifestCatalogueMigration(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(validUserspaceCatalogJSON), &raw); err != nil {
		t.Fatal(err)
	}
	raw["schema_version"] = 3
	component := raw["components"].([]any)[0].(map[string]any)
	delete(component, "kernel_compatibility")
	component["compatibility"] = map[string]any{"size": 1000, "sha256": strings.Repeat("a", 64)}
	release := component["release"].(map[string]any)
	release["asset_allowlist"] = []string{"SHA256SUMS", "component-arm64.deb", "lexr-component-compatibility.json"}
	data, _ := json.Marshal(raw)
	loaded, err := LoadBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := loaded.Get("zulu-component")
	saved.Compatibility.SHA256 = "changed"
	again, _ := loaded.Get("zulu-component")
	if again.Compatibility.SHA256 != strings.Repeat("a", 64) {
		t.Fatal("reference is mutable")
	}
	delete(component, "compatibility")
	component["legacy_profile"] = "component-v1"
	data, _ = json.Marshal(raw)
	if _, err := LoadBytes(data); err == nil {
		t.Fatal("unknown legacy release accepted")
	}
}

// TestManifestCatalogueRejectsAmbiguousReference checks strict nested decoding.
func TestManifestCatalogueRejectsAmbiguousReference(t *testing.T) {
	for _, reference := range []string{`null`, `{"size":1,"Size":2,"sha256":"a"}`, `{"size":1,"size":2,"sha256":"a"}`, `{"size":1,"sha256":"a","url":"https://example.test"}`} {
		data := strings.Replace(validUserspaceCatalogJSON, `"id": "zulu-component",`, `"id": "zulu-component", "compatibility":`+reference+`,`, 1)
		if _, err := LoadBytes([]byte(data)); err == nil {
			t.Fatalf("ambiguous reference accepted: %s", reference)
		}
	}
}
