package build

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLocalCommitBuildRetainsExactSnapshot verifies a clean local-only commit
// crosses the private transaction without exposing its host path publicly.
func TestLocalCommitBuildRetainsExactSnapshot(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "kernel source")
	writeLocalSourceRepository(t, source)
	manager := newTestBuildManager(&fakeBuildRunner{})
	receipt, err := manager.Run(context.Background(), Request{
		RepositoryRoot: root, SourceDirectory: source,
		WorkDirectory: "work", OutputDirectory: "output",
	})
	if err != nil {
		t.Fatalf("Run(local source) error = %v", err)
	}
	if receipt.Provenance == nil || receipt.Provenance.SourceKind != SourceKindLocalGitCommit ||
		receipt.Provenance.LocalSourceRevision == "" || receipt.Provenance.LocalSourceRevision != receipt.Provenance.Revision ||
		receipt.Provenance.SourceArchiveName != LocalSourceArchiveName || receipt.Provenance.SourceFileCount != 2 {
		t.Fatalf("local source provenance = %#v", receipt.Provenance)
	}
	archivePath := filepath.Join(root, "output", LocalSourceArchiveName)
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	if hex.EncodeToString(digest[:]) != receipt.Provenance.SourceArchiveSHA256 || int64(len(archive)) != receipt.Provenance.SourceArchiveSize {
		t.Fatal("retained local source archive differs from provenance")
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), source) {
		t.Fatalf("serialised receipt exposes local source path: %s", encoded)
	}
}

// TestLocalSourceRequiresCommittedCleanTree verifies local builds cannot
// silently consume mutable working-tree or untracked bytes.
func TestLocalSourceRequiresCommittedCleanTree(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	writeLocalSourceRepository(t, source)
	if err := os.WriteFile(filepath.Join(source, "untracked.c"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := newTestBuildManager(&fakeBuildRunner{}).Plan(context.Background(), Request{
		RepositoryRoot: root, SourceDirectory: source,
		WorkDirectory: "work", OutputDirectory: "output", DryRun: true,
	})
	if err == nil || !strings.Contains(err.Error(), "commit the intended source locally") {
		t.Fatalf("dirty local source error = %v", err)
	}
}

// TestLocalSourceRejectsMixedRemoteFlagsAndEscapingLinks covers the closed
// source union and one archive extraction escape form before Docker runs.
func TestLocalSourceRejectsMixedRemoteFlagsAndEscapingLinks(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	writeLocalSourceRepository(t, source)
	manager := newTestBuildManager(&fakeBuildRunner{})
	if _, err := manager.Plan(context.Background(), Request{
		RepositoryRoot: root, SourceDirectory: source, GitURL: DefaultGitURL,
		WorkDirectory: "mixed-work", OutputDirectory: "mixed-output", DryRun: true,
	}); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("mixed source flags error = %v", err)
	}
	if err := os.Symlink("../../outside", filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	runLocalGit(t, source, "add", "escape")
	runLocalGit(t, source, "commit", "-m", "Add escaping link")
	if _, err := manager.Plan(context.Background(), Request{
		RepositoryRoot: root, SourceDirectory: source,
		WorkDirectory: "link-work", OutputDirectory: "link-output", DryRun: true,
	}); err == nil || !strings.Contains(err.Error(), "symbolic link escapes") {
		t.Fatalf("escaping link error = %v", err)
	}
}

// TestLocalSourceRejectsArchiveAttributeDivergence prevents git archive from
// silently omitting or rewriting bytes from the reviewed commit tree.
func TestLocalSourceRejectsArchiveAttributeDivergence(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	writeLocalSourceRepository(t, source)
	if err := os.WriteFile(filepath.Join(source, ".gitattributes"), []byte("README export-ignore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runLocalGit(t, source, "add", ".gitattributes")
	runLocalGit(t, source, "commit", "-m", "Add archive attribute")
	_, err := newTestBuildManager(&fakeBuildRunner{}).Plan(context.Background(), Request{
		RepositoryRoot: root, SourceDirectory: source,
		WorkDirectory: "work", OutputDirectory: "output", DryRun: true,
	})
	if err == nil || !strings.Contains(err.Error(), "export-ignore") {
		t.Fatalf("archive attribute error = %v", err)
	}
}

// writeLocalSourceRepository creates a deterministic clean Git fixture.
func writeLocalSourceRepository(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(directory, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "README"), []byte("local kernel source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "scripts", "build"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runLocalGit(t, directory, "init")
	runLocalGit(t, directory, "config", "user.name", "Lexr Test")
	runLocalGit(t, directory, "config", "user.email", "lexr@example.invalid")
	runLocalGit(t, directory, "add", "README", "scripts/build")
	runLocalGit(t, directory, "commit", "-m", "Local source fixture")
}

// runLocalGit executes Git with fixed commit timestamps for source fixtures.
func runLocalGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE=2026-08-30T10:00:00+00:00",
		"GIT_COMMITTER_DATE=2026-08-30T10:00:00+00:00",
		"LC_ALL=C",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}
