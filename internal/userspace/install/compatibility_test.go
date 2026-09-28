package install

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/userspace/assessment"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	userspacerelease "github.com/ooaklee/lexr.sh/internal/userspace/release"
	"github.com/ooaklee/lexr.sh/internal/version"
)

// manifestedAudioFixture preserves compiled payload authority while adding a
// separately pinned synthetic declaration for a new packaging identity.
func manifestedAudioFixture(t *testing.T) Options {
	t.Helper()
	oldSpec, oldVersion := audioSpec, version.Version
	t.Cleanup(func() { audioSpec = oldSpec; version.Version = oldVersion })
	version.Version = "0.5.0"
	manifest := &compatibility.Manifest{SchemaVersion: 1, Kind: "lexr.userspace-component-compatibility", ComponentID: AudioComponent, Release: "sp11-audio-next", Capabilities: []string{"audio"}, Lexr: compatibility.VersionRange{MinimumInclusive: "0.5.0", TestedThroughInclusive: "0.5.0"}, Targets: []compatibility.TargetRule{{Architectures: []string{"arm64"}, DeviceProfiles: []string{"surface-pro-11-x1e-oled"}, OperatingSystems: []compatibility.OperatingSystem{{ID: "ubuntu", VersionRanges: []compatibility.OSRange{{MinimumInclusive: "24.04", MaximumExclusive: "26.10"}}, TestedVersions: []string{"24.04"}}}, Kernels: []compatibility.KernelRule{{PatchLine: "7.2.0", PlatformFlavour: "qcom-x1e", Scope: "sp11", ABIGeneration: compatibility.GenerationRange{MinimumInclusive: 19, TestedThroughInclusive: 19}}}}}}
	data, err := compatibility.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	reference := compatibility.ReferenceFor(data)
	contents := map[string][]byte{}
	for _, target := range audioTargets {
		contents[target.source] = []byte("synthetic " + target.source)
	}
	_, audioSpec = makeBundle(t, AudioComponent, "sp11-audio-v19c", contents)
	contents[compatibility.Filename] = data
	directory, _ := makeBundle(t, AudioComponent, manifest.Release, contents)
	path := filepath.Join(directory, bundleManifestName)
	receiptData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt userspacerelease.Bundle
	if err := json.Unmarshal(receiptData, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Compatibility = &reference
	receiptData, err = json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, receiptData, 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	abi := "7.2.0-jg-0sp11v19-qcom-x1e"
	for _, path := range []string{"etc", "lib/modules/" + abi} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc/os-release"), []byte("ID=ubuntu\nVERSION_ID=24.04\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return Options{Root: root, BundleDir: directory, Compatibility: &reference, CompatibilityRelease: manifest.Release, CompatibilityTarget: compatibility.Target{Architecture: "arm64", DeviceProfile: "surface-pro-11-x1e-oled", KernelABI: abi}}
}

// TestManifestedAudioInstallPreservesOfflineEvidence verifies dry-run, mutation
// and offline diagnosis use exactly the same pinned bytes and target tuple.
func TestManifestedAudioInstallPreservesOfflineEvidence(t *testing.T) {
	options := manifestedAudioFixture(t)
	installer := New(&fakeRunner{})
	installer.euid = func() int { return 0 }
	options.DryRun = true
	plan, err := installer.Audio(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Compatibility == nil || plan.Compatibility.Decision.Status != compatibility.Tested || len(plan.Files) != 6 {
		t.Fatalf("incomplete plan: %+v", plan)
	}
	if _, err := os.Stat(filepath.Join(options.Root, "var")); !os.IsNotExist(err) {
		t.Fatal("dry run mutated root")
	}
	options.DryRun = false
	result, err := installer.Audio(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if !result.FilesInstalled {
		t.Fatal("successful payload publication not recorded")
	}
	directory := filepath.Join(options.Root, "var/lib/lexr/userspace", AudioComponent)
	record, err := assessment.Evaluate(directory, *options.Compatibility, AudioComponent, options.CompatibilityRelease, result.Compatibility.Decision.Target, false)
	if err != nil || record.Decision.Status != compatibility.Tested {
		t.Fatalf("offline evidence: %+v %v", record, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "compatibility-assessment.json")); err != nil {
		t.Fatal(err)
	}
}

// TestCompatibilityBlocksBeforePrivilegeAndCannotBeConfirmedAway proves a
// general confirmation or the dedicated override cannot bypass hard failure.
func TestCompatibilityBlocksBeforePrivilegeAndCannotBeConfirmedAway(t *testing.T) {
	for _, test := range []struct {
		name  string
		allow bool
		os    string
		want  string
	}{
		{"untested", false, "26.04", "allow-unverified-compatibility"},
		{"hard maximum", true, "26.10", "incompatible"},
		{"missing identity", true, "", "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := manifestedAudioFixture(t)
			options.AllowUnverifiedCompatibility = test.allow
			if err := os.WriteFile(filepath.Join(options.Root, "etc/os-release"), []byte("ID=ubuntu\nVERSION_ID="+test.os+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			installer := New(&fakeRunner{})
			privilegeCalls := 0
			installer.euid = func() int { privilegeCalls++; return 0 }
			_, err := installer.Audio(context.Background(), options)
			if err == nil || privilegeCalls != 0 {
				t.Fatalf("unsafe preflight: %v privilege calls %d", err, privilegeCalls)
			}
			if test.os != "" && !strings.Contains(err.Error(), test.want) {
				t.Fatalf("wrong diagnostic: %v", err)
			}
		})
	}
}

// TestCompatibilityRechecksChangedTargetAtMutation simulates a target change
// after planning and proves no payload reaches that newly selected root state.
func TestCompatibilityRechecksChangedTargetAtMutation(t *testing.T) {
	options := manifestedAudioFixture(t)
	options.AllowUnverifiedCompatibility = true
	installer := New(&fakeRunner{})
	installer.euid = func() int {
		if err := os.WriteFile(filepath.Join(options.Root, "etc/os-release"), []byte("ID=ubuntu\nVERSION_ID=26.04\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return 0
	}
	_, err := installer.Audio(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "changed after planning") {
		t.Fatalf("target drift accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(options.Root, audioTargets[0].relative)); !os.IsNotExist(err) {
		t.Fatal("payload installed after target drift")
	}
}

// TestUnauthorisedManifestCannotDowngradeToLegacy closes the omission path.
func TestUnauthorisedManifestCannotDowngradeToLegacy(t *testing.T) {
	options := manifestedAudioFixture(t)
	options.Compatibility = nil
	options.DryRun = true
	if _, err := New(&fakeRunner{}).Audio(context.Background(), options); err == nil {
		t.Fatal("untrusted manifest treated as legacy")
	}
}
