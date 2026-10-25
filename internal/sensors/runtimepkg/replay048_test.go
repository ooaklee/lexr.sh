package runtimepkg

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/build"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// TestMetadata048QualificationPackageOptIn composes an independently pinned
// fresh archive/source pair without executing payloads or claiming its producer
// ran. The coordinator must bind actual production evidence externally.
func TestMetadata048QualificationPackageOptIn(t *testing.T) {
	name := os.Getenv("LEXR_TEST_SENSOR_ARTIFACT_ARCHIVE_048")
	pin := os.Getenv("LEXR_TEST_SENSOR_ARTIFACT_SHA256_048")
	root := os.Getenv("LEXR_TEST_SENSOR_PACKAGE_OE_ROOT_048")
	output := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_OUTPUT_048")
	if name == "" && pin == "" && root == "" && output == "" {
		t.Skip("explicit metadata0.4.8 archive/source qualification opt-in required")
	}
	decodedPin, err := hex.DecodeString(pin)
	if name == "" || root == "" || err != nil || len(decodedPin) != 32 || hex.EncodeToString(decodedPin) != pin {
		t.Fatal("paired canonical archive SHA and source root required")
	}
	data := readRetainedRegular(t, name, build.MaximumArchiveBytes, pin)
	artifacts, err := build.DecodeArtifactArchive048(data, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !artifacts.IsMetadata048() {
		t.Fatal("new decoder did not retain sealed profile")
	}
	if _, err := build.DecodeArtifactArchive(data, 2); err == nil {
		t.Fatal("legacy artefact decoder accepted metadata0.4.8")
	}
	sources, err := source.Capture048(root)
	if err != nil {
		t.Fatal(err)
	}
	if !sources.IsMetadata048() {
		t.Fatal("new source profile was not sealed")
	}
	if _, err := source.FromEntries(sources.Entries()); err == nil {
		t.Fatal("legacy source decoder accepted metadata0.4.8")
	}
	declaration := readRetainedRegular(t, filepath.Join(root, "userspace/sensors/packaging/qualification-compatibility.json"),
		compatibility.MaxBytes, qualificationDeclarationSHA256)
	if _, err := Build(artifacts, sources, declaration); err == nil {
		t.Fatal("legacy package producer accepted metadata0.4.8")
	}
	pkg, err := Build048(artifacts, sources, declaration)
	if err != nil {
		t.Fatal(err)
	}
	raw, ref := pkg.Bytes(), pkg.Reference()
	if !pkg.IsMetadata048() || pkg.SourceLineage() != source.Metadata048SourceCommit ||
		len(pkg.Files()) != 9 || len(pkg.Links()) != 3 ||
		pkg.Assess(qualificationTarget()).Status != compatibility.Unverified {
		t.Fatal("metadata package closure or unknown support evidence changed")
	}
	if _, err := Decode(raw, ref); err == nil {
		t.Fatal("legacy package decoder accepted metadata0.4.8")
	}
	again, err := Decode048(raw, ref)
	if err != nil || !bytes.Equal(again.Bytes(), raw) ||
		!reflect.DeepEqual(again.Files(), pkg.Files()) {
		t.Fatalf("metadata package round trip differs: %v", err)
	}
	copied, err := source.FromEntries048(sources.Entries())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build048(artifacts, copied, declaration)
	if err != nil || !bytes.Equal(second.Bytes(), raw) {
		t.Fatal("new package composition is not deterministic")
	}
	outer, err := readArchive(raw, MaximumPackageBytes, maximumPayloadBytes, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range outer {
		if entry.Path == sourceFilename {
			inner, err := readArchive(entry.Data, maximumSourceBytes, 32<<20, 968)
			if err != nil || len(inner) != 968 {
				t.Fatal("new corresponding-source closure differs")
			}
		}
	}
	assertMetadata048MutationsRefuse(t, outer)
	// Accessor buffers remain mechanical copies, never mutable opaque authority.
	copiedFiles := pkg.Files()
	copiedFiles[0].Data[0] ^= 1
	copiedRaw := pkg.Bytes()
	copiedRaw[0] ^= 1
	if !bytes.Equal(pkg.Bytes(), raw) || reflect.DeepEqual(pkg.Files(), copiedFiles) {
		t.Fatal("metadata package exposed its internal buffers")
	}
	if output != "" {
		if !filepath.IsAbs(output) || filepath.Clean(output) != output {
			t.Fatal("output must be an explicit fresh canonical file")
		}
		f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(raw); err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("retain new package: %v %v", err, closeErr)
		}
		if !bytes.Equal(readRetainedRegular(t, output, MaximumPackageBytes, ref.SHA256), raw) {
			t.Fatal("retained new package differs")
		}
	}
	t.Logf("metadata0.4.8 static qualification package bytes=%d sha256=%s; no observed producer or execution inferred", ref.Bytes, ref.SHA256)
}

// assertMetadata048MutationsRefuse rechecks rebuilt checksums so inverse controls
// exercise the fixed inner contract rather than merely a stale outer digest.
func assertMetadata048MutationsRefuse(t *testing.T, original []archiveEntry) {
	t.Helper()
	for _, kind := range []string{"provenance", "payload", "declaration", "extra"} {
		entries := make([]archiveEntry, len(original))
		copy(entries, original)
		for i := range entries {
			entries[i].Data = bytes.Clone(entries[i].Data)
			switch {
			case kind == "provenance" && entries[i].Path == metadataFilename:
				entries[i].Data = bytes.ReplaceAll(entries[i].Data,
					[]byte("static_archive_verification"), []byte("observed_producer"))
			case kind == "payload" && entries[i].Path == payloadFilename:
				payload, err := readArchive(entries[i].Data, maximumPayloadBytes, 64<<20, 12)
				if err != nil {
					t.Fatal(err)
				}
				for j := range payload {
					if payload[j].Path == "lib/libssc.so.2" {
						payload[j].Data[0] ^= 1
					}
				}
				entries[i].Data, err = writeArchive(payload, maximumPayloadBytes)
				if err != nil {
					t.Fatal(err)
				}
			case kind == "declaration" && entries[i].Path == compatibility.Filename:
				entries[i].Data = append(entries[i].Data, ' ')
			}
		}
		if kind == "extra" {
			entries = append(entries, archiveEntry{Path: "extra", Data: []byte("x"), Mode: 0o644})
		}
		var checked []archiveEntry
		for _, e := range entries {
			if e.Path != "SHA256SUMS" {
				checked = append(checked, e)
			}
		}
		for i := range entries {
			if entries[i].Path == "SHA256SUMS" {
				entries[i].Data = checksumList(checked)
			}
		}
		raw, err := writeArchive(entries, MaximumPackageBytes)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Decode048(raw, reference(raw)); err == nil {
			t.Fatalf("metadata0.4.8 accepted rebuilt %s mutation", kind)
		}
	}
}
