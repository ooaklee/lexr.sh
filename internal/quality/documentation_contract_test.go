package quality

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// currentOperatorDocuments is the standalone repository's maintained public
// entry-point set.
var currentOperatorDocuments = []string{"README.md", "CHANGELOG.md"}

// privateOutputContract connects one ignored private-output shape to the glob
// used by the current workflow's tracked-file guard.
type privateOutputContract struct {
	// ignore is the exact root .gitignore entry.
	ignore string
	// workflow is the recursive Git pathspec suffix used by CI.
	workflow string
}

// privateOutputContracts is the reviewed set of diagnostic output shapes that
// must remain both untracked by default and rejected when deliberately added.
var privateOutputContracts = []privateOutputContract{
	{ignore: "sp11-linux-checks/", workflow: "**/sp11-linux-checks/**"},
	{ignore: "sp11-linux-checks-*.zip", workflow: "**/sp11-linux-checks-*.zip"},
	{ignore: "report-*/", workflow: "**/report-*/**"},
}

// generatedOutputIgnores lists repository-root paths populated by the CLI's
// default build and release commands. Keeping them ignored prevents an
// ordinary build from making the source checkout unsuitable for a companion
// source archive.
var generatedOutputIgnores = []string{"/bin/", "/build/", "/dist/", "/lexr"}

// TestMaintainedDocumentationReferencesLexr verifies the standalone entry
// points describe the current product and repository rather than an OE-owned
// nested command path.
func TestMaintainedDocumentationReferencesLexr(t *testing.T) {
	repositoryRoot := lexrRepositoryRoot(t)
	for _, relative := range currentOperatorDocuments {
		content := readBoundedRepositoryFile(t, repositoryRoot, relative)
		if !bytes.Contains(content, []byte("Lexr")) && !bytes.Contains(content, []byte("lexr")) {
			t.Errorf("maintained documentation %s has no Lexr operator path", relative)
		}
	}
	readme := readBoundedRepositoryFile(t, repositoryRoot, "README.md")
	if !bytes.Contains(readme, []byte("https://github.com/ooaklee/lexr.sh")) {
		t.Error("README.md does not identify the standalone Lexr repository")
	}
	workflow := readBoundedRepositoryFile(t, repositoryRoot, ".github/workflows/lexr.yml")
	for _, required := range [][]byte{[]byte("cmd/lexr"), []byte("go test -race ./...")} {
		if !bytes.Contains(workflow, required) {
			t.Errorf("current workflow omits %q", required)
		}
	}
}

// TestPrivateDiagnosticOutputRemainsGuarded prevents private hardware captures
// from becoming ordinary standalone repository artefacts.
func TestPrivateDiagnosticOutputRemainsGuarded(t *testing.T) {
	repositoryRoot := lexrRepositoryRoot(t)
	ignore := lineSet(readBoundedRepositoryFile(t, repositoryRoot, ".gitignore"))
	workflow := readBoundedRepositoryFile(t, repositoryRoot, ".github/workflows/lexr.yml")
	for _, contract := range privateOutputContracts {
		if !ignore[contract.ignore] {
			t.Errorf(".gitignore omits private diagnostic pattern %q", contract.ignore)
		}
		guard := []byte("':(glob)" + contract.workflow + "'")
		if count := bytes.Count(workflow, guard); count != 1 {
			t.Errorf("workflow contains %d tracked-output guards for %q, want one", count, contract.workflow)
		}
	}
}

// TestGeneratedOutputRemainsIgnored verifies that the standalone checkout
// stays clean when operators use the command's documented default paths.
func TestGeneratedOutputRemainsIgnored(t *testing.T) {
	repositoryRoot := lexrRepositoryRoot(t)
	ignore := lineSet(readBoundedRepositoryFile(t, repositoryRoot, ".gitignore"))
	for _, required := range generatedOutputIgnores {
		if !ignore[required] {
			t.Errorf(".gitignore omits generated-output path %q", required)
		}
	}
}

// lexrRepositoryRoot returns the canonical standalone module root and fails if
// the package is accidentally tested from an unrelated source arrangement.
func lexrRepositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	module := readBoundedRepositoryFile(t, root, "go.mod")
	if !bytes.Contains(module, []byte("module github.com/ooaklee/lexr.sh\n")) {
		t.Fatalf("quality package root is not the Lexr module: %s", root)
	}
	return root
}

// readBoundedRepositoryFile reads one regular, non-symbolic-link repository
// file whose size is suitable for a source-quality gate.
func readBoundedRepositoryFile(t *testing.T, root, relative string) []byte {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	information, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("inspect repository file %s: %v", relative, err)
	}
	if !information.Mode().IsRegular() || information.Mode()&os.ModeSymlink != 0 || information.Size() > 4<<20 {
		t.Fatalf("repository file %s is not a bounded non-symlink regular file", relative)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read repository file %s: %v", relative, err)
	}
	return content
}

// lineSet returns the trimmed, non-empty lines in one textual source file.
func lineSet(content []byte) map[string]bool {
	lines := make(map[string]bool)
	for _, line := range strings.Split(string(content), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines[line] = true
		}
	}
	return lines
}
