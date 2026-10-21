package kernel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalBundleReaderPreservesLegacyAndOrderedSchemas exercises public
// discovery without relabelling the legacy unique-route contract.
func TestLocalBundleReaderPreservesLegacyAndOrderedSchemas(t *testing.T) {
	for _, ordered := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-v2", true: "ordered-v3"}[ordered], func(t *testing.T) {
			directory, bundle := localSchemaReaderFixture(t, ordered)
			actual, err := DiscoverLocalBundle(directory)
			if err != nil {
				t.Fatal(err)
			}
			if actual.SchemaVersion != bundle.SchemaVersion {
				t.Fatalf("schema = %d, want %d", actual.SchemaVersion, bundle.SchemaVersion)
			}
			if (actual.DTBSelectionProvenance.StubbleSelection != nil) != ordered {
				t.Fatal("ordered policy was lost or invented during discovery")
			}
		})
	}
}

// TestLocalBundleReaderRejectsSchemaPolicyMismatch retains closed version and
// canonical inventory validation for both supported reader contracts.
func TestLocalBundleReaderRejectsSchemaPolicyMismatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ordered bool
		mutate  func(*Bundle)
	}{
		{"legacy-with-ordered-policy", true, func(b *Bundle) { b.SchemaVersion = BundleSchemaVersion }},
		{"ordered-without-policy", false, func(b *Bundle) { b.SchemaVersion = OrderedBundleSchemaVersion }},
		{"unknown-version", false, func(b *Bundle) { b.SchemaVersion = 4 }},
		{"ordered-missing-payload", true, func(b *Bundle) { b.DTBSelectionProvenance.StubbleSelection.PayloadOrder = nil }},
		{"ordered-noncanonical-trees", true, func(b *Bundle) { b.DeviceTrees[0], b.DeviceTrees[1] = b.DeviceTrees[1], b.DeviceTrees[0] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory, bundle := localSchemaReaderFixture(t, tc.ordered)
			tc.mutate(&bundle)
			data, err := json.Marshal(bundle)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, localBundleManifest), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := DiscoverLocalBundle(directory); err == nil {
				t.Fatal("invalid schema, policy or canonical inventory was accepted")
			}
		})
	}
}

// localSchemaReaderFixture adds ordered observations to an otherwise identical
// synthetic fixture; its hashes do not claim a measured boot image.
func localSchemaReaderFixture(t *testing.T, ordered bool) (string, Bundle) {
	t.Helper()
	directory := t.TempDir()
	writeLocalPair(t, directory, testLocalABI, testLocalVersion)
	writeLocalBundleManifest(t, directory, testLocalABI, testLocalVersion, false)
	data, err := os.ReadFile(filepath.Join(directory, localBundleManifest))
	if err != nil {
		t.Fatal(err)
	}
	var bundle Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatal(err)
	}
	if ordered {
		selection := &StubbleSelection{Policy: StubbleInventoryOrderPolicy, ImageSHA256: strings.Repeat("a", 64), HWIDTableSHA256: strings.Repeat("b", 64)}
		for _, tree := range bundle.DeviceTrees {
			selection.PayloadOrder = append(selection.PayloadOrder, tree.Device)
			for _, selector := range tree.Selectors {
				if selector.Kind == DeviceTreeSelectorHWID {
					selection.HWIDRecords = append(selection.HWIDRecords, StubbleHWIDRecord{HWID: selector.Value, Compatible: tree.CompatibleStrings[0]})
				}
			}
		}
		bundle.DTBSelectionProvenance.Tool = "stubble"
		bundle.DTBSelectionProvenance.StubbleSelection = selection
		bundle, err = NewBundle(BundleOptions{Release: bundle.Release, Repository: bundle.Repository,
			RequestedBootImageMode: bundle.RequestedBootImageMode, EffectiveDTBDelivery: bundle.EffectiveDTBDelivery,
			EmbeddedDTBCount: bundle.EmbeddedDTBCount, DTBSelectionProvenance: bundle.DTBSelectionProvenance,
			Packages: bundle.Packages, DeviceTrees: bundle.DeviceTrees})
		if err != nil {
			t.Fatal(err)
		}
		data, err = json.Marshal(bundle)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, localBundleManifest), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory, bundle
}
