package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// fakeBuildRunner records the helper's argument-separated Git and Go commands.
type fakeBuildRunner struct {
	// commands contains every mutating command passed to Run.
	commands []platform.Command
	// status is the synthetic Git porcelain output.
	status string
}

// Capture returns deterministic Git metadata for one selected Lexr checkout.
func (runner *fakeBuildRunner) Capture(_ context.Context, command platform.Command) ([]byte, error) {
	switch strings.Join(command.Args, " ") {
	case "rev-parse --verify HEAD":
		return []byte("39ba8053de7ff016aa4274deb29895a00f7a2d3c\n"), nil
	case "describe --tags --always":
		return []byte("39ba805\n"), nil
	case "status --porcelain=v1 --untracked-files=normal -- .":
		return []byte(runner.status), nil
	default:
		return nil, fmt.Errorf("unexpected capture command: %s %s", command.Name, strings.Join(command.Args, " "))
	}
}

// Run records the Go build command and creates its requested test executable.
func (runner *fakeBuildRunner) Run(_ context.Context, command platform.Command) error {
	runner.commands = append(runner.commands, command)
	for index, argument := range command.Args {
		if argument == "-o" && index+1 < len(command.Args) {
			return os.WriteFile(command.Args[index+1], []byte("test executable"), 0o755)
		}
	}
	return fmt.Errorf("Go command has no output argument: %v", command.Args)
}

// TestRunBuildsWithExplicitSubmoduleMetadata verifies that the documented
// helper disables Go's VCS discovery and injects the selected Lexr identity.
func TestRunBuildsWithExplicitSubmoduleMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(moduleDeclaration+"\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../selected-module\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeBuildRunner{}
	var stdout, stderr bytes.Buffer
	status := run(
		context.Background(),
		[]string{"--repository-root", root, "--output", filepath.Join("bin", "lexr")},
		&stdout,
		&stderr,
		runner,
		func() time.Time { return time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC) },
	)
	if status != 0 {
		t.Fatalf("run() status = %d, stderr = %q", status, stderr.String())
	}
	if len(runner.commands) != 1 {
		t.Fatalf("Run commands = %d, want one", len(runner.commands))
	}
	command := runner.commands[0]
	arguments := strings.Join(command.Args, " ")
	for _, required := range []string{
		"-buildvcs=false",
		versionVariable + "=39ba805",
		commitVariable + "=39ba8053de7ff016aa4274deb29895a00f7a2d3c",
		dateVariable + "=2026-08-31T08:00:00Z",
	} {
		if !strings.Contains(arguments, required) {
			t.Errorf("Go build arguments omit %q: %s", required, arguments)
		}
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if command.Dir != canonicalRoot {
		t.Errorf("Go build directory = %q, want %q", command.Dir, canonicalRoot)
	}
	if len(command.Env) != 1 || command.Env[0] != "CGO_ENABLED=0" {
		t.Errorf("Go build environment = %v, want CGO_ENABLED=0", command.Env)
	}
	if !strings.Contains(stdout.String(), "commit: 39ba8053de7ff016aa4274deb29895a00f7a2d3c") {
		t.Errorf("helper output omits selected Lexr commit: %q", stdout.String())
	}
}

// TestInspectMetadataMarksChangedSource verifies that tracked or untracked
// source changes cannot share the clean checkout's descriptive version.
func TestInspectMetadataMarksChangedSource(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../selected-module\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &fakeBuildRunner{status: "?? local-change.go\n"}
	metadata, err := inspectMetadata(
		context.Background(),
		runner,
		root,
		time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.version != "39ba805-dirty" {
		t.Fatalf("version = %q, want dirty descriptive version", metadata.version)
	}
}

// TestInspectMetadataWithoutGitFailsClosed verifies that an exported source
// archive remains buildable without inventing commit provenance.
func TestInspectMetadataWithoutGitFailsClosed(t *testing.T) {
	metadata, err := inspectMetadata(
		context.Background(),
		&fakeBuildRunner{},
		t.TempDir(),
		time.Date(2026, 8, 31, 8, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.version != "dev" || metadata.commit != "unknown" || metadata.date != "2026-08-31T08:00:00Z" {
		t.Fatalf("metadata = %#v, want honest archive defaults", metadata)
	}
}
