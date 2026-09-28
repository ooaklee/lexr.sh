package compatibility

import (
	"bytes"
	"strings"
	"testing"
)

// TestCanonicalCodecRejectsAmbiguity exercises untrusted bytes before any
// workflow can use the manifest as compatibility evidence.
func TestCanonicalCodecRejectsAmbiguity(t *testing.T) {
	m, _ := evaluationFixture()
	canonical, err := Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(canonical)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Marshal(loaded)
	if err != nil || !bytes.Equal(canonical, again) {
		t.Fatalf("unstable canonical bytes: %v", err)
	}
	source := string(canonical)
	cases := map[string]string{
		"duplicate":         strings.Replace(source, `"schema_version": 1,`, `"schema_version": 1, "schema_version": 1,`, 1),
		"nested duplicate":  strings.Replace(source, `"id": "ubuntu",`, `"id": "ubuntu", "id": "ubuntu",`, 1),
		"case alias":        strings.Replace(source, `"component_id"`, `"COMPONENT_ID"`, 1),
		"escaped field":     strings.Replace(source, `"component_id"`, `"component_\u0069d"`, 1),
		"unknown":           strings.Replace(source, `"schema_version": 1,`, `"schema_version": 1, "actions": [],`, 1),
		"null":              strings.Replace(source, `"pen"`, `null`, 1),
		"unknown schema":    strings.Replace(source, `"schema_version": 1`, `"schema_version": 2`, 1),
		"fractional schema": strings.Replace(source, `"schema_version": 1`, `"schema_version": 1.0`, 1),
		"trailing object":   source + "{}",
		"no newline":        strings.TrimSuffix(source, "\n"),
		"invalid UTF8":      source + "\xff",
		"overflow":          strings.Replace(source, `"minimum_inclusive": 19`, `"minimum_inclusive": 999999999999999999999`, 1),
		"depth":             strings.Repeat("[", 20) + "0" + strings.Repeat("]", 20),
		"array bound":       "[" + strings.Repeat("0,", 64) + "0]",
		"byte bound":        strings.Repeat(" ", MaxBytes+1),
		"empty":             "",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load([]byte(input)); err == nil {
				t.Fatal("accepted ambiguous or malformed manifest")
			}
		})
	}
}

// TestManifestRejectsInvalidBounds prevents contradictory evidence intervals.
func TestManifestRejectsInvalidBounds(t *testing.T) {
	cases := map[string]func(*Manifest){
		"lexr empty":                      func(m *Manifest) { m.Lexr.MaximumExclusive = m.Lexr.MinimumInclusive },
		"tested past hard maximum":        func(m *Manifest) { m.Lexr.MaximumExclusive = m.Lexr.TestedThroughInclusive },
		"persisted metadata":              func(m *Manifest) { m.Lexr.MinimumInclusive = "0.5.0+build" },
		"generation zero":                 func(m *Manifest) { m.Targets[0].Kernels[0].ABIGeneration.MinimumInclusive = 0 },
		"generation tested below minimum": func(m *Manifest) { m.Targets[0].Kernels[0].ABIGeneration.TestedThroughInclusive = 18 },
		"os empty":                        func(m *Manifest) { m.Targets[0].OperatingSystems[0].VersionRanges[0].MaximumExclusive = "24.04" },
		"os tested outside range":         func(m *Manifest) { m.Targets[0].OperatingSystems[0].TestedVersions = []string{"22.04"} },
		"unknown scope":                   func(m *Manifest) { m.Targets[0].Kernels[0].Scope = "future-board" },
		"unknown device":                  func(m *Manifest) { m.Targets[0].DeviceProfiles = []string{"surface-pro-11"} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m, _ := evaluationFixture()
			change(m)
			if _, err := Marshal(m); err == nil {
				t.Fatal("accepted invalid manifest bounds")
			}
		})
	}
}
