//go:build linux || darwin

package lifecycle

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/integration"
	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

const (
	// accepted048Bytes binds the independently accepted new package, not a
	// caller-selected runtime identity or an observed service process.
	accepted048Bytes = int64(51671040)
	// accepted048SHA256 retains the external package reference qualified before
	// these version-scoped fixture lifecycle tests were introduced.
	accepted048SHA256 = "289fa8f2977cd398e8e9547fbcda98260193eb6dc333fc895c02ff4da27842ae"
)

// saved048Bundle reads only the paired explicitly pinned package/source inputs.
// It does not execute package programs, observe services or adopt a live root.
func saved048Bundle(t *testing.T) *integration.Bundle {
	t.Helper()
	name := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_048")
	want := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_048_SHA256")
	repository := os.Getenv("LEXR_TEST_SENSOR_INTEGRATION_OE_ROOT_048")
	if name == "" && want == "" && repository == "" {
		t.Skip("explicit accepted metadata 0.4.8 package/SHA/source tuple required")
	}
	if name == "" || want != accepted048SHA256 || repository == "" {
		t.Fatal("accepted metadata 0.4.8 package/SHA/source tuple differs")
	}
	data := acceptedPackageBytes(t, name)
	if int64(len(data)) != accepted048Bytes {
		t.Fatal("accepted metadata package size differs")
	}
	pkg, err := runtimepkg.Decode048(data, runtimepkg.Reference{Bytes: accepted048Bytes, SHA256: want})
	if err != nil || !pkg.IsMetadata048() {
		t.Fatalf("accepted metadata package failed: %v", err)
	}
	captured, err := source.Capture048(repository)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := integration.Render048(pkg, captured)
	if err != nil || !bundle.IsMetadata048() {
		t.Fatalf("metadata integration rendering failed: %v", err)
	}
	binding, err := bundle.Snapshot()
	if err != nil || binding.RuntimePackage != pkg.Reference() || binding.SourceLineage != source.Metadata048SourceCommit || !binding.QualificationOnly || binding.ActivationEnabled || binding.RegistrationEnabled {
		t.Fatalf("metadata integration acquired a different scope: %v", err)
	}
	return bundle
}

// TestSavedPackage048Lifecycle exercises actual accepted package/unit bytes in
// constructor-owned fresh roots with simulated manager/process/bus evidence.
// Success is not native service execution, activation or physical support.
func TestSavedPackage048Lifecycle(t *testing.T) {
	bundle := saved048Bundle(t)
	refusal := freshBackend(t, false)
	if _, err := NewQualification(bundle, refusal); err == nil || len(refusal.calls) != 0 {
		t.Fatal("original constructor accepted metadata bundle or touched backend")
	}
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprintf("vendor-active-%t", active), func(t *testing.T) {
			f := freshBackend(t, active)
			baseline := fixtureSnapshot(t, f.path)
			q, err := NewQualification048(bundle, f)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := q.Apply(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if phase, _ := tx.Status(); phase != Active {
				t.Fatal("metadata fixture did not reach simulated active phase")
			}
			if f.observation.Native.Process.RuntimePackage != (runtimepkg.Reference{Bytes: accepted048Bytes, SHA256: accepted048SHA256}) {
				t.Fatal("fixture process did not bind exact new package reference")
			}
			calls := len(f.calls)
			again, err := q.Apply(context.Background())
			if err != nil || again != tx {
				t.Fatalf("metadata exact managed repeat failed: %v", err)
			}
			for _, call := range f.calls[calls:] {
				if call != "Observe" {
					t.Fatal("metadata repeat performed a fixture mutation")
				}
			}
			if err := q.Rollback(context.Background(), tx); err != nil {
				t.Fatal(err)
			}
			if phase, _ := tx.Status(); phase != Restored || !reflect.DeepEqual(baseline, fixtureSnapshot(t, f.path)) {
				t.Fatal("metadata rollback did not restore exact fresh files")
			}
			if (f.observation.Vendor.ActiveState == "active") != active || !active && slices.Contains(f.calls, "StartVendor") {
				t.Fatal("metadata rollback changed prior vendor functional state")
			}
		})
	}
	t.Log("accepted metadata 0.4.8 apply/repeat/rollback uses real unit bytes and simulated services only")
}

// TestSavedPackage048RejectsLegacyProcessIdentity substitutes only the original
// accepted package reference after simulated start. Unknown runtime identity
// must never be accepted or stopped to restore a competing vendor process.
func TestSavedPackage048RejectsLegacyProcessIdentity(t *testing.T) {
	bundle := saved048Bundle(t)
	legacy := runtimepkg.Reference{Bytes: 51598336, SHA256: "ca05c65c6658c11f4fc9f2711290e063b186ac4c904fd0a6a7cd96718027eb08"}
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprintf("vendor-active-%t", active), func(t *testing.T) {
			f := freshBackend(t, active)
			f.after = func(operation string) {
				if operation == "StartNative" {
					f.observation.Native.Process.RuntimePackage = legacy
				}
			}
			q, err := NewQualification048(bundle, f)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := q.Apply(context.Background())
			if err == nil || tx == nil {
				t.Fatal("legacy package process identity was accepted")
			}
			if phase, _ := tx.Status(); phase != RecoveryRequired {
				t.Fatal("unknown process identity claimed restoration")
			}
			for _, operation := range []string{"StopNative", "RemoveNative", "ReleaseVendor", "StartVendor"} {
				if slices.Contains(f.calls, operation) {
					t.Fatalf("identity substitution allowed unsafe compensation: %s", operation)
				}
			}
			if state := f.fileEvidence("run/systemd/system/"+integration.VendorUnitName, true); !state.Present || !state.Exact {
				t.Fatal("identity substitution lost owned vendor inhibition")
			}
		})
	}
}
