package manager

import (
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/image/elementary"
)

// TestImageManagerPlansElementarySingleRootWorkflow verifies catalogue routing
// and the pinned source checksum reach elementary's actual GRUB-centred plan
// without another adapter's workflow.
func TestImageManagerPlansElementarySingleRootWorkflow(t *testing.T) {
	operation, err := newImagePlanTestManager().Plan(CreateImageRequest{CatalogID: "elementary-os-8-1-20260219", Output: "/output/elementary.iso", KernelProfile: "surface-pro-11-x1e-oled"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, step := range operation.Steps {
		ids = append(ids, step.ID)
	}
	if !reflect.DeepEqual(ids, elementary.CreationStepIDs()) {
		t.Fatalf("elementary plan=%v", ids)
	}
	if operation.Steps[0].Inputs["sha256"] != "85116d48c406ae7cd60c936050a099d4b8610321273f6f0a694796db4d4e86ba" {
		t.Fatal("elementary publisher checksum was not retained")
	}
	if operation.Steps[3].Inputs["adapter"] != elementary.AdapterID {
		t.Fatal("elementary selected another adapter")
	}
}

// TestImageManagerRoutesElementaryRequestToItsAdapter verifies request
// propagation by checking every resolved input appears in elementary's plan.
func TestImageManagerRoutesElementaryRequestToItsAdapter(t *testing.T) {
	operation, err := newImagePlanTestManager().Plan(CreateImageRequest{
		CatalogID:                "elementary-os-8-1-20260219",
		Source:                   "/inputs/source.iso",
		Output:                   "/output/elementary.iso",
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
