package manager

import (
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/image/popos"
)

// TestImageManagerPlansPopSingleRootWorkflow verifies catalogue routing and
// the source checksum reach Pop's actual plan without Ubuntu's layered step.
func TestImageManagerPlansPopSingleRootWorkflow(t *testing.T) {
	operation, err := newImagePlanTestManager().Plan(CreateImageRequest{CatalogID: "pop-os-24-04-arm64-generic-3", Output: "/output/pop.iso", KernelProfile: "surface-pro-11-x1e-oled"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, step := range operation.Steps {
		ids = append(ids, step.ID)
	}
	if !reflect.DeepEqual(ids, popos.CreationStepIDs()) {
		t.Fatalf("Pop plan=%v", ids)
	}
	if operation.Steps[0].Inputs["sha256"] != "7b4cce0e92dc5c903464e7e7c33760c4417f1400165e9ecfcd064fbceb68ef22" {
		t.Fatal("Pop publisher checksum was not retained")
	}
	if operation.Steps[3].Inputs["adapter"] != popos.AdapterID {
		t.Fatal("Pop selected another adapter")
	}
}
