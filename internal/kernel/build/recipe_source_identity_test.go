package build

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

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
