//go:build linux || darwin

package offline

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
)

// TestSavedRuntimePackageOfflineTransaction is an opt-in real package replay.
// It creates a fresh synthetic root and never executes the package payload,
// modifies an existing installation or claims physical sensor qualification.
func TestSavedRuntimePackageOfflineTransaction(t *testing.T) {
	name, expected := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE"), os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_SHA256")
	if name == "" && expected == "" {
		t.Skip("set the paired retained sensor runtime package path and SHA-256 for software qualification")
	}
	if name == "" || expected == "" {
		t.Fatal("runtime package replay requires both path and independently retained SHA-256")
	}
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		t.Fatal("saved package needs an explicit canonical absolute path")
	}
	real, err := filepath.EvalSymlinks(name)
	if err != nil || real != name {
		t.Fatal("saved package path must not contain symlinks")
	}
	parent, err := openAbsoluteDirectory(filepath.Dir(name), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.close()
	data, _, err := readRegular(parent, filepath.Base(name), runtimepkg.MaximumPackageBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := runtimepkg.Decode(data, runtimepkg.Reference{Bytes: int64(len(data)), SHA256: expected})
	if err != nil {
		t.Fatal(err)
	}
	s := testSession(t)
	before := snapshot(t, s.Path())
	plan, err := s.Plan(pkg, syntheticRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.QualificationOnly || plan.Decision.Status != "unverified" {
		t.Fatalf("real package gained qualification authority: %+v", plan)
	}
	if !reflect.DeepEqual(before, snapshot(t, s.Path())) {
		t.Fatal("actual-package Plan changed the target")
	}
	h, err := s.Install(pkg, syntheticRequest())
	if err != nil {
		t.Fatal(err)
	}
	installed := snapshot(t, s.Path())
	again, err := s.Install(pkg, syntheticRequest())
	if err != nil || again != h {
		t.Fatalf("actual-package repeat differs: %v", err)
	}
	if !reflect.DeepEqual(installed, snapshot(t, s.Path())) {
		t.Fatal("actual-package repeat changed bytes")
	}
	if err := s.Rollback(h); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshot(t, s.Path())) {
		t.Fatal("actual-package rollback changed baseline")
	}
	t.Logf("passive offline package %s: exact fresh install/repeat/rollback; no target execution or production support claim", expected)
}
