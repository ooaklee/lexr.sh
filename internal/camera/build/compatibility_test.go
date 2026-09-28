package build

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
	return compatibility.Target{Architecture: "arm64", DeviceProfile: "surface-pro-11-x1e-oled", OSID: "ubuntu", OSVersion: "26.04", KernelABI: "7.2.0-jg-0sp11v19-qcom-x1e"}
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

// TestBuildCompatibilityPreflight rejects unverified, missing and mismatched
// payload evidence before any Docker command or output-directory mutation.
func TestBuildCompatibilityPreflight(t *testing.T) {
	for _, name := range []string{"override", "missing", "wrong-os", "wrong-kernel"} {
		t.Run(name, func(t *testing.T) {
			root, tuning := makeCameraRepository(t)
			runner := &fakeCameraRunner{tuning: tuning}
			request := Request{RepositoryRoot: root, PayloadTarget: cameraPayloadTarget(), AllowUnverifiedCompatibility: true, MinimumFreeGiB: 1}
			switch name {
			case "override":
				request.AllowUnverifiedCompatibility = false
			case "missing":
				request.PayloadTarget.DeviceProfile = ""
			case "wrong-os":
				request.PayloadTarget.OSVersion = "26.10"
			case "wrong-kernel":
				request.PayloadTarget.KernelABI = "7.2.5-jg-0sp11v19-qcom-x1e"
			}
			if _, err := newExecutableTestManager(runner).Run(context.Background(), request); err == nil {
				t.Fatal("invalid payload accepted")
			}
			for _, command := range runner.commands {
				if command.Name == "docker" {
					t.Fatal("Docker ran before compatibility preflight")
				}
			}
			if _, err := os.Stat(filepath.Join(root, "build")); !os.IsNotExist(err) {
				t.Fatal("preflight created output")
			}
		})
	}
}

// TestBuildBundleRequiresPinnedCompatibility rejects missing, modified and
// forged compatibility evidence even when receipt bytes are presented again.
func TestBuildBundleRequiresPinnedCompatibility(t *testing.T) {
	root, tuning := makeCameraRepository(t)
	runner := &fakeCameraRunner{tuning: tuning}
	receipt, err := newExecutableTestManager(runner).Run(context.Background(), Request{RepositoryRoot: root, PayloadTarget: cameraPayloadTarget(), AllowUnverifiedCompatibility: true, MinimumFreeGiB: 1})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Bundle.Compatibility == nil || receipt.Bundle.Compatibility.Decision.Status != compatibility.Unverified {
		t.Fatal("missing unverified record")
	}
	path := filepath.Join(receipt.OutputDirectory, compatibility.Filename)
	if err := os.WriteFile(path, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateBundleStatic(context.Background(), runner, ValidationRequest{RepositoryRoot: root, Directory: receipt.OutputDirectory, ExpectedAuthoritySHA256: receipt.AuthoritySHA256}); err == nil {
		t.Fatal("modified manifest passed independent receipt check")
	}
}
