package source

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/platform"
	userspaceiptsd "github.com/ooaklee/lexr.sh/internal/userspace/iptsd"
)

// fixtureSourceURL is the admitted HTTPS identity rewritten only by test code.
const fixtureSourceURL = "https://example.invalid/maintainer/oe.git"

// fixtureCameraBase is a valid existing camera authority, not a native build.
const fixtureCameraBase = `Upstream project: https://git.libcamera.org/libcamera/libcamera.git
Upstream tag: v0.7.0
Upstream commit: b7854fd07d42168f099b5ce30d1702e0e0875bf5
Ubuntu package validated on device: 0.7.0-1ubuntu2 (arm64, resolute)
Ubuntu DSC SHA-256: 27a10011fd5efe43564e94bb0328342ec11441963bed37db10a2d524553a02d8
Ubuntu orig tarball SHA-256: ebd90a3aa2ca87a39323ffb7a4f5bbf72090b43a2431133759620b63e982db87
Ubuntu Debian tarball SHA-256: 0ee3195d74ad85b089c62eb7b2904023f8556544b2d9dbcc67aa834ddde5a09e
Patch validation: dry-run applies after Ubuntu's 0.7.0-1ubuntu2 patch series
IPA validation: native package proof required
Turbine userspace source: repository b1f5237957b9, libcamera tip 72dc8cff6447
Validation date: 2026-08-29
`

// fixtureSourceRunner changes only the fetch transport in tests. All source
// manager Git commands still run through the production isolated runner.
type fixtureSourceRunner struct {
	base        platform.Runner
	remote      string
	commands    []platform.Command
	beforeFetch func()
	fetchError  error
}

// command records each invocation and redirects only its exact fetch transport.
func (runner *fixtureSourceRunner) command(command platform.Command) (platform.Command, error) {
	copy := command
	copy.Args = append([]string(nil), command.Args...)
	runner.commands = append(runner.commands, copy)
	if command.Name != "git" {
		return platform.Command{}, errors.New("source preparation attempted a non-Git process")
	}
	fetch := false
	for _, arg := range command.Args {
		fetch = fetch || arg == "fetch"
	}
	if !fetch {
		return copy, nil
	}
	if runner.beforeFetch != nil {
		callback := runner.beforeFetch
		runner.beforeFetch = nil
		callback()
	}
	if runner.fetchError != nil {
		return platform.Command{}, runner.fetchError
	}
	replaced := 0
	for index, arg := range copy.Args {
		if arg == fixtureSourceURL {
			copy.Args[index] = runner.remote
			replaced++
		}
	}
	if replaced != 1 {
		return platform.Command{}, errors.New("fixture must rewrite exactly one admitted fetch URL")
	}
	// This file-transport exception exists exclusively in this test adapter.
	copy.Args = append([]string{"-c", "protocol.file.allow=always"}, copy.Args...)
	return copy, nil
}

// Run delegates the fixture-adjusted command to the production isolated runner.
func (runner *fixtureSourceRunner) Run(ctx context.Context, command platform.Command) error {
	command, err := runner.command(command)
	if err != nil {
		return err
	}
	return runner.base.Run(ctx, command)
}

// Capture retains the production isolation boundary for Git metadata reads.
func (runner *fixtureSourceRunner) Capture(ctx context.Context, command platform.Command) ([]byte, error) {
	command, err := runner.command(command)
	if err != nil {
		return nil, err
	}
	return runner.base.Capture(ctx, command)
}

// sourceFixture records the private seed, bare remote and exact committed tree.
type sourceFixture struct {
	seed   string
	remote string
	commit string
	tree   string
}

// sourceGit operates solely on fresh temporary fixtures without inherited Git
// repository or executable configuration.
func sourceGit(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	args := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "init.templateDir=", "-C", root}, arguments...)
	command := exec.Command("git", args...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_AUTHOR_DATE=2026-10-22T00:00:00Z",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid", "GIT_COMMITTER_DATE=2026-10-22T00:00:00Z")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture git %v: %v\n%s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

// sourceWrite creates one component input beneath a fresh test-owned directory.
func sourceWrite(t *testing.T, root, relative, contents string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newSourceFixture publishes a minimal camera fixture to a local bare remote.
func newSourceFixture(t *testing.T) sourceFixture {
	t.Helper()
	requireGitHost(t)
	parent := t.TempDir()
	fixture := sourceFixture{seed: filepath.Join(parent, "seed"), remote: filepath.Join(parent, "remote.git")}
	for _, directory := range []string{fixture.seed, fixture.remote} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sourceGit(t, fixture.remote, "init", "--bare")
	sourceGit(t, fixture.seed, "init", "-b", "main")
	sourceWrite(t, fixture.seed, "userspace/camera/libcamera/BASE.txt", fixtureCameraBase)
	sourceWrite(t, fixture.seed, "userspace/camera/libcamera/0001-libipa-add-imx681-simple-ipa-support.patch", "fixture patch\n")
	sourceWrite(t, fixture.seed, "userspace/camera/libcamera/imx681.yaml", "version: 1\n")
	sourceWrite(t, fixture.seed, "userspace/iptsd-sp11/SOURCE.env", "IPTSD_COMMIT=unreviewed-fixture\n")
	sourceWrite(t, fixture.seed, "unrelated.txt", "not a component input\n")
	sourceGit(t, fixture.seed, "add", "--all")
	sourceGit(t, fixture.seed, "commit", "-m", "fixture source")
	fixture.commit = sourceGit(t, fixture.seed, "rev-parse", "HEAD")
	fixture.tree = sourceGit(t, fixture.seed, "rev-parse", "HEAD^{tree}")
	sourceGit(t, fixture.seed, "remote", "add", "origin", fixture.remote)
	sourceGit(t, fixture.seed, "push", "origin", "refs/heads/main")
	return fixture
}

// fixtureSourceManager wraps production Git isolation with test-only transport.
func fixtureSourceManager(fixture sourceFixture) (*Manager, *fixtureSourceRunner) {
	manager := New()
	runner := &fixtureSourceRunner{base: manager.runner, remote: fixture.remote}
	manager.runner = runner
	return manager, runner
}

// fixtureSourceRequest pins one full commit and a fresh publication destination.
func fixtureSourceRequest(t *testing.T, fixture sourceFixture) Request {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Request{Component: "camera", Experimental: true, Repository: fixtureSourceURL,
		Ref: "refs/heads/main", Commit: fixture.commit, OutputDirectory: filepath.Join(parent, "selected")}
}

// assertSourceUnpublished checks failure truth and retention of incomplete work.
func assertSourceUnpublished(t *testing.T, receipt Receipt, output string) {
	t.Helper()
	if receipt.Prepared {
		t.Fatal("failed preparation was marked prepared")
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed preparation published output: %v", err)
	}
	if receipt.StagingDirectory == "" {
		t.Fatal("failed acquisition did not return its retained staging path")
	}
	if info, err := os.Lstat(receipt.StagingDirectory); err != nil || !info.IsDir() {
		t.Fatalf("failed staging was not retained: %v", err)
	}
}

// TestPrepareRejectsUnclosedSelectionBeforeAcquisition keeps invalid requests
// outside both Git execution and filesystem acquisition.
func TestPrepareRejectsUnclosedSelectionBeforeAcquisition(t *testing.T) {
	valid := Request{Component: "camera", Experimental: true, Repository: fixtureSourceURL,
		Ref: "refs/heads/main", Commit: strings.Repeat("a", 40)}
	for _, test := range []struct {
		name   string
		change func(*Request)
	}{
		{"no opt in", func(r *Request) { r.Experimental = false }},
		{"missing repository", func(r *Request) { r.Repository = "" }},
		{"missing ref", func(r *Request) { r.Ref = "" }},
		{"missing commit", func(r *Request) { r.Commit = "" }},
		{"missing output", func(r *Request) { r.OutputDirectory = "" }},
		{"short commit", func(r *Request) { r.Commit = strings.Repeat("a", 12) }},
		{"uppercase commit", func(r *Request) { r.Commit = strings.Repeat("A", 40) }},
		{"unsupported sensors", func(r *Request) { r.Component = "sensors" }},
		{"unsupported compute", func(r *Request) { r.Component = "compute" }},
		{"http repository", func(r *Request) { r.Repository = "http://example.invalid/oe.git" }},
		{"file repository", func(r *Request) { r.Repository = "file:///tmp/oe.git" }},
		{"repository credentials", func(r *Request) { r.Repository = "https://user:password@example.invalid/oe.git" }},
		{"repository query", func(r *Request) { r.Repository += "?ref=main" }},
		{"repository fragment", func(r *Request) { r.Repository += "#main" }},
		{"repository control", func(r *Request) { r.Repository += "\nnext" }},
		{"escaped repository path", func(r *Request) { r.Repository = "https://example.invalid/owner/%2e%2e/oe.git" }},
		{"repository path traversal", func(r *Request) { r.Repository = "https://example.invalid/owner/../oe.git" }},
		{"empty repository component", func(r *Request) { r.Repository = "https://example.invalid/owner//oe.git" }},
		{"short ref", func(r *Request) { r.Ref = "main" }},
		{"remote tracking ref", func(r *Request) { r.Ref = "refs/remotes/origin/main" }},
		{"ref expression", func(r *Request) { r.Ref = "refs/tags/v1^{}" }},
		{"ref traversal", func(r *Request) { r.Ref = "refs/heads/../main" }},
		{"ref flag", func(r *Request) { r.Ref = "--upload-pack=anything" }},
		{"ref hidden component", func(r *Request) { r.Ref = "refs/heads/.hidden/main" }},
		{"ref lock component", func(r *Request) { r.Ref = "refs/heads/topic.lock" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			parent := t.TempDir()
			request.OutputDirectory = filepath.Join(parent, "selected")
			test.change(&request)
			manager, runner := fixtureSourceManager(sourceFixture{})
			_, err := manager.Prepare(context.Background(), request)
			if err == nil {
				t.Fatal("invalid request passed")
			}
			if len(runner.commands) != 0 {
				t.Fatalf("invalid request crossed Git boundary: %v", runner.commands)
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatalf("invalid request mutated destination parent: %v %v", entries, err)
			}
		})
	}
}

// TestPrepareDryRunDoesNotAcquireOrValidate prevents a plan from claiming fetched
// identity, component validation or filesystem publication.
func TestPrepareDryRunDoesNotAcquireOrValidate(t *testing.T) {
	parent := t.TempDir()
	request := Request{Component: "camera", Experimental: true, Repository: fixtureSourceURL,
		Ref: "refs/heads/main", Commit: strings.Repeat("a", 40), OutputDirectory: filepath.Join(parent, "selected"), DryRun: true}
	manager, runner := fixtureSourceManager(sourceFixture{})
	manager.validate = func(context.Context, string, string) error {
		t.Fatal("dry run invoked component validator")
		return nil
	}
	receipt, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.DryRun || receipt.Prepared || receipt.ComponentValidated || receipt.Commit != "" || receipt.Tree != "" {
		t.Fatalf("dry run claimed acquired or validated source: %+v", receipt)
	}
	if len(runner.commands) != 0 {
		t.Fatal("dry run invoked Git")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("dry run mutated destination parent: %v %v", entries, err)
	}
}

// TestPreparePinnedBranchUsesRealComponentValidation binds a published checkout
// and receipt to the exact commit through the unchanged camera input validator.
func TestPreparePinnedBranchUsesRealComponentValidation(t *testing.T) {
	fixture := newSourceFixture(t)
	manager, runner := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	receipt, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Prepared || !receipt.ComponentValidated || receipt.DryRun || receipt.Commit != fixture.commit || receipt.ExpectedCommit != fixture.commit || receipt.Tree != fixture.tree {
		t.Fatalf("prepared identity = %+v", receipt)
	}
	if receipt.Component != "camera" || receipt.Repository != fixtureSourceURL || receipt.Ref != request.Ref || receipt.Validation == "" {
		t.Fatalf("source authority = %+v", receipt)
	}
	if receipt.Directory != request.OutputDirectory || receipt.RepositoryRoot != filepath.Join(request.OutputDirectory, "repository") {
		t.Fatalf("prepared paths = %+v", receipt)
	}
	data, err := os.ReadFile(filepath.Join(receipt.Directory, ReceiptName))
	if err != nil {
		t.Fatal(err)
	}
	var recorded Receipt
	if err := json.Unmarshal(data, &recorded); err != nil || !reflect.DeepEqual(recorded, receipt) {
		t.Fatalf("published receipt differs from actual selection: %+v %v", recorded, err)
	}
	if got := sourceGit(t, receipt.RepositoryRoot, "rev-parse", "HEAD"); got != fixture.commit {
		t.Fatalf("checkout HEAD = %s", got)
	}
	if got := sourceGit(t, receipt.RepositoryRoot, "status", "--porcelain=v1"); got != "" {
		t.Fatalf("prepared checkout dirty: %s", got)
	}
	for _, command := range runner.commands {
		if command.Name != "git" {
			t.Fatalf("source preparation executed non-Git command: %+v", command)
		}
	}
}

// TestPrepareAnnotatedTagPinsPeeledCommit distinguishes a tag object from the
// immutable commit containing the selected source tree.
func TestPrepareAnnotatedTagPinsPeeledCommit(t *testing.T) {
	fixture := newSourceFixture(t)
	sourceGit(t, fixture.seed, "tag", "-a", "v-fixture", "-m", "annotated source fixture")
	sourceGit(t, fixture.seed, "push", "origin", "refs/tags/v-fixture")
	if tag := sourceGit(t, fixture.seed, "rev-parse", "refs/tags/v-fixture"); tag == fixture.commit {
		t.Fatal("fixture tag is not annotated")
	}
	manager, _ := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	request.Ref = "refs/tags/v-fixture"
	receipt, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Prepared || receipt.Commit != fixture.commit || receipt.Ref != request.Ref {
		t.Fatalf("annotated tag selected wrong commit: %+v", receipt)
	}
}

// TestPrepareAnnotatedTagObjectIsNotCommitAuthority rejects an expected SHA
// which identifies the annotation rather than its peeled source commit.
func TestPrepareAnnotatedTagObjectIsNotCommitAuthority(t *testing.T) {
	fixture := newSourceFixture(t)
	sourceGit(t, fixture.seed, "tag", "-a", "v-fixture", "-m", "annotated source fixture")
	sourceGit(t, fixture.seed, "push", "origin", "refs/tags/v-fixture")
	tagObject := sourceGit(t, fixture.seed, "rev-parse", "refs/tags/v-fixture")
	if tagObject == fixture.commit {
		t.Fatal("fixture tag is not annotated")
	}
	manager, _ := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	request.Ref = "refs/tags/v-fixture"
	request.Commit = tagObject
	validated := false
	manager.validate = func(context.Context, string, string) error { validated = true; return nil }
	receipt, err := manager.Prepare(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "peeled commit") {
		t.Fatalf("tag-object authority was not refused: %v", err)
	}
	if validated {
		t.Fatal("unmatched tag object reached component validation")
	}
	assertSourceUnpublished(t, receipt, request.OutputDirectory)
}

// TestPrepareMovingBranchFailsWithoutPublication refuses remote ref drift
// instead of silently updating the caller's pinned source identity.
func TestPrepareMovingBranchFailsWithoutPublication(t *testing.T) {
	fixture := newSourceFixture(t)
	manager, runner := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	validated := false
	manager.validate = func(context.Context, string, string) error { validated = true; return nil }
	runner.beforeFetch = func() {
		sourceWrite(t, fixture.seed, "branch-advanced.txt", "new remote generation\n")
		sourceGit(t, fixture.seed, "add", "--all")
		sourceGit(t, fixture.seed, "commit", "-m", "advance fixture branch")
		sourceGit(t, fixture.seed, "push", "origin", "refs/heads/main")
	}
	receipt, err := manager.Prepare(context.Background(), request)
	if err == nil {
		t.Fatal("moving branch silently selected a different commit")
	}
	if validated {
		t.Fatal("unmatched commit reached component validation")
	}
	assertSourceUnpublished(t, receipt, request.OutputDirectory)
}

// TestPrepareExplicitBranchDoesNotFallBackToTag preserves the requested ref
// namespace even when the same basename exists as a tag.
func TestPrepareExplicitBranchDoesNotFallBackToTag(t *testing.T) {
	fixture := newSourceFixture(t)
	sourceGit(t, fixture.seed, "tag", "only-a-tag")
	sourceGit(t, fixture.seed, "push", "origin", "refs/tags/only-a-tag")
	manager, _ := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	request.Ref = "refs/heads/only-a-tag"
	receipt, err := manager.Prepare(context.Background(), request)
	if err == nil {
		t.Fatal("missing branch fell back to same-name tag")
	}
	assertSourceUnpublished(t, receipt, request.OutputDirectory)
}

// TestPrepareCanceledFetchRetainsStaging preserves incomplete acquisition
// evidence while withholding the final publication directory.
func TestPrepareCanceledFetchRetainsStaging(t *testing.T) {
	fixture := newSourceFixture(t)
	manager, runner := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	runner.fetchError = context.Canceled
	receipt, err := manager.Prepare(context.Background(), request)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled fetch returned %v", err)
	}
	assertSourceUnpublished(t, receipt, request.OutputDirectory)
}

// TestPrepareRejectsExistingDestinationWithoutMutation protects existing
// directories and symlinks before any Git command can run.
func TestPrepareRejectsExistingDestinationWithoutMutation(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "symlink"}[symlink], func(t *testing.T) {
			fixture := newSourceFixture(t)
			manager, runner := fixtureSourceManager(fixture)
			request := fixtureSourceRequest(t, fixture)
			target := request.OutputDirectory
			if symlink {
				target = t.TempDir()
				if err := os.Symlink(target, request.OutputDirectory); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			sourceWrite(t, target, "preserve.txt", "do not replace\n")
			_, err := manager.Prepare(context.Background(), request)
			if err == nil {
				t.Fatal("existing destination accepted")
			}
			if len(runner.commands) != 0 {
				t.Fatal("existing destination crossed Git boundary")
			}
			if b, err := os.ReadFile(filepath.Join(target, "preserve.txt")); err != nil || string(b) != "do not replace\n" {
				t.Fatalf("existing data changed: %q %v", b, err)
			}
		})
	}
}

// TestPreparePublicationRaceDoesNotOverwrite preserves another owner's newly
// created destination and retains the losing validated staging directory.
func TestPreparePublicationRaceDoesNotOverwrite(t *testing.T) {
	fixture := newSourceFixture(t)
	manager, _ := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	manager.beforePublish = func() error {
		if err := os.Mkdir(request.OutputDirectory, 0o700); err != nil {
			return err
		}
		sourceWrite(t, request.OutputDirectory, "winner.txt", "other owner\n")
		return nil
	}
	receipt, err := manager.Prepare(context.Background(), request)
	if err == nil || receipt.Prepared {
		t.Fatalf("publication race was not refused: %+v %v", receipt, err)
	}
	if b, err := os.ReadFile(filepath.Join(request.OutputDirectory, "winner.txt")); err != nil || string(b) != "other owner\n" {
		t.Fatalf("racing owner's data changed: %q %v", b, err)
	}
	if info, err := os.Stat(receipt.StagingDirectory); err != nil || !info.IsDir() {
		t.Fatalf("validated losing staging not retained: %v", err)
	}
}

// TestPrepareBeforePublicationFailureRetainsOnlyStaging distinguishes a refused
// rename from a failure after source has already reached the final directory.
func TestPrepareBeforePublicationFailureRetainsOnlyStaging(t *testing.T) {
	fixture := newSourceFixture(t)
	manager, _ := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	want := errors.New("before-publication refusal")
	manager.beforePublish = func() error { return want }
	receipt, err := manager.Prepare(context.Background(), request)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "incomplete source staging") {
		t.Fatalf("pre-publication failure lost its phase: %v", err)
	}
	assertSourceUnpublished(t, receipt, request.OutputDirectory)
}

// TestPreparePostPublicationSyncFailurePreservesConsistentReceipt retains the
// published source without falsely calling it incomplete staging or success.
func TestPreparePostPublicationSyncFailurePreservesConsistentReceipt(t *testing.T) {
	fixture := newSourceFixture(t)
	manager, _ := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	want := errors.New("parent sync refused")
	manager.syncParent = func(*os.File) error { return want }
	receipt, err := manager.Prepare(context.Background(), request)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "publication rename completed") || strings.Contains(err.Error(), "incomplete source staging") {
		t.Fatalf("post-publication failure lost its phase: %v", err)
	}
	if !receipt.Prepared || !receipt.ComponentValidated || receipt.StagingDirectory != "" || receipt.Commit != fixture.commit || receipt.Tree != fixture.tree {
		t.Fatalf("published source was relabelled as incomplete: %+v", receipt)
	}
	bytes, readErr := os.ReadFile(filepath.Join(request.OutputDirectory, ReceiptName))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var saved Receipt
	if err := json.Unmarshal(bytes, &saved); err != nil || !reflect.DeepEqual(saved, receipt) {
		t.Fatalf("returned and published receipts disagree: %+v %+v %v", receipt, saved, err)
	}
	if got := sourceGit(t, receipt.RepositoryRoot, "rev-parse", "HEAD"); got != fixture.commit {
		t.Fatalf("published source changed: %s", got)
	}
	if _, err := manager.Prepare(context.Background(), request); err == nil {
		t.Fatal("failed final sync allowed an existing publication to be overwritten")
	}
}

// TestPrepareComponentFailureIsNotAuthority refuses to convert a fetched commit
// into publication authority after the maintained input validator rejects it.
func TestPrepareComponentFailureIsNotAuthority(t *testing.T) {
	fixture := newSourceFixture(t)
	manager, _ := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	want := errors.New("component policy refused fixture")
	manager.validate = func(context.Context, string, string) error { return want }
	receipt, err := manager.Prepare(context.Background(), request)
	if err == nil || !errors.Is(err, want) {
		t.Fatalf("component validator failure = %v", err)
	}
	if receipt.ComponentValidated {
		t.Fatal("rejected component marked validated")
	}
	assertSourceUnpublished(t, receipt, request.OutputDirectory)
}

// TestPrepareRejectsSourceMutationDuringValidation detects both changed input
// bytes and a different committed HEAD before publication.
func TestPrepareRejectsSourceMutationDuringValidation(t *testing.T) {
	for _, changeHEAD := range []bool{false, true} {
		t.Run(map[bool]string{false: "worktree", true: "HEAD"}[changeHEAD], func(t *testing.T) {
			fixture := newSourceFixture(t)
			manager, _ := fixtureSourceManager(fixture)
			request := fixtureSourceRequest(t, fixture)
			manager.validate = func(_ context.Context, component, root string) error {
				if component != "camera" {
					t.Fatalf("wrong component validated: %s", component)
				}
				if changeHEAD {
					sourceGit(t, root, "commit", "--allow-empty", "-m", "unexpected validation mutation")
				} else {
					sourceWrite(t, root, "userspace/camera/libcamera/BASE.txt", "unexpected changed authority\n")
				}
				return nil
			}
			receipt, err := manager.Prepare(context.Background(), request)
			if err == nil {
				t.Fatal("source changed during validation was published")
			}
			assertSourceUnpublished(t, receipt, request.OutputDirectory)
		})
	}
}

// TestPrepareRevalidatesInputsHiddenFromGitStatusBeforePublication proves that
// final component authentication does not rely only on Git status flags.
func TestPrepareRevalidatesInputsHiddenFromGitStatusBeforePublication(t *testing.T) {
	fixture := newSourceFixture(t)
	manager, _ := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	validate := manager.validate
	root := ""
	calls := 0
	manager.validate = func(ctx context.Context, component, repository string) error {
		root = repository
		calls++
		return validate(ctx, component, repository)
	}
	manager.beforePublish = func() error {
		if root == "" {
			t.Fatal("publication hook ran before component validation")
		}
		sourceWrite(t, root, "userspace/camera/libcamera/BASE.txt", fixtureCameraBase+"Unexpected input mutation: fixture\n")
		sourceGit(t, root, "update-index", "--assume-unchanged", "userspace/camera/libcamera/BASE.txt")
		if status := sourceGit(t, root, "status", "--porcelain=v1"); status != "" {
			t.Fatalf("fixture mutation is not hidden from status: %s", status)
		}
		return nil
	}
	receipt, err := manager.Prepare(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "revalidate camera") {
		t.Fatalf("hidden input mutation escaped final compiled validation: %v", err)
	}
	if calls != 2 {
		t.Fatalf("component validation calls = %d, want2", calls)
	}
	assertSourceUnpublished(t, receipt, request.OutputDirectory)
}

// TestPrepareCameraSymlinkCannotBecomeBuildInput rejects a tracked symlink in
// place of the regular camera authority without changing its external target.
func TestPrepareCameraSymlinkCannotBecomeBuildInput(t *testing.T) {
	fixture := newSourceFixture(t)
	outside := filepath.Join(t.TempDir(), "BASE.txt")
	if err := os.WriteFile(outside, []byte(fixtureCameraBase), 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(fixture.seed, "userspace/camera/libcamera/BASE.txt")
	if err := os.Remove(base); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, base); err != nil {
		t.Fatal(err)
	}
	sourceGit(t, fixture.seed, "add", "--all")
	sourceGit(t, fixture.seed, "commit", "-m", "symlink fixture input")
	sourceGit(t, fixture.seed, "push", "origin", "refs/heads/main")
	fixture.commit = sourceGit(t, fixture.seed, "rev-parse", "HEAD")
	manager, _ := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	receipt, err := manager.Prepare(context.Background(), request)
	if err == nil {
		t.Fatal("symlink component authority accepted")
	}
	assertSourceUnpublished(t, receipt, request.OutputDirectory)
	if b, err := os.ReadFile(outside); err != nil || string(b) != fixtureCameraBase {
		t.Fatalf("external symlink target changed: %v", err)
	}
}

// TestPrepareIPTSDRetainsCompiledIntegrationPolicy demonstrates that repository
// selection cannot authorise an unreviewed IPTSD integration tree.
func TestPrepareIPTSDRetainsCompiledIntegrationPolicy(t *testing.T) {
	fixture := newSourceFixture(t)
	manager, runner := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	request.Component = "iptsd"
	receipt, err := manager.Prepare(context.Background(), request)
	if err == nil {
		t.Fatal("remote-source opt in authorized unreviewed IPTSD integration")
	}
	assertSourceUnpublished(t, receipt, request.OutputDirectory)
	for _, command := range runner.commands {
		if command.Name != "git" {
			t.Fatalf("rejected IPTSD source reached native build: %+v", command)
		}
	}
}

// TestPrepareDoesNotInitializeUnrelatedSubmodule retains the complete Git tree
// identity without materialising or fetching unrelated submodule contents.
func TestPrepareDoesNotInitializeUnrelatedSubmodule(t *testing.T) {
	fixture := newSourceFixture(t)
	sourceWrite(t, fixture.seed, ".gitmodules", "[submodule \"cli/lexr\"]\n\tpath = cli/lexr\n\turl = "+fixture.remote+"\n")
	sourceGit(t, fixture.seed, "add", ".gitmodules")
	sourceGit(t, fixture.seed, "update-index", "--add", "--cacheinfo", "160000,"+fixture.commit+",cli/lexr")
	sourceGit(t, fixture.seed, "commit", "-m", "unrelated submodule fixture")
	sourceGit(t, fixture.seed, "push", "origin", "refs/heads/main")
	fixture.commit = sourceGit(t, fixture.seed, "rev-parse", "HEAD")
	fixture.tree = sourceGit(t, fixture.seed, "rev-parse", "HEAD^{tree}")
	manager, runner := fixtureSourceManager(fixture)
	receipt, err := manager.Prepare(context.Background(), fixtureSourceRequest(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Prepared || receipt.Commit != fixture.commit || receipt.Tree != fixture.tree {
		t.Fatalf("unrelated Git link changed complete source identity: %+v", receipt)
	}
	for _, relative := range []string{"cli/lexr", ".git/modules", ".gitmodules"} {
		if _, err := os.Lstat(filepath.Join(receipt.RepositoryRoot, filepath.FromSlash(relative))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unrelated submodule path was materialized: %s: %v", relative, err)
		}
	}
	for _, command := range runner.commands {
		isFetch, forbidsRecursion := false, false
		for _, argument := range command.Args {
			if argument == "submodule" {
				t.Fatalf("source preparation initialized a submodule: %+v", command)
			}
			isFetch = isFetch || argument == "fetch"
			forbidsRecursion = forbidsRecursion || argument == "--no-recurse-submodules"
		}
		if isFetch && !forbidsRecursion {
			t.Fatalf("source fetch did not explicitly disable submodule recursion: %+v", command)
		}
	}
}

// TestPrepareRejectsGitlinkAtRequiredInputBeforeValidation blocks a submodule
// entry from replacing a required regular component input.
func TestPrepareRejectsGitlinkAtRequiredInputBeforeValidation(t *testing.T) {
	fixture := newSourceFixture(t)
	sourceGit(t, fixture.seed, "update-index", "--add", "--cacheinfo", "160000,"+fixture.commit+",userspace/camera/libcamera/BASE.txt")
	sourceGit(t, fixture.seed, "commit", "-m", "required input Git link fixture")
	sourceGit(t, fixture.seed, "push", "origin", "refs/heads/main")
	fixture.commit = sourceGit(t, fixture.seed, "rev-parse", "HEAD")
	manager, _ := fixtureSourceManager(fixture)
	validated := false
	manager.validate = func(context.Context, string, string) error { validated = true; return nil }
	request := fixtureSourceRequest(t, fixture)
	receipt, err := manager.Prepare(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "regular blobs") {
		t.Fatalf("Git link at required component input was not rejected: %v", err)
	}
	if validated {
		t.Fatal("Git link reached component validation")
	}
	assertSourceUnpublished(t, receipt, request.OutputDirectory)
}

// TestOptionalPrepareAcceptsCurrentIPTSDIntegration exercises the unchanged
// complete compiled policy when the caller explicitly supplies a trusted OE
// checkout. It reads that checkout and copies only into a fresh Git fixture.
func TestOptionalPrepareAcceptsCurrentIPTSDIntegration(t *testing.T) {
	trustedRoot := strings.TrimSpace(os.Getenv("LEXR_TEST_OE_ROOT"))
	if trustedRoot == "" {
		t.Skip("set LEXR_TEST_OE_ROOT for complete IPTSD source-selection validation")
	}
	if !filepath.IsAbs(trustedRoot) {
		t.Fatal("LEXR_TEST_OE_ROOT must be an absolute path")
	}
	integration := filepath.Join(filepath.Clean(trustedRoot), "userspace", "iptsd-sp11")
	if err := userspaceiptsd.ValidateRepositoryIntegration(integration); err != nil {
		t.Fatalf("trusted OE integration does not satisfy existing compiled policy: %v", err)
	}
	fixture := newSourceFixture(t)
	err := filepath.WalkDir(integration, func(file string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return errors.New("trusted integration entry must be a bounded regular file")
		}
		relative, err := filepath.Rel(integration, file)
		if err != nil {
			return err
		}
		contents, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		sourceWrite(t, fixture.seed, filepath.Join("userspace", "iptsd-sp11", relative), string(contents))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sourceGit(t, fixture.seed, "add", "userspace/iptsd-sp11")
	sourceGit(t, fixture.seed, "commit", "-m", "exact compiled IPTSD integration fixture")
	sourceGit(t, fixture.seed, "push", "origin", "refs/heads/main")
	fixture.commit = sourceGit(t, fixture.seed, "rev-parse", "HEAD")
	fixture.tree = sourceGit(t, fixture.seed, "rev-parse", "HEAD^{tree}")
	manager, _ := fixtureSourceManager(fixture)
	request := fixtureSourceRequest(t, fixture)
	request.Component = "iptsd"
	receipt, err := manager.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Prepared || !receipt.ComponentValidated || receipt.Commit != fixture.commit || receipt.Tree != fixture.tree {
		t.Fatalf("IPTSD source identity = %+v", receipt)
	}
	if err := userspaceiptsd.ValidateRepositoryIntegration(filepath.Join(receipt.RepositoryRoot, "userspace", "iptsd-sp11")); err != nil {
		t.Fatalf("published integration differs from existing compiled policy: %v", err)
	}
	if err := userspaceiptsd.ValidateRepositoryIntegration(integration); err != nil {
		t.Fatalf("read-only trusted integration changed: %v", err)
	}
}

// TestPrepareSourceSelectionPreservesUnrelatedRepository verifies that acquiring
// a new checkout does not change the seed or its bare remote ref.
func TestPrepareSourceSelectionPreservesUnrelatedRepository(t *testing.T) {
	fixture := newSourceFixture(t)
	before := []string{sourceGit(t, fixture.seed, "rev-parse", "HEAD"), sourceGit(t, fixture.seed, "status", "--porcelain=v1"), sourceGit(t, fixture.remote, "rev-parse", "refs/heads/main")}
	manager, _ := fixtureSourceManager(fixture)
	if _, err := manager.Prepare(context.Background(), fixtureSourceRequest(t, fixture)); err != nil {
		t.Fatal(err)
	}
	after := []string{sourceGit(t, fixture.seed, "rev-parse", "HEAD"), sourceGit(t, fixture.seed, "status", "--porcelain=v1"), sourceGit(t, fixture.remote, "rev-parse", "refs/heads/main")}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("preparation mutated original repository: before=%v after=%v", before, after)
	}
}
