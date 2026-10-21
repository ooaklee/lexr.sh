package install

import (
	"context"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// TestInstallMetadataPreservesLegacyAndOrderedSchemas checks the read-only
// metadata entry point without invoking any package manager or target writes.
func TestInstallMetadataPreservesLegacyAndOrderedSchemas(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-v2", true: "ordered-v3"}[ordered], func(t *testing.T) {
			bundle := installSchemaReaderFixture(t, ordered)
			runner := &fakeRunner{}
			packages, err := fixtureManager(runner).inspectBundle(context.Background(), bundle)
			if err != nil || len(packages) != 2 {
				t.Fatalf("inspectBundle() packages = %d, error = %v", len(packages), err)
			}
			if len(runner.runs) != 0 {
				t.Fatal("read-only inspection issued a mutating command")
			}
		})
	}
}

// TestInstallMetadataRejectsSchemaPolicyMismatch retains the version/policy
// guard before even read-only Debian metadata queries are performed.
func TestInstallMetadataRejectsSchemaPolicyMismatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ordered bool
		mutate  func(*kernel.Bundle)
	}{
		{"legacy-with-policy", true, func(b *kernel.Bundle) { b.SchemaVersion = kernel.BundleSchemaVersion }},
		{"ordered-without-policy", false, func(b *kernel.Bundle) { b.SchemaVersion = kernel.OrderedBundleSchemaVersion }},
		{"unknown-version", false, func(b *kernel.Bundle) { b.SchemaVersion = 4 }},
		{"incomplete-ordered-evidence", true, func(b *kernel.Bundle) { b.DTBSelectionProvenance.StubbleSelection.PayloadOrder = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := installSchemaReaderFixture(t, tc.ordered)
			tc.mutate(&bundle)
			if err := validateDeviceTrees(bundle); err == nil {
				t.Fatal("delivery validation accepted the invalid schema or policy")
			}
			runner := &fakeRunner{}
			if _, err := fixtureManager(runner).inspectBundle(context.Background(), bundle); err == nil {
				t.Fatal("metadata inspection accepted the invalid schema or policy")
			}
			if len(runner.captures) != 0 || len(runner.runs) != 0 {
				t.Fatal("invalid schema or policy reached metadata commands")
			}
		})
	}
}

// installSchemaReaderFixture constructs synthetic selector observations over
// existing immutable fixture packages, not an installed firmware measurement.
func installSchemaReaderFixture(t *testing.T, ordered bool) kernel.Bundle {
	t.Helper()
	_, base := fixtureEnvironment(t)
	bundle := fixtureEmbeddedBundle(t, base)
	if !ordered {
		return bundle
	}
	selection := &kernel.StubbleSelection{Policy: kernel.StubbleInventoryOrderPolicy, ImageSHA256: strings.Repeat("a", 64), HWIDTableSHA256: strings.Repeat("b", 64)}
	for _, tree := range bundle.DeviceTrees {
		selection.PayloadOrder = append(selection.PayloadOrder, tree.Device)
		for _, selector := range tree.Selectors {
			if selector.Kind == kernel.DeviceTreeSelectorHWID {
				selection.HWIDRecords = append(selection.HWIDRecords, kernel.StubbleHWIDRecord{HWID: selector.Value, Compatible: tree.CompatibleStrings[0]})
			}
		}
	}
	bundle.DTBSelectionProvenance.Tool = "stubble"
	bundle.DTBSelectionProvenance.StubbleSelection = selection
	actual, err := kernel.NewBundle(kernel.BundleOptions{Release: bundle.Release, Repository: bundle.Repository,
		RequestedBootImageMode: bundle.RequestedBootImageMode, EffectiveDTBDelivery: bundle.EffectiveDTBDelivery,
		EmbeddedDTBCount: bundle.EmbeddedDTBCount, DTBSelectionProvenance: bundle.DTBSelectionProvenance,
		Packages: bundle.Packages, DeviceTrees: bundle.DeviceTrees})
	if err != nil {
		t.Fatal(err)
	}
	return actual
}
