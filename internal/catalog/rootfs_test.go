package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRootfsCatalogueContract keeps archive inputs distinct from bootable images
// and preserves the original version-2 accepted formats.
func TestRootfsCatalogueContract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(map[string]any, map[string]any)
		want   string
	}{
		{name: "v3 pinned rolling archive"},
		{name: "v2 rejects new format", mutate: func(doc, entry map[string]any) { doc["schema_version"] = 2 }, want: "requires schema_version 3"},
		{name: "wrong extension", mutate: func(doc, entry map[string]any) {
			entry["filename"] = "root.iso"
			entry["url"] = "https://example.test/root.iso"
		}, want: "must end in .tar.gz"},
		{name: "no pin", mutate: func(doc, entry map[string]any) { delete(entry, "checksum") }, want: "requires a pinned SHA-256 snapshot"},
		{name: "short sha256", mutate: func(doc, entry map[string]any) {
			entry["checksum"] = map[string]any{"algorithm": "sha256", "value": "deadbeef"}
		}, want: "checksum.value"},
		{name: "nonhex sha256", mutate: func(doc, entry map[string]any) {
			entry["checksum"] = map[string]any{"algorithm": "sha256", "value": strings.Repeat("z", 64)}
		}, want: "checksum.value"},
		{name: "md5 rejected", mutate: func(doc, entry map[string]any) {
			entry["checksum"] = map[string]any{"algorithm": "md5", "value": strings.Repeat("a", 32)}
		}, want: "requires a pinned SHA-256 snapshot"},
		{name: "unknown kind", mutate: func(doc, entry map[string]any) { entry["artifact_kind"] = "rootfs-tar" }, want: "artifact_kind"},
		{name: "iso adapter rejected", mutate: func(doc, entry map[string]any) {
			entry["adapter"] = "ubuntu-casper"
			entry["support_level"] = "implemented"
		}, want: "does not support artifact_kind"},
		{name: "http rejected", mutate: func(doc, entry map[string]any) { entry["url"] = "http://example.test/root.tar.gz" }, want: "must use https"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := decodeDocument(t, validCatalogJSON)
			doc["schema_version"] = 3
			entry := firstEntry(doc)
			entry["artifact_kind"] = "rootfs-tar-gz"
			entry["filename"] = "root.tar.gz"
			entry["url"] = "https://example.test/root.tar.gz"
			entry["adapter"] = "none"
			entry["support_level"] = "catalog-only"
			entry["mutable"] = true
			if test.mutate != nil {
				test.mutate(doc, entry)
			}
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadBytes(data)
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("error = %v, want %q", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := loaded.Get("zulu-image")
			if loaded.SchemaVersion != 3 || got.ArtifactKind != ArtifactKindRootfsTarGZ || !got.Mutable || got.Checksum == nil {
				t.Fatalf("lost snapshot metadata: %#v", got)
			}
			for _, adapter := range []Adapter{AdapterNone, AdapterUbuntuCasper, AdapterElementaryCasper, AdapterFedoraLive} {
				if AdapterSupportsArtifact(adapter, got.ArtifactKind) {
					t.Fatalf("%s claims rootfs support before a live adapter exists", adapter)
				}
			}
		})
	}
}

// TestVersionThreeRetainsISOCatalogueBehaviour proves an existing v2 document
// accepts the version upgrade without changing its entries or pin policy.
func TestVersionThreeRetainsISOCatalogueBehaviour(t *testing.T) {
	t.Parallel()
	for _, version := range []int{LegacySchemaVersion, CurrentSchemaVersion} {
		doc := decodeDocument(t, validCatalogJSON)
		doc["schema_version"] = version
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadBytes(data)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.SchemaVersion != version || loaded.Len() != 2 {
			t.Fatalf("schema %d did not preserve existing catalogue", version)
		}
	}
}
