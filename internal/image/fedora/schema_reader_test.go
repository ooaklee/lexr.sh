package fedora

import (
	"strings"
	"testing"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// TestFedoraManifestPreservesLegacyAndOrderedBundleSchemas keeps the image
// envelope unchanged while accepting each explicit kernel delivery contract.
func TestFedoraManifestPreservesLegacyAndOrderedBundleSchemas(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-v2", true: "ordered-v3"}[ordered], func(t *testing.T) {
			if err := validateFedoraManifest(fedoraSchemaReaderFixture(t, ordered)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestFedoraManifestRejectsBundleSchemaPolicyMismatch preserves schema and
// canonical inventory rejection inside otherwise valid image manifests.
func TestFedoraManifestRejectsBundleSchemaPolicyMismatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ordered bool
		mutate  func(*kernel.Bundle)
	}{
		{"legacy-with-policy", true, func(b *kernel.Bundle) { b.SchemaVersion = kernel.BundleSchemaVersion }},
		{"ordered-without-policy", false, func(b *kernel.Bundle) { b.SchemaVersion = kernel.OrderedBundleSchemaVersion }},
		{"unknown-version", false, func(b *kernel.Bundle) { b.SchemaVersion = 4 }},
		{"incomplete-payload-order", true, func(b *kernel.Bundle) { b.DTBSelectionProvenance.StubbleSelection.PayloadOrder = nil }},
		{"noncanonical-trees", true, func(b *kernel.Bundle) { b.DeviceTrees[0], b.DeviceTrees[1] = b.DeviceTrees[1], b.DeviceTrees[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := fedoraSchemaReaderFixture(t, tc.ordered)
			tc.mutate(&manifest.KernelBundle)
			if err := validateFedoraManifest(manifest); err == nil {
				t.Fatal("invalid kernel schema, policy or canonical inventory was accepted")
			}
		})
	}
}

// fedoraSchemaReaderFixture records synthetic ordered observations without
// claiming that the temporary image fixture represents measured hardware.
func fedoraSchemaReaderFixture(t *testing.T, ordered bool) imagecontract.Manifest {
	t.Helper()
	manifest := completeFedoraManifestFixture()
	if !ordered {
		return manifest
	}
	bundle := manifest.KernelBundle
	selection := &kernel.StubbleSelection{Policy: kernel.StubbleInventoryOrderPolicy, ImageSHA256: strings.Repeat("a", 64), HWIDTableSHA256: strings.Repeat("b", 64)}
	for _, tree := range bundle.DeviceTrees {
		selection.PayloadOrder = append(selection.PayloadOrder, tree.Device)
		for _, selector := range tree.Selectors {
			if selector.Kind == kernel.DeviceTreeSelectorHWID {
				selection.HWIDRecords = append(selection.HWIDRecords, kernel.StubbleHWIDRecord{HWID: selector.Value, Compatible: tree.CompatibleStrings[0]})
			}
		}
	}
	bundle.DTBSelectionProvenance.StubbleSelection = selection
	actual, err := kernel.NewBundle(kernel.BundleOptions{Release: bundle.Release, Repository: bundle.Repository,
		RequestedBootImageMode: bundle.RequestedBootImageMode, EffectiveDTBDelivery: bundle.EffectiveDTBDelivery,
		EmbeddedDTBCount: bundle.EmbeddedDTBCount, DTBSelectionProvenance: bundle.DTBSelectionProvenance,
		Packages: bundle.Packages, DeviceTrees: bundle.DeviceTrees})
	if err != nil {
		t.Fatal(err)
	}
	manifest.KernelBundle = actual
	return manifest
}
