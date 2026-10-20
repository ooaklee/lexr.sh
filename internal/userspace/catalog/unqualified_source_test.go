package catalog

import (
	"encoding/json"
	"errors"
	"testing"
)

// unqualifiedSourceCatalogJSON describes a source-only development component,
// not a shipped workflow, deployment payload, or hardware support declaration.
const unqualifiedSourceCatalogJSON = `{
  "schema_version": 2,
  "description": "Experimental source build fixture.",
  "components": [{
    "id": "native-sensors",
    "name": "Native sensor development sources",
    "level": "experimental",
    "capability": "sensors",
    "redistribution": "source-required",
    "support_actions": {
      "status": false,
      "pull": false,
      "build": true,
      "install": false
    },
    "compatibility_evidence": "unqualified_source",
    "notes": ["Build-only sources; deployment and physical sensors are unqualified."],
    "remediation": "Keep the distribution sensor service unchanged."
  }]
}`

// TestUnqualifiedSourceBuildOnlyContract accepts an honest source-only entry
// without fabricating release or kernel-generation compatibility metadata.
func TestUnqualifiedSourceBuildOnlyContract(t *testing.T) {
	t.Parallel()
	loaded, err := LoadBytes([]byte(unqualifiedSourceCatalogJSON))
	if err != nil {
		t.Fatalf("LoadBytes(source-only fixture) error = %v", err)
	}
	component, found := loaded.Get("native-sensors")
	if !found || loaded.SchemaVersion != 2 || CurrentSchemaVersion != 2 {
		t.Fatalf("catalogue contract = found %t, schema %d/%d", found, loaded.SchemaVersion, CurrentSchemaVersion)
	}
	if component.Capability != CapabilitySensors || component.Level != LevelExperimental ||
		component.CompatibilityEvidence != EvidenceUnqualifiedSource {
		t.Fatalf("source-only component = %#v", component)
	}
	if component.SupportActions != (SupportActions{Build: true}) ||
		component.Release != nil || component.KernelCompatibility != nil {
		t.Fatalf("source-only fixture exposed deployment authority: %#v", component)
	}
}

// TestUnqualifiedSourceRejectsEscalation proves the new token cannot grant
// status, pulling, installation, mature support, or kernel-pairing authority.
func TestUnqualifiedSourceRejectsEscalation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		mutate    func(map[string]any)
		wantField string
		wantText  string
	}{
		{
			name:      "status",
			mutate:    func(document map[string]any) { firstActions(document)["status"] = true },
			wantField: "components[0].support_actions.status", wantText: "must be false",
		},
		{
			name:      "pull",
			mutate:    func(document map[string]any) { firstActions(document)["pull"] = true },
			wantField: "components[0].support_actions.pull", wantText: "must be false",
		},
		{
			name:      "install",
			mutate:    func(document map[string]any) { firstActions(document)["install"] = true },
			wantField: "components[0].support_actions.install", wantText: "must be false",
		},
		{
			name:      "build disabled",
			mutate:    func(document map[string]any) { firstActions(document)["build"] = false },
			wantField: "components[0].support_actions.build", wantText: "must be true",
		},
		{
			name: "release metadata",
			mutate: func(document map[string]any) {
				firstComponent(document)["release"] = map[string]any{
					"url": "https://github.com/example/project/releases/tag/source-v1",
					"tag": "source-v1", "asset_allowlist": []any{"source.tar.xz", "SHA256SUMS"},
				}
			},
			wantField: "components[0].release", wantText: "must be omitted",
		},
		{
			name: "kernel compatibility",
			mutate: func(document map[string]any) {
				firstComponent(document)["kernel_compatibility"] = map[string]any{
					"minimum_sp11_generation": 25, "tested_through_sp11_generation": 27,
					"summary": "Invented qualification must be rejected.",
				}
			},
			wantField: "components[0].kernel_compatibility", wantText: "must be omitted",
		},
	}
	for _, level := range []Level{LevelRequired, LevelSupported, LevelDiagnosticOnly, LevelObsolete} {
		level := level
		tests = append(tests, struct {
			name      string
			mutate    func(map[string]any)
			wantField string
			wantText  string
		}{
			name:      "level " + string(level),
			mutate:    func(document map[string]any) { firstComponent(document)["level"] = string(level) },
			wantField: "components[0].level", wantText: "must be experimental",
		})
	}
	for _, action := range []string{"status", "pull", "build", "install"} {
		action := action
		tests = append(tests, struct {
			name      string
			mutate    func(map[string]any)
			wantField string
			wantText  string
		}{
			name:      "missing explicit " + action,
			mutate:    func(document map[string]any) { delete(firstActions(document), action) },
			wantField: "components[0].support_actions." + action, wantText: "is required",
		})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := decodeUserspaceDocument(t, unqualifiedSourceCatalogJSON)
			test.mutate(document)
			data, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			_, err = LoadBytes(data)
			var invalid *ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("LoadBytes() error = %v, want *ValidationError", err)
			}
			if !hasUserspaceIssue(invalid, test.wantField, test.wantText) {
				t.Fatalf("issues = %#v, want field %q containing %q", invalid.Issues, test.wantField, test.wantText)
			}
		})
	}
}
