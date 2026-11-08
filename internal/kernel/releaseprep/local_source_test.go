package releaseprep

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/kernel/build"
	kernelrelease "github.com/ooaklee/lexr.sh/internal/kernel/release"
)

// TestLocalSourcePrepareRoundTrip exercises the complete local producer,
// public projection, atomic preparation and public validation contract.
func TestLocalSourcePrepareRoundTrip(t *testing.T) {
	fixture := localReleaseFixture(t)
	original := readProvenance(t, fixture.Build)
	manager := New()
	dry := fixture.Request
	dry.DryRun = true
	review, err := manager.Prepare(t.Context(), dry)
	if err != nil || review.Published || !review.Plan.DryRun {
		t.Fatalf("dry run: %+v, %v", review, err)
	}
	assertAbsent(t, fixture.Output)
	receipt, err := manager.Prepare(t.Context(), fixture.Request)
	if err != nil || !receipt.Published || !receipt.Durable {
		t.Fatalf("prepare: %+v, %v", receipt, err)
	}
	manifest, err := manager.Validate(t.Context(), fixture.Output)
	if err != nil || manifest.SchemaVersion != LocalSourceSchemaVersion || !reflect.DeepEqual(manifest.Source, publicProvenance(original)) {
		t.Fatalf("source projection: %+v, %v", manifest.Source, err)
	}
	if manifest.Source.GitURL != "" || manifest.Source.GitRef != "" || manifest.Source.RefKind != "" {
		t.Fatal("local producer was labelled as an HTTPS fetch")
	}
	bundle := readBundle(t, fixture.Output)
	if bundle.SchemaVersion != kernel.OrderedBundleSchemaVersion || bundle.Repository != "" || len(bundle.Packages) != 4 {
		t.Fatalf("consumer contract changed: %+v", bundle)
	}
	for _, pkg := range bundle.Packages {
		if pkg.Path != "" || !bytes.Equal(mustReadLocal(t, filepath.Join(fixture.Build, pkg.Name)), mustReadLocal(t, filepath.Join(fixture.Output, pkg.Name))) {
			t.Fatalf("package bytes or path projection changed: %+v", pkg)
		}
	}
	archiveAssets := 0
	for _, asset := range manifest.Assets {
		if asset.Name == build.LocalSourceArchiveName {
			archiveAssets++
		}
	}
	if archiveAssets != 1 || !bytes.Equal(mustReadLocal(t, fixture.Source), mustReadLocal(t, filepath.Join(fixture.Output, build.LocalSourceArchiveName))) {
		t.Fatal("retained source must be copied exactly once without changing bytes")
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{fixture.Root, original.WorkVolume, "\"git_url\"", "\"git_ref\"", "\"ref_kind\""} {
		if bytes.Contains(encoded, []byte(private)) {
			t.Fatalf("local public receipt leaked %q", private)
		}
	}
	if !strings.Contains(string(mustReadLocal(t, filepath.Join(fixture.Output, ReleaseNotesFileName))), "Producer source kind: local-git-commit") {
		t.Fatal("notes omit the producer source kind")
	}
}

// TestLocalSourcePlanRejectsBrokenBinding rejects missing, changed, mixed or
// substituted producer source inputs before publishing any output.
func TestLocalSourcePlanRejectsBrokenBinding(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, releaseFixture, *build.Provenance)
	}{
		{"missing archive", func(t *testing.T, f releaseFixture, _ *build.Provenance) { mustRemoveLocal(t, f.Source) }},
		{"archive digest", func(t *testing.T, f releaseFixture, _ *build.Provenance) {
			mustWriteFile(t, f.Source, []byte("different bytes"))
		}},
		{"declared digest", func(_ *testing.T, _ releaseFixture, p *build.Provenance) {
			p.SourceArchiveSHA256 = strings.Repeat("a", 64)
		}},
		{"declared size", func(_ *testing.T, _ releaseFixture, p *build.Provenance) { p.SourceArchiveSize++ }},
		{"declared count", func(_ *testing.T, _ releaseFixture, p *build.Provenance) { p.SourceFileCount++ }},
		{"local revision", func(_ *testing.T, _ releaseFixture, p *build.Provenance) {
			p.LocalSourceRevision = strings.Repeat("e", 40)
		}},
		{"archive leaf", func(_ *testing.T, _ releaseFixture, p *build.Provenance) {
			p.SourceArchiveName = "../lexr-kernel-source.tar"
		}},
		{"wrong kind", func(_ *testing.T, _ releaseFixture, p *build.Provenance) { p.SourceKind = "other" }},
		{"mixed URL", func(_ *testing.T, _ releaseFixture, p *build.Provenance) { p.GitURL = fixtureGitURL }},
		{"mixed ref", func(_ *testing.T, _ releaseFixture, p *build.Provenance) { p.GitRef = "main" }},
		{"mixed ref kind", func(_ *testing.T, _ releaseFixture, p *build.Provenance) { p.RefKind = "branch" }},
		{"extra builder file", func(t *testing.T, f releaseFixture, _ *build.Provenance) {
			mustWriteFile(t, filepath.Join(f.Build, "extra.tar"), []byte("extra"))
		}},
		{"archive symlink", func(t *testing.T, f releaseFixture, _ *build.Provenance) {
			mustRemoveLocal(t, f.Source)
			if err := os.Symlink(f.Licence, f.Source); err != nil {
				t.Fatal(err)
			}
		}},
		{"archive directory", func(t *testing.T, f releaseFixture, _ *build.Provenance) {
			mustRemoveLocal(t, f.Source)
			if err := os.Mkdir(f.Source, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := localReleaseFixture(t)
			p := readProvenance(t, f.Build)
			tc.mutate(t, f, &p)
			mustWriteJSON(t, filepath.Join(f.Build, BuildProvenanceFileName), p)
			if _, err := New().Plan(t.Context(), f.Request); err == nil {
				t.Fatal("invalid local source binding accepted")
			}
			assertAbsent(t, f.Output)
		})
	}
	for _, mode := range []string{"omitted retained source", "same named copy", "duplicate retained source"} {
		t.Run(mode, func(t *testing.T) {
			f := localReleaseFixture(t)
			switch mode {
			case "omitted retained source":
				f.Request.SourceAssets = []string{filepath.Join(f.Root, "unrelated.tar")}
				mustWriteFile(t, f.Request.SourceAssets[0], []byte("unrelated"))
			case "same named copy":
				f.Request.SourceAssets = []string{filepath.Join(f.Root, build.LocalSourceArchiveName)}
				mustWriteFile(t, f.Request.SourceAssets[0], mustReadLocal(t, f.Source))
			case "duplicate retained source":
				f.Request.SourceAssets = append(f.Request.SourceAssets, f.Source)
			}
			if _, err := New().Plan(t.Context(), f.Request); err == nil {
				t.Fatal("unbound supplementary source accepted")
			}
		})
	}
}

// TestLocalSourcePublicValidationRejectsDrift repeats the source binding at the
// public boundary, including attacks with internally consistent new checksums.
func TestLocalSourcePublicValidationRejectsDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"schema downgrade", func(m *Manifest) { m.SchemaVersion = SchemaVersion }},
		{"unknown schema", func(m *Manifest) { m.SchemaVersion = 6 }},
		{"wrong kind", func(m *Manifest) { m.Source.SourceKind = build.SourceKindHTTPSGit }},
		{"mixed URL", func(m *Manifest) { m.Source.GitURL = fixtureGitURL }},
		{"local revision", func(m *Manifest) { m.Source.LocalSourceRevision = strings.Repeat("e", 40) }},
		{"commit comment", func(m *Manifest) {
			m.Source.Revision = strings.Repeat("e", 40)
			m.Source.LocalSourceRevision = m.Source.Revision
		}},
		{"archive digest", func(m *Manifest) { m.Source.SourceArchiveSHA256 = strings.Repeat("e", 64) }},
		{"archive size", func(m *Manifest) { m.Source.SourceArchiveSize++ }},
		{"archive count", func(m *Manifest) { m.Source.SourceFileCount++ }},
		{"archive leaf", func(m *Manifest) { m.Source.SourceArchiveName = "other.tar" }},
		{"source omitted", func(m *Manifest) { m.Assets = withoutKind(m.Assets, AssetSource) }},
		{"source kind", func(m *Manifest) {
			for i := range m.Assets {
				if m.Assets[i].Kind == AssetSource {
					m.Assets[i].Kind = AssetLicence
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := localReleaseFixture(t)
			r, err := New().Prepare(t.Context(), f.Request)
			if err != nil {
				t.Fatal(err)
			}
			m := r.Plan.Manifest
			tc.mutate(&m)
			mustWriteJSON(t, filepath.Join(f.Output, ReleaseManifestFileName), m)
			rewriteChecksums(t, f.Output)
			if _, err := New().Validate(t.Context(), f.Output); err == nil {
				t.Fatal("public source drift accepted")
			}
		})
	}
}

// TestLocalSourceRevalidationRejectsArchiveMutation prevents a reviewed source
// archive from changing before release staging begins.
func TestLocalSourceRevalidationRejectsArchiveMutation(t *testing.T) {
	f := localReleaseFixture(t)
	plan, err := New().Plan(t.Context(), f.Request)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, f.Source, []byte("changed after review"))
	if err := revalidatePlan(t.Context(), plan); err == nil {
		t.Fatal("changed archive survived revalidation")
	}
	assertAbsent(t, f.Output)
}

// TestHTTPSReleaseRetainsSchemaFour prevents a new local source contract from
// silently changing existing generated HTTPS releases or accepting mixed fields.
func TestHTTPSReleaseRetainsSchemaFour(t *testing.T) {
	f := newReleaseFixture(t, true)
	r, err := New().Prepare(t.Context(), f.Request)
	if err != nil || r.Plan.Manifest.SchemaVersion != 4 {
		t.Fatalf("HTTPS schema: %+v, %v", r.Plan.Manifest, err)
	}
	if _, err := New().Validate(t.Context(), f.Output); err != nil {
		t.Fatal(err)
	}
	m := r.Plan.Manifest
	m.SchemaVersion = LocalSourceSchemaVersion
	if err := validateManifest(m); err == nil {
		t.Fatal("HTTPS source accepted under local schema")
	}
	m = r.Plan.Manifest
	m.Source.SourceArchiveName = build.LocalSourceArchiveName
	if err := validateManifest(m); err == nil {
		t.Fatal("HTTPS source accepted mixed local field")
	}
}

// TestLocalSourceReleaseDownloadsWithUnchangedBundleConsumer serves the actual
// prepared bytes through the existing release client, not a new lenient reader.
func TestLocalSourceReleaseDownloadsWithUnchangedBundleConsumer(t *testing.T) {
	f := localReleaseFixture(t)
	r, err := New().Prepare(t.Context(), f.Request)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(f.Output)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/repos/fixture/releases/tags/"+fixtureRelease {
			var assets []kernelrelease.Asset
			for _, entry := range entries {
				assets = append(assets, kernelrelease.Asset{Name: entry.Name(), DownloadURL: "https://" + request.Host + "/" + entry.Name()})
			}
			if err := json.NewEncoder(w).Encode(kernelrelease.Release{TagName: fixtureRelease, Assets: assets}); err != nil {
				t.Error(err)
			}
			return
		}
		http.ServeFile(w, request, filepath.Join(f.Output, filepath.Base(request.URL.Path)))
	}))
	defer server.Close()
	client := kernelrelease.NewClient(server.Client())
	client.APIBaseURL, client.Token = server.URL, ""
	downloaded, err := client.DownloadBundle(context.Background(), "fixture", fixtureRelease, t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if downloaded.SchemaVersion != kernel.OrderedBundleSchemaVersion || len(downloaded.Packages) != 4 ||
		!reflect.DeepEqual(downloaded.DTBSelectionProvenance, r.Plan.Bundle.DTBSelectionProvenance) || !reflect.DeepEqual(downloaded.DeviceTrees, r.Plan.Bundle.DeviceTrees) {
		t.Fatal("unchanged release consumer lost the ordered bundle contract")
	}
	for _, pkg := range downloaded.Packages {
		if !pkg.Verified || !bytes.Equal(mustReadLocal(t, pkg.Path), mustReadLocal(t, filepath.Join(f.Build, pkg.Name))) {
			t.Fatal("consumer changed package bytes")
		}
	}
}

// localReleaseFixture creates a four-package ordered build with a retained
// raw Git archive and matching local producer provenance.
func localReleaseFixture(t *testing.T) releaseFixture {
	t.Helper()
	f := newReleaseFixture(t, true)
	p := readProvenance(t, f.Build)
	p.SourceKind, p.GitURL, p.GitRef, p.RefKind = build.SourceKindLocalGitCommit, "", "", ""
	p.LocalSourceRevision, p.SourceArchiveName, p.SourceFileCount = p.Revision, build.LocalSourceArchiveName, 2
	f.Source = filepath.Join(f.Build, build.LocalSourceArchiveName)
	var data bytes.Buffer
	archive := tar.NewWriter(&data)
	for _, h := range []*tar.Header{
		{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": p.Revision}},
		{Name: "COPYING", Typeflag: tar.TypeReg, Mode: 0o664, Size: 6},
		{Name: "source-link", Typeflag: tar.TypeSymlink, Mode: 0o777, Linkname: "COPYING"},
	} {
		if err := archive.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size != 0 {
			if _, err := archive.Write([]byte("source")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, f.Source, data.Bytes())
	p.SourceArchiveSHA256, p.SourceArchiveSize = digestBytes(data.Bytes()), int64(data.Len())
	bundle := readBundle(t, f.Build)
	bundle.Repository = ""
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
	bundle.SchemaVersion = kernel.OrderedBundleSchemaVersion
	p.DTBSelectionProvenance = bundle.DTBSelectionProvenance
	mustWriteJSON(t, filepath.Join(f.Build, BundleFileName), bundle)
	mustWriteJSON(t, filepath.Join(f.Build, BuildProvenanceFileName), p)
	f.Request.SourceAssets = []string{f.Source}
	return f
}

// mustReadLocal reads one fixture file or fails the current test.
func mustReadLocal(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// mustRemoveLocal removes one fixture file or fails the current test.
func mustRemoveLocal(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// TestLocalSourceArchiveNamespaceRejectsUnsafeMembers checks the non-extracting
// archive boundary with otherwise matching producer digests and byte lengths.
func TestLocalSourceArchiveNamespaceRejectsUnsafeMembers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers []*tar.Header
	}{
		{"path escape", []*tar.Header{{Name: "../outside", Typeflag: tar.TypeReg}}},
		{"Git metadata", []*tar.Header{{Name: ".Git/config", Typeflag: tar.TypeReg}}},
		{"duplicate", []*tar.Header{{Name: "COPYING", Typeflag: tar.TypeReg}, {Name: "COPYING", Typeflag: tar.TypeReg}}},
		{"hardlink", []*tar.Header{{Name: "linked", Typeflag: tar.TypeLink, Linkname: "COPYING"}}},
		{"escaping symlink", []*tar.Header{{Name: "linked", Typeflag: tar.TypeSymlink, Linkname: "../outside"}}},
		{"file ancestor", []*tar.Header{{Name: "parent", Typeflag: tar.TypeReg}, {Name: "parent/file", Typeflag: tar.TypeReg}}},
		{"late file ancestor", []*tar.Header{{Name: "parent/file", Typeflag: tar.TypeReg}, {Name: "parent", Typeflag: tar.TypeReg}}},
		{"missing Git comment", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := localReleaseFixture(t)
			p := readProvenance(t, f.Build)
			var data bytes.Buffer
			archive := tar.NewWriter(&data)
			if tc.name != "missing Git comment" {
				if err := archive.WriteHeader(&tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": p.Revision}}); err != nil {
					t.Fatal(err)
				}
			}
			for _, header := range tc.headers {
				if err := archive.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			mustWriteFile(t, f.Source, data.Bytes())
			p.SourceArchiveSHA256, p.SourceArchiveSize = digestBytes(data.Bytes()), int64(data.Len())
			mustWriteJSON(t, filepath.Join(f.Build, BuildProvenanceFileName), p)
			if _, err := New().Plan(t.Context(), f.Request); err == nil {
				t.Fatal("unsafe archive accepted under matching digest")
			}
		})
	}
}
