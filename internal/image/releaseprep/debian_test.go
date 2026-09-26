package releaseprep

import (
	"github.com/ooaklee/lexr.sh/internal/image/debianlive"
	"github.com/ooaklee/lexr.sh/internal/image/elementary"
	"reflect"
	"testing"
)

// TestDebianJournalUsesItsInstalledGRUBStep prevents another distro's producer
// sequence from qualifying a Debian release without the offline GRUB hand-off.
func TestDebianJournalUsesItsInstalledGRUBStep(t *testing.T) {
	expected := debianlive.CreationStepIDs()
	if got := imageStepsForAdapter(debianlive.AdapterID, len(expected)); !reflect.DeepEqual(got, expected) {
		t.Fatalf("Debian journal sequence = %v", got)
	}
	if got := imageStepsForAdapter(debianlive.AdapterID, len(elementary.CreationStepIDs())); got != nil {
		t.Fatalf("accepted elementary producer count: %v", got)
	}
	if imageStepsForAdapter(debianlive.AdapterID, len(expected)-1) != nil {
		t.Fatal("accepted incomplete Debian producer")
	}
}
