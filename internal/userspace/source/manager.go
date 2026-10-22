// Package source prepares explicitly pinned support checkouts without granting
// build, installation, or hardware authority to a repository or branch.
package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	camerabuild "github.com/ooaklee/lexr.sh/internal/camera/build"
	camerarelease "github.com/ooaklee/lexr.sh/internal/camera/release"
	"github.com/ooaklee/lexr.sh/internal/platform"
	userspaceiptsd "github.com/ooaklee/lexr.sh/internal/userspace/iptsd"
)

// ReceiptName is source-selection evidence, never an installation authority.
const ReceiptName = "lexr-userspace-source.json"

// commitExpression requires a complete lowercase Git SHA-1 object identity.
var commitExpression = regexp.MustCompile(`^[0-9a-f]{40}$`)

// refExpression admits only fully qualified branch or tag names.
var refExpression = regexp.MustCompile(`^refs/(heads|tags)/[A-Za-z0-9][A-Za-z0-9._/-]{0,240}$`)

// repositoryPathExpression excludes URL syntax from the repository path.
var repositoryPathExpression = regexp.MustCompile(`^/[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// Request selects one public support repository, not an arbitrary installer.
type Request struct {
	Component       string
	Experimental    bool
	Repository      string
	Ref             string
	Commit          string
	OutputDirectory string
	DryRun          bool
}

// Receipt distinguishes an unverified acquisition plan from a prepared source.
// Commit and Tree are absent in dry-run output: only ExpectedCommit is known.
// Prepared records validated source and a completed publication rename. Callers
// must also require a nil error: final publication checks can fail afterwards.
type Receipt struct {
	SchemaVersion      int       `json:"schema_version"`
	Component          string    `json:"component"`
	Repository         string    `json:"repository"`
	Ref                string    `json:"ref"`
	ExpectedCommit     string    `json:"expected_commit"`
	Commit             string    `json:"commit,omitempty"`
	Tree               string    `json:"tree,omitempty"`
	Directory          string    `json:"directory"`
	RepositoryRoot     string    `json:"repository_root"`
	StagingDirectory   string    `json:"staging_directory,omitempty"`
	Validation         string    `json:"validation,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	DryRun             bool      `json:"dry_run"`
	Prepared           bool      `json:"prepared"`
	ComponentValidated bool      `json:"component_validated"`
}

// Manager owns source acquisition only. Its runner is private so production
// cannot accidentally substitute an inherited-environment Git boundary.
type Manager struct {
	runner        platform.Runner
	validate      func(context.Context, string, string) error
	beforePublish func() error
	syncParent    func(*os.File) error
}

// New returns the isolated Git boundary and unchanged component validators.
func New() *Manager {
	m := &Manager{runner: &isolatedGitRunner{}}
	m.validate = m.validateComponent
	return m
}

// Prepare verifies one fetched ref against the caller's full expected commit,
// validates component inputs, and publishes a new detached support checkout.
// Pre-publication failures retain private staging; it must not be resumed.
// Errors after the rename identify the published destination and still fail.
func (m *Manager) Prepare(ctx context.Context, request Request) (receipt Receipt, resultErr error) {
	if err := validateRequest(request); err != nil {
		return receipt, err
	}
	if m == nil || m.runner == nil || m.validate == nil {
		return receipt, errors.New("userspace source preparer is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	destination, err := newDestination(request.OutputDirectory)
	if err != nil {
		return receipt, err
	}
	receipt = Receipt{SchemaVersion: 1, Component: request.Component, Repository: request.Repository,
		Ref: request.Ref, ExpectedCommit: request.Commit, Directory: destination,
		RepositoryRoot: filepath.Join(destination, "repository"), CreatedAt: time.Now().UTC(), DryRun: request.DryRun}
	if request.DryRun {
		return receipt, nil
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return receipt, errors.New("userspace source publication is supported only on Linux and macOS")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	parentPath := filepath.Dir(destination)
	parent, err := os.Open(parentPath)
	if err != nil {
		return receipt, fmt.Errorf("open source publication parent: %w", err)
	}
	defer parent.Close()
	parentInfo, err := parent.Stat()
	if err != nil || !parentInfo.IsDir() {
		return receipt, errors.New("source publication parent is not a directory")
	}
	stage, err := os.MkdirTemp(parentPath, ".lexr-userspace-source-")
	if err != nil {
		return receipt, fmt.Errorf("create private source staging directory: %w", err)
	}
	receipt.StagingDirectory = stage
	stageInfo, err := os.Lstat(stage)
	if err != nil || !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
		return receipt, errors.New("could not identify private source staging directory")
	}
	published := false
	defer func() {
		if resultErr != nil {
			if published {
				resultErr = fmt.Errorf("%w; validated source publication rename completed for %s, but final verification failed; inspect the destination before use", resultErr, destination)
			} else {
				resultErr = fmt.Errorf("%w; incomplete source staging retained at %s (not resumable)", resultErr, stage)
			}
		}
	}()
	root := filepath.Join(stage, "repository")
	if err := m.run(ctx, stage, "init", "--quiet", "--template=", "repository"); err != nil {
		return receipt, fmt.Errorf("initialize isolated source repository: %w", err)
	}
	if err := m.run(ctx, root, "fetch", "--quiet", "--depth=1", "--no-tags", "--no-recurse-submodules", "--", request.Repository, request.Ref); err != nil {
		return receipt, fmt.Errorf("fetch explicit userspace ref: %w", err)
	}
	commit, err := m.capture(ctx, root, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil || !commitExpression.MatchString(commit) {
		return receipt, errors.New("fetched ref does not resolve to a complete commit")
	}
	if commit != request.Commit {
		return receipt, fmt.Errorf("ref resolved to commit %s, not expected %s; annotated tags require the peeled commit SHA, not the tag-object SHA", commit, request.Commit)
	}
	if err := m.requireComponentBlobs(ctx, root, request.Component, commit); err != nil {
		return receipt, err
	}
	// These are fixed component patterns, not repository-provided instructions.
	// Configure sparse checkout before the first checkout so unrelated source
	// files, filters, submodules and case-distinct paths are not materialised.
	info := filepath.Join(root, ".git", "info")
	if err := os.Mkdir(info, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return receipt, err
	}
	if err := writeGeneratedOutputExclusions(root, request.Component); err != nil {
		return receipt, err
	}
	patterns := "/userspace/iptsd-sp11/\n"
	if request.Component == "camera" {
		patterns = ""
		for _, p := range camerabuild.TrackedInputPaths() {
			patterns += "/" + p + "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(info, "sparse-checkout"), []byte(patterns), 0o600); err != nil {
		return receipt, err
	}
	for _, setting := range [][2]string{{"core.sparseCheckout", "true"}, {"core.sparseCheckoutCone", "false"}} {
		if err := m.run(ctx, root, "config", "--local", setting[0], setting[1]); err != nil {
			return receipt, err
		}
	}
	if err := m.run(ctx, root, "checkout", "--quiet", "--detach", commit); err != nil {
		return receipt, fmt.Errorf("checkout pinned userspace commit: %w", err)
	}
	tree, err := m.capture(ctx, root, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil || !commitExpression.MatchString(tree) {
		return receipt, errors.New("could not identify the complete committed source tree")
	}
	if err := m.validate(ctx, request.Component, root); err != nil {
		return receipt, fmt.Errorf("validate %s source inputs: %w", request.Component, err)
	}
	if err := m.checkSource(ctx, root, request.Component, commit, tree); err != nil {
		return receipt, err
	}
	if m.beforePublish != nil {
		if err := m.beforePublish(); err != nil {
			return receipt, err
		}
	}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	if err := sameDirectory(parentPath, parentInfo); err != nil {
		return receipt, err
	}
	if err := sameDirectory(stage, stageInfo); err != nil {
		return receipt, err
	}
	if err := m.validate(ctx, request.Component, root); err != nil {
		return receipt, fmt.Errorf("revalidate %s source inputs before publication: %w", request.Component, err)
	}
	if err := m.checkSource(ctx, root, request.Component, commit, tree); err != nil {
		return receipt, err
	}
	completed := receipt
	completed.Commit, completed.Tree = commit, tree
	completed.ComponentValidated, completed.Prepared = true, true
	completed.StagingDirectory = ""
	completed.Validation = request.Component + "-compiled-source-input-contract"
	data, err := json.MarshalIndent(completed, "", "  ")
	if err != nil {
		return receipt, err
	}
	file, err := os.OpenFile(filepath.Join(stage, ReceiptName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return receipt, err
	}
	_, writeErr := file.Write(append(data, '\n'))
	syncErr, closeErr := file.Sync(), file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return receipt, err
	}
	if err := publishNoReplace(parent, filepath.Base(stage), filepath.Base(destination)); err != nil {
		return receipt, fmt.Errorf("publish userspace source without replacement: %w", err)
	}
	// The validated directory has moved. Keep the returned and on-disk receipts
	// consistent even if final verification fails, and distinguish that error
	// from incomplete staging. A nil error is still required for CLI success.
	published = true
	receipt = completed
	if err := sameDirectory(parentPath, parentInfo); err != nil {
		return receipt, err
	}
	if err := sameDirectory(destination, stageInfo); err != nil {
		return receipt, err
	}
	syncParent := m.syncParent
	if syncParent == nil {
		syncParent = func(directory *os.File) error { return directory.Sync() }
	}
	if err := syncParent(parent); err != nil {
		return receipt, fmt.Errorf("flush source publication parent: %w", err)
	}
	return receipt, nil
}

// validateRequest rejects unsupported capabilities before acquisition work.
func validateRequest(r Request) error {
	if r.Component != "camera" && r.Component != "iptsd" {
		return fmt.Errorf("component %q has no supported source selection contract", r.Component)
	}
	if !r.Experimental {
		return errors.New("userspace source selection requires explicit --experimental opt-in")
	}
	u, err := url.Parse(r.Repository)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.String() != r.Repository ||
		!repositoryPathExpression.MatchString(u.Path) || len(u.Path) > 1024 || len(r.Repository) > 2048 || strings.ContainsAny(r.Repository, "\r\n\t\\%") {
		return errors.New("repository must be a canonical public HTTPS Git URL without credentials, query, fragment, escapes or control bytes")
	}
	if err := validPathComponents(strings.TrimPrefix(u.Path, "/")); err != nil {
		return errors.New("repository URL contains an empty or traversal path component")
	}
	if !refExpression.MatchString(r.Ref) || strings.Contains(r.Ref, "..") || strings.Contains(r.Ref, "@{") || strings.HasSuffix(r.Ref, ".") {
		return errors.New("ref must be an explicit refs/heads/<branch> or refs/tags/<tag> without revision expressions")
	}
	if err := validPathComponents(r.Ref); err != nil {
		return errors.New("ref contains an empty, hidden, traversal or lock component")
	}
	for _, p := range strings.Split(r.Ref, "/") {
		if strings.HasPrefix(p, ".") || strings.HasSuffix(p, ".lock") {
			return errors.New("ref contains an empty, hidden, traversal or lock component")
		}
	}
	if !commitExpression.MatchString(r.Commit) {
		return errors.New("commit must be the full 40-character lowercase peeled commit SHA")
	}
	if r.OutputDirectory == "" {
		return errors.New("an explicit new --output-dir is required")
	}
	return nil
}

// validPathComponents rejects empty and traversal segments before acquisition.
func validPathComponents(value string) error {
	for _, p := range strings.Split(value, "/") {
		if p == "" || p == "." || p == ".." {
			return errors.New("invalid path component")
		}
	}
	return nil
}

// newDestination resolves an existing parent without creating or replacing it.
func newDestination(configured string) (string, error) {
	abs, err := filepath.Abs(configured)
	if err != nil {
		return "", err
	}
	if abs == string(filepath.Separator) {
		return "", errors.New("source output must be a new dedicated directory")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("source output parent must already exist: %w", err)
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return "", errors.New("source output parent must be a directory")
	}
	destination := filepath.Join(parent, filepath.Base(abs))
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("source output must not already exist (including links)")
	}
	return destination, nil
}

// sameDirectory checks that an anchored publication path has not been replaced.
func sameDirectory(name string, expected os.FileInfo) error {
	current, err := os.Lstat(name)
	if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(current, expected) {
		return errors.New("source publication directory identity changed")
	}
	return nil
}

// run invokes a compiled Git operation through the isolated source boundary.
func (m *Manager) run(ctx context.Context, dir string, args ...string) error {
	return m.runner.Run(ctx, platform.Command{Name: "git", Dir: dir, Args: args})
}

// capture reads bounded Git metadata, removing only its terminal newline.
func (m *Manager) capture(ctx context.Context, dir string, args ...string) (string, error) {
	output, err := m.runner.Capture(ctx, platform.Command{Name: "git", Dir: dir, Args: args})
	return strings.TrimSuffix(string(output), "\n"), err
}

// requireComponentBlobs excludes symlinks/gitlinks at the selected input paths.
// Unrelated gitlinks are not initialised and confer no source authority.
func (m *Manager) requireComponentBlobs(ctx context.Context, root, component, commit string) error {
	paths := []string{"userspace/iptsd-sp11"}
	if component == "camera" {
		paths = camerabuild.TrackedInputPaths()
	}
	args := append([]string{"ls-tree", "-r", "-z", commit, "--"}, paths...)
	listing, err := m.capture(ctx, root, args...)
	if err != nil || listing == "" {
		return errors.New("selected component has no committed source input blobs")
	}
	seen := make(map[string]bool)
	for _, row := range strings.Split(strings.TrimSuffix(listing, "\x00"), "\x00") {
		fields := strings.SplitN(row, "\t", 2)
		if len(fields) != 2 {
			return errors.New("malformed component source tree metadata")
		}
		metadata := strings.Fields(fields[0])
		if len(metadata) != 3 || (metadata[0] != "100644" && metadata[0] != "100755") || metadata[1] != "blob" || !commitExpression.MatchString(metadata[2]) {
			return errors.New("selected component inputs must be committed regular blobs, not links or submodules")
		}
		seen[fields[1]] = true
	}
	if component == "camera" {
		for _, name := range paths {
			if !seen[name] {
				return errors.New("camera source is missing a required tracked input")
			}
		}
	}
	return nil
}

// validateComponent reuses source contracts without native build or deployment.
func (m *Manager) validateComponent(ctx context.Context, component, root string) error {
	if component == "iptsd" {
		return userspaceiptsd.ValidateRepositoryIntegration(filepath.Join(root, "userspace", "iptsd-sp11"))
	}
	return camerabuild.AuthenticateSource(ctx, m.runner, root)
}

// generatedOutputDirectories names only compiled default output directories,
// never a source path or an operator-selected publication destination.
func generatedOutputDirectories(component string) []string {
	if component == "camera" {
		return []string{camerabuild.DefaultWorkDirectory, camerabuild.DefaultOutputDirectory, camerarelease.DefaultOutputDirectory}
	}
	if component == "iptsd" {
		// This exact default is owned by prepareIPTSDOutput in userspace/build.
		return []string{"build/iptsd-sp11"}
	}
	return nil
}

// generatedOutputExclusions ignores only compiled default output directories.
// Source repository ignore files and operator-selected output paths are not
// imported into the prepared checkout's cleanliness boundary.
func generatedOutputExclusions(component string) string {
	var contents strings.Builder
	for _, directory := range generatedOutputDirectories(component) {
		contents.WriteString("/" + directory + "/\n")
	}
	return contents.String()
}

// writeGeneratedOutputExclusions creates private, machine-owned Git metadata
// in the new repository without evaluating or replacing repository rules.
func writeGeneratedOutputExclusions(root, component string) error {
	contents := generatedOutputExclusions(component)
	if contents == "" {
		return errors.New("component has no compiled generated-output exclusions")
	}
	file, err := os.OpenFile(filepath.Join(root, ".git", "info", "exclude"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create private generated-output exclusions: %w", err)
	}
	_, writeErr := io.WriteString(file, contents)
	return errors.Join(writeErr, file.Sync(), file.Close())
}

// validateGeneratedOutputExclusions refuses altered or replaced ignore metadata
// before source publication; it never permits a broader path or rule set.
func validateGeneratedOutputExclusions(root, component string) error {
	expected := generatedOutputExclusions(component)
	filePath := filepath.Join(root, ".git", "info", "exclude")
	info, err := os.Lstat(filePath)
	if err != nil || expected == "" || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != int64(len(expected)) {
		return errors.New("prepared generated-output exclusions are missing or changed")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return errors.New("prepared generated-output exclusions cannot be inspected")
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) || !actual.Mode().IsRegular() || actual.Mode().Perm() != 0o600 {
		return errors.New("prepared generated-output exclusions changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, int64(len(expected)+1)))
	if err != nil || string(contents) != expected {
		return errors.New("prepared generated-output exclusions differ from compiled policy")
	}
	return nil
}

// checkSource fences commit, full tree, detached HEAD, owned exclusions and
// checkout mutation without treating generated output as source authority.
func (m *Manager) checkSource(ctx context.Context, root, component, commit, tree string) error {
	if err := validateGeneratedOutputExclusions(root, component); err != nil {
		return err
	}
	for _, check := range [][2]string{{"HEAD^{commit}", commit}, {"HEAD^{tree}", tree}} {
		actual, err := m.capture(ctx, root, "rev-parse", "--verify", check[0])
		if err != nil || actual != check[1] {
			return errors.New("pinned userspace commit/tree changed during validation")
		}
	}
	detached, err := m.capture(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || detached != "HEAD" {
		return errors.New("prepared userspace source must have detached HEAD")
	}
	status, err := m.capture(ctx, root, "status", "--porcelain=v1", "--untracked-files=no")
	if err != nil || status != "" {
		return errors.New("prepared userspace source has changed tracked files")
	}
	// Sparse Git may consult a committed .gitignore from the index even when
	// that file is not materialised. Enumerate without --exclude-standard so
	// repository rules cannot hide unrelated files from this source fence.
	untracked, err := m.capture(ctx, root, "ls-files", "--others", "-z")
	if err != nil {
		return errors.New("prepared userspace untracked files cannot be inspected")
	}
	if untracked == "" {
		return nil
	}
	if !strings.HasSuffix(untracked, "\x00") {
		return errors.New("prepared userspace untracked file metadata is malformed")
	}
	for _, relative := range strings.Split(strings.TrimSuffix(untracked, "\x00"), "\x00") {
		generated := false
		for _, directory := range generatedOutputDirectories(component) {
			generated = generated || strings.HasPrefix(relative, directory+"/")
		}
		if !generated {
			return errors.New("prepared userspace source has untracked files outside compiled default outputs")
		}
	}
	return nil
}
