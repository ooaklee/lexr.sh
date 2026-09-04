//go:build darwin || linux

package platform

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// workspaceOwnerRunner records the command and emulates Docker Desktop's
// private probe materialisation for the Darwin unit test.
type workspaceOwnerRunner struct {
	commands  []Command
	workspace string
}

// Run records one Docker command and creates the Darwin probe which the real
// bind mount would expose to the host after the container exits.
func (r *workspaceOwnerRunner) Run(_ context.Context, command Command) error {
	r.commands = append(r.commands, command)
	if runtime.GOOS != "darwin" {
		return nil
	}
	for index, argument := range command.Args {
		if argument == "lexr-workspace-owner" && index+2 < len(command.Args) {
			return os.WriteFile(filepath.Join(r.workspace, command.Args[index+2]), []byte(workspaceOwnerProbeContents), 0o600)
		}
	}
	return nil
}

// Capture is unused by the host-user execution unit test.
func (r *workspaceOwnerRunner) Capture(context.Context, Command) ([]byte, error) {
	return nil, nil
}

// TestRunInWorkspaceAsHostUserScopesTheIdentityOverride verifies only the
// requested container receives the numeric private-workspace owner.
func TestRunInWorkspaceAsHostUserScopesTheIdentityOverride(t *testing.T) {
	workspace := t.TempDir()
	runner := &workspaceOwnerRunner{workspace: workspace}
	docker := NewDocker(runner)
	owner, err := numericWorkspaceOwner(workspace)
	if err != nil {
		t.Fatal(err)
	}

	if err := docker.RunInWorkspaceAsHostUser(
		context.Background(), "tools:test", workspace,
		"unsquashfs", "-d", "/work/installed-root", "/work/minimal.squashfs",
	); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("runner commands = %#v", runner.commands)
	}
	joined := strings.Join(runner.commands[0].Args, "\n")
	required := []string{workspace + ":/work", "unsquashfs"}
	if runtime.GOOS == "linux" {
		required = append(required, "--user", owner, "stat -c '%u:%g' /work", "workspace ownership mapping is unsupported")
	} else {
		required = append(required, "umask 077", "lexr-workspace-owner")
		if strings.Contains(joined, "--user") {
			t.Fatalf("Docker Desktop extraction unexpectedly forces a numeric container user: %q", runner.commands[0].Args)
		}
	}
	for _, required := range required {
		if !strings.Contains(joined, required) {
			t.Errorf("Docker arguments do not contain %q: %q", required, runner.commands[0].Args)
		}
	}
	for _, forbidden := range []string{"--privileged", "--cap-add", "chmod", "chown"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("Docker arguments unexpectedly contain %q: %q", forbidden, runner.commands[0].Args)
		}
	}
}

// TestRunInWorkspaceAsHostUserIntegration proves the real daemon's bind-mount
// mapping leaves a private extracted file readable and removable by the host.
func TestRunInWorkspaceAsHostUserIntegration(t *testing.T) {
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 to exercise the Docker daemon")
	}
	ctx := context.Background()
	docker := NewDocker(nil)
	if err := docker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	image, err := docker.EnsureToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(".", ".lexr-docker-owner-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)
	if err := docker.RunInWorkspaceAsHostUser(ctx, image, workspace,
		"sh", "-ceu", "umask 077; mkdir /work/private; printf payload > /work/private/output"); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(workspace, "private")
	privateInfo, err := os.Lstat(private)
	if err != nil {
		t.Fatal(err)
	}
	if !privateInfo.IsDir() || privateInfo.Mode().Perm() != 0o700 {
		t.Fatalf("private directory mode = %s, want 0700 directory", privateInfo.Mode())
	}
	output := filepath.Join(private, "output")
	info, err := os.Lstat(output)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("output mode = %s, want private regular file", info.Mode())
	}
	contents, err := os.ReadFile(output)
	if err != nil || string(contents) != "payload" {
		t.Fatalf("host read = %q, %v", contents, err)
	}
	if err := os.RemoveAll(private); err != nil {
		t.Fatal(err)
	}
}
