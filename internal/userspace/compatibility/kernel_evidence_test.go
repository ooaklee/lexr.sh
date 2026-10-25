package compatibility

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestMissingKernelEvidenceRequiresDedicatedOverride keeps the declared hard
// interval distinct from absent qualification, including for sensor manifests.
func TestMissingKernelEvidenceRequiresDedicatedOverride(t *testing.T) {
	manifest, target := evaluationFixture()
	manifest.Capabilities = []string{"sensors"}
	manifest.Targets[0].Kernels[0].ABIGeneration = GenerationRange{MinimumInclusive: 19, MaximumExclusive: 21}
	data, err := Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"abi_generation\": {\n            \"minimum_inclusive\": 19,\n            \"maximum_exclusive\": 21\n          }") {
		t.Fatal("unknown kernel evidence was not omitted canonically")
	}
	loaded, err := Load(data)
	if err != nil || loaded.Targets[0].Kernels[0].ABIGeneration.TestedThroughInclusive != 0 {
		t.Fatalf("missing evidence did not round trip: %+v %v", loaded, err)
	}
	for _, generation := range []string{"19", "20"} {
		t.Run(generation, func(t *testing.T) {
			target.KernelABI = "7.2.0-jg-0sp11v" + generation + "-qcom-x1e"
			decision := Evaluate(loaded, target)
			if decision.Status != Unverified || decision.TargetIndex != 0 || !reflect.DeepEqual(decision.Reasons, []string{"kernel generation has no recorded tested-through evidence"}) {
				t.Fatalf("unknown evidence became qualification: %+v", decision)
			}
			if RequireAllowed(decision, false) == nil || RequireAllowed(decision, true) != nil {
				t.Fatal("unknown evidence lost the dedicated override boundary")
			}
		})
	}
}

// TestMissingKernelEvidenceRetainsHardBounds rejects targets outside complete
// declared tuples regardless of whether the dedicated override is requested.
func TestMissingKernelEvidenceRetainsHardBounds(t *testing.T) {
	cases := []struct {
		name, status string
		change       func(*Manifest, *Target)
	}{
		{"below kernel minimum", Incompatible, func(_ *Manifest, target *Target) { target.KernelABI = "7.2.0-jg-0sp11v18-qcom-x1e" }},
		{"at kernel maximum", Incompatible, func(_ *Manifest, target *Target) { target.KernelABI = "7.2.0-jg-0sp11v21-qcom-x1e" }},
		{"different patch line", Incompatible, func(_ *Manifest, target *Target) { target.KernelABI = "7.2.9-jg-0sp11v19-qcom-x1e" }},
		{"different scope", Incompatible, func(_ *Manifest, target *Target) { target.KernelABI = "7.2.0-jg-0sl7v19-qcom-x1e" }},
		{"below Lexr minimum", Incompatible, func(_ *Manifest, target *Target) { target.LexrVersion = "0.4.0" }},
		{"at OS maximum", Incompatible, func(_ *Manifest, target *Target) { target.OSVersion = "26.10" }},
		{"different device", Incompatible, func(_ *Manifest, target *Target) { target.DeviceProfile = "x1p64100-microsoft-denali" }},
		{"missing kernel", Unavailable, func(_ *Manifest, target *Target) { target.KernelABI = "" }},
		{"noncanonical kernel", Unavailable, func(_ *Manifest, target *Target) { target.KernelABI = "7.2.0-jg-0sp11v019-qcom-x1e" }},
		{"invalid declaration", Unavailable, func(manifest *Manifest, _ *Target) { manifest.Targets[0].Kernels[0].ABIGeneration.MinimumInclusive = 0 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			manifest, target := evaluationFixture()
			manifest.Targets[0].Kernels[0].ABIGeneration = GenerationRange{MinimumInclusive: 19, MaximumExclusive: 21}
			test.change(manifest, &target)
			decision := Evaluate(manifest, target)
			if decision.Status != test.status {
				t.Fatalf("decision=%+v want %s", decision, test.status)
			}
			for _, allow := range []bool{false, true} {
				if RequireAllowed(decision, allow) == nil {
					t.Fatalf("override %t bypassed %s", allow, test.status)
				}
			}
		})
	}
}

// TestOptionalKernelEvidenceRejectsMalformedMetadata keeps omission distinct
// from explicit zero, null, ambiguous names and malformed integer evidence.
func TestOptionalKernelEvidenceRejectsMalformedMetadata(t *testing.T) {
	manifest, _ := evaluationFixture()
	manifest.Targets[0].Kernels[0].ABIGeneration = GenerationRange{MinimumInclusive: 19, MaximumExclusive: 21}
	data, err := Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	anchor := "            \"maximum_exclusive\": 21\n"
	for name, addition := range map[string]string{
		"zero":          `"tested_through_inclusive": 0`,
		"negative":      `"tested_through_inclusive": -1`,
		"below minimum": `"tested_through_inclusive": 18`,
		"at maximum":    `"tested_through_inclusive": 21`,
		"null":          `"tested_through_inclusive": null`,
		"fraction":      `"tested_through_inclusive": 19.5`,
		"integer float": `"tested_through_inclusive": 19.0`,
		"overflow":      `"tested_through_inclusive": 999999999999999999999999999`,
		"string":        `"tested_through_inclusive": "19"`,
		"boolean":       `"tested_through_inclusive": false`,
		"array":         `"tested_through_inclusive": []`,
		"object":        `"tested_through_inclusive": {}`,
		"case alias":    `"Tested_through_inclusive": 19`,
		"escaped key":   `"tested_through_inclu\u0073ive": 19`,
		"duplicate":     "\"tested_through_inclusive\": 19,\n            \"tested_through_inclusive\": 20",
	} {
		t.Run(name, func(t *testing.T) {
			changed := strings.Replace(string(data), anchor, "            \"maximum_exclusive\": 21,\n            "+addition+"\n", 1)
			if changed == string(data) {
				t.Fatal("malformed evidence mutation did not alter the fixture")
			}
			if _, err := Load([]byte(changed)); err == nil {
				t.Fatal("accepted explicit malformed or ambiguous kernel evidence")
			}
		})
	}
}

// TestUnknownKernelEvidenceStillRequiresValidIntervals checks hard declaration
// intervals even when there is no tested-through evidence to compare against.
func TestUnknownKernelEvidenceStillRequiresValidIntervals(t *testing.T) {
	for name, bounds := range map[string]GenerationRange{
		"zero minimum":      {MinimumInclusive: 0},
		"negative minimum":  {MinimumInclusive: -1},
		"negative maximum":  {MinimumInclusive: 19, MaximumExclusive: -1},
		"empty interval":    {MinimumInclusive: 19, MaximumExclusive: 19},
		"reversed interval": {MinimumInclusive: 19, MaximumExclusive: 18},
	} {
		t.Run(name, func(t *testing.T) {
			manifest, _ := evaluationFixture()
			manifest.Targets[0].Kernels[0].ABIGeneration = bounds
			if _, err := Marshal(manifest); err == nil {
				t.Fatal("unknown evidence bypassed malformed hard bounds")
			}
		})
	}
	for name, bounds := range map[string]GenerationRange{
		"unbounded unknown": {MinimumInclusive: 19},
		"bounded unknown":   {MinimumInclusive: 19, MaximumExclusive: 21},
		"tested minimum":    {MinimumInclusive: 19, MaximumExclusive: 21, TestedThroughInclusive: 19},
		"tested last":       {MinimumInclusive: 19, MaximumExclusive: 21, TestedThroughInclusive: 20},
	} {
		t.Run(name, func(t *testing.T) {
			manifest, _ := evaluationFixture()
			manifest.Targets[0].Kernels[0].ABIGeneration = bounds
			if _, err := Marshal(manifest); err != nil {
				t.Fatalf("valid interval refused: %v", err)
			}
		})
	}
}

// TestKernelEvidenceCannotBeBorrowedAcrossCompleteRules keeps unknown evidence
// local to its matching target and still prefers a fully tested alternative.
func TestKernelEvidenceCannotBeBorrowedAcrossCompleteRules(t *testing.T) {
	manifest, target := evaluationFixture()
	other := manifest.Targets[0]
	manifest.Targets[0].Kernels = []KernelRule{{PatchLine: "7.2.0", PlatformFlavour: "qcom-x1e", Scope: "sp11", ABIGeneration: GenerationRange{MinimumInclusive: 19}}}
	other.OperatingSystems = []OperatingSystem{{ID: "fedora", VersionRanges: []OSRange{{MinimumInclusive: "44"}}, TestedVersions: []string{"44"}}}
	manifest.Targets = append(manifest.Targets, other)
	decision := Evaluate(manifest, target)
	if decision.Status != Unverified || decision.TargetIndex != 0 || !reflect.DeepEqual(decision.Reasons, []string{"kernel generation has no recorded tested-through evidence"}) {
		t.Fatalf("borrowed another OS rule's kernel evidence: %+v", decision)
	}
	fresh, _ := evaluationFixture()
	other = fresh.Targets[0]
	other.DeviceProfiles = []string{"x1p64100-microsoft-denali"}
	manifest.Targets[1] = other
	decision = Evaluate(manifest, target)
	if decision.Status != Unverified || decision.TargetIndex != 0 {
		t.Fatalf("borrowed another device rule's kernel evidence: %+v", decision)
	}
	fresh, _ = evaluationFixture()
	other = fresh.Targets[0]
	other.OperatingSystems = []OperatingSystem{{ID: "ubuntu", VersionRanges: []OSRange{{MinimumInclusive: "24.04", MaximumExclusive: "26.10"}}, TestedVersions: []string{}}}
	manifest.Targets[1] = other
	decision = Evaluate(manifest, target)
	if decision.Status != Unverified || decision.TargetIndex != 0 || !reflect.DeepEqual(decision.Reasons, []string{"kernel generation has no recorded tested-through evidence"}) {
		t.Fatalf("combined OS and kernel evidence from different matching rules: %+v", decision)
	}
	fixture, _ := evaluationFixture()
	manifest.Targets = append(manifest.Targets, fixture.Targets[0])
	decision = Evaluate(manifest, target)
	if decision.Status != Tested || decision.TargetIndex != 2 {
		t.Fatalf("did not prefer a complete tested alternative: %+v", decision)
	}
}

// TestExistingDeclarationBytesAndDecisionsStayPinned preserves independent
// pre-extension authorities rather than regenerating expected canonical data.
func TestExistingDeclarationBytesAndDecisionsStayPinned(t *testing.T) {
	for component, expected := range map[string]Reference{
		"audio-fullio-v19c":   {Size: 1088, SHA256: "6111f5a42dd500005dab077f8f75ed500295f5b226e48f1b4a252c6ee8bebda2"},
		"iptsd-v1":            {Size: 1056, SHA256: "8ad2a07d06511ff66714d69b8806b8b334844821c29f8af9e7f184b9e643dbc4"},
		"imx681-libcamera-v1": {Size: 1081, SHA256: "6ac39f4f451487d253c483b0a2eefa068978c41c4b53d82b77044aea606924a4"},
	} {
		t.Run(component, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "producer", "testdata", "declarations", component, Filename))
			if err != nil {
				t.Fatal(err)
			}
			if ReferenceFor(data) != expected {
				t.Fatal("existing independent declaration pin changed")
			}
			manifest, err := Load(data)
			if err != nil {
				t.Fatal(err)
			}
			again, err := Marshal(manifest)
			if err != nil || !bytes.Equal(data, again) {
				t.Fatalf("existing canonical declaration bytes changed: %v", err)
			}
			_, target := evaluationFixture()
			target.LexrVersion, target.OSVersion = "0.7.0", "26.04"
			decision := Evaluate(manifest, target)
			if decision.Status != Unverified || decision.TargetIndex != 0 || !reflect.DeepEqual(decision.Reasons, []string{"Lexr version has no recorded tested-through evidence", "operating system version is within hard bounds but untested"}) {
				t.Fatalf("existing declaration decision changed: %+v", decision)
			}
		})
	}
}

// TestSensorCapabilityRegistrationDoesNotWidenVocabulary keeps the new token
// closed and declarative; an authenticated manifest still cannot grant actions.
func TestSensorCapabilityRegistrationDoesNotWidenVocabulary(t *testing.T) {
	for name, capabilities := range map[string][]string{
		"sensor capability":   {"sensors"},
		"duplicate sensor":    {"sensors", "sensors"},
		"unregistered sensor": {"accelerometer"},
		"case alias":          {"Sensors"},
	} {
		t.Run(name, func(t *testing.T) {
			manifest, _ := evaluationFixture()
			manifest.Capabilities = capabilities
			_, err := Marshal(manifest)
			if (err == nil) != (name == "sensor capability") {
				t.Fatalf("unexpected vocabulary result: %v", err)
			}
		})
	}
}
