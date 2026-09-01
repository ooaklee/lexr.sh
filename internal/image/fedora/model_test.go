package fedora

import (
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// TestRequireSupportedKernelEnforcesGenerationFloor locks the Fedora adapter
// to the first Stubble generation with the required in-tree input support.
func TestRequireSupportedKernelEnforcesGenerationFloor(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		abi     string
		wantErr string
	}{
		{name: "minimum", abi: "6.17.0-sp11v19-qcom-x1e"},
		{name: "newer", abi: "6.18.0-sp11v23-qcom-x1e"},
		{name: "old", abi: "6.17.0-sp11v18-qcom-x1e", wantErr: "sp11v19-or-newer"},
		{name: "missing generation", abi: "6.17.0-qcom-x1e", wantErr: "does not declare"},
		{name: "overflow", abi: "6.17.0-sp11v999999999999999999999-qcom-x1e", wantErr: "sp11v19-or-newer"},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := requireSupportedKernel(testCase.abi)
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("requireSupportedKernel(%q) error = %v", testCase.abi, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("requireSupportedKernel(%q) error = %v, want %q", testCase.abi, err, testCase.wantErr)
			}
		})
	}
}

// TestBuildPlanDeclaresTheCompleteFedoraWorkflow verifies stable ordering and
// the adapter-specific inputs surfaced to dry-run and journal consumers.
func TestBuildPlanDeclaresTheCompleteFedoraWorkflow(t *testing.T) {
	t.Parallel()

	request := Request{
		SourceISO:          "Fedora-Workstation-Live-44-1.2.aarch64.iso",
		SourceSHA256:       strings.Repeat("a", 64),
		OutputISO:          "fedora-surface.iso",
		Bundle:             kernel.Bundle{Release: "surface-v19", ABI: "6.17.0-sp11v19-qcom-x1e"},
		CompanionUserspace: []string{"iptsd", "audio"},
	}
	request.Companion.SourceDirectory = "/verified/lexr-source"

	operationPlan, err := BuildPlan(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := operationPlan.Validate(); err != nil {
		t.Fatalf("BuildPlan() returned an invalid plan: %v", err)
	}
	if operationPlan.Operation != "image.create" {
		t.Fatalf("operation = %q, want image.create", operationPlan.Operation)
	}
	wantIDs := []string{
		"verify-source", "verify-kernel", "stage-companion", "prepare-tools",
		"extract-live-root", "install-kernel", "install-userspace", "assemble-initramfs-root",
		"build-initramfs", "bind-live-media", "pair-device-trees",
		"repack-live-root", "replay-hybrid-boot", "validate-output", "publish-output",
	}
	if len(operationPlan.Steps) != len(wantIDs) {
		t.Fatalf("step count = %d, want %d", len(operationPlan.Steps), len(wantIDs))
	}
	steps := make(map[string]map[string]string, len(operationPlan.Steps))
	for index, step := range operationPlan.Steps {
		if step.ID != wantIDs[index] {
			t.Errorf("step %d ID = %q, want %q", index, step.ID, wantIDs[index])
		}
		steps[step.ID] = step.Inputs
	}
	for _, expected := range []struct{ step, key, value string }{
		{step: "verify-source", key: "path", value: request.SourceISO},
		{step: "verify-source", key: "sha256", value: request.SourceSHA256},
		{step: "verify-kernel", key: "abi", value: request.Bundle.ABI},
		{step: "stage-companion", key: "source", value: request.Companion.SourceDirectory},
		{step: "stage-companion", key: "userspace", value: "iptsd,audio"},
		{step: "install-userspace", key: "userspace", value: "iptsd,audio"},
		{step: "prepare-tools", key: "adapter", value: AdapterID},
		{step: "publish-output", key: "path", value: request.OutputISO},
	} {
		if got := steps[expected.step][expected.key]; got != expected.value {
			t.Errorf("step %s input %s = %q, want %q", expected.step, expected.key, got, expected.value)
		}
	}
}

// TestBuildPlanRejectsIncompleteOrUnsupportedRequests keeps invalid requests
// outside every external mutation boundary while permitting deferred ABI lookup.
func TestBuildPlanRejectsIncompleteOrUnsupportedRequests(t *testing.T) {
	t.Parallel()

	valid := Request{SourceISO: "source.iso", OutputISO: "output.iso", Bundle: kernel.Bundle{ABI: "6.17.0-sp11v19-qcom-x1e"}}
	for _, testCase := range []struct {
		name    string
		mutate  func(*Request)
		wantErr string
	}{
		{name: "source", mutate: func(request *Request) { request.SourceISO = "" }, wantErr: "source and output"},
		{name: "output", mutate: func(request *Request) { request.OutputISO = "" }, wantErr: "source and output"},
		{name: "ABI", mutate: func(request *Request) { request.Bundle.ABI = "" }, wantErr: "ABI is required"},
		{name: "generation", mutate: func(request *Request) { request.Bundle.ABI = "6.17.0-sp11v18-qcom-x1e" }, wantErr: "sp11v19-or-newer"},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			request := valid
			testCase.mutate(&request)
			_, err := BuildPlan(request)
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("BuildPlan() error = %v, want %q", err, testCase.wantErr)
			}
		})
	}

	deferred := valid
	deferred.Bundle.ABI = "resolved-at-execution"
	if _, err := BuildPlan(deferred); err != nil {
		t.Fatalf("BuildPlan() rejected deferred ABI resolution: %v", err)
	}
}
