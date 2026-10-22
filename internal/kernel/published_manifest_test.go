package kernel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// publishedV2ManifestSHA256 pins the exact public metadata, not package bytes.
const publishedV2ManifestSHA256 = "c9099602a5b0f8ae972d41aa5ba25c779b08cddf513605fd44feb8f5c9c46ba4"

// TestPublishedV2FixtureRetainsPublicOriginsAndNotices binds the unmodified
// metadata and associated release notices to their exact public asset records.
func TestPublishedV2FixtureRetainsPublicOriginsAndNotices(t *testing.T) {
	directory := filepath.Join("testdata", "published-sp11-7.2.9-v2")
	data, err := os.ReadFile(filepath.Join(directory, "SOURCES.json"))
	if err != nil {
		t.Fatal(err)
	}
	var provenance struct {
		SchemaVersion   int    `json:"schema_version"`
		Release         string `json:"release"`
		ReleaseURL      string `json:"release_url"`
		DownloadBaseURL string `json:"download_base_url"`
		Files           map[string]struct {
			GitHubAssetID int64  `json:"github_asset_id"`
			Bytes         int    `json:"bytes"`
			SHA256        string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &provenance); err != nil {
		t.Fatal(err)
	}
	release := "sp11-qcom-x1e-7.2.9-jg-0sp11v2"
	if provenance.SchemaVersion != 1 || provenance.Release != release || provenance.ReleaseURL != "https://github.com/ooaklee/linux-surface-pro-11-oe/releases/tag/"+release || provenance.DownloadBaseURL != "https://github.com/ooaklee/linux-surface-pro-11-oe/releases/download/"+release+"/" || len(provenance.Files) != 3 {
		t.Fatal("public fixture provenance differs from its release")
	}
	expected := map[string]struct {
		bytes   int
		sha256  string
		assetID int64
	}{
		"lexr-kernel-bundle.json": {178937, publishedV2ManifestSHA256, 664644600},
		"COPYING":                 {496, "fb5a425bd3b3cd6071a3a9aff9909a859e7c1158d54d32e07658398cd67eb6a0", 664644599},
		"LICENSE-GPL-2.0":         {18600, "8780e78a1a737e127f25a65f6d95269bffd36158dc261114de7859b490bfc5aa", 664644605},
	}
	for name, record := range provenance.Files {
		want, found := expected[name]
		if !found || record.Bytes != want.bytes || record.SHA256 != want.sha256 || record.GitHubAssetID != want.assetID {
			t.Fatal("unexpected, duplicate or changed public fixture asset")
		}
		bytes, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(bytes)
		if len(bytes) != want.bytes || hex.EncodeToString(digest[:]) != want.sha256 {
			t.Fatalf("public fixture asset or notice changed: %s", name)
		}
		delete(expected, name)
	}
	if len(expected) != 0 {
		t.Fatal("public fixture omitted an asset or associated notice")
	}
}

// readPublishedV2Manifest authenticates the offline public fixture and decodes
// its original observations without acquiring any referenced package.
func readPublishedV2Manifest(t *testing.T) ([]byte, Bundle) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "published-sp11-7.2.9-v2", localBundleManifest))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if len(data) != 178937 || hex.EncodeToString(digest[:]) != publishedV2ManifestSHA256 {
		t.Fatal("public manifest fixture differs from the published asset")
	}
	var bundle Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatal(err)
	}
	return data, bundle
}

// validatePublishedV2LocalMetadata exercises the normal local declaration
// validator with recorded names and checksums, not fabricated Debian payloads.
func validatePublishedV2LocalMetadata(bundle Bundle, authority Bundle) error {
	checksums := make(map[string]string, len(authority.Packages))
	var image, modules localCandidate
	for _, pkg := range authority.Packages {
		checksums[pkg.Name] = pkg.SHA256
		candidate := localCandidate{name: pkg.Name, role: pkg.Role, abi: authority.ABI, version: authority.Version}
		if pkg.Role == RoleImage {
			image = candidate
		}
		if pkg.Role == RoleModules {
			modules = candidate
		}
	}
	_, err := validateLocalBundleManifest(bundle, image, modules, checksums, true)
	return err
}

// readPublishedV2LocalMetadata uses the ordinary bounded local JSON reader.
// It deliberately creates no package files or package-manager evidence.
func readPublishedV2LocalMetadata(t *testing.T, data []byte) (Bundle, error) {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, localBundleManifest), data, 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, present, err := loadLocalBundleManifest(directory)
	if err == nil && !present {
		t.Fatal("local reader did not observe its manifest")
	}
	return bundle, err
}

// TestPublishedV2LocalManifestPreservesCompleteOrderedInventory reproduces the
// released metadata shape through normal local reading and canonicalisation.
func TestPublishedV2LocalManifestPreservesCompleteOrderedInventory(t *testing.T) {
	data, expected := readPublishedV2Manifest(t)
	actual, err := readPublishedV2LocalMetadata(t, data)
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("published local metadata changed: %v", err)
	}
	if err := validatePublishedV2LocalMetadata(actual, expected); err != nil {
		t.Fatal(err)
	}
	selection := actual.DTBSelectionProvenance.StubbleSelection
	if actual.SchemaVersion != OrderedBundleSchemaVersion || actual.EmbeddedDTBCount != 39 || len(actual.DeviceTrees) != 39 || len(actual.Packages) != 4 || len(selection.PayloadOrder) != 39 || len(selection.HWIDRecords) != 501 {
		t.Fatal("published complete delivery counts were lost")
	}
	if selection.PayloadOrder[0] != "dtb-84d40c8dd036b093e24d9bb0" || selection.PayloadOrder[1] != "surface-pro-11-x1e-oled" || !HasSharedSelectionRoutes(actual.DeviceTrees) {
		t.Fatal("published payload order or observed optional route overlap changed")
	}
	var encoded bytes.Buffer
	if err := actual.WriteJSON(&encoded); err != nil || !bytes.Equal(encoded.Bytes(), data) {
		t.Fatalf("published metadata does not round-trip byte-for-byte: %v", err)
	}
}

// TestPublishedV2LocalManifestRejectsContractTampering retains the schema,
// route, canonical-path and checksum boundaries for the real large inventory.
func TestPublishedV2LocalManifestRejectsContractTampering(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Bundle)
	}{
		{"schema-downgrade", func(b *Bundle) { b.SchemaVersion = BundleSchemaVersion }},
		{"unknown-schema", func(b *Bundle) { b.SchemaVersion = 4 }},
		{"missing-ordered-policy", func(b *Bundle) { b.DTBSelectionProvenance.StubbleSelection = nil }},
		{"missing-payload", func(b *Bundle) {
			b.DTBSelectionProvenance.StubbleSelection.PayloadOrder = b.DTBSelectionProvenance.StubbleSelection.PayloadOrder[1:]
		}},
		{"duplicate-payload", func(b *Bundle) {
			b.DTBSelectionProvenance.StubbleSelection.PayloadOrder[0] = b.DTBSelectionProvenance.StubbleSelection.PayloadOrder[1]
		}},
		{"invalid-hwid", func(b *Bundle) { b.DTBSelectionProvenance.StubbleSelection.HWIDRecords[0].HWID = "invalid" }},
		{"missing-selection-evidence", func(b *Bundle) { b.DTBSelectionProvenance.Selections = b.DTBSelectionProvenance.Selections[1:] }},
		{"unsafe-dtb-path", func(b *Bundle) { b.DeviceTrees[0].Path = "../outside.dtb" }},
		{"noncanonical-inventory", func(b *Bundle) { b.DeviceTrees[0], b.DeviceTrees[1] = b.DeviceTrees[1], b.DeviceTrees[0] }},
		{"package-checksum-disagreement", func(b *Bundle) { b.Packages[0].SHA256 = strings.Repeat("0", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, authority := readPublishedV2Manifest(t)
			_, changed := readPublishedV2Manifest(t)
			test.mutate(&changed)
			data, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			actual, err := readPublishedV2LocalMetadata(t, data)
			if err == nil {
				err = validatePublishedV2LocalMetadata(actual, authority)
			}
			if err == nil {
				t.Fatal("tampered published delivery metadata was accepted")
			}
		})
	}
}

// TestPublishedV2LocalManifestRetainsStrictSyntaxAndByteBound proves the larger
// legitimate fixture does not become an exception to existing reader limits.
func TestPublishedV2LocalManifestRetainsStrictSyntaxAndByteBound(t *testing.T) {
	data, authority := readPublishedV2Manifest(t)
	if MaximumBundleManifestBytes != 1<<20 {
		t.Fatal("shared manifest bound changed instead of restoring compatibility")
	}
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"unknown-field", bytes.Replace(data, []byte("{\n"), []byte("{\n  \"unexpected\": true,\n"), 1)},
		{"trailing-json", append(append([]byte(nil), data...), []byte("{}\n")...)},
		{"malformed-json", []byte("{invalid}")},
		{"limit-plus-one", append(append([]byte(nil), data...), bytes.Repeat([]byte(" "), int(MaximumBundleManifestBytes)-len(data)+1)...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := readPublishedV2LocalMetadata(t, test.data); err == nil {
				t.Fatal("invalid or oversized published metadata was accepted")
			}
		})
	}
	padded := append(append([]byte(nil), data...), bytes.Repeat([]byte(" "), int(MaximumBundleManifestBytes)-len(data))...)
	actual, err := readPublishedV2LocalMetadata(t, padded)
	if err != nil || validatePublishedV2LocalMetadata(actual, authority) != nil {
		t.Fatalf("otherwise valid metadata at the existing byte bound was rejected: %v", err)
	}
}
