package build

import (
	"context"
	"encoding/json"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	"github.com/ooaklee/lexr.sh/internal/userspace/producer"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	camerabuild "github.com/ooaklee/lexr.sh/internal/camera/build"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// recordingRunner captures Docker commands and returns bounded fixture
// metadata without executing a container.
type recordingRunner struct {
	commands []platform.Command
}

// recordingCameraBuilder captures the native request without invoking Docker.
type recordingCameraBuilder struct {
	requests []camerabuild.Request
	receipt  camerabuild.ExecutionReceipt
	err      error
}

// Run records and returns one deterministic native camera build result.
func (builder *recordingCameraBuilder) Run(_ context.Context, request camerabuild.Request) (camerabuild.ExecutionReceipt, error) {
	builder.requests = append(builder.requests, request)
	return builder.receipt, builder.err
}

// Run records a command as a successful simulated execution.
func (r *recordingRunner) Run(_ context.Context, command platform.Command) error {
	r.commands = append(r.commands, command)
	if len(command.Args) >= 2 && command.Name == "docker" && command.Args[0] == "image" && command.Args[1] == "inspect" {
		_, _ = io.WriteString(command.Stdout, "sha256:"+strings.Repeat("a", 64)+"\nubuntu@sha256:"+strings.Repeat("b", 64)+"\n")
	}
	if len(command.Args) >= 2 && command.Name == "docker" && command.Args[0] == "volume" && command.Args[1] == "inspect" {
		_, _ = io.WriteString(command.Stdout, command.Args[len(command.Args)-1]+"\n")
	}
	return nil
}

// Capture satisfies the platform runner contract for tests that do not require
// command output.
func (*recordingRunner) Capture(ctx context.Context, command platform.Command) ([]byte, error) {
	if command.Name == "git" {
		return (platform.ExecRunner{}).Capture(ctx, command)
	}
	return nil, nil
}

// TestIPTSDBuildUsesCompiledDockerRecipe verifies that the native workflow
// validates both ends and never invokes a host repository script.
func TestIPTSDBuildUsesCompiledDockerRecipe(t *testing.T) {
	root := fakeRepository(t)
	runner := &recordingRunner{}
	manager := New(runner)
	manager.validateIPTSDIntegration = func(path string) error {
		if path != filepath.Join(root, "userspace", "iptsd-sp11") {
			t.Fatalf("integration path = %s", path)
		}
		return nil
	}
	output := filepath.Join(t.TempDir(), "output")
	resolvedOutput := filepath.Join(mustResolvePath(t, filepath.Dir(output)), filepath.Base(output))
	manager.validateIPTSDPayload = func(payload, integration string) error {
		if payload != filepath.Join(resolvedOutput, "stage") || integration != filepath.Join(root, "userspace", "iptsd-sp11") {
			t.Fatalf("payload validation = %s, %s", payload, integration)
		}
		if err := os.MkdirAll(payload, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(payload, "SHA256SUMS"), []byte("fixture payload checksum\n"), 0o644)
	}
	err := manager.Run(context.Background(), Request{
		Component: ComponentIPTSD, RepositoryRoot: root,
		OutputDirectory: output, Image: "ubuntu:26.04",
		WorkVolume: "lexr-iptsd", Jobs: 8,
		PayloadTarget: compatibility.Target{Architecture: "arm64", DeviceProfile: "x1e80100-microsoft-denali-oled", OSID: "ubuntu", OSVersion: "26.04", KernelABI: "7.2.0-jg-0sp11v19-qcom-x1e"}, AllowUnverifiedCompatibility: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "iptsd-build-compatibility.json"))
	if err != nil {
		t.Fatal(err)
	}
	var assessment iptsdBuildCompatibility
	if err := json.Unmarshal(data, &assessment); err != nil {
		t.Fatal(err)
	}
	if assessment.Compatibility.Decision.Status != compatibility.Unverified || !assessment.Compatibility.AllowUnverified {
		t.Fatal("build omitted recorded compatibility override")
	}
	declaration, err := os.ReadFile(filepath.Join(output, compatibility.Filename))
	if err != nil {
		t.Fatal(err)
	}
	if err := producer.ValidatePublication(declaration, assessment.Compatibility); err != nil {
		t.Fatal(err)
	}
	checksums, err := os.ReadFile(filepath.Join(output, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(checksums), compatibility.ReferenceFor(declaration).SHA256+"  "+compatibility.Filename) || !strings.Contains(string(checksums), assessment.PayloadChecksum.SHA256+"  stage/SHA256SUMS") {
		t.Fatal("build checksum coverage is incomplete")
	}
	if len(runner.commands) != 3 {
		t.Fatalf("commands = %d, want 3", len(runner.commands))
	}
	for _, command := range runner.commands {
		if command.Name == "bash" || strings.Contains(strings.Join(command.Args, "\n"), "build-sp11-iptsd-docker.sh") {
			t.Fatalf("host script invocation leaked into command: %+v", command)
		}
	}
	run := runner.commands[2]
	joined := strings.Join(run.Args, "\n")
	if run.Name != "docker" || run.Args[0] != "run" || !strings.Contains(joined, userspaceRecipeMarker) ||
		!strings.Contains(joined, resolvedOutput+":/out") || !strings.Contains(joined, "lexr-iptsd:/work") {
		t.Fatalf("unexpected Docker build command: %+v", run)
	}
}

// TestDefaultIPTSDValidatorAcceptsCurrentOEProfile optionally proves that the
// production build manager selects the repository profile, rather than the
// narrower immutable-release profile used by the portable installer.
func TestDefaultIPTSDValidatorAcceptsCurrentOEProfile(t *testing.T) {
	repositoryRoot := strings.TrimSpace(os.Getenv("LEXR_TEST_OE_ROOT"))
	if repositoryRoot == "" {
		t.Skip("set LEXR_TEST_OE_ROOT to the current linux-surface-pro-11-oe checkout")
	}
	integrationRoot := filepath.Join(repositoryRoot, "userspace", "iptsd-sp11")
	manager := New(&recordingRunner{})
	if err := manager.validateIPTSDIntegration(integrationRoot); err != nil {
		t.Fatalf("default IPTSD build validator rejected current OE profile: %v", err)
	}
}

// userspaceRecipeMarker is one invariant line proving the compiled recipe was
// supplied to Docker rather than a repository script path.
const userspaceRecipeMarker = "-Dforce_access_checks=true"

// mustResolvePath resolves one existing test path or terminates the test.
func mustResolvePath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// TestRejectsUnsafeDockerVolume verifies that traversal-like volume names never
// reach a delegated build helper.
func TestRejectsUnsafeDockerVolume(t *testing.T) {
	err := New(&recordingRunner{}).Run(context.Background(), Request{
		Component: ComponentIPTSD, RepositoryRoot: fakeRepository(t), WorkVolume: "../escape",
	})
	if err == nil {
		t.Fatal("expected unsafe volume name to fail")
	}
}

// TestRejectsCrossComponentFlags verifies that camera-only options cannot be
// silently passed to an IPTSD build.
func TestRejectsCrossComponentFlags(t *testing.T) {
	err := New(&recordingRunner{}).Run(context.Background(), Request{
		Component: ComponentIPTSD, RepositoryRoot: fakeRepository(t), NoPull: true,
	})
	if err == nil {
		t.Fatal("expected camera-only flag to fail")
	}
}

// TestCameraBuildUsesNativeManager verifies request mapping and structured delivery.
func TestCameraBuildUsesNativeManager(t *testing.T) {
	root := fakeRepository(t)
	runner := &recordingRunner{}
	builder := &recordingCameraBuilder{receipt: camerabuild.ExecutionReceipt{Plan: camerabuild.Plan{DryRun: true, Executable: false, ExecutionBlocker: "fixture blocker"}}}
	manager := New(runner)
	manager.cameraBuilder = builder
	result, err := manager.RunWithResult(context.Background(), Request{
		Component: ComponentCamera, RepositoryRoot: root,
		OutputDirectory: "build/camera-fixture", Jobs: 6,
		MinimumFreeGiB: 24, NoPull: true, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(builder.requests) != 1 || result.Camera == nil || result.Component != ComponentCamera {
		t.Fatalf("camera result = %+v, requests = %+v", result, builder.requests)
	}
	request := builder.requests[0]
	if request.RepositoryRoot != root || request.OutputDirectory != "build/camera-fixture" || request.Jobs != 6 || request.MinimumFreeGiB != 24 || !request.NoPull || !request.DryRun {
		t.Fatalf("native camera request = %+v", request)
	}
	for _, command := range runner.commands {
		if command.Name == "bash" || strings.Contains(strings.Join(command.Args, "\n"), "build-sp11-imx681-libcamera-docker.sh") {
			t.Fatalf("legacy camera command was invoked: %+v", command)
		}
	}
}

// TestRejectsUnsafeDockerImage verifies that a flag-like image reference never
// reaches Docker.
func TestRejectsUnsafeDockerImage(t *testing.T) {
	err := New(&recordingRunner{}).Run(context.Background(), Request{
		Component: ComponentIPTSD, RepositoryRoot: fakeRepository(t), Image: "--privileged",
	})
	if err == nil {
		t.Fatal("expected unsafe image to fail")
	}
}

// TestRejectsBroadOrPayloadOutput verifies that the native recipe cannot clean
// a repository root or publish build intermediates into release payload data.
func TestRejectsBroadOrPayloadOutput(t *testing.T) {
	root := fakeRepository(t)
	if err := os.Mkdir(filepath.Join(root, "payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{root, filepath.Join(root, "payload", "iptsd-sp11")} {
		if _, err := prepareIPTSDOutput(root, output); err == nil {
			t.Fatalf("unsafe output %s passed", output)
		}
	}
}

// fakeRepository creates minimal IPTSD and native camera input markers.
func fakeRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	integration := filepath.Join(root, "userspace", "iptsd-sp11")
	if err := os.MkdirAll(integration, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(integration, "SOURCE.env"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, relative := range camerabuild.TrackedInputPaths() {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	declaration, err := os.ReadFile("../producer/testdata/declarations/iptsd-v1/" + compatibility.Filename)
	if err != nil {
		t.Fatal(err)
	}
	declarationPath := filepath.Join(root, producer.DeclarationPath("iptsd-v1"))
	if err := os.MkdirAll(filepath.Dir(declarationPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(declarationPath, declaration, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/build/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Git fixture: %v %s", err, output)
		}
	}
	return root
}
