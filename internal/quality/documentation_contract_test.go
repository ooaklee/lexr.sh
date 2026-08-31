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
	workflowContracts := []struct {
		path     string
		required [][]byte
	}{
		{path: ".github/workflows/lexr.yml", required: [][]byte{[]byte("cmd/lexr"), []byte("go test -race ./..."), []byte("GH_REPO: ${{ github.repository }}")}},
		{path: ".github/workflows/iptsd-integration-tests.yml", required: [][]byte{[]byte("LEXR_TEST_OE_ROOT"), []byte("linux-surface-pro-11-oe.git")}},
		{path: ".github/workflows/sp11-kernel-build.yml", required: [][]byte{[]byte("cmd/lexr"), []byte("kernel build"), []byte("kernel-build-ci-${source_identity}"), []byte("Revalidate the OE publication boundary"), []byte("GH_TOKEN: ${{ secrets.OE_RELEASE_TOKEN }}"), []byte("GH_REPO: ooaklee/linux-surface-pro-11-oe"), []byte("^sp11-kernel-")}},
	}
	for _, contract := range workflowContracts {
		workflow := readBoundedRepositoryFile(t, repositoryRoot, contract.path)
		if bytes.Contains(workflow, []byte("LEXR_"+"REPOSITORY_TOKEN")) {
			t.Errorf("Lexr-owned workflow %s contains an OE cross-repository token", contract.path)
		}
		for _, required := range contract.required {
			if !bytes.Contains(workflow, required) {
				t.Errorf("current workflow %s omits %q", contract.path, required)
			}
		}
	}

	kernelWorkflow := readBoundedRepositoryFile(t, repositoryRoot, ".github/workflows/sp11-kernel-build.yml")
	revalidation := bytes.Index(kernelWorkflow, []byte("- name: Revalidate the OE publication boundary"))
	publication := bytes.Index(kernelWorkflow, []byte("- name: Publish a remotely verified OE prerelease"))
	secretReference := bytes.Index(kernelWorkflow, []byte("GH_TOKEN: ${{ secrets.OE_RELEASE_TOKEN }}"))
	if revalidation < 0 || publication < 0 || secretReference < 0 || revalidation >= publication || publication >= secretReference {
		t.Error("kernel workflow does not revalidate self-hosted release metadata before entering its secret-bearing OE publication step")
	}
	if count := bytes.Count(kernelWorkflow, []byte("secrets.OE_RELEASE_TOKEN")); count != 1 {
		t.Errorf("kernel workflow contains %d OE release-token references, want one", count)
	}
	for _, forbidden := range [][]byte{[]byte("octo-sts"), []byte("id-token: write")} {
		if bytes.Contains(kernelWorkflow, forbidden) {
			t.Errorf("kernel workflow retains obsolete federated-authority configuration %q", forbidden)
		}
	}
}

// TestCLIReleaseContainsOnlyBinariesAndChecksums prevents Lexr's own GitHub
// Release from becoming a second channel for hardware support or supplementary
// project resources.
func TestCLIReleaseContainsOnlyBinariesAndChecksums(t *testing.T) {
	repositoryRoot := lexrRepositoryRoot(t)
	configuration := readBoundedRepositoryFile(t, repositoryRoot, ".goreleaser.yaml")
	workflow := readBoundedRepositoryFile(t, repositoryRoot, ".github/workflows/lexr.yml")
	releaseContract := bytes.Join([][]byte{configuration, workflow}, []byte("\n"))

	for _, required := range [][]byte{
		[]byte("formats:\n      - binary"),
		[]byte("lexr-v${LEXR_VERSION}-darwin-amd64"),
		[]byte("lexr-v${LEXR_VERSION}-darwin-arm64"),
		[]byte("lexr-v${LEXR_VERSION}-linux-amd64"),
		[]byte("lexr-v${LEXR_VERSION}-linux-arm64"),
		[]byte("lexr-v${LEXR_VERSION}-windows-amd64.exe"),
		[]byte("lexr-v${LEXR_VERSION}-windows-arm64.exe"),
		[]byte("${RUNNER_TEMP}/lexr-release/lexr-v${LEXR_VERSION}.sha256sums"),
		[]byte("sha256sum --strict --check"),
	} {
		if !bytes.Contains(releaseContract, required) {
			t.Errorf("binary-only CLI release contract omits %q", required)
		}
	}
	for _, forbidden := range [][]byte{
		[]byte("formats:\n      - tar.gz"),
		[]byte("\n    files:"),
		[]byte("dist/*.tar.gz"),
		[]byte("src: supported-isos.json"),
		[]byte("src: supported-userspace.json"),
		[]byte("src: tools/collect-sp11-windows-handoff.ps1"),
	} {
		if bytes.Contains(releaseContract, forbidden) {
			t.Errorf("binary-only CLI release contract contains %q", forbidden)
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
