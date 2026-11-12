package relocation

import (
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
)

// TestMetadata048SmokeVersionBinding keeps both exact safe version contracts.
func TestMetadata048SmokeVersionBinding(t *testing.T) {
	old := syntheticSmokes()
	fresh := syntheticSmokes()
	fresh[1].Stdout = "libssc version 0.4.8\n"
	if err := verifySmokesProfile(fresh, true); err != nil {
		t.Fatal(err)
	}
	if err := verifySmokesProfile(old, true); err == nil {
		t.Fatal("metadata0.4.8 accepted legacy version")
	}
	if err := verifySmokes(fresh); err == nil {
		t.Fatal("legacy smoke contract accepted metadata0.4.8")
	}
	for _, version := range []string{"libssc version 0.4.9\n", "libssc version 0.4.8\n\n", ""} {
		fresh[1].Stdout = version
		if err := verifySmokesProfile(fresh, true); err == nil {
			t.Fatal("metadata0.4.8 accepted unknown or malformed version")
		}
	}
	for _, pkg := range []*runtimepkg.VerifiedPackage{nil, {}} {
		if err := Verify048(pkg, syntheticObservation()); err == nil {
			t.Fatal("metadata0.4.8 relocation accepted absent opaque authority")
		}
	}
}
