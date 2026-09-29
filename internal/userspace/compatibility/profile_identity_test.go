package compatibility

import (
	"testing"
)

// canonicalProfiles lists the exact canonical device identities the manifest
// registry must accept independently of its implementation.
var canonicalProfiles = []string{"x1e80100-microsoft-denali-oled", "x1p64100-microsoft-denali"}

// legacyAliases are superseded identifiers that must never validate again.
var legacyAliases = []string{"surface-pro-11-x1e-oled", "surface-pro-11-x1p-lcd"}

// profileManifest builds one minimal manifest carrying the given device
// profile list so each test composes only the dimension under examination.
func profileManifest(t *testing.T, deviceProfiles []string) *Manifest {
	t.Helper()
	return &Manifest{
		SchemaVersion: 1, Kind: "lexr.userspace-component-compatibility",
		ComponentID: "iptsd-v1", Release: "sp11-iptsd-v3", Capabilities: []string{"pen"},
		Lexr: VersionRange{MinimumInclusive: "0.3.0", TestedThroughInclusive: "0.5.0"},
		Targets: []TargetRule{{
			Architectures: []string{"arm64"}, DeviceProfiles: deviceProfiles,
			OperatingSystems: []OperatingSystem{{ID: "ubuntu", VersionRanges: []OSRange{{MinimumInclusive: "24.04", MaximumExclusive: "26.10"}}, TestedVersions: []string{"24.04"}}},
			Kernels:          []KernelRule{{PatchLine: "7.2.0", PlatformFlavour: "qcom-x1e", Scope: "sp11", ABIGeneration: GenerationRange{MinimumInclusive: 19, TestedThroughInclusive: 19}}},
		}},
	}
}

// TestValidateAcceptsCanonicalProfileIDs proves every compiled canonical
// identity validates as a device profile, so X1P declarations are first-class.
func TestValidateAcceptsCanonicalProfileIDs(t *testing.T) {
	for _, id := range canonicalProfiles {
		if err := Validate(profileManifest(t, []string{id})); err != nil {
			t.Fatalf("canonical profile %q rejected: %v", id, err)
		}
	}
}

// TestValidateRejectsLegacyProfileAliases proves historical platform aliases
// cannot masquerade as manifest device-profile identities.
func TestValidateRejectsLegacyProfileAliases(t *testing.T) {
	for _, alias := range legacyAliases {
		if err := Validate(profileManifest(t, []string{alias})); err == nil {
			t.Fatalf("legacy alias %q accepted", alias)
		}
	}
}

// TestValidateRejectsUnknownProfileIDs proves unregistered identities fail
// closed even when they superficially resemble the canonical grammar.
func TestValidateRejectsUnknownProfileIDs(t *testing.T) {
	for _, id := range []string{"x1e80100-microsoft-denali", "made-up-board", "surface-pro-12"} {
		if err := Validate(profileManifest(t, []string{id})); err == nil {
			t.Fatalf("unknown profile %q accepted", id)
		}
	}
}

// TestValidateRejectsDuplicateProfileIDs proves repetition inside one target
// rule is rejected rather than silently deduplicated.
func TestValidateRejectsDuplicateProfileIDs(t *testing.T) {
	id := canonicalProfiles[0]
	if err := Validate(profileManifest(t, []string{id, id})); err == nil {
		t.Fatal("duplicate device profile accepted")
	}
}

// TestEvaluateDoesNotEquateAliasesWithCanonicalIDs proves a target observed
// under a legacy alias never satisfies a rule that names only the canonical
// identity: aliases remain incompatible even with the dedicated override.
func TestEvaluateDoesNotEquateAliasesWithCanonicalIDs(t *testing.T) {
	manifest := profileManifest(t, canonicalProfiles)
	target := Target{
		LexrVersion: "0.5.0", Architecture: "arm64",
		OSID: "ubuntu", OSVersion: "24.04", KernelABI: "7.2.0-jg-0sp11v19-qcom-x1e",
	}
	for index, alias := range legacyAliases {
		target.DeviceProfile = canonicalProfiles[index]
		if decision := Evaluate(manifest, target); decision.Status != Tested {
			t.Fatalf("canonical control failed: %+v", decision)
		}
		target.DeviceProfile = alias
		decision := Evaluate(manifest, target)
		if decision.Status != Incompatible {
			t.Fatalf("legacy alias %q was equated with a canonical identity", alias)
		}
		if decision.TargetIndex != -1 {
			t.Fatalf("alias %q matched target rule %d", alias, decision.TargetIndex)
		}
		if err := RequireAllowed(decision, true); err == nil {
			t.Fatalf("override accepted legacy alias %q", alias)
		}
	}
}
