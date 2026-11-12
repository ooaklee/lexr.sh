//go:build linux || darwin

package offline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/packageplan"
	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
)

// savedPackage048 reads only a paired independently retained package reference;
// no environment variable may select a target root or bypass the new decoder.
func savedPackage048(t *testing.T) *runtimepkg.VerifiedPackage {
	t.Helper()
	name := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_048")
	expected := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_048_SHA256")
	if name == "" && expected == "" {
		t.Skip("explicit metadata0.4.8 package/SHA pair required")
	}
	if name == "" || expected == "" || !filepath.IsAbs(name) || filepath.Clean(name) != name {
		t.Fatal("paired canonical metadata0.4.8 package reference required")
	}
	real, err := filepath.EvalSymlinks(name)
	if err != nil || real != name {
		t.Fatal("metadata0.4.8 package path contains an alias")
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
	pkg, err := runtimepkg.Decode048(data, runtimepkg.Reference{Bytes: int64(len(data)), SHA256: expected})
	if err != nil || !pkg.IsMetadata048() {
		t.Fatalf("metadata0.4.8 package verification failed: %v", err)
	}
	if _, err := runtimepkg.Decode(data, pkg.Reference()); err == nil {
		t.Fatal("legacy decoder adopted metadata0.4.8 package")
	}
	return pkg
}

// TestSavedRuntimePackage048OfflineTransaction installs actual new bytes only
// into a constructor-owned fresh root; plan/repeat/rollback never execute them.
func TestSavedRuntimePackage048OfflineTransaction(t *testing.T) {
	pkg := savedPackage048(t)
	s := testSession(t)
	before := snapshot(t, s.Path())
	plan, err := s.Plan(pkg, syntheticRequest())
	if err != nil || !plan.QualificationOnly || plan.Decision.Status != "unverified" {
		t.Fatalf("new package gained compatibility authority: %v %+v", err, plan)
	}
	if !reflect.DeepEqual(before, snapshot(t, s.Path())) {
		t.Fatal("new package plan wrote target bytes")
	}
	h, err := s.Install(pkg, syntheticRequest())
	if err != nil {
		t.Fatal(err)
	}
	installed := snapshot(t, s.Path())
	again, err := s.Install(pkg, syntheticRequest())
	if err != nil || again != h || !reflect.DeepEqual(installed, snapshot(t, s.Path())) {
		t.Fatalf("new package repeat differs: %v", err)
	}
	if err := s.Rollback(h); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshot(t, s.Path())) {
		t.Fatal("new package rollback did not restore exact fresh baseline")
	}
	t.Logf("metadata0.4.8 fresh plan/install/repeat/rollback package=%s; passive software-only", pkg.Reference().SHA256)
}

// TestSavedRuntimePackage048Conflicts proves no-overwrite with real package
// bytes, not only the smaller synthetic transaction fixtures.
func TestSavedRuntimePackage048Conflicts(t *testing.T) {
	pkg := savedPackage048(t)
	t.Run("unmanaged-prefix", func(t *testing.T) {
		s := testSession(t)
		prefix := filepath.Join(s.Path(), strings.TrimPrefix(packageplan.PrivatePrefix, "/"))
		if err := os.MkdirAll(prefix, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(prefix, "foreign-sentinel"), []byte("unmanaged fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, filepath.Dir(s.Path()))
		if _, err := s.Install(pkg, syntheticRequest()); err == nil {
			t.Fatal("new package overwrote unmanaged prefix")
		}
		if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(s.Path()))) {
			t.Fatal("unmanaged conflict changed fresh target/evidence")
		}
	})
	t.Run("managed-tamper", func(t *testing.T) {
		s := testSession(t)
		h, err := s.Install(pkg, syntheticRequest())
		if err != nil {
			t.Fatal(err)
		}
		prefix := filepath.Join(s.Path(), strings.TrimPrefix(packageplan.PrivatePrefix, "/"))
		licence := filepath.Join(prefix, "share/licenses/LICENSE.libssc")
		if err := os.WriteFile(licence, []byte("external modification in fresh fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		changed := snapshot(t, filepath.Dir(s.Path()))
		if _, err := s.Install(pkg, syntheticRequest()); err == nil {
			t.Fatal("new package repeat accepted modified payload")
		}
		if err := s.Rollback(h); err == nil {
			t.Fatal("new package rollback erased modified payload")
		}
		if !reflect.DeepEqual(changed, snapshot(t, filepath.Dir(s.Path()))) {
			t.Fatal("tamper refusal changed target/evidence")
		}
	})
}

// TestRetainFreshNativeLoaderFixture048 retains new package bytes installed by
// the real passive adapter for a separately admitted ARM64 relocation proof.
// It cannot adopt an existing root and never executes the target payload.
func TestRetainFreshNativeLoaderFixture048(t *testing.T) {
	parent := os.Getenv("LEXR_TEST_SENSOR_RETAIN_PARENT_048")
	if parent == "" {
		t.Skip("explicit new private metadata0.4.8 fixture parent required")
	}
	pkg := savedPackage048(t)
	session, err := NewSession(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := os.Mkdir(filepath.Join(session.Path(), "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session.Path(), "etc/os-release"), []byte("ID=ubuntu\nVERSION_ID=26.04\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := session.Plan(pkg, syntheticRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Install(pkg, syntheticRequest()); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(session.Path(), strings.TrimPrefix(packageplan.PrivatePrefix, "/"))
	record, err := json.MarshalIndent(struct {
		Schema        string `json:"schema"`
		Prefix        string `json:"prefix"`
		SourceLineage string `json:"source_lineage"`
		Plan          Plan   `json:"plan"`
	}{"lexr-retained-native-loader-fixture048-v1", prefix, pkg.SourceLineage(), plan}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(parent, "fixture048.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(append(record, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("retain new passive fixture: %v %v", err, closeErr)
	}
	t.Log("retained metadata0.4.8 passive fixture; native execution and service activation remain separate gates")
}
