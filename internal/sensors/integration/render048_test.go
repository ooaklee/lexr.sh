package integration

import (
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// TestMetadata048RendererRefusesUnauthenticatedHandles checks both explicit
// constructors without manufacturing source or runtime-package authority.
func TestMetadata048RendererRefusesUnauthenticatedHandles(t *testing.T) {
	for _, render := range []func(*runtimepkg.VerifiedPackage, *source.Captured) (*Bundle, error){Render, Render048} {
		for _, input := range []struct {
			pkg *runtimepkg.VerifiedPackage
			src *source.Captured
		}{{nil, nil}, {new(runtimepkg.VerifiedPackage), nil}, {nil, new(source.Captured)}, {new(runtimepkg.VerifiedPackage), new(source.Captured)}} {
			if _, err := render(input.pkg, input.src); err == nil {
				t.Fatal("zero authenticated handles became inert integration authority")
			}
		}
	}
}

// TestMetadata048OpaqueProfileSeal uses private mechanical fixtures solely to
// check profile accessors. These are not authenticated renderer outputs.
func TestMetadata048OpaqueProfileSeal(t *testing.T) {
	for _, bundle := range []*Bundle{nil, {}, {profile: metadata048}, {profile: legacy047}, {valid: true}, {valid: true, profile: bundleProfile(255)}} {
		if bundle.IsMetadata048() || bundle.IsLegacy047() {
			t.Fatal("invalid or unknown profile acquired a valid profile seal")
		}
	}
	old := &Bundle{valid: true, profile: legacy047}
	if !old.IsLegacy047() || old.IsMetadata048() {
		t.Fatal("private valid legacy seal differs")
	}
	fresh := &Bundle{valid: true, profile: metadata048, snapshot: Snapshot{SourceLineage: "diagnostic only"}}
	if !fresh.IsMetadata048() || fresh.IsLegacy047() {
		t.Fatal("private valid successor seal was lost")
	}
	snapshot, err := fresh.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.SourceLineage = "changed"
	if !fresh.IsMetadata048() || fresh.IsLegacy047() || reflect.DeepEqual(snapshot, fresh.snapshot) {
		t.Fatal("public diagnostic copy changed the private profile seal")
	}
}
