package releaseprep

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/kernel/build"
)

// TestNativeReleaseReaderPreservesLegacyAndOrderedSchemas verifies exact
// builder-output projection for both declared delivery contracts.
func TestNativeReleaseReaderPreservesLegacyAndOrderedSchemas(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-v2", true: "ordered-v3"}[ordered], func(t *testing.T) {
			fixture, expected := nativeSchemaReaderFixture(t, ordered)
			actual, _, _, err := inspectNativeBuild(context.Background(), fixture.Build, fixtureRelease)
			if err != nil || actual.SchemaVersion != expected.SchemaVersion {
				t.Fatalf("native projection schema = %d, error = %v", actual.SchemaVersion, err)
			}
		})
	}
}

// TestNativeReleaseReaderRejectsSchemaPolicyMismatch prevents relabelling a
// provenance-bound native build while retaining exact canonical comparisons.
func TestNativeReleaseReaderRejectsSchemaPolicyMismatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ordered bool
		mutate  func(*kernel.Bundle)
	}{
		{"legacy-with-policy", true, func(b *kernel.Bundle) { b.SchemaVersion = kernel.BundleSchemaVersion }},
		{"ordered-without-policy", false, func(b *kernel.Bundle) { b.SchemaVersion = kernel.OrderedBundleSchemaVersion }},
		{"unknown-version", false, func(b *kernel.Bundle) { b.SchemaVersion = 4 }},
		{"noncanonical-trees", true, func(b *kernel.Bundle) { b.DeviceTrees[0], b.DeviceTrees[1] = b.DeviceTrees[1], b.DeviceTrees[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, bundle := nativeSchemaReaderFixture(t, tc.ordered)
			tc.mutate(&bundle)
			mustWriteJSON(t, filepath.Join(fixture.Build, BundleFileName), bundle)
			if _, _, _, err := inspectNativeBuild(context.Background(), fixture.Build, fixtureRelease); err == nil {
				t.Fatal("invalid schema or changed canonical output was accepted")
			}
		})
	}
}

// nativeSchemaReaderFixture keeps recorded bundle and builder provenance
// coherent, using only synthetic hashes and temporary package fixtures.
func nativeSchemaReaderFixture(t *testing.T, ordered bool) (releaseFixture, kernel.Bundle) {
	t.Helper()
	fixture := newReleaseFixture(t, false)
	bundle := readBundle(t, fixture.Build)
	if !ordered {
		return fixture, bundle
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
	bundle.DTBSelectionProvenance.StubbleSelection = selection
	actual, err := kernel.NewBundle(kernel.BundleOptions{Release: bundle.Release, Repository: bundle.Repository,
		RequestedBootImageMode: bundle.RequestedBootImageMode, EffectiveDTBDelivery: bundle.EffectiveDTBDelivery,
		EmbeddedDTBCount: bundle.EmbeddedDTBCount, DTBSelectionProvenance: bundle.DTBSelectionProvenance,
		Packages: bundle.Packages, DeviceTrees: bundle.DeviceTrees})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(fixture.Build, BuildProvenanceFileName))
	if err != nil {
		t.Fatal(err)
	}
	var provenance build.Provenance
	if err := json.Unmarshal(data, &provenance); err != nil {
		t.Fatal(err)
	}
	provenance.DTBSelectionProvenance = actual.DTBSelectionProvenance
	mustWriteJSON(t, filepath.Join(fixture.Build, BundleFileName), actual)
	mustWriteJSON(t, filepath.Join(fixture.Build, BuildProvenanceFileName), provenance)
	return fixture, actual
}
