package platform

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// volumeRunner records Docker volume commands while allowing tests to inject
// inspect output and removal failures without contacting a daemon.
type volumeRunner struct {
	commands      []Command
	inspectResult string
	removeErr     error
}

// Run records a Docker command and returns the configured failure for volume
// removal operations.
func (r *volumeRunner) Run(_ context.Context, command Command) error {
	r.commands = append(r.commands, command)
	if len(command.Args) >= 2 && command.Args[0] == "volume" && command.Args[1] == "rm" {
		return r.removeErr
	}
	return nil
}

// Capture simulates Docker volume creation and identity inspection using either
// a valid default response or an explicitly configured result.
func (r *volumeRunner) Capture(_ context.Context, command Command) ([]byte, error) {
	r.commands = append(r.commands, command)
	if len(command.Args) < 2 || command.Args[0] != "volume" {
		return nil, errors.New("unexpected capture command")
	}
	name := command.Args[len(command.Args)-1]
	switch command.Args[1] {
	case "create":
		return []byte(name + "\n"), nil
	case "inspect":
		if r.inspectResult != "" {
			return []byte(r.inspectResult), nil
		}
		return []byte(name + " local true\n"), nil
	default:
		return nil, errors.New("unexpected Docker volume operation")
	}
}

// TestDockerWorkVolumeLifecycle verifies generated work-volume names pass their
// safety predicate and the same exact volume is removed after use.
func TestDockerWorkVolumeLifecycle(t *testing.T) {
	runner := &volumeRunner{}
	docker := NewDocker(runner)

	name, err := docker.CreateWorkVolume(context.Background())
	if err != nil {
		t.Fatalf("CreateWorkVolume() error = %v", err)
	}
	if !validWorkVolumeName(name) {
		t.Fatalf("CreateWorkVolume() returned invalid name %q", name)
	}
	if err := docker.RemoveWorkVolume(context.Background(), name); err != nil {
		t.Fatalf("RemoveWorkVolume() error = %v", err)
	}
	last := runner.commands[len(runner.commands)-1]
	if !slices.Equal(last.Args, []string{"volume", "rm", name}) {
		t.Fatalf("remove args = %q", last.Args)
	}
}

// TestCreateWorkVolumeRetainsFailedIdentity verifies a newly created volume with
// unexpected metadata is reported and retained instead of being blindly removed.
func TestCreateWorkVolumeRetainsFailedIdentity(t *testing.T) {
	runner := &volumeRunner{inspectResult: "wrong local true\n"}
	docker := NewDocker(runner)

	name, err := docker.CreateWorkVolume(context.Background())
	if err == nil || !strings.Contains(err.Error(), "retained") || !strings.Contains(err.Error(), workVolumePrefix) {
		t.Fatalf("CreateWorkVolume() = %q, %v; want identity error", name, err)
	}
	last := runner.commands[len(runner.commands)-1]
	if len(last.Args) < 2 || last.Args[0] != "volume" || last.Args[1] != "inspect" {
		t.Fatalf("identity failure performed an unexpected command: %#v", last)
	}
}

// TestRemoveWorkVolumeRejectsUnrecognisedName verifies deletion is refused before
// invoking Docker when a volume lacks the tool-owned random naming pattern.
func TestRemoveWorkVolumeRejectsUnrecognisedName(t *testing.T) {
	runner := &volumeRunner{}
	docker := NewDocker(runner)

	err := docker.RemoveWorkVolume(context.Background(), "user-data")
	if err == nil || !strings.Contains(err.Error(), "refuse") {
		t.Fatalf("RemoveWorkVolume() error = %v, want refusal", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("runner received destructive command: %#v", runner.commands)
	}
}

// TestRemoveWorkVolumeRefusesIdentityMismatch verifies deletion cannot proceed
// when Docker inspection does not match the exact owned name, driver, and label.
func TestRemoveWorkVolumeRefusesIdentityMismatch(t *testing.T) {
	runner := &volumeRunner{inspectResult: "someone-elses-volume local false\n"}
	docker := NewDocker(runner)
	name := workVolumePrefix + "0123456789abcdef01234567"

	err := docker.RemoveWorkVolume(context.Background(), name)
	if err == nil || !strings.Contains(err.Error(), "refuse") || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("RemoveWorkVolume() error = %v, want identity refusal", err)
	}
	if slices.ContainsFunc(runner.commands, func(command Command) bool {
		return len(command.Args) >= 2 && command.Args[0] == "volume" && command.Args[1] == "rm"
	}) {
		t.Fatalf("identity mismatch invoked volume rm: %#v", runner.commands)
	}
}

// TestWorkspaceVolumeArgsMountsLinuxWorkVolume verifies remaster containers use
// the named Linux volume and receive only the device-node capabilities they need.
func TestWorkspaceVolumeArgsMountsLinuxWorkVolume(t *testing.T) {
	name := workVolumePrefix + "0123456789abcdef01234567"
	arguments, err := workspaceVolumeArgs("builder:test", t.TempDir(), name)
	if err != nil {
		t.Fatalf("workspaceVolumeArgs() error = %v", err)
	}
	joined := strings.Join(arguments, "\n")
	for _, required := range []string{
		"MKNOD",
		"c *:* m",
		"b *:* m",
		name + ":/linux-work",
		"builder:test",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("Docker arguments do not contain %q: %q", required, arguments)
		}
	}
}

// TestUbuntuToolsDefinitionOwnsOfflineInitramfsTooling prevents remastering
// from executing a source image's Dracut engine or modules as container root.
func TestUbuntuToolsDefinitionOwnsOfflineInitramfsTooling(t *testing.T) {
	t.Parallel()

	for _, required := range []string{"FROM ubuntu:24.04", "dracut-core", "initramfs-tools"} {
		if !strings.Contains(toolsDockerfile, required) {
			t.Errorf("Ubuntu tools definition does not contain %q", required)
		}
	}
}

// TestUbuntuToolsImageIntegration optionally proves the production ARM64 image
// supplies the trusted Dracut engine, modules and helper used for sysroot mode.
func TestUbuntuToolsImageIntegration(t *testing.T) {
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 to exercise the Docker daemon")
	}
	docker := NewDocker(nil)
	ctx := context.Background()
	if err := docker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	image, err := docker.EnsureToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := docker.Runner.Run(ctx, Command{
		Name: "docker",
		Args: []string{
			"run", "--rm", "--platform", "linux/arm64", image,
			"bash", "-ceu", "test -x /usr/bin/dracut; test -x /usr/lib/dracut/dracut-install; test -d /usr/lib/dracut/modules.d; /usr/bin/dracut --help | grep -qF -- --sysroot",
		},
	}); err != nil {
		t.Fatal(err)
	}
}

// TestSecurityXattrWorkspaceAddsOnlyTheDeclaredCapabilities locks the elevated
// Fedora EROFS boundary to its explicit MAC and mount capability set.
func TestSecurityXattrWorkspaceAddsOnlyTheDeclaredCapabilities(t *testing.T) {
	runner := &volumeRunner{}
	docker := NewDocker(runner)
	name := workVolumePrefix + "0123456789abcdef01234567"

	err := docker.RunInWorkspaceVolumePreservingXattrs(
		context.Background(), "fedora-builder:test", t.TempDir(), name,
		"fsck.erofs", "--xattrs", "/work/live.erofs",
	)
	if err != nil {
		t.Fatalf("RunInWorkspaceVolumePreservingXattrs() error = %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("runner commands = %#v", runner.commands)
	}
	joined := strings.Join(runner.commands[0].Args, "\n")
	for _, required := range []string{
		"SYS_ADMIN",
		"MAC_ADMIN",
		"label=disable",
		"MKNOD",
		name + ":/linux-work",
		"fedora-builder:test",
		"fsck.erofs",
		"--xattrs",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("Docker arguments do not contain %q: %q", required, runner.commands[0].Args)
		}
	}
	if strings.Contains(joined, "--privileged") {
		t.Fatalf("security-xattr boundary unexpectedly grants --privileged: %q", runner.commands[0].Args)
	}
}

// TestFedoraToolsDefinitionPinsTheEROFSContract prevents mutable Fedora package
// drift from silently changing extraction semantics behind the cached tag.
func TestFedoraToolsDefinitionPinsTheEROFSContract(t *testing.T) {
	t.Parallel()

	for _, required := range []string{
		"fedora@sha256:43b29f65a41eb9c35e1cd5323e3bdf3b655c2357a9f4f1ff2f9c2798e5045d80",
		"erofs-utils-1.9.2-2.fc44",
		"erofs-utils-1.9.2-2.fc44.aarch64",
		"--path=X",
		"gcc-c++",
		"meson",
		"ninja-build",
		"systemd-devel",
		"systemd-rpm-macros",
		"unzip",
	} {
		if !strings.Contains(fedoraToolsDockerfile, required) {
			t.Errorf("Fedora tools definition does not contain %q", required)
		}
	}
}

// TestFedoraToolsImageIntegration optionally builds the pinned ARM64 image and
// queries the exact EROFS implementation used by production remasters.
func TestFedoraToolsImageIntegration(t *testing.T) {
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 to exercise the Docker daemon")
	}
	docker := NewDocker(nil)
	ctx := context.Background()
	if err := docker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	image, err := docker.EnsureFedoraToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	output, err := docker.Runner.Capture(ctx, Command{
		Name: "docker",
		Args: []string{
			"run", "--rm", "--platform", "linux/arm64", image,
			"rpm", "-q", "--qf", "%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}", "erofs-utils",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(output)), "erofs-utils-1.9.2-2.fc44.aarch64"; got != want {
		t.Fatalf("Fedora EROFS tool = %q, want %q", got, want)
	}
}

// TestDockerWorkVolumeIntegration optionally exercises creation, inspection, and
// exact removal against a real Docker daemon when explicitly enabled.
func TestDockerWorkVolumeIntegration(t *testing.T) {
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 to exercise the Docker daemon")
	}
	docker := NewDocker(nil)
	ctx := context.Background()
	if err := docker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	name, err := docker.CreateWorkVolume(ctx)
	if err != nil {
		t.Fatalf("CreateWorkVolume() error = %v", err)
	}
	removed := false
	t.Cleanup(func() {
		if !removed {
			_ = docker.RemoveWorkVolume(context.Background(), name)
		}
	})
	if err := docker.RemoveWorkVolume(ctx, name); err != nil {
		t.Fatalf("RemoveWorkVolume() error = %v", err)
	}
	removed = true
}
