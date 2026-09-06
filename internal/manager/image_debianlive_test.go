package manager

import (
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/image/debianlive"
)

// TestImageManagerPlansDebianLiveWorkflow verifies catalogue routing
// and the pinned source checksum reach Debian's actual GRUB-centred plan
// without another adapter's workflow.
func TestImageManagerPlansDebianLiveWorkflow(t *testing.T) {
	operation, err := newImagePlanTestManager().Plan(CreateImageRequest{CatalogID: "debian-live-testing-gnome-arm64-20240902", Output: "/output/debianlive.iso", KernelProfile: "surface-pro-11-x1e-oled"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, step := range operation.Steps {
		ids = append(ids, step.ID)
	}
	if !reflect.DeepEqual(ids, debianlive.CreationStepIDs()) {
		t.Fatalf("Debian plan=%v", ids)
	}
	if operation.Steps[0].Inputs["sha256"] != "3260c69821f85464974e2136a0cda5d3954818467dda168bdfcb69547c4d7abc" {
		t.Fatal("Debian publisher checksum was not retained")
	}
	if operation.Steps[3].Inputs["adapter"] != debianlive.AdapterID {
		t.Fatal("Debian selected another adapter")
	}
}

// TestImageManagerRoutesDebianLiveRequestToItsAdapter verifies request
// propagation by checking every resolved input appears in Debian's plan.
func TestImageManagerRoutesDebianLiveRequestToItsAdapter(t *testing.T) {
	operation, err := newImagePlanTestManager().Plan(CreateImageRequest{
		CatalogID:                "debian-live-testing-gnome-arm64-20240902",
		Source:                   "/inputs/source.iso",
		Output:                   "/output/debianlive.iso",
		SourceSHA256:             "1111111111111111111111111111111111111111111111111111111111111111",
		KernelProfile:            "surface-pro-11-x1e-oled",
		CompanionSourceDirectory: "/inputs/lexr",
		CompanionUserspace:       []string{"iptsd"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := operation.Steps[0].Inputs["sha256"]; got != "1111111111111111111111111111111111111111111111111111111111111111" {
		t.Fatalf("caller checksum override not propagated: %q", got)
	}
	if got := operation.Steps[0].Inputs["path"]; got != "/inputs/source.iso" {
		t.Fatalf("source path not propagated: %q", got)
	}
	if got := operation.Steps[2].Inputs["userspace"]; got != "iptsd-v1" {
		t.Fatalf("companion userspace not propagated: %q", got)
	}
	if got := operation.Steps[1].Inputs["profile"]; got != "surface-pro-11-x1e-oled" {
		t.Fatalf("kernel profile not propagated: %q", got)
	}
}
