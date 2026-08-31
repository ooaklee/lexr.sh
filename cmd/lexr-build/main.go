// Command lexr-build produces a local Lexr executable with explicit source
// metadata, including when the source is checked out as a Git submodule.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

const (
	// moduleDeclaration is the exact module line required at the selected root.
	moduleDeclaration = "module github.com/ooaklee/lexr.sh"
	// versionVariable receives the helper's descriptive source version.
	versionVariable = "github.com/ooaklee/lexr.sh/internal/version.Version"
	// commitVariable receives the helper's full Lexr source revision.
	commitVariable = "github.com/ooaklee/lexr.sh/internal/version.Commit"
	// dateVariable receives the helper's UTC build time.
	dateVariable = "github.com/ooaklee/lexr.sh/internal/version.Date"
)

var (
	// commitExpression accepts full SHA-1 and SHA-256 Git object identities.
	commitExpression = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	// versionExpression permits one bounded, linker-safe Git description.
	versionExpression = regexp.MustCompile(
		`^[A-Za-z0-9][A-Za-z0-9._+~/-]{0,126}([A-Za-z0-9._+~/-]|-dirty)$`,
	)
)

// buildMetadata contains the explicit values injected into a local binary.
type buildMetadata struct {
	// version is an exact tag or descriptive Git revision, with a dirty suffix
	// when the source checkout contains local changes.
	version string
	// commit is the full Lexr source revision or unknown outside a Git checkout.
	commit string
	// date is the actual UTC build time.
	date string
}

// buildRequest contains the complete local source-build request.
type buildRequest struct {
	// repositoryRoot is the expected Lexr module root.
	repositoryRoot string
	// output is the requested executable path, relative to the module by default.
	output string
}

// buildResult records the completed executable and its injected metadata.
type buildResult struct {
	// output is the canonical built executable path.
	output string
	// metadata is the exact identity injected by the linker.
	metadata buildMetadata
}

// main runs the local source builder and returns its bounded process status.
func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, platform.ExecRunner{}, time.Now))
}

// run parses build-helper arguments and renders one concise result or error.
func run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	runner platform.Runner,
	now func() time.Time,
) int {
	flags := flag.NewFlagSet("lexr-build", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repositoryRoot := flags.String("repository-root", ".", "Lexr source root containing go.mod")
	output := flags.String("output", defaultOutput(), "built executable path, relative to the Lexr source root")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "lexr-build accepts flags only")
		return 2
	}
	if now == nil {
		_, _ = fmt.Fprintln(stderr, "lexr-build has no clock")
		return 1
	}

	result, err := build(ctx, runner, now, buildRequest{
		repositoryRoot: *repositoryRoot,
		output:         *output,
	}, stdout, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "build Lexr: %v\n", err)
		return 1
	}
	_, err = fmt.Fprintf(stdout,
		"built Lexr\npath: %s\nversion: %s\ncommit: %s\nbuilt: %s\n",
		result.output,
		result.metadata.version,
		result.metadata.commit,
		result.metadata.date,
	)
	if err != nil {
		return 1
	}
	return 0
}

// build validates the selected source, derives trusted metadata and invokes Go
// without its ambiguous automatic VCS stamping.
func build(
	ctx context.Context,
	runner platform.Runner,
	now func() time.Time,
	request buildRequest,
	stdout io.Writer,
	stderr io.Writer,
) (buildResult, error) {
	if runner == nil {
		return buildResult{}, errors.New("no process runner is configured")
	}
	root, err := resolveModuleRoot(request.repositoryRoot)
	if err != nil {
		return buildResult{}, err
	}
	output, err := resolveOutput(root, request.output)
	if err != nil {
		return buildResult{}, err
	}
	metadata, err := inspectMetadata(ctx, runner, root, now().UTC())
	if err != nil {
		return buildResult{}, err
	}
	if err := prepareOutput(output); err != nil {
		return buildResult{}, err
	}

	command := platform.Command{
		Name:   "go",
		Args:   buildArguments(metadata, output),
		Dir:    root,
		Env:    []string{"CGO_ENABLED=0"},
		Stdout: stdout,
		Stderr: stderr,
	}
	if err := runner.Run(ctx, command); err != nil {
		return buildResult{}, err
	}
	info, err := os.Lstat(output)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
		return buildResult{}, fmt.Errorf("Go did not produce a non-empty regular executable: %s", output)
	}
	return buildResult{output: output, metadata: metadata}, nil
}

// resolveModuleRoot returns one canonical directory containing Lexr's exact Go
// module declaration.
func resolveModuleRoot(candidate string) (string, error) {
	if strings.TrimSpace(candidate) == "" {
		return "", errors.New("repository root is empty")
	}
	root, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("canonicalise repository root: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("repository root is not a real directory: %s", root)
	}
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read Lexr go.mod: %w", err)
	}
	if !bytes.Contains(module, []byte(moduleDeclaration+"\n")) {
		return "", errors.New("repository root is not the Lexr Go module")
	}
	return root, nil
}

// resolveOutput makes a caller-selected output path absolute without changing
// its requested location.
func resolveOutput(root, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", errors.New("output path is empty")
	}
	if !filepath.IsAbs(requested) {
		requested = filepath.Join(root, requested)
	}
	output, err := filepath.Abs(requested)
	if err != nil {
		return "", fmt.Errorf("resolve output path: %w", err)
	}
	if filepath.Clean(output) == root {
		return "", errors.New("output path cannot replace the repository root")
	}
	return filepath.Clean(output), nil
}

// inspectMetadata derives an exact Lexr Git identity when repository metadata
// is available and otherwise returns honest development defaults.
func inspectMetadata(
	ctx context.Context,
	runner platform.Runner,
	root string,
	buildTime time.Time,
) (buildMetadata, error) {
	metadata := buildMetadata{
		version: "dev",
		commit:  "unknown",
		date:    buildTime.Format(time.RFC3339),
	}
	gitEntry, err := os.Lstat(filepath.Join(root, ".git"))
	if errors.Is(err, os.ErrNotExist) {
		return metadata, nil
	}
	if err != nil {
		return buildMetadata{}, fmt.Errorf("inspect Lexr Git metadata: %w", err)
	}
	if gitEntry.Mode()&os.ModeSymlink != 0 || !gitEntry.IsDir() && !gitEntry.Mode().IsRegular() {
		return buildMetadata{}, errors.New("Lexr .git entry is neither a directory nor a pointer file")
	}

	commit, err := captureSingleLine(ctx, runner, root, "git", "rev-parse", "--verify", "HEAD")
	if err != nil || !commitExpression.MatchString(commit) {
		return buildMetadata{}, errors.New("Git did not return a full Lexr source revision")
	}
	version, err := captureSingleLine(ctx, runner, root, "git", "describe", "--tags", "--always")
	if err != nil {
		return buildMetadata{}, fmt.Errorf("describe Lexr source: %w", err)
	}
	status, err := runner.Capture(ctx, platform.Command{
		Name: "git",
		Args: []string{"status", "--porcelain=v1", "--untracked-files=normal", "--", "."},
		Dir:  root,
	})
	if err != nil {
		return buildMetadata{}, fmt.Errorf("inspect Lexr source status: %w", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		version += "-dirty"
	}
	if !versionExpression.MatchString(version) {
		return buildMetadata{}, errors.New("Git returned an unsafe descriptive Lexr version")
	}
	metadata.version = version
	metadata.commit = commit
	return metadata, nil
}

// captureSingleLine executes one argument-separated metadata query and rejects
// empty or multiline output.
func captureSingleLine(
	ctx context.Context,
	runner platform.Runner,
	directory string,
	name string,
	args ...string,
) (string, error) {
	output, err := runner.Capture(ctx, platform.Command{Name: name, Args: args, Dir: directory})
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("metadata command returned empty or multiline output")
	}
	return value, nil
}

// prepareOutput creates the output parent and refuses symbolic or non-regular
// existing destinations before Go is allowed to replace one.
func prepareOutput(output string) error {
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	info, err := os.Lstat(output)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect output path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("output path is not a regular file: %s", output)
	}
	return nil
}

// buildArguments constructs the fixed Go build command and explicit linker
// assignments used for every local source build.
func buildArguments(metadata buildMetadata, output string) []string {
	linkerFlags := strings.Join([]string{
		"-s",
		"-w",
		"-X", versionVariable + "=" + metadata.version,
		"-X", commitVariable + "=" + metadata.commit,
		"-X", dateVariable + "=" + metadata.date,
	}, " ")
	return []string{
		"build",
		"-buildvcs=false",
		"-trimpath",
		"-ldflags", linkerFlags,
		"-o", output,
		"./cmd/lexr",
	}
}

// defaultOutput returns the conventional ignored local executable path for the
// current host operating system.
func defaultOutput() string {
	if runtime.GOOS == "windows" {
		return filepath.Join("bin", "lexr.exe")
	}
	return filepath.Join("bin", "lexr")
}
