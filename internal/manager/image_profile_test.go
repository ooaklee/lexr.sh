package manager

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// TestImageProfileSelection preserves embedded routing while narrowing external
// images to the selected platform, without modifying source bundle authority.
func TestImageProfileSelection(t *testing.T) {
	source := externalProfileBundle(t)
	for _, id := range []string{"x1e80100-microsoft-denali-oled", "x1p64100-microsoft-denali"} {
		projected, err := selectImageKernel(source, CreateImageRequest{Profile: id})
		if err != nil {
			t.Fatal(err)
		}
		var required []string
		for _, tree := range projected.DeviceTrees {
			if tree.Required {
				required = append(required, tree.Device)
			}
		}
		want := "surface-pro-11-x1e-oled"
		if strings.HasPrefix(id, "x1p") {
			want = "surface-pro-11-x1p-lcd"
		}
		if !reflect.DeepEqual(required, []string{want}) {
			t.Fatalf("selected external targets = %v", required)
		}
	}
	if !source.DeviceTrees[0].Required || !source.DeviceTrees[1].Required {
		t.Fatal("modified source inventory")
	}
	// This helper receives bundles already validated by the resolver. A minimal
	// embedded inventory isolates the profile check from bundle serialisation.
	embedded := source
	embedded.DeviceTrees = kernel.CloneDeviceTrees(source.DeviceTrees)
	for index := range embedded.DeviceTrees {
		embedded.DeviceTrees[index].EmbeddedMatches = 1
	}
	embedded.EffectiveDTBDelivery = kernel.DTBDeliveryEmbedded
	for _, id := range []string{"x1e80100-microsoft-denali-oled", "x1p64100-microsoft-denali"} {
		got, err := selectImageKernel(embedded, CreateImageRequest{Profile: id})
		if err != nil || !reflect.DeepEqual(got, embedded) {
			t.Fatalf("embedded routing changed: %#v, %v", got, err)
		}
	}
	embedded.DeviceTrees[1].EmbeddedMatches = 0
	if _, err := selectImageKernel(embedded, CreateImageRequest{Profile: "x1p64100-microsoft-denali"}); err == nil {
		t.Fatal("accepted a packaged LCD DTB absent from the embedded boot image")
	}
	embedded.DeviceTrees = embedded.DeviceTrees[:1]
	if _, err := selectImageKernel(embedded, CreateImageRequest{Profile: "x1p64100-microsoft-denali"}); err == nil {
		t.Fatal("accepted undeclared LCD embedded support")
	}
	embedded.DeviceTrees = kernel.CloneDeviceTrees(embedded.DeviceTrees)
	embedded.DeviceTrees[0].Selectors = nil
	if _, err := selectImageKernel(embedded, CreateImageRequest{Profile: "x1e80100-microsoft-denali-oled"}); err == nil {
		t.Fatal("accepted a DTB without an embedded selector")
	}
}
