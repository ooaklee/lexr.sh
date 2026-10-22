package fedora

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// companionExtractionRunner simulates ISO extraction without Docker. It
// supports both individual files and trees so the former hash-only validator
// demonstrably accepts the undeclared-entry regression fixture.
type companionExtractionRunner struct {
	workspace     string
	extractedTree bool
}

// Run maps only the compiled xorriso extraction operation into private fixtures.
func (r *companionExtractionRunner) Run(_ context.Context, command platform.Command) error {
	for _, arg := range command.Args {
		if strings.HasPrefix(arg, ".lexr-workspace-owner-") {
			if err := os.WriteFile(filepath.Join(r.workspace, arg), []byte("lexr-workspace-owner"), 0600); err != nil {
				return err
			}
		}
	}
	for i, arg := range command.Args {
		if arg != "-extract" || i+2 >= len(command.Args) {
			continue
		}
		source := filepath.Join(r.workspace, strings.TrimPrefix(command.Args[i+1], "/"))
		destination := filepath.Join(r.workspace, strings.TrimPrefix(command.Args[i+2], "/work/"))
		info, err := os.Stat(source)
		if err != nil {
			return err
		}
		if info.IsDir() {
			r.extractedTree = true
			return os.CopyFS(destination, os.DirFS(source))
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0644)
	}
	return errors.New("unexpected companion extraction command")
}

// Capture rejects any unplanned external inspection in this isolated test.
func (r *companionExtractionRunner) Capture(context.Context, platform.Command) ([]byte, error) {
	return nil, errors.New("unexpected capture")
}

// TestFedoraCompanionRepeatsSharedDirectoryProof catches the old per-file hash
// shortcut: matching declared hashes alone cannot admit undeclared entries.
// The same shared validator replays compatibility decisions from pinned bytes.
func TestFedoraCompanionRepeatsSharedDirectoryProof(t *testing.T) {
	for _, extra := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "undeclared-entry"}[extra], func(t *testing.T) {
			workspace := t.TempDir()
			manifest := fedoraUserSupportFixture(t, workspace, true)
			if extra {
				if err := os.WriteFile(filepath.Join(workspace, companion.ISOFilesystemRoot, "extra"), []byte("unrecorded"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			runner := &companionExtractionRunner{workspace: workspace}
			checks := NewValidator(platform.NewDocker(runner)).validateCompanion(context.Background(), "tools:test", workspace, manifest.CompanionBundle)
			if len(checks) != 1 || checks[0].Passed == extra {
				t.Fatalf("checks=%+v", checks)
			}
			if !runner.extractedTree {
				t.Fatal("validator bypassed shared directory proof")
			}
		})
	}
}
