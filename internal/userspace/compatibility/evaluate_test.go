package compatibility

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// evaluationFixture supplies independent, synthetic target evidence.
func evaluationFixture() (*Manifest, Target) {
	m := &Manifest{SchemaVersion: 1, Kind: "lexr.userspace-component-compatibility", ComponentID: "iptsd-v1", Release: "sp11-iptsd-v3", Capabilities: []string{"pen"}, Lexr: VersionRange{MinimumInclusive: "0.5.0-rc.1", TestedThroughInclusive: "0.5.0"}, Targets: []TargetRule{{Architectures: []string{"arm64"}, DeviceProfiles: []string{"x1e80100-microsoft-denali-oled"}, OperatingSystems: []OperatingSystem{{ID: "ubuntu", VersionRanges: []OSRange{{MinimumInclusive: "24.04", MaximumExclusive: "26.10"}}, TestedVersions: []string{"24.04"}}}, Kernels: []KernelRule{{PatchLine: "7.2.0", PlatformFlavour: "qcom-x1e", Scope: "sp11", ABIGeneration: GenerationRange{MinimumInclusive: 19, TestedThroughInclusive: 19}}}}}}
	return m, Target{LexrVersion: "0.5.0", Architecture: "arm64", DeviceProfile: "x1e80100-microsoft-denali-oled", OSID: "ubuntu", OSVersion: "24.04", KernelABI: "7.2.0-jg-0sp11v19-qcom-x1e"}
}

// TestDecisionBoundaries covers each stable status and override semantics.
func TestDecisionBoundaries(t *testing.T) {
	cases := []struct {
		name, status string
		change       func(*Manifest, *Target)
	}{
		{"tested", Tested, func(*Manifest, *Target) {}},
		{"prerelease", Tested, func(_ *Manifest, x *Target) { x.LexrVersion = "0.5.0-rc.9" }},
		{"build metadata", Tested, func(_ *Manifest, x *Target) { x.LexrVersion = "0.5.0+build.123" }},
		{"new version", Unverified, func(_ *Manifest, x *Target) { x.LexrVersion = "0.6.0" }},
		{"dev", Unverified, func(_ *Manifest, x *Target) { x.LexrVersion = "dev" }},
		{"dirty", Unverified, func(_ *Manifest, x *Target) { x.LexrVersion = "0.5.0+dirty" }},
		{"noncanonical", Unverified, func(_ *Manifest, x *Target) { x.LexrVersion = "v0.5.0" }},
		{"malformed", Unverified, func(_ *Manifest, x *Target) { x.LexrVersion = "not-a-version" }},
		{"hard minimum", Incompatible, func(_ *Manifest, x *Target) { x.LexrVersion = "0.4.99" }},
		{"hard maximum", Incompatible, func(m *Manifest, x *Target) { m.Lexr.MaximumExclusive = "0.6.0"; x.LexrVersion = "0.6.0" }},
		{"new OS", Unverified, func(_ *Manifest, x *Target) { x.OSVersion = "26.04" }},
		{"OS maximum", Incompatible, func(_ *Manifest, x *Target) { x.OSVersion = "26.10" }},
		{"malformed OS", Unavailable, func(_ *Manifest, x *Target) { x.OSVersion = "24.4" }},
		{"derivative", Unavailable, func(_ *Manifest, x *Target) { x.OSID = "pop" }},
		{"missing device", Unavailable, func(_ *Manifest, x *Target) { x.DeviceProfile = "" }},
		{"other board", Incompatible, func(_ *Manifest, x *Target) { x.DeviceProfile = "x1p64100-microsoft-denali" }},
		{"other architecture", Incompatible, func(_ *Manifest, x *Target) { x.Architecture = "amd64" }},
		{"new generation", Unverified, func(_ *Manifest, x *Target) { x.KernelABI = "7.2.0-jg-0sp11v20-qcom-x1e" }},
		{"upstream release candidate", Incompatible, func(_ *Manifest, x *Target) { x.KernelABI = "7.2.0-rc5-jg-0sp11v19-qcom-x1e" }},
		{"new patch line", Incompatible, func(_ *Manifest, x *Target) { x.KernelABI = "7.2.2-jg-0sp11v19-qcom-x1e" }},
		{"other scope", Incompatible, func(_ *Manifest, x *Target) { x.KernelABI = "7.2.0-jg-0sl7v19-qcom-x1e" }},
		{"leading zero", Unavailable, func(_ *Manifest, x *Target) { x.KernelABI = "7.2.0-jg-0sp11v019-qcom-x1e" }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			m, x := evaluationFixture()
			test.change(m, &x)
			decision := Evaluate(m, x)
			if decision.Status != test.status {
				t.Fatalf("decision=%+v want %s", decision, test.status)
			}
			for _, allow := range []bool{false, true} {
				want := test.status == Tested || test.status == Unverified && allow
				if got := RequireAllowed(decision, allow) == nil; got != want {
					t.Fatalf("allow %v: %v want %v", allow, got, want)
				}
			}
		})
	}
}

// TestTargetRulesCannotShareEvidence rejects cross-rule combinations and
// prefers a complete tested alternative over an earlier unverified one.
func TestTargetRulesCannotShareEvidence(t *testing.T) {
	m, target := evaluationFixture()
	other := m.Targets[0]
	other.OperatingSystems = []OperatingSystem{{ID: "fedora", VersionRanges: []OSRange{{MinimumInclusive: "44"}}, TestedVersions: []string{"44"}}}
	other.Kernels = []KernelRule{{PatchLine: "7.2.2", PlatformFlavour: "qcom-x1e", Scope: "sp11", ABIGeneration: GenerationRange{MinimumInclusive: 1, TestedThroughInclusive: 1}}}
	m.Targets = append(m.Targets, other)
	target.OSID = "fedora"
	target.OSVersion = "44"
	if got := Evaluate(m, target); got.Status != Incompatible {
		t.Fatalf("mixed rules: %+v", got)
	}
	target.KernelABI = "7.2.2-jg-0sp11v1-qcom-x1e"
	if got := Evaluate(m, target); got.Status != Tested || got.TargetIndex != 1 {
		t.Fatalf("complete rule: %+v", got)
	}
	m.Targets[0] = other
	m.Targets[0].OperatingSystems = []OperatingSystem{{ID: "fedora", VersionRanges: []OSRange{{MinimumInclusive: "44"}}, TestedVersions: []string{}}}
	if got := Evaluate(m, target); got.Status != Tested || got.TargetIndex != 1 {
		t.Fatalf("tested alternative: %+v", got)
	}
}

// TestSemVerPrecedence verifies normal prerelease ordering without overflow.
func TestSemVerPrecedence(t *testing.T) {
	values := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "999999999999999999999999999.0.0"}
	for i := 1; i < len(values); i++ {
		for _, value := range values[i-1 : i+1] {
			if _, err := semverParts(value); err != nil {
				t.Fatal(err)
			}
		}
		if compareVersion(values[i-1], values[i]) >= 0 {
			t.Fatalf("wrong order %s %s", values[i-1], values[i])
		}
	}
	for _, value := range []string{"01.0.0", "1.0.0-01", "1.0.0+build", "v1.0.0", "1.0.0-", "1.0.0-a_1", "-1.0.0"} {
		if _, err := semverParts(value); err == nil {
			t.Errorf("accepted %s", value)
		}
	}
	if !osVersion("fedora", "100") || compareOS("fedora", "99", "100") >= 0 || osVersion("fedora", "044") || osVersion("fedora", "24.04") {
		t.Fatal("Fedora comparison is not canonical integer ordering")
	}
}

// TestReadOSReleaseBoundsAndExactIdentity rejects identity ambiguity and escape.
func TestReadOSReleaseBoundsAndExactIdentity(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "etc/os-release")
	for _, data := range []string{"ID_LIKE=ubuntu\nVERSION_ID=24.04\n", "ID=ubuntu\nID=fedora\nVERSION_ID=44\n", "ID=ubuntu\nVERSION_ID=\n", "ID=ubuntu\nVERSION_ID=\"24.04\n", `ID="ub\u0075ntu"` + "\nVERSION_ID=24.04\n", strings.Repeat("a", MaxBytes+1), "ID=ubuntu\nVERSION_ID=24.04\n\xff"} {
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ReadOSRelease(root); err == nil {
			t.Fatalf("accepted malformed identity %q", data[:min(len(data), 80)])
		}
	}
	if err := os.WriteFile(path, []byte("ID=\"ubuntu\"\nVERSION_ID='24.04'\nID_LIKE=debian\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if id, version, err := ReadOSRelease(root); err != nil || id != "ubuntu" || version != "24.04" {
		t.Fatalf("%s/%s: %v", id, version, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/os-release", path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadOSRelease(root); err == nil {
		t.Fatal("host identity escaped target root")
	}
}

// TestRuntimeDevelopmentMarkersRespectTokenBoundaries keeps ordinary SemVer
// names distinct from explicit development or dirty build markers.
func TestRuntimeDevelopmentMarkersRespectTokenBoundaries(t *testing.T) {
	for _, value := range []string{"1.0.0-newdevice", "1.0.0-devtools", "1.0.0+device.123"} {
		if _, known := runtimeVersion(value); !known {
			t.Errorf("ordinary prerelease rejected: %s", value)
		}
	}
	for _, value := range []string{"dev", "1.0.0-dev.2", "1.0.0+git-dirty", "1.0.0+unknown"} {
		if _, known := runtimeVersion(value); known {
			t.Errorf("unverified marker accepted: %s", value)
		}
	}
}
