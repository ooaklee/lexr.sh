package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	userspacesource "github.com/ooaklee/lexr.sh/internal/userspace/source"
)

// fakeSourcePreparer captures CLI selections without network or filesystem work.
type fakeSourcePreparer struct {
	requests []userspacesource.Request
	receipt  userspacesource.Receipt
	err      error
}

// Prepare returns one fixture receipt while retaining the forwarded selection.
func (f *fakeSourcePreparer) Prepare(_ context.Context, r userspacesource.Request) (userspacesource.Receipt, error) {
	f.requests = append(f.requests, r)
	return f.receipt, f.err
}

// TestUserspaceSourcePrepareForwardsClosedSelection checks all explicit flags.
func TestUserspaceSourcePrepareForwardsClosedSelection(t *testing.T) {
	var output bytes.Buffer
	fake := &fakeSourcePreparer{receipt: userspacesource.Receipt{DryRun: true}}
	app := &application{out: &output, userspaceSources: fake}
	command := app.newUserspaceSourceCommand()
	commit := strings.Repeat("a", 40)
	command.SetArgs([]string{"prepare", "camera", "--experimental", "--repository", "https://example.invalid/oe.git", "--ref", "refs/heads/test", "--commit", commit, "--output-dir", "new-source", "--dry-run", "--json"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("requests: %+v", fake.requests)
	}
	r := fake.requests[0]
	if r.Component != "camera" || !r.Experimental || r.Repository != "https://example.invalid/oe.git" || r.Ref != "refs/heads/test" || r.Commit != commit || r.OutputDirectory != "new-source" || !r.DryRun {
		t.Fatalf("selection lost or silently changed: %+v", r)
	}
	var receipt userspacesource.Receipt
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil || !receipt.DryRun {
		t.Fatalf("JSON source plan: %s %v", output.String(), err)
	}
}

// TestUserspaceSourcePreparePreservesFailedReceipt retains structured failures.
func TestUserspaceSourcePreparePreservesFailedReceipt(t *testing.T) {
	var output bytes.Buffer
	want := errors.New("selection refused")
	fake := &fakeSourcePreparer{receipt: userspacesource.Receipt{StagingDirectory: "/fixture/retained", Prepared: false}, err: want}
	app := &application{out: &output, userspaceSources: fake}
	command := app.newUserspaceSourceCommand()
	command.SilenceErrors, command.SilenceUsage = true, true
	command.SetArgs([]string{"prepare", "camera", "--json"})
	if err := command.ExecuteContext(context.Background()); !errors.Is(err, want) {
		t.Fatalf("failure was swallowed: %v", err)
	}
	var receipt userspacesource.Receipt
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil || receipt.Prepared || receipt.StagingDirectory != "/fixture/retained" {
		t.Fatalf("failure receipt: %s %v", output.String(), err)
	}
}

// TestUserspaceSourcePublishedFailureDoesNotReportSuccess keeps post-rename
// verification errors authoritative even when a prepared receipt is present.
func TestUserspaceSourcePublishedFailureDoesNotReportSuccess(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[asJSON], func(t *testing.T) {
			var output bytes.Buffer
			want := errors.New("publication verification failed after rename")
			fake := &fakeSourcePreparer{receipt: userspacesource.Receipt{Prepared: true, ComponentValidated: true}, err: want}
			app := &application{out: &output, userspaceSources: fake}
			command := app.newUserspaceSourceCommand()
			command.SilenceErrors, command.SilenceUsage = true, true
			args := []string{"prepare", "camera"}
			if asJSON {
				args = append(args, "--json")
			}
			command.SetArgs(args)
			if err := command.ExecuteContext(context.Background()); !errors.Is(err, want) {
				t.Fatalf("published receipt swallowed final verification failure: %v", err)
			}
			if asJSON {
				var receipt userspacesource.Receipt
				if err := json.Unmarshal(output.Bytes(), &receipt); err != nil || !receipt.Prepared || !receipt.ComponentValidated {
					t.Fatalf("published receipt was misrepresented: %s %v", output.String(), err)
				}
			} else if output.Len() != 0 {
				t.Fatalf("failed publication printed a success message: %s", output.String())
			}
		})
	}
}

// TestUserspaceSourceDryRunIsUnverifiedAndNonMutating rejects false authority.
func TestUserspaceSourceDryRunIsUnverifiedAndNonMutating(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[asJSON], func(t *testing.T) {
			var output bytes.Buffer
			parent := t.TempDir()
			app := &application{out: &output, userspaceSources: userspacesource.New()}
			command := app.newUserspaceSourceCommand()
			args := []string{"prepare", "camera", "--experimental", "--repository", "https://example.invalid/oe.git", "--ref", "refs/heads/test", "--commit", strings.Repeat("a", 40), "--output-dir", filepath.Join(parent, "new-source"), "--dry-run"}
			if asJSON {
				args = append(args, "--json")
			}
			command.SetArgs(args)
			if err := command.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatalf("dry run changed output: %v %v", entries, err)
			}
			if asJSON {
				var fields map[string]any
				if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
					t.Fatal(err)
				}
				if _, exists := fields["commit"]; exists {
					t.Fatal("dry run presented expected commit as resolved")
				}
				if _, exists := fields["tree"]; exists {
					t.Fatal("dry run claimed an observed tree")
				}
				if fields["prepared"] != false || fields["component_validated"] != false {
					t.Fatalf("dry run claimed authority: %+v", fields)
				}
			} else if !strings.Contains(output.String(), "not verified") || !strings.Contains(output.String(), "no network access or filesystem changes") {
				t.Fatalf("human plan overclaims: %s", output.String())
			}
		})
	}
}

// TestSourceFlagsDoNotWidenUserspaceInstall separates source and install policy.
func TestSourceFlagsDoNotWidenUserspaceInstall(t *testing.T) {
	app := &application{out: &bytes.Buffer{}}
	command := app.newUserspaceInstallCommand()
	command.SilenceUsage, command.SilenceErrors = true, true
	command.SetArgs([]string{"camera", "--repository", "https://example.invalid/oe.git", "--ref", "refs/heads/test", "--experimental"})
	if err := command.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("source selection entered installation authority: %v", err)
	}
}
