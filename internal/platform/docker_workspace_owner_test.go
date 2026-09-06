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

// TestRunWithReadOnlyInputInWorkspaceAsHostUserRetainsIsolation verifies the
// ownership-safe path also preserves the router's narrow container sandbox.
func TestRunWithReadOnlyInputInWorkspaceAsHostUserRetainsIsolation(t *testing.T) {
	workspace := t.TempDir()
	input := filepath.Join(t.TempDir(), "image.iso")
	if err := os.WriteFile(input, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &workspaceOwnerRunner{workspace: workspace}
	docker := NewDocker(runner)
	if err := docker.RunWithReadOnlyInputInWorkspaceAsHostUser(
		context.Background(), "tools:test", workspace, input, "/input.iso",
		"xorriso", "-indev", "/input.iso",
	); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("runner commands = %#v", runner.commands)
	}
	joined := strings.Join(runner.commands[0].Args, "\n")
	for _, required := range []string{
		"--network", "none", "--read-only", "--cap-drop", "ALL",
		"no-new-privileges", "/tmp:rw,noexec,nosuid,nodev,size=16m",
		input + ":/input.iso:ro", workspace + ":/work", "xorriso",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("Docker arguments do not contain %q: %q", required, runner.commands[0].Args)
		}
	}
}

// TestRunWithReadOnlyVolumeAsHostUserRetainsIsolation checks that copying
// evidence cannot modify the Linux source volume or bypass caller ownership.
func TestRunWithReadOnlyVolumeAsHostUserRetainsIsolation(t *testing.T) {
	workspace := t.TempDir()
	runner := &workspaceOwnerRunner{workspace: workspace}
	docker := NewDocker(runner)
	volume := "lexr-work-0123456789abcdef01234567"
	if err := docker.RunWithReadOnlyVolumeAsHostUser(context.Background(), "tools:test", workspace, volume, "cp", "-R", "/linux-work/source", "/work/copy"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.commands[0].Args, "\n")
	for _, required := range []string{volume + ":/linux-work:ro", "--network", "none", "--cap-drop", "ALL", "--read-only", "no-new-privileges"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing isolation argument %q", required)
		}
	}
	if err := docker.RunWithReadOnlyVolumeAsHostUser(context.Background(), "tools:test", workspace, "../outside", "true"); err == nil {
		t.Fatal("accepted a non-Lexr source volume")
	}
}

// TestReadOnlyVolumeEvidenceOwnershipIntegration reproduces native Linux
// root-owned extraction, then proves nested copies are removable by the caller.
func TestReadOnlyVolumeEvidenceOwnershipIntegration(t *testing.T) {
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 to exercise the Docker daemon")
	}
	ctx := context.Background()
	docker := NewDocker(nil)
	image, err := docker.EnsureToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(".", ".lexr-volume-owner-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace)
	volume, err := docker.CreateWorkVolume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := docker.RemoveWorkVolume(ctx, volume); err != nil {
			t.Error(err)
		}
	}()
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "sh", "-ceu", "umask 022; mkdir -p /linux-work/evidence/licences; printf licence > /linux-work/evidence/licences/LICENSE"); err != nil {
		t.Fatal(err)
	}
	if err := docker.RunWithReadOnlyVolumeAsHostUser(ctx, image, workspace, volume, "sh", "-ceu", "cp -R --no-dereference --preserve=mode,timestamps /linux-work/evidence /work/copied; if touch /linux-work/forbidden 2>/dev/null; then exit 1; fi"); err != nil {
		t.Fatal(err)
	}
	copy := filepath.Join(workspace, "copied")
	contents, err := os.ReadFile(filepath.Join(copy, "licences/LICENSE"))
	if err != nil || string(contents) != "licence" {
		t.Fatalf("copied evidence: %q %v", contents, err)
	}
	if err := os.RemoveAll(copy); err != nil {
		t.Fatalf("caller could not remove nested evidence: %v", err)
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
	input := filepath.Join(workspace, "input")
	if err := os.WriteFile(input, []byte("read-only input"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err = filepath.Abs(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := docker.RunWithReadOnlyInputInWorkspaceAsHostUser(
		ctx, image, workspace, input, "/input.bin",
		"sh", "-ceu", "umask 077; cp /input.bin /work/copied",
	); err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(filepath.Join(workspace, "copied"))
	if err != nil || string(copied) != "read-only input" {
		t.Fatalf("read-only input copy = %q, %v", copied, err)
	}
}
