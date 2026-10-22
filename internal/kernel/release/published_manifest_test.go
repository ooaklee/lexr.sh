package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// publishedV2ReleaseManifestSHA256 binds the offline fixture to the public asset.
const publishedV2ReleaseManifestSHA256 = "c9099602a5b0f8ae972d41aa5ba25c779b08cddf513605fd44feb8f5c9c46ba4"

// publishedV2ReleaseMetadata reads exact public metadata without network access
// or referenced package acquisition. Its origin is recorded beside the fixture.
func publishedV2ReleaseMetadata(t *testing.T) ([]byte, kernel.Bundle) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "published-sp11-7.2.9-v2", releaseBundleManifestName))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if len(data) != 178937 || hex.EncodeToString(digest[:]) != publishedV2ReleaseManifestSHA256 {
		t.Fatal("public manifest fixture differs from the published asset")
	}
	var bundle kernel.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatal(err)
	}
	return data, bundle
}

// decodePublishedV2ReleaseMetadata feeds actual fixture bytes to the unchanged
// bounded release decoder without simulating any Debian package contents.
func decodePublishedV2ReleaseMetadata(t *testing.T, data []byte) (kernel.Bundle, error) {
	t.Helper()
	file := filepath.Join(t.TempDir(), releaseBundleManifestName)
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return decodeReleaseBundle(file)
}

// TestPublishedV2ReleaseManifestPreservesCompleteOrderedInventory proves the
// ordinary release decoder retains all 39 payloads and 501 ordered HWID records.
func TestPublishedV2ReleaseManifestPreservesCompleteOrderedInventory(t *testing.T) {
	data, expected := publishedV2ReleaseMetadata(t)
	actual, err := decodePublishedV2ReleaseMetadata(t, data)
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("published release metadata changed: %v", err)
	}
	selection := actual.DTBSelectionProvenance.StubbleSelection
	if actual.SchemaVersion != kernel.OrderedBundleSchemaVersion || actual.EmbeddedDTBCount != 39 || len(actual.DeviceTrees) != 39 || len(selection.PayloadOrder) != 39 || len(selection.HWIDRecords) != 501 || !kernel.HasSharedSelectionRoutes(actual.DeviceTrees) {
		t.Fatal("published ordered inventory or optional route observations were lost")
	}
}

// TestPublishedV2ReleaseManifestRejectsContractTampering exercises version,
// canonicalisation and complete routing checks on the actual public inventory.
func TestPublishedV2ReleaseManifestRejectsContractTampering(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*kernel.Bundle)
	}{
		{"schema-downgrade", func(b *kernel.Bundle) { b.SchemaVersion = kernel.BundleSchemaVersion }},
		{"unknown-schema", func(b *kernel.Bundle) { b.SchemaVersion = 4 }},
		{"missing-ordered-policy", func(b *kernel.Bundle) { b.DTBSelectionProvenance.StubbleSelection = nil }},
		{"missing-payload", func(b *kernel.Bundle) {
			b.DTBSelectionProvenance.StubbleSelection.PayloadOrder = b.DTBSelectionProvenance.StubbleSelection.PayloadOrder[1:]
		}},
		{"duplicate-payload", func(b *kernel.Bundle) {
			b.DTBSelectionProvenance.StubbleSelection.PayloadOrder[0] = b.DTBSelectionProvenance.StubbleSelection.PayloadOrder[1]
		}},
		{"invalid-hwid", func(b *kernel.Bundle) { b.DTBSelectionProvenance.StubbleSelection.HWIDRecords[0].HWID = "invalid" }},
		{"missing-selection-evidence", func(b *kernel.Bundle) { b.DTBSelectionProvenance.Selections = b.DTBSelectionProvenance.Selections[1:] }},
		{"unsafe-dtb-path", func(b *kernel.Bundle) { b.DeviceTrees[0].Path = "../outside.dtb" }},
		{"noncanonical-inventory", func(b *kernel.Bundle) { b.DeviceTrees[0], b.DeviceTrees[1] = b.DeviceTrees[1], b.DeviceTrees[0] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, changed := publishedV2ReleaseMetadata(t)
			test.mutate(&changed)
			data, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodePublishedV2ReleaseMetadata(t, data); err == nil {
				t.Fatal("tampered published delivery metadata was accepted")
			}
		})
	}
}

// TestPublishedV2ReleaseManifestRetainsStrictSyntaxAndByteBound keeps the
// restored reader bounded and strict even for a genuine larger release asset.
func TestPublishedV2ReleaseManifestRetainsStrictSyntaxAndByteBound(t *testing.T) {
	data, expected := publishedV2ReleaseMetadata(t)
	if maximumReleaseBundleBytes != 1<<20 {
		t.Fatal("shared manifest bound changed instead of restoring compatibility")
	}
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"unknown-field", bytes.Replace(data, []byte("{\n"), []byte("{\n  \"unexpected\": true,\n"), 1)},
		{"trailing-json", append(append([]byte(nil), data...), []byte("{}\n")...)},
		{"malformed-json", []byte("{invalid}")},
		{"limit-plus-one", append(append([]byte(nil), data...), bytes.Repeat([]byte(" "), int(maximumReleaseBundleBytes)-len(data)+1)...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodePublishedV2ReleaseMetadata(t, test.data); err == nil {
				t.Fatal("invalid or oversized published metadata was accepted")
			}
		})
	}
	padded := append(append([]byte(nil), data...), bytes.Repeat([]byte(" "), int(maximumReleaseBundleBytes)-len(data))...)
	actual, err := decodePublishedV2ReleaseMetadata(t, padded)
	if err != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatalf("metadata at the existing byte bound was rejected or changed: %v", err)
	}
}

// TestPublishedV2DownloadRejectsByteTamperingBeforePackageAcquisition exercises
// the ordinary release client's digest gate using only a loopback HTTP fixture.
// Valid JSON whitespace changes are still rejected against the published hash.
func TestPublishedV2DownloadRejectsByteTamperingBeforePackageAcquisition(t *testing.T) {
	data, bundle := publishedV2ReleaseMetadata(t)
	changed := append(append([]byte(nil), data...), ' ')
	checksums := fmt.Sprintf("%s  %s\n", publishedV2ReleaseManifestSHA256, releaseBundleManifestName)
	for _, pkg := range bundle.Packages {
		checksums += fmt.Sprintf("%s  %s\n", pkg.SHA256, pkg.Name)
	}
	checksumDigest := sha256.Sum256([]byte(checksums))
	var packageRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "https://" + r.Host
		switch r.URL.Path {
		case "/repos/fixture/releases/tags/" + bundle.Release:
			assets := []Asset{
				{Name: "SHA256SUMS", DownloadURL: base + "/SHA256SUMS", Digest: "sha256:" + hex.EncodeToString(checksumDigest[:]), Size: int64(len(checksums))},
				{Name: releaseBundleManifestName, DownloadURL: base + "/" + releaseBundleManifestName, Digest: "sha256:" + publishedV2ReleaseManifestSHA256, Size: int64(len(data))},
			}
			for _, pkg := range bundle.Packages {
				assets = append(assets, Asset{Name: pkg.Name, DownloadURL: base + "/packages/" + pkg.Name, Digest: "sha256:" + pkg.SHA256, Size: pkg.Size})
			}
			if err := json.NewEncoder(w).Encode(Release{TagName: bundle.Release, Assets: assets}); err != nil {
				t.Error(err)
			}
		case "/SHA256SUMS":
			_, _ = w.Write([]byte(checksums))
		case "/" + releaseBundleManifestName:
			_, _ = w.Write(changed)
		default:
			packageRequests.Add(1)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.APIBaseURL = server.URL
	client.Token = ""
	_, err := client.DownloadBundle(context.Background(), "fixture", bundle.Release, t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") || packageRequests.Load() != 0 {
		t.Fatalf("tampered metadata reached package acquisition: requests=%d error=%v", packageRequests.Load(), err)
	}
}
