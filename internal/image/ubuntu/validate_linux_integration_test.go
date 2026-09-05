//go:build linux

package ubuntu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestInstalledSupportExtractionOwnershipIntegration proves a root-owned
// SquashFS member retains its mode while becoming readable and removable by
// the owner of the private host workspace.
func TestInstalledSupportExtractionOwnershipIntegration(t *testing.T) {
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 to exercise the Docker daemon")
	}
	ctx := context.Background()
	docker := platform.NewDocker(nil)
	if err := docker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	image, err := docker.EnsureToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	source := filepath.Join(workspace, "source")
	private := filepath.Join(source, "private")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "vmlinuz"), []byte("kernel bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "state"), []byte("state bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := docker.RunInWorkspace(ctx, image, workspace,
		"mksquashfs", "/work/source", "/work/minimal.squashfs", "-noappend", "-all-root", "-no-progress"); err != nil {
		t.Fatal(err)
	}
	paths := []string{"vmlinuz", "private/state"}
	arguments := []string{"unsquashfs", "-no-xattrs", "-no-progress", "-d", "/work/installed-root", "/work/minimal.squashfs"}
	arguments = append(arguments, paths...)
	if err := docker.RunInWorkspaceAsHostUser(ctx, image, workspace, arguments...); err != nil {
		t.Fatal(err)
	}
	extracted := filepath.Join(workspace, "installed-root")
	if err := validateExtractedRegularFiles(extracted, paths); err != nil {
		t.Fatal(err)
	}
	workspaceInfo, err := os.Stat(workspace)
	if err != nil {
		t.Fatal(err)
	}
	workspaceStat := workspaceInfo.Sys().(*syscall.Stat_t)
	for _, expected := range []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(extracted, "vmlinuz"), 0o600},
		{filepath.Join(extracted, "private"), 0o700},
	} {
		info, err := os.Stat(expected.path)
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Uid != workspaceStat.Uid || stat.Gid != workspaceStat.Gid {
			t.Errorf("%s owner = %d:%d, want %d:%d", expected.path, stat.Uid, stat.Gid, workspaceStat.Uid, workspaceStat.Gid)
		}
		if info.Mode().Perm() != expected.mode {
			t.Errorf("%s mode = %o, want %o", expected.path, info.Mode().Perm(), expected.mode)
		}
	}
	if _, err := artifact.HashFile(filepath.Join(extracted, "vmlinuz")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(extracted, "private", "state")); err != nil || string(data) != "state bytes" {
		t.Fatalf("read extracted state = %q, %v", data, err)
	}
	if err := os.RemoveAll(extracted); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(extracted); !os.IsNotExist(err) {
		t.Fatalf("extracted root still exists after cleanup: %v", err)
	}
}

// TestXorrisoSymlinkMemberIsRejectedIntegration proves Rock Ridge links
// restored by the real tools image cannot become host-side validation inputs.
func TestXorrisoSymlinkMemberIsRejectedIntegration(t *testing.T) {
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 to exercise the Docker daemon")
	}
	ctx := context.Background()
	docker := platform.NewDocker(nil)
	if err := docker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	image, err := docker.EnsureToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	memberDirectory := filepath.Join(workspace, "iso-source", "casper")
	if err := os.MkdirAll(memberDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(memberDirectory, "vmlinuz")); err != nil {
		t.Fatal(err)
	}
	if err := docker.RunInWorkspaceAsHostUser(ctx, image, workspace,
		"xorriso", "-as", "mkisofs", "-R", "-o", "/work/symlink.iso", "/work/iso-source"); err != nil {
		t.Fatal(err)
	}
	if err := docker.RunInWorkspaceAsHostUser(ctx, image, workspace,
		"xorriso", "-osirrox", "on", "-indev", "/work/symlink.iso",
		"-extract", "/casper/vmlinuz", "/work/extracted-vmlinuz"); err != nil {
		t.Fatal(err)
	}
	if err := validateExtractedRegularFiles(workspace, []string{"extracted-vmlinuz"}); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("restored Rock Ridge link validation error = %v", err)
	}
}

// TestFullValidatorCleanupIntegration validates a caller-supplied known-good
// ISO and proves the complete successful path leaves no private workspaces.
func TestFullValidatorCleanupIntegration(t *testing.T) {
	isoPath := os.Getenv("LEXR_VALIDATION_INTEGRATION_ISO")
	if isoPath == "" {
		t.Skip("set LEXR_VALIDATION_INTEGRATION_ISO to a known-good ISO")
	}
	absolute, err := filepath.Abs(isoPath)
	if err != nil {
		t.Fatal(err)
	}
	pattern := filepath.Join(filepath.Dir(absolute), ".lexr-validate-*")
	before, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	report, validationErr := NewValidator(nil).Validate(context.Background(), absolute)
	after, globErr := filepath.Glob(pattern)
	if globErr != nil {
		t.Fatal(globErr)
	}
	if validationErr != nil || !report.Valid {
		t.Fatalf("known-good ISO validation failed: valid=%t err=%v", report.Valid, validationErr)
	}
	for _, candidate := range after {
		if !containsString(before, candidate) {
			t.Errorf("validator leaked private workspace %s", candidate)
		}
	}
}

// containsString reports whether one exact workspace path existed before the
// full validator integration run.
func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
