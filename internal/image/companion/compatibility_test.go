package companion

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/userspace/assessment"
	"github.com/ooaklee/lexr.sh/internal/userspace/catalog"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	userspacerelease "github.com/ooaklee/lexr.sh/internal/userspace/release"
)

// TestCompanionUsesTheOfflineEvaluator proves that image staging uses the
// complete declared image tuple and authenticates the same bytes as status.
func TestCompanionUsesTheOfflineEvaluator(t *testing.T) {
	manifest := &compatibility.Manifest{SchemaVersion: 1, Kind: "lexr.userspace-component-compatibility", ComponentID: "iptsd-v1", Release: "sp11-iptsd-v3", Capabilities: []string{"pen"}, Lexr: compatibility.VersionRange{MinimumInclusive: "0.5.0", TestedThroughInclusive: "0.5.0"}, Targets: []compatibility.TargetRule{{Architectures: []string{"arm64"}, DeviceProfiles: []string{"surface-pro-11-x1e-oled"}, OperatingSystems: []compatibility.OperatingSystem{{ID: "ubuntu", VersionRanges: []compatibility.OSRange{{MinimumInclusive: "26.04", MaximumExclusive: "26.10"}}, TestedVersions: []string{}}}, Kernels: []compatibility.KernelRule{{PatchLine: "7.2.0", PlatformFlavour: "qcom-x1e", Scope: "sp11", ABIGeneration: compatibility.GenerationRange{MinimumInclusive: 19, TestedThroughInclusive: 19}}}}}}
	data, err := compatibility.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	ref := compatibility.ReferenceFor(data)
	root := t.TempDir()
	directory := filepath.Join(root, "userspace", manifest.ComponentID, manifest.Release)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, compatibility.Filename), data, 0o644); err != nil {
		t.Fatal(err)
	}
	bundles := []preparedUserspaceBundle{{component: catalog.Component{ID: manifest.ComponentID, Compatibility: &ref}, bundle: userspacerelease.Bundle{Component: manifest.ComponentID, Release: manifest.Release, Directory: directory, Compatibility: &ref}}}
	target := compatibility.Target{LexrVersion: "0.5.0", Architecture: "arm64", DeviceProfile: "surface-pro-11-x1e-oled", OSID: "ubuntu", OSVersion: "26.04", KernelABI: "7.2.0-jg-0sp11v19-qcom-x1e"}
	request := BuildRequest{Target: target}
	if err := assessCompanion(request, bundles); err == nil {
		t.Fatal("untested image target was accepted without dedicated override")
	}
	request.AllowUnverifiedCompatibility = true
	if err := assessCompanion(request, bundles); err != nil {
		t.Fatal(err)
	}
	offline, err := assessment.Evaluate(directory, ref, manifest.ComponentID, manifest.Release, target, true)
	if err != nil || !reflect.DeepEqual(*bundles[0].compatibility, offline) {
		t.Fatalf("image/status evaluator divergence: %v", err)
	}
	old := iptsdOfflineReleaseContract
	t.Cleanup(func() { iptsdOfflineReleaseContract = old })
	iptsdOfflineReleaseContract.Compatibility = &ref
	record := imagecontract.CompanionBundleRecord{Tool: &imagecontract.ToolIdentityRecord{Version: target.LexrVersion}, Userspace: []imagecontract.OfflineUserspaceRecord{{Component: manifest.ComponentID, Release: manifest.Release, Root: ISOFilesystemRoot + "/userspace/" + manifest.ComponentID + "/" + manifest.Release, Compatibility: bundles[0].compatibility}}}
	if err := validateCompatibilityRecords(record, root); err != nil {
		t.Fatal(err)
	}
	record.Userspace[0].Compatibility.Decision.Status = compatibility.Tested
	if err := validateCompatibilityRecords(record, root); err == nil {
		t.Fatal("forged tested receipt accepted")
	}
	request.Target.KernelABI = "7.2.2-jg-0sp11v19-qcom-x1e"
	if err := assessCompanion(request, bundles); err == nil {
		t.Fatal("image override crossed kernel patch line")
	}
	if err := os.WriteFile(filepath.Join(directory, compatibility.Filename), append(data, ' '), 0o644); err != nil {
		t.Fatal(err)
	}
	request.Target = target
	if err := assessCompanion(request, bundles); err == nil {
		t.Fatal("changed image manifest escaped independent pin")
	}
}
