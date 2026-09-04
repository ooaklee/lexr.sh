package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// workVolumePrefix distinguishes generated temporary work volumes from
// user-managed Docker storage.
const workVolumePrefix = "lexr-work-"

// toolsDockerfile defines the reproducible ARM64 tool environment used for
// filesystem and boot-media operations.
const toolsDockerfile = `FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
	  binutils ca-certificates coreutils cpio dosfstools dpkg e2fsprogs \
	  dracut-core file initramfs-tools kmod libarchive-tools md5deep mtools parted \
	  squashfs-tools systemd-sysv xorriso xz-utils zstd \
 && rm -rf /var/lib/apt/lists/*
`

// fedoraToolsDockerfile uses Fedora's current EROFS extractor because Ubuntu
// 24.04's version cannot preserve the capabilities and SELinux xattrs carried
// by Fedora 44 live roots.
const fedoraToolsDockerfile = `FROM fedora@sha256:43b29f65a41eb9c35e1cd5323e3bdf3b655c2357a9f4f1ff2f9c2798e5045d80
RUN dnf install -y --setopt=install_weak_deps=False \
	  attr binutils bzip2 coreutils cpio diffutils dpkg erofs-utils-1.9.2-2.fc44 file findutils gawk gcc-c++ grep \
	  grub2-tools grub2-tools-extra gzip kmod meson mtools ninja-build pkgconf-pkg-config python3 rpm-build sed \
	  systemd systemd-devel systemd-rpm-macros tar unzip util-linux-core xorriso xz zstd \
 && test "$(rpm -q --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}' erofs-utils)" = \
	  erofs-utils-1.9.2-2.fc44.aarch64 \
 && fsck.erofs --help 2>&1 | grep -F -- '--path=X' \
 && dnf clean all
`

// Docker provides the narrowly scoped container operations needed by image
// workflows through an injectable command runner.
type Docker struct {
	// Runner executes Docker CLI commands.
	Runner Runner
}

// NewDocker constructs a Docker boundary, using direct process execution when
// runner is nil.
func NewDocker(runner Runner) *Docker {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Docker{Runner: runner}
}

// Check verifies that the Docker CLI can communicate with a running daemon.
func (d *Docker) Check(ctx context.Context) error {
	_, err := d.Runner.Capture(ctx, Command{Name: "docker", Args: []string{"info", "--format", "{{.ServerVersion}}"}})
	if err != nil {
		return fmt.Errorf("Docker is required and its daemon must be running: %w", err)
	}
	return nil
}

// EnsureToolsImage returns the definition-keyed tooling image name, building
// it for ARM64 only when that exact definition is not already available.
func (d *Docker) EnsureToolsImage(ctx context.Context) (string, error) {
	return d.ensureToolsImage(ctx, "lexr-builder", toolsDockerfile)
}

// EnsureFedoraToolsImage returns tooling whose EROFS implementation preserves
// all Fedora filesystem xattrs during extraction.
func (d *Docker) EnsureFedoraToolsImage(ctx context.Context) (string, error) {
	return d.ensureToolsImage(ctx, "lexr-fedora-builder", fedoraToolsDockerfile)
}

// ensureToolsImage builds one definition-keyed ARM64 tool environment on demand.
func (d *Docker) ensureToolsImage(ctx context.Context, repository, definition string) (string, error) {
	digest := sha256.Sum256([]byte(definition))
	name := fmt.Sprintf("%s:%x", repository, digest[:6])
	if _, err := d.Runner.Capture(ctx, Command{Name: "docker", Args: []string{"image", "inspect", name}}); err == nil {
		return name, nil
	}
	contextDir, err := os.MkdirTemp("", "lexr-docker-context-")
	if err != nil {
		return "", fmt.Errorf("create Docker build context: %w", err)
	}
	defer os.RemoveAll(contextDir)
	err = d.Runner.Run(ctx, Command{
		Name:  "docker",
		Args:  []string{"build", "--platform", "linux/arm64", "--tag", name, "--file", "-", contextDir},
		Stdin: strings.NewReader(definition),
	})
	if err != nil {
		return "", fmt.Errorf("build image tooling container: %w", err)
	}
	return name, nil
}

// RunInWorkspace executes a disposable ARM64 container with workspace mounted
// at /work and streams its output through the configured runner.
func (d *Docker) RunInWorkspace(ctx context.Context, image, workspace string, args ...string) error {
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	dockerArgs := []string{
		"run", "--rm", "--platform", "linux/arm64",
		"--volume", absolute + ":/work",
		"--workdir", "/work",
		image,
	}
	dockerArgs = append(dockerArgs, args...)
	return d.Runner.Run(ctx, Command{Name: "docker", Args: dockerArgs})
}

// CaptureInWorkspace executes a disposable ARM64 container and returns its
// standard output while mounting workspace at /work.
func (d *Docker) CaptureInWorkspace(ctx context.Context, image, workspace string, args ...string) ([]byte, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	dockerArgs := []string{
		"run", "--rm", "--platform", "linux/arm64",
		"--volume", absolute + ":/work",
		"--workdir", "/work",
		image,
	}
	dockerArgs = append(dockerArgs, args...)
	return d.Runner.Capture(ctx, Command{Name: "docker", Args: dockerArgs})
}

// CreateWorkVolume creates and verifies a Docker-local Linux filesystem used
// for case-sensitive, device-node-aware image manipulation.
func (d *Docker) CreateWorkVolume(ctx context.Context) (string, error) {
	var token [12]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate Docker work-volume name: %w", err)
	}
	name := fmt.Sprintf("%s%x", workVolumePrefix, token)
	created, err := d.Runner.Capture(ctx, Command{
		Name: "docker",
		Args: []string{"volume", "create", "--driver", "local", "--label", "io.lexr.temporary=true", name},
	})
	if err != nil {
		return "", fmt.Errorf("create Docker work volume %s failed; retention state is unknown: %w", name, err)
	}
	if strings.TrimSpace(string(created)) != name {
		creationErr := fmt.Errorf("Docker created unexpected work volume %q", strings.TrimSpace(string(created)))
		if cleanupErr := d.RemoveWorkVolume(ctx, name); cleanupErr != nil {
			return "", fmt.Errorf("%w; intended volume %s was retained: %v", creationErr, name, cleanupErr)
		}
		return "", fmt.Errorf("%w; intended volume %s was removed", creationErr, name)
	}
	if err := d.inspectWorkVolume(ctx, name); err != nil {
		return "", fmt.Errorf("Docker work volume retained because its identity could not be validated: %s: %w", name, err)
	}
	return name, nil
}

// RemoveWorkVolume removes only volumes bearing lexr's generated name.
func (d *Docker) RemoveWorkVolume(ctx context.Context, name string) error {
	if !validWorkVolumeName(name) {
		return fmt.Errorf("refuse to remove unrecognised Docker work volume %q", name)
	}
	if err := d.inspectWorkVolume(ctx, name); err != nil {
		return fmt.Errorf("refuse to remove Docker work volume %q; volume was retained: %w", name, err)
	}
	if err := d.Runner.Run(ctx, Command{Name: "docker", Args: []string{"volume", "rm", name}}); err != nil {
		return fmt.Errorf("remove Docker work volume %s failed; volume was retained: %w", name, err)
	}
	return nil
}

// inspectWorkVolume verifies the generated name, local driver, and ownership
// label before a volume is trusted or removed.
func (d *Docker) inspectWorkVolume(ctx context.Context, name string) error {
	if !validWorkVolumeName(name) {
		return fmt.Errorf("invalid generated volume name")
	}
	inspected, err := d.Runner.Capture(ctx, Command{
		Name: "docker",
		Args: []string{"volume", "inspect", "--format", "{{.Name}} {{.Driver}} {{index .Labels \"io.lexr.temporary\"}}", name},
	})
	if err != nil {
		return fmt.Errorf("inspect identity: %w", err)
	}
	want := name + " local true"
	if got := strings.TrimSpace(string(inspected)); got != want {
		return fmt.Errorf("identity mismatch: got %q, want %q", got, want)
	}
	return nil
}

// RunInWorkspaceVolume executes a disposable ARM64 container with both the host
// exchange directory and a Linux-native scratch volume mounted.
func (d *Docker) RunInWorkspaceVolume(ctx context.Context, image, workspace, volume string, args ...string) error {
	dockerArgs, err := workspaceVolumeArgs(image, workspace, volume)
	if err != nil {
		return err
	}
	dockerArgs = append(dockerArgs, args...)
	return d.Runner.Run(ctx, Command{Name: "docker", Args: dockerArgs})
}

// RunInWorkspaceVolumePreservingXattrs grants only the additional Linux
// capabilities needed to restore Fedora security.selinux and capability xattrs
// on a Docker-local filesystem. It is reserved for EROFS extraction/relabeling.
func (d *Docker) RunInWorkspaceVolumePreservingXattrs(ctx context.Context, image, workspace, volume string, args ...string) error {
	dockerArgs, err := workspaceVolumeArgs(image, workspace, volume)
	if err != nil {
		return err
	}
	capabilities := []string{"--cap-add", "SYS_ADMIN", "--cap-add", "MAC_ADMIN", "--security-opt", "label=disable"}
	dockerArgs = append(dockerArgs[:4], append(capabilities, dockerArgs[4:]...)...)
	dockerArgs = append(dockerArgs, args...)
	return d.Runner.Run(ctx, Command{Name: "docker", Args: dockerArgs})
}

// CaptureInWorkspaceVolume runs with host and Linux-native workspaces mounted
// and returns the command's standard output.
func (d *Docker) CaptureInWorkspaceVolume(ctx context.Context, image, workspace, volume string, args ...string) ([]byte, error) {
	dockerArgs, err := workspaceVolumeArgs(image, workspace, volume)
	if err != nil {
		return nil, err
	}
	dockerArgs = append(dockerArgs, args...)
	return d.Runner.Capture(ctx, Command{Name: "docker", Args: dockerArgs})
}

// workspaceVolumeArgs builds the fixed Docker invocation used for Linux
// filesystem work, validating the volume identity by name before it is mounted.
func workspaceVolumeArgs(image, workspace, volume string) ([]string, error) {
	if !validWorkVolumeName(volume) {
		return nil, fmt.Errorf("invalid Docker work volume %q", volume)
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	return []string{
		"run", "--rm", "--platform", "linux/arm64",
		"--cap-add", "MKNOD",
		"--device-cgroup-rule", "c *:* m",
		"--device-cgroup-rule", "b *:* m",
		"--volume", absolute + ":/work",
		"--volume", volume + ":/linux-work",
		"--workdir", "/work",
		image,
	}, nil
}

// validWorkVolumeName accepts only the generated prefix followed by 96 bits of
// lowercase hexadecimal entropy.
func validWorkVolumeName(name string) bool {
	if !strings.HasPrefix(name, workVolumePrefix) || len(name) != len(workVolumePrefix)+24 {
		return false
	}
	for _, character := range strings.TrimPrefix(name, workVolumePrefix) {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}
