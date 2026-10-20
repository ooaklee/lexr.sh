package source

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type fixture struct {
	root   string
	policy authority
}

func fixtureHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func newFixture(t *testing.T) fixture {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("regular-file source boundary intentionally unsupported on this platform")
	}
	f := fixture{root: t.TempDir(), policy: authority{commit: strings.Repeat("a", 40)}}
	for _, name := range []string{"libqmi", "libssc", "iio-sensor-proxy"} {
		inv := inventory{Schema: "oe-sensor-source-inventory-v1", Component: name, Status: "experimental-source-only", InventoryRoot: "source", Files: map[string]inventoryFile{}, Symlinks: map[string]string{}}
		members := map[string][]byte{"data.c": []byte(name + " source\n")}
		if name == "libqmi" {
			members["tools/codegen"] = []byte("#!/bin/sh\nexit 0\n")
			members["empty"] = []byte{}
			inv.Symlinks["data-link"] = "data.c"
		}
		for member, data := range members {
			mode, git := os.FileMode(0o644), "100644"
			if member == "tools/codegen" {
				mode, git = 0o755, "100755"
			}
			p := filepath.Join(f.root, sourcePrefix, name, "source", member)
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, data, mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(p, mode); err != nil {
				t.Fatal(err)
			}
			inv.Files[member] = inventoryFile{Bytes: int64(len(data)), SHA256: fixtureHash(data), GitMode: git}
		}
		for member, target := range inv.Symlinks {
			if err := os.Symlink(target, filepath.Join(f.root, sourcePrefix, name, "source", member)); err != nil {
				t.Fatal(err)
			}
		}
		data, err := json.MarshalIndent(inv, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, '\n')
		if err := os.WriteFile(filepath.Join(f.root, sourcePrefix, name, "SOURCE.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		f.policy.components = append(f.policy.components, metadataPin{name: name, sha256: fixtureHash(data), bytes: int64(len(data)), files: len(inv.Files), links: len(inv.Symlinks)})
	}
	checksums := []byte("Fixture checksum record is separately authenticated, not self-authority.\n")
	if err := os.WriteFile(filepath.Join(f.root, sourcePrefix, "SOURCE.sha256"), checksums, 0o644); err != nil {
		t.Fatal(err)
	}
	f.policy.checksums = metadataPin{name: "SOURCE.sha256", sha256: fixtureHash(checksums), bytes: int64(len(checksums))}
	if err := os.WriteFile(filepath.Join(f.root, sourcePrefix, "README.md"), []byte("not a build input\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixture) file(relative string) string { return filepath.Join(f.root, sourcePrefix, relative) }

func (f *fixture) changeInventory(t *testing.T, component string, change func(*inventory)) {
	t.Helper()
	p := f.file(component + "/SOURCE.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var inv inventory
	if err := json.Unmarshal(data, &inv); err != nil {
		t.Fatal(err)
	}
	change(&inv)
	data, err = json.Marshal(inv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range f.policy.components {
		if f.policy.components[i].name == component {
			f.policy.components[i].sha256 = fixtureHash(data)
			f.policy.components[i].bytes = int64(len(data))
			f.policy.components[i].files = len(inv.Files)
			f.policy.components[i].links = len(inv.Symlinks)
		}
	}
}

func TestVerifyFixtureWithoutGitAndDefensiveCopies(t *testing.T) {
	f := newFixture(t)
	if _, err := os.Stat(filepath.Join(f.root, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fixture unexpectedly has Git metadata")
	}
	s, err := capture(f.root, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	c := s.composition
	if c.SourceCommit != f.policy.commit || c.SourceFileCount != 5 || c.SourceSymlinkCount != 1 || len(c.Metadata()) != 4 || len(c.SourceFiles()) != 5 || len(c.Symlinks()) != 1 {
		t.Fatalf("incomplete composition: %+v", c)
	}
	files, metadata, links := c.SourceFiles(), c.Metadata(), c.Symlinks()
	files[0].Path = "changed"
	metadata[0].SHA256 = "changed"
	links[0].Target = "changed"
	if c.SourceFiles()[0].Path == "changed" || c.Metadata()[0].SHA256 == "changed" || c.Symlinks()[0].Target == "changed" {
		t.Fatal("caller mutated private composition slices")
	}
	if _, err := Verify(f.root); err == nil {
		t.Fatal("public verifier accepted caller-created metadata instead of compiled authority")
	}
}

func TestMetadataDigestPrecedesParsing(t *testing.T) {
	f := newFixture(t)
	member := f.file("libqmi/SOURCE.json")
	data, err := os.ReadFile(member)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the approved length while making the JSON invalid. A parse error
	// would not prove authentication happened first; require the digest refusal.
	data[0] = '!'
	if err := os.WriteFile(member, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := capture(f.root, f.policy); err == nil || !strings.Contains(err.Error(), "source byte identity differs") {
		t.Fatalf("metadata was not authenticated before parsing: %v", err)
	}
}

func TestVerifyRefusesChangedSourceSet(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testing.T, fixture)
	}{
		{"metadata-before-parse", func(t *testing.T, f fixture) {
			if err := os.WriteFile(f.file("libqmi/SOURCE.json"), []byte("not JSON"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"same-size-byte-tamper", func(t *testing.T, f fixture) {
			p := f.file("libqmi/source/data.c")
			b, _ := os.ReadFile(p)
			b[0] ^= 1
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing-file", func(t *testing.T, f fixture) {
			if err := os.Rename(f.file("libqmi/source/data.c"), filepath.Join(f.root, "held-data")); err != nil {
				t.Fatal(err)
			}
		}},
		{"extra-file", func(t *testing.T, f fixture) {
			if err := os.WriteFile(f.file("libqmi/source/extra"), []byte("extra"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"extra-empty-directory", func(t *testing.T, f fixture) {
			if err := os.Mkdir(f.file("libqmi/source/extra"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"executable-bit", func(t *testing.T, f fixture) {
			if err := os.Chmod(f.file("libqmi/source/tools/codegen"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"unexpected-executable-bit", func(t *testing.T, f fixture) {
			if err := os.Chmod(f.file("libssc/source/data.c"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"regular-becomes-link", func(t *testing.T, f fixture) {
			p := f.file("libqmi/source/data.c")
			if err := os.Rename(p, filepath.Join(f.root, "held-data")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(f.root, "held-data"), p); err != nil {
				t.Fatal(err)
			}
		}},
		{"link-escape", func(t *testing.T, f fixture) {
			p := f.file("libqmi/source/data-link")
			if err := os.Rename(p, filepath.Join(f.root, "held-link")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../../../../outside", p); err != nil {
				t.Fatal(err)
			}
		}},
		{"source-directory-link", func(t *testing.T, f fixture) {
			p := f.file("libqmi/source")
			if err := os.Rename(p, filepath.Join(f.root, "held-source")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(f.root, "held-source"), p); err != nil {
				t.Fatal(err)
			}
		}},
		{"hardlink", func(t *testing.T, f fixture) {
			if err := os.Link(f.file("libqmi/source/data.c"), filepath.Join(f.root, "hardlinked-input")); err != nil {
				t.Fatal(err)
			}
		}},
		{"special-mode", func(t *testing.T, f fixture) {
			if err := os.Chmod(f.file("libqmi/source/data.c"), 0o644|os.ModeSetuid); err != nil {
				t.Fatal(err)
			}
		}},
		{"checksum-tamper", func(t *testing.T, f fixture) {
			if err := os.WriteFile(f.file("SOURCE.sha256"), []byte("changed"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			f := newFixture(t)
			row.mutate(t, f)
			if _, err := capture(f.root, f.policy); err == nil {
				t.Fatal("changed source set was accepted")
			}
		})
	}
}

func TestInventoryRejectsUnsafeGeometry(t *testing.T) {
	for _, member := range []string{"../outside", "/absolute", "a/../outside", "a\\outside", "a//b", ".", "bad\nname"} {
		t.Run(member, func(t *testing.T) {
			f := newFixture(t)
			f.changeInventory(t, "libssc", func(inv *inventory) { inv.Files[member] = inventoryFile{SHA256: fixtureHash(nil), GitMode: "100644"} })
			if _, err := capture(f.root, f.policy); err == nil {
				t.Fatal("unsafe authenticated inventory path accepted")
			}
		})
	}
	for _, target := range []string{"/absolute", "../outside", "missing", "data-link"} {
		t.Run("target-"+target, func(t *testing.T) {
			f := newFixture(t)
			f.changeInventory(t, "libqmi", func(inv *inventory) { inv.Symlinks["data-link"] = target })
			if _, err := capture(f.root, f.policy); err == nil {
				t.Fatal("unsafe or non-regular symlink target accepted")
			}
		})
	}
	for _, modify := range []func(*inventory){
		func(inv *inventory) {
			inv.Files["data.c"] = inventoryFile{Bytes: -1, SHA256: fixtureHash(nil), GitMode: "100644"}
		},
		func(inv *inventory) {
			inv.Files["data.c"] = inventoryFile{Bytes: 1, SHA256: "not-a-hash", GitMode: "100644"}
		},
		func(inv *inventory) {
			inv.Files["data.c"] = inventoryFile{Bytes: 1, SHA256: fixtureHash(nil), GitMode: "100777"}
		},
		func(inv *inventory) {
			inv.Files["data.c/overlap"] = inventoryFile{SHA256: fixtureHash(nil), GitMode: "100644"}
		},
	} {
		f := newFixture(t)
		f.changeInventory(t, "libssc", modify)
		if _, err := capture(f.root, f.policy); err == nil {
			t.Fatal("invalid authenticated regular-file record accepted")
		}
	}
}

func TestStageExactSnapshotAndReusedDestination(t *testing.T) {
	f := newFixture(t)
	destination := filepath.Join(t.TempDir(), "snapshot")
	c, err := stage(f.root, destination, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	if c.SourceCommit != f.policy.commit {
		t.Fatal("approved lineage lost")
	}
	for _, entry := range append(c.SourceFiles(), c.Metadata()...) {
		p := filepath.Join(destination, entry.Path)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fixtureHash(b) != entry.SHA256 || int64(len(b)) != entry.Bytes || gitMode(info.Mode()) != entry.GitMode {
			t.Fatalf("snapshot differs: %s", entry.Path)
		}
	}
	for _, link := range c.Symlinks() {
		target, err := os.Readlink(filepath.Join(destination, link.Path))
		if err != nil || target != link.Target {
			t.Fatal("snapshot link differs")
		}
	}
	if _, err := os.Lstat(filepath.Join(destination, "README.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("untrusted integration document was staged")
	}
	before, _ := os.ReadFile(filepath.Join(destination, "SOURCE.sha256"))
	if _, err := stage(f.root, destination, f.policy); err == nil {
		t.Fatal("existing snapshot was reused")
	}
	after, _ := os.ReadFile(filepath.Join(destination, "SOURCE.sha256"))
	if !bytes.Equal(before, after) {
		t.Fatal("existing snapshot changed")
	}
}

func TestStageRejectsOverlapAndChangedInputs(t *testing.T) {
	f := newFixture(t)
	for _, destination := range []string{f.root, filepath.Join(f.root, "new-snapshot"), filepath.Dir(f.root)} {
		if _, err := stage(f.root, destination, f.policy); err == nil {
			t.Fatal("overlapping destination accepted")
		}
	}
	parent := t.TempDir()
	if err := os.Symlink(f.root, filepath.Join(parent, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := stage(f.root, filepath.Join(parent, "alias", "snapshot"), f.policy); err == nil {
		t.Fatal("symlink-mediated overlap accepted")
	}
	if _, err := capture(f.root, f.policy); err != nil {
		t.Fatal(err)
	}
	p := f.file("libssc/source/data.c")
	b, _ := os.ReadFile(p)
	b[0] ^= 1
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "new-snapshot")
	if _, err := stage(f.root, destination, f.policy); err == nil {
		t.Fatal("old verification allowed later changed source to stage")
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("destination created before fresh source verification")
	}
}

func TestStageRetainsPartialAndNeverOverwrites(t *testing.T) {
	f := newFixture(t)
	destination := filepath.Join(t.TempDir(), "snapshot")
	collided := ""
	f.policy.beforeCopy = func(root *os.Root, name string) error {
		if collided != "" {
			return nil
		}
		collided = name
		return root.WriteFile(name, []byte("retained sentinel"), 0o600)
	}
	if _, err := stage(f.root, destination, f.policy); err == nil || !strings.Contains(err.Error(), "partial snapshot retained") {
		t.Fatalf("partial failure not reported: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(destination, collided))
	if err != nil || string(b) != "retained sentinel" {
		t.Fatal("partial snapshot or collision sentinel was removed/overwritten")
	}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	bad := capturedFile{identity: File{Path: "member", SHA256: fixtureHash([]byte("expected")), Bytes: 8, GitMode: "100644"}, data: []byte("tampered")}
	if err := writeCaptured(root, bad); err == nil {
		t.Fatal("captured-byte mutation accepted")
	}
	if _, err := root.Lstat("member"); err != nil {
		t.Fatal("failed owned copy was removed")
	}
}

func TestStageRefusesDestinationAndParentReplacement(t *testing.T) {
	for _, replace := range []string{"destination", "parent"} {
		t.Run(replace, func(t *testing.T) {
			f := newFixture(t)
			container := t.TempDir()
			parent := filepath.Join(container, "parent")
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(parent, "snapshot")
			moved := false
			f.policy.beforeCopy = func(_ *os.Root, _ string) error {
				if moved {
					return nil
				}
				moved = true
				if replace == "parent" {
					if err := os.Rename(parent, filepath.Join(container, "held-parent")); err != nil {
						return err
					}
					if err := os.Mkdir(parent, 0o700); err != nil {
						return err
					}
				} else if err := os.Rename(destination, filepath.Join(parent, "held-snapshot")); err != nil {
					return err
				}
				if err := os.Mkdir(destination, 0o700); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(destination, "replacement-sentinel"), []byte("untouched replacement"), 0o600)
			}
			if _, err := stage(f.root, destination, f.policy); err == nil || !strings.Contains(err.Error(), "pathname changed") {
				t.Fatalf("pathname replacement was endorsed: %v", err)
			}
			b, err := os.ReadFile(filepath.Join(destination, "replacement-sentinel"))
			if err != nil || string(b) != "untouched replacement" {
				t.Fatal("replacement directory was modified or removed")
			}
		})
	}
}

func TestStageRefusesRepositoryReplacement(t *testing.T) {
	f := newFixture(t)
	heldRepository := filepath.Join(t.TempDir(), "held-repository")
	destination := filepath.Join(t.TempDir(), "snapshot")
	const sentinel = "untouched replacement repository"
	moved := false
	f.policy.beforeCopy = func(_ *os.Root, _ string) error {
		if moved {
			return nil
		}
		moved = true
		if err := os.Rename(f.root, heldRepository); err != nil {
			return err
		}
		if err := os.Mkdir(f.root, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(f.root, "replacement-sentinel"), []byte(sentinel), 0o600)
	}
	if _, err := stage(f.root, destination, f.policy); !moved || err == nil || !strings.Contains(err.Error(), "source root pathname changed") {
		t.Fatalf("repository pathname replacement was endorsed or failed elsewhere: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "replacement-sentinel"))
	if err != nil || string(data) != sentinel {
		t.Fatal("replacement repository sentinel was modified or removed")
	}
	entries, err := os.ReadDir(f.root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "replacement-sentinel" {
		t.Fatal("staging wrote unexpected members into the replacement repository")
	}
}

func TestStageRefusesCaseAliasedSourceIdentity(t *testing.T) {
	f := newFixture(t)
	alias := strings.ToUpper(f.root)
	original, err := os.Stat(f.root)
	if err != nil {
		t.Fatal(err)
	}
	aliased, err := os.Stat(alias)
	if err != nil || !os.SameFile(original, aliased) {
		t.Skip("filesystem does not provide the same differently cased directory alias")
	}
	repository, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := filepath.EvalSymlinks(alias)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(parent, "snapshot")
	if overlap(repository, destination) {
		t.Skip("filesystem canonicalization already resolves the case alias lexically")
	}
	if _, err := stage(f.root, destination, f.policy); err == nil || !strings.Contains(err.Error(), "identities must not overlap") {
		t.Fatalf("identity-aliased source/output was accepted: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.root, "snapshot")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("case-aliased snapshot mutated source before refusal")
	}
}

func TestApprovedAuthorityIdentity(t *testing.T) {
	a, b := approvedAuthority(), approvedAuthority()
	a.components[0].sha256 = "changed"
	if b.commit != approvedCommit || b.components[0].sha256 == "changed" || b.beforeOpen != nil || b.beforeCopy != nil {
		t.Fatal("compiled authority is mutable or has fixture hooks")
	}
	if len(b.components) != 3 || !reflect.DeepEqual([]int{b.components[0].files, b.components[1].files, b.components[2].files}, []int{793, 77, 79}) {
		t.Fatal("compiled source counts differ")
	}
}

// TestApprovedTreeOptIn is deliberately not a default fixture pass. It uses no
// fixed local path, Git, subprocess, build tool, transport, or source execution.
func TestApprovedTreeOptIn(t *testing.T) {
	repository := os.Getenv("LEXR_TEST_SENSOR_OE_ROOT")
	if repository == "" {
		t.Skip("set LEXR_TEST_SENSOR_OE_ROOT to independently check the full approved raw tree")
	}
	if err := regularReadSupported(); err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(repository)
	if err != nil {
		t.Fatal(err)
	}
	if verified.SourceCommit != approvedCommit || verified.SourceFileCount != 949 || verified.SourceSymlinkCount != 6 {
		t.Fatalf("unexpected approved lineage/counts: %+v", verified)
	}
	destination := filepath.Join(t.TempDir(), "snapshot")
	staged, err := Stage(repository, destination)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(verified, staged) {
		t.Fatal("staged composition differs from independently verified input")
	}
	// Darwin may spell the same temporary directory as /var or /private/var.
	// Compare link destinations against the canonical staged root, after also
	// proving that this spelling still identifies the staged directory itself.
	canonicalDestination, err := filepath.EvalSymlinks(destination)
	if err != nil {
		t.Fatal(err)
	}
	stagedInfo, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	canonicalInfo, err := os.Stat(canonicalDestination)
	if err != nil || !os.SameFile(stagedInfo, canonicalInfo) {
		t.Fatal("canonical staged root does not identify the staged directory")
	}
	files := append(staged.SourceFiles(), staged.Metadata()...)
	expected := make(map[string]byte, len(files)+6)
	for _, entry := range files {
		expected[entry.Path] = 'f'
		data, err := os.ReadFile(filepath.Join(destination, entry.Path))
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(filepath.Join(destination, entry.Path))
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || int64(len(data)) != entry.Bytes || fixtureHash(data) != entry.SHA256 || gitMode(info.Mode()) != entry.GitMode {
			t.Fatalf("independent staged member check failed: %s", entry.Path)
		}
	}
	for _, link := range staged.Symlinks() {
		expected[link.Path] = 'l'
		target, err := os.Readlink(filepath.Join(destination, link.Path))
		if err != nil || target != link.Target {
			t.Fatalf("independent staged link check failed: %s", link.Path)
		}
		resolved, err := filepath.EvalSymlinks(filepath.Join(destination, link.Path))
		if err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(canonicalDestination, resolved)
		if err != nil || !safeRelative(filepath.ToSlash(relative)) {
			t.Fatal("staged link escaped composition")
		}
		if kind, ok := expected[filepath.ToSlash(relative)]; !ok || kind != 'f' {
			t.Fatal("staged link does not resolve to approved regular bytes")
		}
	}
	regular, links := 0, 0
	if err := filepath.WalkDir(destination, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(destination, name)
		if err != nil {
			return err
		}
		member := filepath.ToSlash(relative)
		kind, ok := expected[member]
		if !ok {
			t.Fatalf("unexpected staged leaf: %s", member)
		}
		if kind == 'f' && entry.Type().IsRegular() {
			regular++
		} else if kind == 'l' && entry.Type()&os.ModeSymlink != 0 {
			links++
		} else {
			t.Fatalf("unexpected staged leaf type: %s", member)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if regular != 953 || links != 6 {
		t.Fatalf("full staged closure: %d regular / %d links, want 953 / 6", regular, links)
	}
}
