package runtimepkg

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/build"
	"github.com/ooaklee/lexr.sh/internal/sensors/packageplan"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// retainedArchiveSHA256 binds the previously accepted development archive used
// by the explicit read-only replay, not a new native producer invocation.
const retainedArchiveSHA256 = "3eba709791fa4ac33dfaa27202eb3a8032956fae832a44c50fb3bfaefef5ddf6"

// readRetainedRegular bounds an existing regular-file read and rechecks inode,
// size, modification time and complete digest before returning copied bytes.
func readRetainedRegular(t *testing.T, name string, maximum int64, wantSHA string) []byte {
	t.Helper()
	before, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maximum {
		t.Fatal("retained input is not a bounded nonempty regular file")
	}
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != before.Size() || !opened.ModTime().Equal(before.ModTime()) {
		t.Fatal("retained input changed before read")
	}
	data, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != opened.Size() || int64(len(data)) > maximum || !os.SameFile(opened, after) || !os.SameFile(opened, current) || !current.Mode().IsRegular() || after.Size() != opened.Size() || current.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) || !current.ModTime().Equal(opened.ModTime()) || digest(data) != wantSHA {
		t.Fatal("retained input identity or exact digest differs")
	}
	return data
}

// TestRetainedQualificationPackageOptIn replays only unchanged authenticated
// development bytes and source. It does not build, publish, extract an archive,
// execute an ARM64 binary, install into a target root or contact a device.
// Optional output is one explicit absent file, created exclusively, never an
// overwrite or an extraction destination.
func TestRetainedQualificationPackageOptIn(t *testing.T) {
	archivePath := os.Getenv("LEXR_TEST_SENSOR_ARTIFACT_ARCHIVE")
	archiveSHA := os.Getenv("LEXR_TEST_SENSOR_ARTIFACT_SHA256")
	oeRoot := os.Getenv("LEXR_TEST_SENSOR_PACKAGE_OE_ROOT")
	output := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_OUTPUT")
	if archivePath == "" && archiveSHA == "" && oeRoot == "" && output == "" {
		t.Skip("explicit retained archive/source opt-in required")
	}
	if archivePath == "" || archiveSHA != retainedArchiveSHA256 || oeRoot == "" {
		t.Fatal("approved retained archive SHA and explicit OE source root are required together")
	}
	data := readRetainedRegular(t, archivePath, build.MaximumArchiveBytes+(64<<10), retainedArchiveSHA256)
	artifacts, err := build.DecodeDockerCopy(data, 2)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := source.Capture(oeRoot)
	if err != nil {
		t.Fatal(err)
	}
	decl := readRetainedRegular(t, filepath.Join(oeRoot, "userspace", "sensors", "packaging", "qualification-compatibility.json"), compatibility.MaxBytes, qualificationDeclarationSHA256)
	if !bytes.Equal(decl, []byte(qualificationFixture)) {
		t.Fatal("OE qualification fixture differs from independently reviewed bytes")
	}
	if got := sources.Composition(); got.SourceFileCount != 949 || got.SourceSymlinkCount != 6 || len(got.Metadata()) != 4 {
		t.Fatal("approved source composition is incomplete")
	}
	entries := sources.Entries()
	sourcesCopy, err := source.FromEntries(entries)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sources.Composition(), sourcesCopy.Composition()) {
		t.Fatal("pure source verifier changed composition")
	}
	if _, err := Build(artifacts, sources, append(decl, ' ')); err == nil {
		t.Fatal("Build accepted nonpinned qualification declaration")
	}
	pkg, err := Build(artifacts, sources, decl)
	if err != nil {
		t.Fatal(err)
	}
	raw := pkg.Bytes()
	ref := pkg.Reference()
	if ref != reference(raw) || len(pkg.Files()) != 9 || len(pkg.Links()) != 3 {
		t.Fatal("qualification package is incomplete")
	}
	decoded, err := Decode(raw, ref)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.IsMetadata048() || pkg.SourceLineage() != build.SourceCommit {
		t.Fatal("legacy package acquired successor profile authority")
	}
	if _, err := Decode048(raw, ref); err == nil {
		t.Fatal("successor package decoder accepted legacy0.4.7")
	}
	if _, err := Build048(artifacts, sources, decl); err == nil {
		t.Fatal("successor package producer accepted legacy0.4.7")
	}
	if !bytes.Equal(raw, decoded.Bytes()) || !reflect.DeepEqual(pkg.Files(), decoded.Files()) || !reflect.DeepEqual(pkg.Links(), decoded.Links()) {
		t.Fatal("qualification package did not decode losslessly")
	}
	second, err := Build(artifacts, sourcesCopy, decl)
	if err != nil || !bytes.Equal(raw, second.Bytes()) {
		t.Fatalf("package composition is not deterministic: %v", err)
	}
	if pkg.Assess(qualificationTarget()).Status != compatibility.Unverified {
		t.Fatal("synthetic qualification target acquired support evidence")
	}
	if _, err := build.DecodeArtifactArchive(raw, 2); err == nil {
		t.Fatal("old development consumer accepted qualification package as a development archive")
	}
	assertDefensivePackage(t, pkg, raw, ref)
	assertPackageMutationRefusals(t, raw)
	// Mutating copied source cannot preserve authenticated composition authority.
	for n := range entries {
		if entries[n].Path == "libssc/source/LICENSE" {
			entries[n].Data[0] ^= 1
		}
	}
	if _, err := source.FromEntries(entries); err == nil {
		t.Fatal("corresponding licence corruption accepted")
	}
	for _, file := range pkg.Files() {
		if !safePath(file.Path) || filepath.IsAbs(file.Path) || file.Path == packageplan.PrivatePrefix {
			t.Fatal("payload path is not relative to fixed private prefix")
		}
	}
	if output != "" {
		if !filepath.IsAbs(output) {
			t.Fatal("explicit package output must be an absolute fresh filename")
		}
		f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(raw); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err = f.Sync(); err != nil {
			f.Close()
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
		written := readRetainedRegular(t, output, MaximumPackageBytes, ref.SHA256)
		if !bytes.Equal(raw, written) {
			t.Fatal("new qualification package output differs")
		}
	}
	t.Logf("qualification-only package bytes=%d sha256=%s; retained archive replay; no native rebuild, execution, public installation or hardware qualification", ref.Bytes, ref.SHA256)
}

// assertDefensivePackage checks that neither caller buffers nor accessor copies
// can mutate a verified package's retained bytes, payload or declaration.
func assertDefensivePackage(t *testing.T, pkg *VerifiedPackage, raw []byte, ref Reference) {
	t.Helper()
	retainedFiles, retainedLinks := pkg.Files(), pkg.Links()
	copyBytes := pkg.Bytes()
	copyBytes[0] ^= 1
	copyFiles := pkg.Files()
	copyFiles[0].Path = "changed"
	copyFiles[0].Data[0] ^= 1
	copyLinks := pkg.Links()
	copyLinks[0].Target = "changed"
	copyDecl := pkg.Declaration()
	copyDecl[0] ^= 1
	if !bytes.Equal(raw, pkg.Bytes()) || pkg.Reference() != ref || !reflect.DeepEqual(pkg.Files(), retainedFiles) || !reflect.DeepEqual(pkg.Links(), retainedLinks) || !bytes.Equal(pkg.Declaration(), []byte(qualificationFixture)) {
		t.Fatal("package accessor exposed retained mutable bytes")
	}
	decoded, err := Decode(raw, ref)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 1
	if !bytes.Equal(decoded.Bytes(), pkg.Bytes()) {
		t.Fatal("Decode retained caller-owned archive bytes")
	}
	raw[0] ^= 1
}

// assertPackageMutationRefusals recomputes outer digests after controlled changes
// so failures prove strict inner closures rather than only a stale checksum.
func assertPackageMutationRefusals(t *testing.T, raw []byte) {
	t.Helper()
	for _, test := range []struct {
		name, asset string
		mutate      func([]archiveEntry) []archiveEntry
	}{
		{"extra-outer", "", func(e []archiveEntry) []archiveEntry {
			return append(e, archiveEntry{Path: "unexpected", Data: []byte("x"), Mode: 0o644})
		}},
		{"missing-outer", "", func(e []archiveEntry) []archiveEntry { return e[:len(e)-1] }},
		{"metadata", "", func(e []archiveEntry) []archiveEntry {
			for n := range e {
				if e[n].Path == metadataFilename {
					e[n].Data = append(e[n].Data, ' ')
				}
			}
			return e
		}},
		{"declaration", "", func(e []archiveEntry) []archiveEntry {
			for n := range e {
				if e[n].Path == compatibility.Filename {
					e[n].Data[0] ^= 1
				}
			}
			return e
		}},
		{"licence", payloadFilename, func(e []archiveEntry) []archiveEntry {
			for n := range e {
				if e[n].Path == "share/licenses/LICENSE.libssc" {
					e[n].Data[0] ^= 1
				}
			}
			return e
		}},
		{"ELF", payloadFilename, func(e []archiveEntry) []archiveEntry {
			for n := range e {
				if e[n].Path == "bin/ssccli" {
					e[n].Data[0] ^= 1
				}
			}
			return e
		}},
		{"alias", payloadFilename, func(e []archiveEntry) []archiveEntry {
			for n := range e {
				if e[n].Path == "lib/libssc.so" {
					e[n].LinkTarget = "other.so"
				}
			}
			return e
		}},
		{"payload-mode", payloadFilename, func(e []archiveEntry) []archiveEntry {
			for n := range e {
				if e[n].Path == "bin/ssccli" {
					e[n].Mode = 0o644
				}
			}
			return e
		}},
		{"extra-payload", payloadFilename, func(e []archiveEntry) []archiveEntry {
			return append(e, archiveEntry{Path: "bin/launcher", Data: []byte("#!/bin/sh\n"), Mode: 0o755})
		}},
		{"source-bytes", sourceFilename, func(e []archiveEntry) []archiveEntry {
			for n := range e {
				if e[n].Path == "libssc/source/LICENSE" {
					e[n].Data[0] ^= 1
				}
			}
			return e
		}},
		{"source-link", sourceFilename, func(e []archiveEntry) []archiveEntry {
			for n := range e {
				if e[n].LinkTarget != "" {
					e[n].LinkTarget = "other"
					break
				}
			}
			return e
		}},
		{"recipe", sourceFilename, func(e []archiveEntry) []archiveEntry {
			for n := range e {
				if e[n].Path == "recipes/offline-source-build.sh" {
					e[n].Data[0] ^= 1
				}
			}
			return e
		}},
		{"source-mode", sourceFilename, func(e []archiveEntry) []archiveEntry {
			for n := range e {
				if e[n].Path == "libssc/source/LICENSE" {
					e[n].Mode = 0o755
				}
			}
			return e
		}},
		{"missing-source", sourceFilename, func(e []archiveEntry) []archiveEntry { return e[:len(e)-1] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			outer, err := readArchive(raw, MaximumPackageBytes, maximumPayloadBytes, 5)
			if err != nil {
				t.Fatal(err)
			}
			if test.asset == "" {
				outer = test.mutate(outer)
			} else {
				for n := range outer {
					if outer[n].Path == test.asset {
						max, count := maximumPayloadBytes, 12
						if test.asset == sourceFilename {
							max, count = maximumSourceBytes, 962
						}
						inner, err := readArchive(outer[n].Data, max, 64<<20, count)
						if err != nil {
							t.Fatal(err)
						}
						outer[n].Data, err = writeArchive(test.mutate(inner), max)
						if err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			var checksummed []archiveEntry
			for _, e := range outer {
				if e.Path != "SHA256SUMS" {
					checksummed = append(checksummed, e)
				}
			}
			for n := range outer {
				if outer[n].Path == "SHA256SUMS" {
					outer[n].Data = checksumList(checksummed)
				}
			}
			altered, err := writeArchive(outer, MaximumPackageBytes)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(altered, reference(altered)); err == nil {
				t.Fatal("rehashed altered package escaped strict source/payload/declaration closure")
			}
		})
	}
}
