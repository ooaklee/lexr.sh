//go:build linux || darwin

package lifecycle

import (
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/integration"
)

// TestQualificationProfileConstructorsRejectInvalidHandles proves neither
// public profile selector can turn a nil or zero bundle into fixture authority.
func TestQualificationProfileConstructorsRejectInvalidHandles(t *testing.T) {
	for _, bundle := range []*integration.Bundle{nil, {}} {
		f := freshBackend(t, false)
		if _, err := NewQualification(bundle, f); err == nil {
			t.Fatal("original constructor accepted invalid bundle")
		}
		if _, err := NewQualification048(bundle, f); err == nil {
			t.Fatal("metadata constructor accepted invalid bundle")
		}
		if len(f.calls) != 0 {
			t.Fatal("invalid bundle touched fixture backend")
		}
	}
}
