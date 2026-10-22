package build

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// This opt-in runs the normal Manager, including its owned dependency producer.
// It is not an arbitrary-image shortcut or a physical sensor qualification.
// Work, output, evidence and failed Docker resources are deliberately retained.
func TestNativeSourceBuildOptIn(t *testing.T) {
	if os.Getenv("LEXR_TEST_SENSOR_BUILD") != "1" {
		t.Skip("explicit native sensor source-build opt-in required")
	}
	r := Request{RepositoryRoot: os.Getenv("LEXR_TEST_SENSOR_OE_ROOT"), WorkParent: os.Getenv("LEXR_TEST_SENSOR_BUILD_WORK"), OutputParent: os.Getenv("LEXR_TEST_SENSOR_BUILD_OUTPUT"), Jobs: 2}
	evidence := os.Getenv("LEXR_TEST_SENSOR_BUILD_EVIDENCE")
	if r.RepositoryRoot == "" || r.WorkParent == "" || r.OutputParent == "" || evidence == "" {
		t.Fatal("explicit approved source, private work, output and evidence directories required")
	}
	if err := inspectWorkParent(evidence); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"native-input.json", "native-result.json"} {
		if _, err := os.Lstat(filepath.Join(evidence, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("native evidence directory has already been used; no automatic reuse")
		}
	}
	if err := writeBuildRecord(evidence, "native-input.json", r); err != nil {
		t.Fatal(err)
	}
	result, runErr := New(nil).Run(context.Background(), r)
	message := ""
	if runErr != nil {
		message = runErr.Error()
	}
	record := struct {
		Result Result `json:"result"`
		Error  string `json:"error,omitempty"`
		Scope  string `json:"scope"`
	}{result, message, "Normal native source-build observation only; not an installer, supported kernel/OS range or physical sensor qualification"}
	if err := writeBuildRecord(evidence, "native-result.json", record); err != nil {
		t.Fatalf("native result retention failed: %v; build error: %v", err, runErr)
	}
	if runErr != nil {
		t.Fatalf("native source build failed; evidence and owned resources retained: %v", runErr)
	}
	if !result.Publication.Published || !result.Publication.Durable || !result.Publication.LocationVerified || result.BuildReport == nil || len(result.Artifacts) != 16 {
		t.Fatal("normal source builder returned incomplete publication proof")
	}
	t.Logf("normal source build published %d verified development artifacts; build ID %s; not hardware qualification", len(result.Artifacts), result.BuildID)
}
