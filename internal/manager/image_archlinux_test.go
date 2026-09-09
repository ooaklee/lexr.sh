package manager

import (
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/image/archlinux"
)

// TestImageManagerPlansArchTerminalWorkflow checks the rootfs-specific route,
// signed snapshot and complete producer/validation plan without downloading.
func TestImageManagerPlansArchTerminalWorkflow(t *testing.T) {
	operation, err := newImagePlanTestManager().Plan(CreateImageRequest{CatalogID: "arch-linux-arm-aarch64-20260805", Output: "/output/arch.iso", KernelProfile: "surface-pro-11-x1e-oled"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, step := range operation.Steps {
		ids = append(ids, step.ID)
	}
	if !reflect.DeepEqual(ids, archlinux.CreationStepIDs()) {
		t.Fatalf("Arch plan=%v", ids)
	}
	if operation.Steps[0].Inputs["sha256"] != "42a4eeaa038994ffd31fa173256ef2f0ef511358eeb41b9ea1f8626391b9b319" {
		t.Fatal("Arch source pin was lost")
	}
	if operation.Steps[3].Inputs["adapter"] != archlinux.AdapterID {
		t.Fatal("Arch selected another adapter")
	}
	request := archLinuxARMAdapterRequest(imageAdapterRequest{Source: "/source/rootfs.tar.gz", CacheDirectory: "/cache", Output: "/out/image.iso"})
	if request.CacheDirectory != "/cache" || request.SourceRootfs != "/source/rootfs.tar.gz" {
		t.Fatal("rootfs/cache request was lost")
	}
}
