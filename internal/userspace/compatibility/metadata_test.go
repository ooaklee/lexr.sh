package compatibility

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// TestDescriptionIsOptionalCanonicalProse retains older canonical bytes whilst
// placing new explanatory metadata after capabilities, never in the decision.
func TestDescriptionIsOptionalCanonicalProse(t *testing.T) {
	manifest, target := evaluationFixture()
	legacy, err := Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(legacy)
	if err != nil || loaded.Description != "" {
		t.Fatalf("legacy manifest: %+v %v", loaded, err)
	}
	again, err := Marshal(loaded)
	if err != nil || !bytes.Equal(legacy, again) {
		t.Fatalf("legacy canonical bytes changed: %v", err)
	}
	before := Evaluate(manifest, target)
	manifest.Description = "Explains the component; does not authorise installation or qualify hardware."
	data, err := Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "  ],\n  \"description\": \""+manifest.Description+"\",\n  \"lexr\": {") {
		t.Fatal("description is not in canonical field order")
	}
	loaded, err = Load(data)
	if err != nil || loaded.Description != manifest.Description {
		t.Fatalf("description round trip: %+v %v", loaded, err)
	}
	if got := Evaluate(loaded, target); !reflect.DeepEqual(before, got) {
		t.Fatalf("human metadata changed compatibility authority: %+v %+v", before, got)
	}
}

// TestDescriptionRejectsMalformedMetadata bounds prose independently from the
// complete asset bound, rejecting ambiguous or non-human field representations.
func TestDescriptionRejectsMalformedMetadata(t *testing.T) {
	manifest, _ := evaluationFixture()
	canonical, err := Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"empty": `""`, "blank": `"   "`, "null": `null`, "number": `42`,
		"boolean": `true`, "array": `[]`, "object": `{}`,
		"newline": `"line\nline"`, "nul": `"line\u0000line"`, "control": `"line\u0085line"`,
		"oversized":  `"` + strings.Repeat("a", maximumDescriptionBytes+1) + `"`,
		"duplicate":  `"one",` + "\n  \"description\": \"two\"",
		"case alias": `"one",` + "\n  \"Description\": \"two\"",
	} {
		t.Run(name, func(t *testing.T) {
			data := strings.Replace(string(canonical), "  \"lexr\": {", "  \"description\": "+value+",\n  \"lexr\": {", 1)
			if _, err := Load([]byte(data)); err == nil {
				t.Fatal("accepted malformed description")
			}
		})
	}
	for _, value := range []string{strings.Repeat("a", maximumDescriptionBytes), strings.Repeat("é", maximumDescriptionBytes/2)} {
		manifest.Description = value
		if _, err := Marshal(manifest); err != nil {
			t.Fatalf("valid exact-byte-limit description rejected: %v", err)
		}
	}
	for _, value := range []string{strings.Repeat("é", maximumDescriptionBytes/2+1), "\xff", "\u2003"} {
		manifest.Description = value
		if _, err := Marshal(manifest); err == nil {
			t.Fatal("accepted invalid or overlong direct description")
		}
	}
}

// TestMissingLexrEvidenceRequiresOverride never invents a tested consumer when
// a new declaration knows its required version but records no tested release.
func TestMissingLexrEvidenceRequiresOverride(t *testing.T) {
	manifest, target := evaluationFixture()
	manifest.Lexr = VersionRange{MinimumInclusive: "0.7.0", MaximumExclusive: "0.8.0"}
	data, err := Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "  \"lexr\": {\n    \"minimum_inclusive\": \"0.7.0\",\n    \"maximum_exclusive\": \"0.8.0\"\n  },") {
		t.Fatal("unknown evidence was not omitted canonically")
	}
	loaded, err := Load(data)
	if err != nil || loaded.Lexr.TestedThroughInclusive != "" {
		t.Fatalf("omitted evidence did not round trip: %+v %v", loaded, err)
	}
	for version, status := range map[string]string{
		"0.6.0": Incompatible, "0.7.0-rc.1": Incompatible,
		"0.7.0": Unverified, "0.7.1": Unverified, "0.8.0": Incompatible,
	} {
		t.Run(version, func(t *testing.T) {
			target.LexrVersion = version
			decision := Evaluate(loaded, target)
			if decision.Status != status {
				t.Fatalf("decision=%+v want %s", decision, status)
			}
			for _, allow := range []bool{false, true} {
				if got := RequireAllowed(decision, allow) == nil; got != (status == Unverified && allow) {
					t.Fatalf("override=%t accepted=%t status=%s", allow, got, status)
				}
			}
		})
	}
}

// TestOptionalLexrEvidenceRemainsStrict rejects explicit empty or malformed
// evidence, and keeps hard bounds valid even when qualification is absent.
func TestOptionalLexrEvidenceRemainsStrict(t *testing.T) {
	manifest, _ := evaluationFixture()
	manifest.Lexr = VersionRange{MinimumInclusive: "0.7.0"}
	data, err := Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`""`, `null`, `false`, `7`, `"0.6.0"`, `"0.7.0+build"`, `"v0.7.0"`} {
		changed := strings.Replace(string(data), "\"minimum_inclusive\": \"0.7.0\"", "\"minimum_inclusive\": \"0.7.0\",\n    \"tested_through_inclusive\": "+value, 1)
		if _, err := Load([]byte(changed)); err == nil {
			t.Fatalf("accepted invalid explicit tested-through evidence %s", value)
		}
	}
	for _, maximum := range []string{"0.7.0", "0.6.0", "not-a-version"} {
		manifest.Lexr.MaximumExclusive = maximum
		if _, err := Marshal(manifest); err == nil {
			t.Fatalf("missing evidence bypassed hard maximum %q", maximum)
		}
	}
}
