package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/hostcap"
)

// TestArchRegistrationRequiresExplicitAction proves invoking the command without
// a selected action cannot inspect or mutate the host's boot menu.
func TestArchRegistrationRequiresExplicitAction(t *testing.T) {
	for _, flags := range [][]string{nil, {"--dry-run", "--yes"}} {
		runner := &recordingKernelBootRunner{}
		app := &application{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, kernelBootRunner: runner}
		command := app.newKernelBootCommand()
		command.SetArgs(append([]string{"register-arch", "--arch-root", "/mnt/arch", "--grub-directory", "/boot/grub"}, flags...))
		if err := command.ExecuteContext(context.Background()); err == nil {
			t.Fatal("accepted missing or conflicting action")
		}
		if len(runner.commands) != 0 {
			t.Fatal("ran a command before checking the action")
		}
	}
}

// TestArchRegistrationRejectsUnsupportedHostBeforeInspection proves the CLI
// rejects unavailable host integration before reading mounted targets.
func TestArchRegistrationRejectsUnsupportedHostBeforeInspection(t *testing.T) {
	t.Parallel()
	runner := &recordingKernelBootRunner{}
	app := &application{
		out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, kernelBootRunner: runner,
		host: hostcap.Host{GOOS: "darwin", GOARCH: "arm64"},
	}
	command := app.newKernelBootCommand()
	command.SetArgs([]string{"register-arch", "--arch-root", "/mnt/arch", "--grub-directory", "/boot/grub", "--dry-run"})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "GRUB registration requires operating system linux") {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("unsupported registration invoked commands: %#v", runner.commands)
	}
}

// TestArchRegistrationHelp describes mount ownership and preserves defaults.
func TestArchRegistrationHelp(t *testing.T) {
	var output bytes.Buffer
	command := NewRootCommand(strings.NewReader(""), &output, &output)
	command.SetArgs([]string{"kernel", "boot", "register-arch", "--help"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"--arch-root", "--grub-directory", "--esp", "--dry-run", "--yes", "already be mounted"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("help missing %s", text)
		}
	}
}
