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
var currentOperatorDocuments = []string{"README.md", "CHANGELOG.md", "CODE_OF_CONDUCT.md", "CONTRIBUTING.md"}

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
	projectDocumentContracts := []struct {
		path     string
		required [][]byte
	}{
		{path: "LICENSE", required: [][]byte{[]byte("Apache License"), []byte("Version 2.0, January 2004"), []byte("Copyright © 2026, Leon Silcott <leon@boasi.io> and other contributors.")}},
		{path: "NOTICE", required: [][]byte{[]byte("Lexr.sh"), []byte("Copyright © 2026, Leon Silcott <leon@boasi.io> and other contributors.")}},
		{path: "THIRD_PARTY_NOTICES.md", required: [][]byte{[]byte("github.com/spf13/cobra"), []byte("golang.org/x/sys"), []byte("UNICODE LICENSE V3")}},
		{path: "MAINTAINERS", required: [][]byte{[]byte("Leon Silcott   leon@boasi.io  github.com/ooaklee")}},
	}
	for _, contract := range projectDocumentContracts {
		document := readBoundedRepositoryFile(t, repositoryRoot, contract.path)
		for _, required := range contract.required {
			if !bytes.Contains(document, required) {
				t.Errorf("project document %s omits %q", contract.path, required)
			}
		}
	}
	noticeProse := strings.Join(strings.Fields(string(readBoundedRepositoryFile(t, repositoryRoot, "THIRD_PARTY_NOTICES.md"))), " ")
	for _, required := range []string{
		"During active development, it may briefly lag behind a dependency update.",
		"Before each release, it must be reviewed against every supported binary.",
		"It must be updated before publication.",
	} {
		if !strings.Contains(noticeProse, required) {
			t.Errorf("THIRD_PARTY_NOTICES.md omits maintenance policy %q", required)
		}
	}
	licence := readBoundedRepositoryFile(t, repositoryRoot, "LICENSE")
	for _, placeholder := range [][]byte{
		[]byte("Copyright [yyyy] [name of copyright owner]"),
		[]byte("fields enclosed by brackets"),
	} {
		if bytes.Contains(licence, placeholder) {
			t.Errorf("LICENSE retains Apache application-template text %q", placeholder)
		}
	}
	workflowContracts := []struct {
		path     string
		required [][]byte
	}{
		{path: ".github/workflows/lexr.yml", required: [][]byte{[]byte("cmd/lexr"), []byte("go test -race ./..."), []byte("GH_REPO: ${{ github.repository }}")}},
		{path: ".github/workflows/iptsd-integration-tests.yml", required: [][]byte{[]byte("LEXR_TEST_OE_ROOT"), []byte("linux-surface-pro-11-oe.git")}},
		{path: ".github/workflows/sp11-kernel-build.yml", required: [][]byte{[]byte("cmd/lexr"), []byte("kernel build"), []byte("--boot-image-mode stubble"), []byte("kernel-build-ci-${source_identity}"), []byte("Revalidate the OE publication boundary"), []byte("GH_TOKEN: ${{ secrets.OE_RELEASE_TOKEN }}"), []byte("GH_REPO: ooaklee/linux-surface-pro-11-oe"), []byte("canonical_release_name=\"sp11-qcom-x1e-${kernel_version}\""), []byte("draft_release_id="), []byte("git/refs"), []byte("^sp11-qcom-x1e-")}},
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
	if count := bytes.Count(kernelWorkflow, []byte(`^sp11-qcom-x1e-`)); count != 2 {
		t.Errorf("kernel workflow contains %d canonical release-prefix guards, want two", count)
	}
	if count := bytes.Count(kernelWorkflow, []byte(`.release_name == ("sp11-qcom-x1e-" + .version)`)); count != 2 {
		t.Errorf("kernel workflow contains %d manifest/version release-name bindings, want two", count)
	}
	if count := bytes.Count(kernelWorkflow, []byte(`.source.boot_image_mode == "stubble"`)); count != 2 {
		t.Errorf("kernel workflow contains %d Stubble provenance bindings, want two", count)
	}
	if count := bytes.Count(kernelWorkflow, []byte(`git/refs`)); count != 1 {
		t.Errorf("kernel workflow contains %d explicit draft-tag creations, want one", count)
	}
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

// TestCLIReleaseContainsBinariesNoticesAndChecksums prevents Lexr's own GitHub
// Release from omitting legal documents or becoming a second channel for
// hardware support and unrelated project resources.
func TestCLIReleaseContainsBinariesNoticesAndChecksums(t *testing.T) {
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
		[]byte("${RUNNER_TEMP}/lexr-release/LICENSE"),
		[]byte("${RUNNER_TEMP}/lexr-release/NOTICE"),
		[]byte("${RUNNER_TEMP}/lexr-release/THIRD_PARTY_NOTICES.md"),
		[]byte("${RUNNER_TEMP}/lexr-release/lexr-v${LEXR_VERSION}.sha256sums"),
		[]byte("Checksum manifest does not cover exactly the nine release payloads."),
		[]byte("expected ten"),
		[]byte("sha256sum --strict --check"),
	} {
		if !bytes.Contains(releaseContract, required) {
			t.Errorf("exact CLI release contract omits %q", required)
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
			t.Errorf("exact CLI release contract contains %q", forbidden)
		}
	}
}

// TestContributionEntryPointsPreserveSafetyAndDependencyReview keeps the
// repository's public forms and automation aligned with CONTRIBUTING.md.
func TestContributionEntryPointsPreserveSafetyAndDependencyReview(t *testing.T) {
	repositoryRoot := lexrRepositoryRoot(t)
	contracts := []struct {
		path     string
		required [][]byte
	}{
		{
			path: ".github/ISSUE_TEMPLATE/01-bug-report.yml",
			required: [][]byte{
				[]byte("If this is a security or privacy problem, do not post it here."),
				[]byte("leon@boasi.io"),
				[]byte("I inspected every attachment and log excerpt"),
			},
		},
		{
			path: ".github/ISSUE_TEMPLATE/02-feature-request.yml",
			required: [][]byte{
				[]byte("architecture decision record"),
				[]byte("privacy, privilege, recovery, compatibility, persisted data, or release behaviour"),
			},
		},
		{
			path: ".github/ISSUE_TEMPLATE/config.yml",
			required: [][]byte{
				[]byte("blank_issues_enabled: false"),
				[]byte("CONTRIBUTING.md#report-a-bug-safely"),
			},
		},
		{
			path: ".github/PULL_REQUEST_TEMPLATE.md",
			required: [][]byte{
				[]byte("go test -race ./..."),
				[]byte("THIRD_PARTY_NOTICES.md"),
				[]byte("private diagnostics"),
			},
		},
	}
	for _, contract := range contracts {
		content := readBoundedRepositoryFile(t, repositoryRoot, contract.path)
		for _, required := range contract.required {
			if !bytes.Contains(content, required) {
				t.Errorf("contribution entry point %s omits %q", contract.path, required)
			}
		}
	}

	dependabot := readBoundedRepositoryFile(t, repositoryRoot, ".github/dependabot.yml")
	for _, required := range [][]byte{
		[]byte("package-ecosystem: \"gomod\""),
		[]byte("package-ecosystem: \"github-actions\""),
		[]byte("prefix: \"chore(deps)\""),
		[]byte("THIRD_PARTY_NOTICES.md"),
	} {
		if !bytes.Contains(dependabot, required) {
			t.Errorf("Dependabot configuration omits %q", required)
		}
	}
	if bytes.Contains(dependabot, []byte("package-ecosystem: \"docker\"")) {
		t.Error("Dependabot configures Docker updates without a Docker dependency manifest")
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

// TestSourceBuildUsesExplicitLexrProvenance prevents local and submodule builds
// from trusting a containing repository's automatic Go VCS metadata.
func TestSourceBuildUsesExplicitLexrProvenance(t *testing.T) {
	repositoryRoot := lexrRepositoryRoot(t)
	readme := readBoundedRepositoryFile(t, repositoryRoot, "README.md")
	workflow := readBoundedRepositoryFile(t, repositoryRoot, ".github/workflows/lexr.yml")
	builder := readBoundedRepositoryFile(t, repositoryRoot, "cmd/lexr-build/main.go")
	versionSource := readBoundedRepositoryFile(t, repositoryRoot, "internal/version/version.go")

	for path, content := range map[string][]byte{
		"README.md":                  readme,
		".github/workflows/lexr.yml": workflow,
	} {
		if !bytes.Contains(content, []byte("go run ./cmd/lexr-build")) {
			t.Errorf("%s does not use the explicit source-build helper", path)
		}
	}
	for _, required := range [][]byte{
		[]byte("-buildvcs=false"),
		[]byte("internal/version.Version"),
		[]byte("internal/version.Commit"),
		[]byte("internal/version.Date"),
	} {
		if !bytes.Contains(builder, required) {
			t.Errorf("source-build helper omits %q", required)
		}
	}
	for _, forbidden := range [][]byte{[]byte("vcs.revision"), []byte("vcs.time")} {
		if bytes.Contains(versionSource, forbidden) {
			t.Errorf("version package trusts ambiguous automatic metadata %q", forbidden)
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
