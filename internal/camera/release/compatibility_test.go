package release

import (
	"context"
	"github.com/ooaklee/lexr.sh/internal/platform"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	"github.com/ooaklee/lexr.sh/internal/userspace/producer"
	"os"
	"path/filepath"
	"testing"
)

// cameraPayloadTarget is explicit payload evidence, independent of the test host.
func cameraPayloadTarget() compatibility.Target {
	return compatibility.Target{Architecture: "arm64", DeviceProfile: "x1e80100-microsoft-denali-oled", OSID: "ubuntu", OSVersion: "26.04", KernelABI: "7.2.0-jg-0sp11v19-qcom-x1e"}
}

// sourceCompatibilityFixture stages the canonical declaration in an isolated Git tree.
func sourceCompatibilityFixture(t *testing.T, root string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../userspace/producer/testdata/declarations/imx681-libcamera-v1/lexr-component-compatibility.json")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "userspace/compatibility/imx681-libcamera-v1", compatibility.Filename)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/build/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return data
}

// prepareFixtureRecord uses the production authority and evaluator for test fixtures.
func prepareFixtureRecord(t *testing.T, root string) producer.Record {
	t.Helper()
	record, _, err := producer.Prepare(context.Background(), platform.ExecRunner{}, producer.Request{Component: "imx681-libcamera-v1", RepositoryRoot: root, PayloadTarget: cameraPayloadTarget(), AllowUnverifiedCompatibility: true})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// TestReleaseCompatibilityPreflight refuses mismatched build and release tuples
// and requires the dedicated override even for a non-mutating plan.
func TestReleaseCompatibilityPreflight(t *testing.T) {
	fixture := makeReleaseFixture(t)
	manager := executableReleaseManager(fixture.bundle)
	request := fixtureRequest(fixture)
	request.DryRun = true
	request.AllowUnverifiedCompatibility = false
	if _, err := manager.Prepare(context.Background(), request); err == nil {
		t.Fatal("unverified plan accepted without override")
	}
	request.AllowUnverifiedCompatibility = true
	request.DryRun = false
	request.KernelABI = "7.2.0-jg-0sp11v20-qcom-x1e"
	request.KernelTag = "sp11-qcom-x1e-7.2.0-jg-0sp11v20"
	request.PayloadTarget.KernelABI = request.KernelABI
	if _, err := manager.Prepare(context.Background(), request); err == nil {
		t.Fatal("release silently changed build payload target")
	}
}
