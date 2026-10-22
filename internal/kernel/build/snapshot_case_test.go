package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSnapshotCaseSensitiveGitNamespace validates actual Git objects/index
// entries without checking case-distinct names out onto the host filesystem.
func TestSnapshotCaseSensitiveGitNamespace(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
	}{
		{
			name: "Linux case-distinct headers",
			paths: []string{
				"include/uapi/linux/netfilter/xt_CONNMARK.h",
				"include/uapi/linux/netfilter/xt_connmark.h",
			},
		},
		{
			name: "Linux case-distinct sources",
			paths: []string{
				"net/netfilter/xt_DSCP.c",
				"net/netfilter/xt_dscp.c",
			},
		},
		{
			name:  "case-distinct implied directories",
			paths: []string{"drivers/Codec/route.c", "drivers/codec/route.c"},
		},
		{
			name:  "implied directories with different leaves",
			paths: []string{"drivers/Codec/route.c", "drivers/codec/gate.c"},
		},
		{
			name:  "Unicode lowercase pair",
			paths: []string{"unicode/Ä.c", "unicode/ä.c"},
		},
		{
			name:  "Unicode Kelvin pair",
			paths: []string{"unicode/K.c", "unicode/K.c"},
		},
		{
			name:  "Unicode expansion fold pair",
			paths: []string{"unicode/straße.c", "unicode/strasse.c"},
		},
		{
			name:  "Unicode sigma folds",
			paths: []string{"unicode/Σ.c", "unicode/σ.c", "unicode/ς.c"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries := make([]snapshotCaseIndexEntry, 0, len(test.paths))
			for index, name := range test.paths {
				entries = append(entries, snapshotCaseIndexEntry{
					name: name, mode: "100644", data: fmt.Sprintf("synthetic Git source %d\n", index),
				})
			}
			source, listing := snapshotCaseIndexTree(t, entries)
			count, err := validateLocalTree(context.Background(), source, listing)
			if err != nil {
				t.Fatalf("validate exact case-sensitive Git tree: %v", err)
			}
			if want := 2 + len(entries); count != want {
				t.Fatalf("validated file count = %d, want %d", count, want)
			}
		})
	}
}

// TestSnapshotCaseSensitiveNamespaceRetainsSupportedModes proves the namespace
// change does not reject executable files or a contained committed symlink.
func TestSnapshotCaseSensitiveNamespaceRetainsSupportedModes(t *testing.T) {
	entries := []snapshotCaseIndexEntry{
		{name: "include/UPPER.h", mode: "100644", data: "upper\n"},
		{name: "include/upper.h", mode: "100644", data: "lower\n"},
		{name: "scripts/CASE", mode: "100755", data: "#!/bin/sh\nexit 0\n"},
		{name: "links/upper", mode: "120000", data: "../include/UPPER.h"},
	}
	source, listing := snapshotCaseIndexTree(t, entries)
	count, err := validateLocalTree(context.Background(), source, listing)
	if err != nil || count != 2+len(entries) {
		t.Fatalf("supported case-sensitive modes: count %d, error %v", count, err)
	}
}

// TestSnapshotCaseSensitiveNamespaceRejectsExactDuplicates exercises malformed
// input independently of Git's own unique-path tree writer.
func TestSnapshotCaseSensitiveNamespaceRejectsExactDuplicates(t *testing.T) {
	source, listing := snapshotCaseIndexTree(t, nil)
	first, _, found := bytes.Cut(listing, []byte{0})
	if !found || len(first) == 0 {
		t.Fatal("Git fixture has no first tree record")
	}
	listing = append(listing, first...)
	listing = append(listing, 0)
	if _, err := validateLocalTree(context.Background(), source, listing); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("exact duplicate listing error = %v", err)
	}
}

// TestSnapshotCaseSensitiveNamespaceRejectsMetadata prevents even case variants
// of .git components from crossing the retained-source boundary.
func TestSnapshotCaseSensitiveNamespaceRejectsMetadata(t *testing.T) {
	source, listing := snapshotCaseIndexTree(t, nil)
	header := snapshotCaseFirstHeader(t, listing)
	for _, name := range []string{".git", ".git/config", ".GIT/config", "nested/.GiT/hooks/pre-commit"} {
		t.Run(name, func(t *testing.T) {
			record := []byte(header + "\t" + name + "\x00")
			if _, err := validateLocalTree(context.Background(), source, record); err == nil || !strings.Contains(err.Error(), "metadata") {
				t.Fatalf("reserved metadata path error = %v", err)
			}
		})
	}
}

// TestSnapshotCaseSensitiveNamespaceRetainsPathAndModeGuards covers independent
// safety checks while the collision rule becomes exact-case-sensitive.
func TestSnapshotCaseSensitiveNamespaceRetainsPathAndModeGuards(t *testing.T) {
	source, listing := snapshotCaseIndexTree(t, nil)
	header := snapshotCaseFirstHeader(t, listing)
	fields := strings.Fields(header)
	if len(fields) != 3 {
		t.Fatalf("Git fixture header = %q", header)
	}
	object := fields[2]
	tests := []struct {
		name    string
		record  []byte
		wantErr string
	}{
		{name: "empty tree", wantErr: "empty"},
		{name: "missing tab", record: []byte(header + " README\x00"), wantErr: "malformed"},
		{name: "oversized path", record: []byte(header + "\t" + strings.Repeat("x", maximumSourcePathBytes+1) + "\x00"), wantErr: "oversized"},
		{name: "invalid UTF-8", record: append([]byte(header+"\tbad-"), 0xff, 0), wantErr: "malformed"},
		{name: "absolute path", record: []byte(header + "\t/absolute\x00"), wantErr: "unsafe"},
		{name: "parent traversal", record: []byte(header + "\t../outside\x00"), wantErr: "unsafe"},
		{name: "noncanonical path", record: []byte(header + "\tinside/../file\x00"), wantErr: "unsafe"},
		{name: "empty component", record: []byte(header + "\tinside//file\x00"), wantErr: "unsafe"},
		{name: "control character", record: []byte(header + "\tbad\nname\x00"), wantErr: "control character"},
		{name: "unsupported mode", record: []byte("100664 blob " + object + "\tfile\x00"), wantErr: "unsupported Git mode"},
		{name: "unsupported object type", record: []byte("040000 tree " + object + "\tdirectory\x00"), wantErr: "unsupported Git mode"},
		{name: "invalid object ID", record: []byte("100644 blob invalid\tfile\x00"), wantErr: "unsupported Git mode"},
		{name: "submodule mode", record: []byte("160000 commit " + object + "\tsubmodule\x00"), wantErr: "submodule"},
		{name: "submodule contract", record: []byte(header + "\t.gitmodules\x00"), wantErr: "submodule"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateLocalTree(context.Background(), source, test.record); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("namespace guard error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

// TestSnapshotCaseSensitiveNamespaceRetainsLinkGuards uses actual link blobs
// without depending on host symlink support or case-sensitive path checkout.
func TestSnapshotCaseSensitiveNamespaceRetainsLinkGuards(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantErr string
	}{
		{name: "escaping", target: "../../outside", wantErr: "escapes"},
		{name: "absolute", target: "/outside", wantErr: "unsafe symbolic link"},
		{name: "empty", wantErr: "unsafe symbolic link"},
		{name: "oversized", target: strings.Repeat("x", maximumSymlinkBytes+1), wantErr: "safety limit"},
		{name: "control character", target: "bad\nlink", wantErr: "unsafe symbolic link"},
		{name: "NUL", target: "bad\x00link", wantErr: "unsafe symbolic link"},
		{name: "invalid UTF-8", target: string([]byte{0xff}), wantErr: "unsafe symbolic link"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, listing := snapshotCaseIndexTree(t, []snapshotCaseIndexEntry{
				{name: "links/escape", mode: "120000", data: test.target},
			})
			if _, err := validateLocalTree(context.Background(), source, listing); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("committed link error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

// TestSnapshotCaseSensitiveNamespaceRetainsArchiveAttributeGuards checks cached
// attributes against exact index paths rather than mutable working-tree bytes.
func TestSnapshotCaseSensitiveNamespaceRetainsArchiveAttributeGuards(t *testing.T) {
	for _, attribute := range []string{"export-ignore", "export-subst"} {
		t.Run(attribute, func(t *testing.T) {
			source, listing := snapshotCaseIndexTree(t, []snapshotCaseIndexEntry{
				{name: ".gitattributes", mode: "100644", data: "README " + attribute + "\n"},
			})
			if _, err := validateLocalTree(context.Background(), source, listing); err == nil || !strings.Contains(err.Error(), attribute) {
				t.Fatalf("cached archive attribute error = %v", err)
			}
		})
	}
}

// TestSnapshotCaseSensitiveNamespaceRetainsDirtyPlanGuard ensures relaxing the
// namespace never admits staged, unstaged, or untracked local source bytes.
func TestSnapshotCaseSensitiveNamespaceRetainsDirtyPlanGuard(t *testing.T) {
	for _, dirty := range []string{"staged", "unstaged", "untracked"} {
		t.Run(dirty, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			writeLocalSourceRepository(t, source)
			name := "README"
			if dirty == "untracked" {
				name = "untracked.c"
			}
			if err := os.WriteFile(filepath.Join(source, name), []byte("mutable source\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if dirty == "staged" {
				runLocalGit(t, source, "add", "--", name)
			}
			_, err := newTestBuildManager(&fakeBuildRunner{}).Plan(context.Background(), Request{
				RepositoryRoot: root, SourceDirectory: source,
				WorkDirectory: "work", OutputDirectory: "output", DryRun: true,
			})
			if err == nil || !strings.Contains(err.Error(), "commit the intended source locally") {
				t.Fatalf("%s local source Plan error = %v", dirty, err)
			}
		})
	}
}

// TestSnapshotCaseSensitiveCaptureRestore keeps one full real-filesystem proof
// in addition to direct validator tests which run on every host filesystem.
func TestSnapshotCaseSensitiveCaptureRestore(t *testing.T) {
	root := t.TempDir()
	snapshotCaseRequirePhysicalPair(t, root)
	source := filepath.Join(root, "source")
	writeLocalSourceRepository(t, source)
	entries := []snapshotCaseIndexEntry{
		{name: "include/xt_CONNMARK.h", mode: "100644", data: "upper header\n"},
		{name: "include/xt_connmark.h", mode: "100644", data: "lower header\n"},
		{name: "Codec/route.c", mode: "100644", data: "upper directory\n"},
		{name: "codec/route.c", mode: "100644", data: "lower directory\n"},
		{name: "links/upper", mode: "120000", data: "../include/xt_CONNMARK.h"},
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := filepath.Join(source, filepath.FromSlash(entry.name))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if entry.mode == "120000" {
			if err := os.Symlink(entry.data, name); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(name, []byte(entry.data), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, entry.name)
	}
	runLocalGit(t, source, "config", "core.ignorecase", "false")
	runLocalGit(t, source, append([]string{"add", "--"}, paths...)...)
	runLocalGit(t, source, "commit", "-m", "Add case-sensitive snapshot fixture")
	canonical, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := inspectLocalSource(context.Background(), canonical)
	if err != nil {
		t.Fatalf("inspect case-sensitive source: %v", err)
	}
	if identity.fileCount != 2+len(entries) {
		t.Fatalf("case-sensitive source count = %d", identity.fileCount)
	}
	transaction := filepath.Join(root, "transaction")
	destination := filepath.Join(root, "restored")
	for _, directory := range []string{transaction, destination} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	archive, err := captureLocalSourceSnapshot(context.Background(), transaction, canonical, identity)
	if err != nil {
		t.Fatalf("capture case-sensitive source: %v", err)
	}
	archivePath := filepath.Join(transaction, localSourceArchiveFile)
	encoded, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	if hex.EncodeToString(digest[:]) != archive.sha256 || int64(len(encoded)) != archive.size {
		t.Fatal("captured archive differs from exact retained identity")
	}
	output, err := executeRecipeLocalSourceExtractor(t, archivePath, destination, strconv.Itoa(identity.fileCount))
	if err != nil {
		t.Fatalf("restore exact case-sensitive archive: %v\n%s", err, output)
	}
	for _, entry := range entries {
		name := filepath.Join(destination, filepath.FromSlash(entry.name))
		if entry.mode == "120000" {
			if target, err := os.Readlink(name); err != nil || target != entry.data {
				t.Fatalf("restored symlink %q = %q, %v", entry.name, target, err)
			}
		} else if data, err := os.ReadFile(name); err != nil || string(data) != entry.data {
			t.Fatalf("restored bytes %q = %q, %v", entry.name, data, err)
		}
	}
	runLocalGit(t, destination, "init")
	runLocalGit(t, destination, "config", "core.ignorecase", "false")
	runLocalGit(t, destination, "add", "-A")
	tree, err := gitObjectOutput(context.Background(), destination, "write-tree")
	if err != nil || tree != identity.tree {
		t.Fatalf("restored Git tree = %q, want %q, error %v", tree, identity.tree, err)
	}
	revision, err := gitObjectOutput(context.Background(), destination, "hash-object", "-t", "commit", "-w", filepath.Join(transaction, localSourceCommitFile))
	if err != nil || revision != identity.revision {
		t.Fatalf("restored raw commit = %q, want %q, error %v", revision, identity.revision, err)
	}
}

// snapshotCaseIndexEntry describes one byte-exact synthetic Git index object.
type snapshotCaseIndexEntry struct {
	name string
	mode string
	data string
}

// snapshotCaseIndexTree uses Git plumbing only; none of the additional paths
// are materialised in the fixture worktree, including on case-insensitive macOS.
func snapshotCaseIndexTree(t *testing.T, entries []snapshotCaseIndexEntry) (string, []byte) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source")
	writeLocalSourceRepository(t, source)
	// Keep names supplied to Git plumbing byte-exact on macOS as well.
	runLocalGit(t, source, "config", "core.ignorecase", "false")
	runLocalGit(t, source, "config", "core.precomposeunicode", "false")
	for _, entry := range entries {
		output, err := runGitInputOutput(context.Background(), source, []byte(entry.data), maximumGitIdentityBytes, "hash-object", "-w", "--stdin")
		if err != nil {
			t.Fatalf("write synthetic source blob: %v", err)
		}
		object := strings.TrimSpace(string(output))
		if !gitObjectExpression.MatchString(object) {
			t.Fatalf("synthetic source blob ID = %q", object)
		}
		runLocalGit(t, source, "-c", "core.ignorecase=false", "update-index", "--add", "--cacheinfo", entry.mode+","+object+","+entry.name)
	}
	tree, err := gitObjectOutput(context.Background(), source, "write-tree")
	if err != nil {
		t.Fatalf("write exact source tree: %v", err)
	}
	listing, err := runGitOutput(context.Background(), source, maximumGitTreeListBytes, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		t.Fatalf("list exact source tree: %v", err)
	}
	return source, listing
}

// snapshotCaseFirstHeader returns the mode/type/object prefix for malformed-path fixtures.
func snapshotCaseFirstHeader(t *testing.T, listing []byte) string {
	t.Helper()
	first, _, found := bytes.Cut(listing, []byte{0})
	if !found {
		t.Fatal("Git fixture has no tree record")
	}
	header, _, found := bytes.Cut(first, []byte{'\t'})
	if !found {
		t.Fatal("Git fixture has no tree header")
	}
	return string(header)
}

// snapshotCaseRequirePhysicalPair gates only capture/restore. The direct Git
// object tests above must never be skipped for a case-insensitive host.
func snapshotCaseRequirePhysicalPair(t *testing.T, parent string) {
	t.Helper()
	probe := filepath.Join(parent, "case-probe")
	if err := os.Mkdir(probe, 0o700); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(probe, "case")
	second := filepath.Join(probe, "CASE")
	for index, name := range []string{first, second} {
		file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if index == 1 && errors.Is(err, os.ErrExist) {
			t.Skip("fixture filesystem cannot represent distinct case-sensitive filenames; Git-object validator tests still run")
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	left, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	right, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(left, right) {
		t.Skip("fixture filesystem aliases case-sensitive filenames; Git-object validator tests still run")
	}
}
