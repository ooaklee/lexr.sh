package install

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// publishedV2InstallMetadata reads only the exact public manifest fixture.
// It neither acquires packages nor invents Debian control or boot evidence.
func publishedV2InstallMetadata(t *testing.T) kernel.Bundle {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "published-sp11-7.2.9-v2", "lexr-kernel-bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if len(data) != 178937 || hex.EncodeToString(digest[:]) != "c9099602a5b0f8ae972d41aa5ba25c779b08cddf513605fd44feb8f5c9c46ba4" {
		t.Fatal("public manifest fixture differs from the published asset")
	}
	var bundle kernel.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatal(err)
	}
	return bundle
}

// TestPublishedV2InstallDeliveryMetadataPreservesOrderedContract exercises the
// installer's normal pre-command delivery validator, not a kernel installation.
func TestPublishedV2InstallDeliveryMetadataPreservesOrderedContract(t *testing.T) {
	bundle := publishedV2InstallMetadata(t)
	if err := validateDeviceTrees(bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.SchemaVersion != kernel.OrderedBundleSchemaVersion || len(bundle.DeviceTrees) != 39 || len(bundle.DTBSelectionProvenance.StubbleSelection.HWIDRecords) != 501 {
		t.Fatal("published complete delivery inventory was lost")
	}
}

// TestPublishedV2InstallDeliveryMetadataRejectsContractTampering keeps
// downgraded or incomplete public declarations outside the installer boundary.
func TestPublishedV2InstallDeliveryMetadataRejectsContractTampering(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*kernel.Bundle)
	}{
		{"schema-downgrade", func(b *kernel.Bundle) { b.SchemaVersion = kernel.BundleSchemaVersion }},
		{"missing-ordered-policy", func(b *kernel.Bundle) { b.DTBSelectionProvenance.StubbleSelection = nil }},
		{"missing-payload", func(b *kernel.Bundle) {
			b.DTBSelectionProvenance.StubbleSelection.PayloadOrder = b.DTBSelectionProvenance.StubbleSelection.PayloadOrder[1:]
		}},
		{"duplicate-payload", func(b *kernel.Bundle) {
			b.DTBSelectionProvenance.StubbleSelection.PayloadOrder[0] = b.DTBSelectionProvenance.StubbleSelection.PayloadOrder[1]
		}},
		{"invalid-hwid", func(b *kernel.Bundle) { b.DTBSelectionProvenance.StubbleSelection.HWIDRecords[0].HWID = "invalid" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			bundle := publishedV2InstallMetadata(t)
			test.mutate(&bundle)
			if err := validateDeviceTrees(bundle); err == nil {
				t.Fatal("tampered public declaration passed delivery validation")
			}
		})
	}
}
