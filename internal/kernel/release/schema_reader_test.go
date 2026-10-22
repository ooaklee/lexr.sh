package release

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// TestReleaseBundleReaderPreservesLegacyAndOrderedSchemas proves downloaded
// manifests retain their versioned policy while preserving canonical checks.
func TestReleaseBundleReaderPreservesLegacyAndOrderedSchemas(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-v2", true: "ordered-v3"}[ordered], func(t *testing.T) {
			bundle := releaseSchemaReaderFixture(t, ordered)
			actual, err := decodeReleaseBundle(writeSchemaReaderManifest(t, bundle))
			if err != nil || !reflect.DeepEqual(actual, bundle) {
				t.Fatalf("decoded bundle differs: %v", err)
			}
		})
	}
}

// TestReleaseBundleReaderRejectsSchemaPolicyMismatch rejects relabelled,
// incomplete and noncanonical contracts before download authority is granted.
func TestReleaseBundleReaderRejectsSchemaPolicyMismatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ordered bool
		mutate  func(*kernel.Bundle)
	}{
		{"legacy-with-policy", true, func(b *kernel.Bundle) { b.SchemaVersion = kernel.BundleSchemaVersion }},
		{"ordered-without-policy", false, func(b *kernel.Bundle) { b.SchemaVersion = kernel.OrderedBundleSchemaVersion }},
		{"unknown-version", false, func(b *kernel.Bundle) { b.SchemaVersion = 4 }},
		{"missing-payload-order", true, func(b *kernel.Bundle) { b.DTBSelectionProvenance.StubbleSelection.PayloadOrder = nil }},
		{"noncanonical-trees", true, func(b *kernel.Bundle) { b.DeviceTrees[0], b.DeviceTrees[1] = b.DeviceTrees[1], b.DeviceTrees[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := releaseSchemaReaderFixture(t, tc.ordered)
			tc.mutate(&bundle)
			if _, err := decodeReleaseBundle(writeSchemaReaderManifest(t, bundle)); err == nil {
				t.Fatal("invalid schema, policy or canonical inventory was accepted")
			}
		})
	}
}

// releaseSchemaReaderFixture creates synthetic metadata rather than claiming
// its image digests describe actual firmware or hardware behaviour.
func releaseSchemaReaderFixture(t *testing.T, ordered bool) kernel.Bundle {
	t.Helper()
	files := map[string][]byte{
		"linux-image-" + testReleaseABI + "_" + testReleaseVersion + "_arm64.deb":   []byte("image"),
		"linux-modules-" + testReleaseABI + "_" + testReleaseVersion + "_arm64.deb": []byte("modules"),
	}
	var bundle kernel.Bundle
	if err := json.Unmarshal(kernelReleaseBundle(files), &bundle); err != nil {
		t.Fatal(err)
	}
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

// writeSchemaReaderManifest confines each decoder fixture to a fresh file.
func writeSchemaReaderManifest(t *testing.T, bundle kernel.Bundle) string {
	t.Helper()
	data, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}
