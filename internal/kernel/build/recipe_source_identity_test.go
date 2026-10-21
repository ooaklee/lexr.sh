package build

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRecipeSourceSnapshotHistory exercises publication and the subsequent Git
// metadata reads, not just the tree gate. Captured snapshots have the original
// commit's parent headers but intentionally do not carry ancestor objects.
func TestRecipeSourceSnapshotHistory(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("the exact container publication requires a POSIX shell")
	}
	for _, mutation := range []string{"none", "expected tree", "raw commit"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			writeLocalSourceRepository(t, source)
			parent, err := exec.Command("git", "-C", source, "rev-parse", "HEAD").Output()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "README"), []byte("second source revision\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runLocalGit(t, source, "add", "README")
			runLocalGit(t, source, "commit", "-m", "Retain uncaptured parent in snapshot")
			commitTime, err := exec.Command("git", "-C", source, "show", "-s", "--format=%cI", "HEAD").Output()
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := filepath.EvalSymlinks(source)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := inspectLocalSource(context.Background(), canonical)
			if err != nil {
				t.Fatal(err)
			}
			transaction := filepath.Join(root, "transaction")
			candidate, published := filepath.Join(root, "candidate"), filepath.Join(root, "published")
			for _, directory := range []string{transaction, candidate} {
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := captureLocalSourceSnapshot(context.Background(), transaction, canonical, identity); err != nil {
				t.Fatal(err)
			}
			output, err := executeRecipeLocalSourceExtractor(t, filepath.Join(transaction, localSourceArchiveFile), candidate, strconv.Itoa(identity.fileCount))
			if err != nil {
				t.Fatalf("extract history fixture: %v\n%s", err, output)
			}
			commit := filepath.Join(transaction, localSourceCommitFile)
			rawCommit, err := os.ReadFile(commit)
			if err != nil {
				t.Fatal(err)
			}
			expectedTree := identity.tree
			want := "Extracted local source tree differs from the reviewed Git tree"
			switch mutation {
			case "expected tree":
				expectedTree = strings.Repeat("0", len(identity.tree))
			case "raw commit":
				if err := os.WriteFile(commit, append(append([]byte(nil), rawCommit...), []byte("changed message\n")...), 0o600); err != nil {
					t.Fatal(err)
				}
				want = "Local source commit object differs from the reviewed revision"
			}
			output, err = executeRecipeSourcePublication(t, candidate, published, expectedTree, commit, identity.revision)
			if mutation != "none" {
				if err == nil || !strings.Contains(string(output), want) {
					t.Fatalf("%s: error %v, output %q; want identity rejection %q", mutation, err, output, want)
				}
				if _, err := os.Lstat(published); !os.IsNotExist(err) {
					t.Fatalf("rejected snapshot was published: %v", err)
				}
				if _, err := os.Lstat(filepath.Join(candidate, ".git", "shallow")); !os.IsNotExist(err) {
					t.Fatalf("unverified snapshot was declared shallow: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("publish exact snapshot and read metadata: %v\n%s", err, output)
			}
			for _, check := range []struct {
				arguments []string
				want      string
			}{
				{[]string{"rev-parse", "HEAD"}, identity.revision + "\n"},
				{[]string{"rev-parse", "HEAD^{tree}"}, identity.tree + "\n"},
				{[]string{"rev-parse", "--is-shallow-repository"}, "true\n"},
				{[]string{"show", "-s", "--format=%cI", "HEAD"}, string(commitTime)},
				{[]string{"rev-list", "--count", "HEAD"}, "1\n"},
				{[]string{"status", "--porcelain"}, ""},
			} {
				actual, err := exec.Command("git", append([]string{"-C", published}, check.arguments...)...).CombinedOutput()
				if err != nil || string(actual) != check.want {
					t.Fatalf("git %v: error %v, output %q; want %q", check.arguments, err, actual, check.want)
				}
			}
			actualCommit, err := exec.Command("git", "-C", published, "cat-file", "commit", "HEAD").Output()
			if err != nil || !bytes.Equal(actualCommit, rawCommit) || !bytes.Contains(actualCommit, append([]byte("parent "), parent...)) {
				t.Fatalf("published raw commit or parent header changed: %v", err)
			}
			if err := exec.Command("git", "-C", published, "cat-file", "-e", strings.TrimSpace(string(parent))+"^{commit}").Run(); err == nil {
				t.Fatal("snapshot unexpectedly contains its uncaptured parent object")
			}
			runLocalGit(t, published, "describe", "--always", "HEAD")
			boundary, err := os.ReadFile(filepath.Join(published, ".git", "shallow"))
			if err != nil || string(boundary) != identity.revision+"\n" {
				t.Fatalf("singleton shallow boundary = %q, %v", boundary, err)
			}
			for _, boundaryCase := range []string{"valid", "missing", "different"} {
				t.Run("reuse "+boundaryCase+" boundary", func(t *testing.T) {
					boundaryPath := filepath.Join(published, ".git", "shallow")
					if boundaryCase == "missing" {
						err = os.Remove(boundaryPath)
					} else {
						value := identity.revision + "\n"
						if boundaryCase == "different" {
							value = strings.Repeat("0", len(identity.revision)) + "\n"
						}
						err = os.WriteFile(boundaryPath, []byte(value), 0o600)
					}
					if err != nil {
						t.Fatal(err)
					}
					output, err := executeRecipeSourceReuseDecision(t, published, identity.tree, identity.revision)
					if boundaryCase == "valid" {
						if err != nil || string(output) != "true" {
							t.Fatalf("valid snapshot reuse = %q, %v", output, err)
						}
					} else if err == nil || !strings.Contains(string(output), "--skip-clean cannot reuse a different local source snapshot") {
						t.Fatalf("invalid boundary reused: output %q, error %v", output, err)
					}
					if _, err := os.Stat(published); err != nil {
						t.Fatalf("skip-clean rejection removed the existing source: %v", err)
					}
				})
			}
		})
	}
}

// executeRecipeSourceReuseDecision retains skip-clean's no-deletion policy while
// checking that the real recipe rejects older snapshots without a boundary.
func executeRecipeSourceReuseDecision(t *testing.T, source, tree, revision string) ([]byte, error) {
	t.Helper()
	const first = "    reuse_local=false\n"
	const last = "    if [ \"$reuse_local\" = false ]; then\n"
	if strings.Count(containerRecipe, first) != 1 || strings.Count(containerRecipe, last) != 1 {
		t.Fatal("compiled source reuse boundaries are not unique")
	}
	start, end := strings.Index(containerRecipe, first), strings.Index(containerRecipe, last)
	if end <= start {
		t.Fatal("compiled source reuse boundaries are out of order")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "-eu", "-c", containerRecipe[start:end]+"printf '%s' \"$reuse_local\"")
	command.Env = append(os.Environ(), "source_dir="+source, "expected_tree="+tree, "local_revision="+revision, "source_archive_sha256="+strings.Repeat("a", 64), "reset_source=false", "skip_clean=true")
	return command.CombinedOutput()
}

// executeRecipeSourcePublication runs the exact post-extraction recipe through
// its first published-source metadata read; no fake parent objects are supplied.
func executeRecipeSourcePublication(t *testing.T, candidate, source, tree, commit, revision string) ([]byte, error) {
	t.Helper()
	const first = "      git -C \"$candidate\" init\n"
	const last = "    printf '%s' \"$local_revision\" > \"$provenance_dir/local-source-revision\"\n"
	if strings.Count(containerRecipe, first) != 1 || strings.Count(containerRecipe, last) != 1 {
		t.Fatal("compiled source publication boundaries are not unique")
	}
	start, end := strings.Index(containerRecipe, first), strings.Index(containerRecipe, last)
	if end <= start {
		t.Fatal("compiled source publication boundaries are out of order")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "-eu", "-c", "if true; then\n"+containerRecipe[start:end])
	command.Env = append(os.Environ(), "candidate="+candidate, "source_dir="+source, "expected_tree="+tree, "source_commit="+commit, "local_revision="+revision, "source_archive_sha256="+strings.Repeat("a", 64), "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.fileMode", "GIT_CONFIG_VALUE_0=true")
	return command.CombinedOutput()
}

// TestRecipeSourceIdentityGates runs the unchanged compiled recipe's tree and
// raw-commit checks, rather than treating successful extraction as source proof.
func TestRecipeSourceIdentityGates(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("the exact container identity gates require a POSIX shell")
	}
	for _, mutation := range []string{"none", "file bytes", "executable mode", "link target", "extra path", "expected tree", "raw commit"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			writeLocalSourceRepository(t, source)
			if err := os.Mkdir(filepath.Join(source, "links"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../README", filepath.Join(source, "links", "readme")); err != nil {
				t.Fatal(err)
			}
			runLocalGit(t, source, "add", "links/readme")
			runLocalGit(t, source, "commit", "-m", "Add contained identity fixture link")
			canonical, err := filepath.EvalSymlinks(source)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := inspectLocalSource(context.Background(), canonical)
			if err != nil {
				t.Fatal(err)
			}
			transaction := filepath.Join(root, "transaction")
			destination := filepath.Join(root, "destination")
			for _, directory := range []string{transaction, destination} {
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := captureLocalSourceSnapshot(context.Background(), transaction, canonical, identity); err != nil {
				t.Fatal(err)
			}
			output, err := executeRecipeLocalSourceExtractor(t, filepath.Join(transaction, localSourceArchiveFile), destination, "3")
			if err != nil {
				t.Fatalf("extract identity fixture: %v\n%s", err, output)
			}
			commit := filepath.Join(transaction, localSourceCommitFile)
			expectedTree := identity.tree
			want := "Extracted local source tree differs from the reviewed Git tree"
			switch mutation {
			case "file bytes":
				if err := os.WriteFile(filepath.Join(destination, "README"), []byte("different bytes\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "executable mode":
				if err := os.Chmod(filepath.Join(destination, "scripts", "build"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "link target":
				filename := filepath.Join(destination, "links", "readme")
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../scripts/build", filename); err != nil {
					t.Fatal(err)
				}
			case "extra path":
				if err := os.WriteFile(filepath.Join(destination, "extra.c"), []byte("extra source\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "expected tree":
				expectedTree = strings.Repeat("0", len(identity.tree))
			case "raw commit":
				contents, err := os.ReadFile(commit)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(commit, append(contents, []byte("changed commit message\n")...), 0o600); err != nil {
					t.Fatal(err)
				}
				want = "Local source commit object differs from the reviewed revision"
			}
			output, err = executeRecipeSourceIdentityGates(t, destination, expectedTree, commit, identity.revision)
			if mutation == "none" {
				if err != nil {
					t.Fatalf("exact source identity rejected: %v\n%s", err, output)
				}
			} else if err == nil || !strings.Contains(string(output), want) {
				t.Fatalf("%s identity error = %v, output %q; want %q", mutation, err, output, want)
			}
		})
	}
}

// TestRecipeSourceIdentityTrackedIgnoredMembers preserves committed files even
// when source ignore rules match them. The fresh index must still reject any
// missing, modified, mode-changed or extra ignored payload.
func TestRecipeSourceIdentityTrackedIgnoredMembers(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("the exact container identity gates require a POSIX shell")
	}
	for _, mutation := range []string{"none", "missing ignored file", "changed ignored bytes", "changed ignored mode", "extra ignored file"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			writeLocalSourceRepository(t, source)
			for _, name := range []string{"Documentation", "ignored"} {
				if err := os.Mkdir(filepath.Join(source, name), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for name, contents := range map[string]string{
				".gitignore":                 ".*\nignored/\n*.o\n",
				"Documentation/.renames.txt": "committed hidden source\n",
				"ignored/payload.o":          "committed ignored fixture\n",
			} {
				if err := os.WriteFile(filepath.Join(source, name), []byte(contents), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			runLocalGit(t, source, "add", "--force", "--all")
			runLocalGit(t, source, "commit", "-m", "Commit ignored source fixtures")
			canonical, err := filepath.EvalSymlinks(source)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := inspectLocalSource(context.Background(), canonical)
			if err != nil {
				t.Fatal(err)
			}
			transaction, destination := filepath.Join(root, "transaction"), filepath.Join(root, "destination")
			for _, directory := range []string{transaction, destination} {
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := captureLocalSourceSnapshot(context.Background(), transaction, canonical, identity); err != nil {
				t.Fatal(err)
			}
			output, err := executeRecipeLocalSourceExtractor(t, filepath.Join(transaction, localSourceArchiveFile), destination, strconv.Itoa(identity.fileCount))
			if err != nil {
				t.Fatalf("extract ignored source fixture: %v\n%s", err, output)
			}
			ignored := filepath.Join(destination, "Documentation", ".renames.txt")
			switch mutation {
			case "missing ignored file":
				err = os.Remove(ignored)
			case "changed ignored bytes":
				err = os.WriteFile(ignored, []byte("modified ignored source\n"), 0o644)
			case "changed ignored mode":
				err = os.Chmod(ignored, 0o755)
			case "extra ignored file":
				err = os.WriteFile(filepath.Join(destination, "ignored", "extra.o"), []byte("uncommitted ignored payload\n"), 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			output, err = executeRecipeSourceIdentityGates(t, destination, identity.tree, filepath.Join(transaction, localSourceCommitFile), identity.revision)
			if mutation == "none" {
				if err != nil {
					t.Fatalf("committed ignored source rejected: %v\n%s", err, output)
				}
			} else if err == nil || !strings.Contains(string(output), "Extracted local source tree differs from the reviewed Git tree") {
				t.Fatalf("%s: error %v, output %q; want exact-tree rejection", mutation, err, output)
			}
		})
	}
}

// executeRecipeSourceIdentityGates selects the exact post-extraction shell
// through both identity comparisons, stopping before any source publication.
func executeRecipeSourceIdentityGates(t *testing.T, candidate, tree, commit, revision string) ([]byte, error) {
	t.Helper()
	const first = "      git -C \"$candidate\" init\n"
	const last = "      git -C \"$candidate\" update-ref refs/heads/lexr-local \"$revision\"\n"
	if strings.Count(containerRecipe, first) != 1 || strings.Count(containerRecipe, last) != 1 {
		t.Fatal("compiled source identity boundaries are not unique")
	}
	start := strings.Index(containerRecipe, first)
	end := strings.Index(containerRecipe, last)
	if end <= start {
		t.Fatal("compiled source identity boundaries are out of order")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", "-eu", "-c", containerRecipe[start:end])
	command.Env = append(os.Environ(), "candidate="+candidate, "expected_tree="+tree, "source_commit="+commit, "local_revision="+revision)
	// Exercise the Linux mode contract even if the caller's Git configuration
	// would otherwise disable executable-bit comparisons.
	command.Env = append(command.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.fileMode", "GIT_CONFIG_VALUE_0=true")
	return command.CombinedOutput()
}
