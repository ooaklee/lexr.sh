package build

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/hostcap"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// These seams test Manager ownership and routing only. They do not discover a
// real endpoint, inspect a socket, or establish native Docker/Desktop support.
type controllerTestTransport struct {
	runner        platform.Runner
	underlying    *controllerTestRunner
	engine        string
	version       string
	daemonPrefix  string
	verifyError   error
	verifications int
	hostPaths     []string
}

// controllerTestBindTransport admits only the exact controller simulator and retains its engine identity.
func controllerTestBindTransport(ctx context.Context, runner platform.Runner, _ hostcap.Host) (sourceTransportBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, ok := runner.(*controllerTestRunner)
	if !ok {
		return nil, errors.New("controller transport seam requires its exact fake runner")
	}
	return &controllerTestTransport{runner: runner, underlying: r, engine: r.engine, version: r.version}, nil
}

// Runner returns the transport's original process boundary without reconstruction.
func (b *controllerTestTransport) Runner() platform.Runner { return b.runner }

// EngineIdentity reports the daemon identity captured by the simulated binding.
func (b *controllerTestTransport) EngineIdentity() (string, string) {
	return b.engine, b.version
}

// DaemonSnapshotPath records canonical source coordinates and applies the controlled fixture prefix.
func (b *controllerTestTransport) DaemonSnapshotPath(host string) (string, error) {
	if !filepath.IsAbs(host) || filepath.Clean(host) != host {
		return "", errors.New("fake transport received a noncanonical host snapshot")
	}
	b.hostPaths = append(b.hostPaths, host)
	return b.daemonPrefix + host, nil
}

// Verify checks cancellation and the pinned simulator engine identity on each reuse.
func (b *controllerTestTransport) Verify(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.verifications++
	if b.verifyError != nil {
		return b.verifyError
	}
	if b.underlying.engine != b.engine || b.underlying.version != b.version {
		return errors.New("fake bound transport engine identity changed")
	}
	return nil
}

// controllerTestPinnedPrefix is the exact admitted selection prefix expected at the decorated boundary.
var controllerTestPinnedPrefix = []string{"--config", "/fixture/docker-config", "--host", "unix:///fixture/docker.sock"}

// controllerTestPinnedEnv lists the inherited transport selectors neutralised by the fixture runner.
var controllerTestPinnedEnv = []string{"DOCKER_HOST=", "DOCKER_CONTEXT=", "DOCKER_TLS=", "DOCKER_TLS_VERIFY=", "DOCKER_CERT_PATH=", "DOCKER_API_VERSION="}

// controllerTestPinnedCall preserves the decorated command and context state for routing assertions.
type controllerTestPinnedCall struct {
	name     string
	args     []string
	env      []string
	deadline bool
	canceled bool
}

// A separate fake boundary validates the decorated command before normalising
// that exact prefix for the unchanged legacy Docker-operation simulator. If a
// Manager/dependency/recovery path bypasses the bound Runner, counts diverge.
type controllerTestPinnedBoundary struct {
	runner *controllerTestRunner
	calls  []controllerTestPinnedCall
}

// Run validates the frozen wrapper before delegating to the unchanged operation simulator.
func (b *controllerTestPinnedBoundary) Run(ctx context.Context, command platform.Command) error {
	_, bounded := ctx.Deadline()
	b.calls = append(b.calls, controllerTestPinnedCall{command.Name, append([]string(nil), command.Args...), append([]string(nil), command.Env...), bounded, ctx.Err() != nil})
	if command.Name != "/fixture/docker" || len(command.Args) <= len(controllerTestPinnedPrefix) || !reflect.DeepEqual(command.Args[:len(controllerTestPinnedPrefix)], controllerTestPinnedPrefix) || !reflect.DeepEqual(command.Env, controllerTestPinnedEnv) {
		return errors.New("fake bound Docker command lost its frozen client, endpoint or environment")
	}
	command.Name = "docker"
	command.Args = append([]string(nil), command.Args[len(controllerTestPinnedPrefix):]...)
	command.Env = nil
	return b.runner.Run(ctx, command)
}

// Capture refuses the unbounded capture shortcut at the pinned controller boundary.
func (b *controllerTestPinnedBoundary) Capture(context.Context, platform.Command) ([]byte, error) {
	return nil, errors.New("controller transport tests forbid Capture")
}

// controllerTestPinnedRunner applies the fixed transport wrapper before entering its paired boundary.
type controllerTestPinnedRunner struct{ boundary *controllerTestPinnedBoundary }

// Run decorates only the manager's expected unselected Docker commands.
func (r *controllerTestPinnedRunner) Run(ctx context.Context, command platform.Command) error {
	if command.Name != "docker" || len(command.Env) != 0 {
		return errors.New("Manager supplied an unexpected command to its bound fake transport")
	}
	command.Name = "/fixture/docker"
	command.Args = append(append([]string(nil), controllerTestPinnedPrefix...), command.Args...)
	command.Env = append([]string(nil), controllerTestPinnedEnv...)
	return r.boundary.Run(ctx, command)
}

// Capture keeps the manager tests on bounded Run observations rather than a capture shortcut.
func (r *controllerTestPinnedRunner) Capture(context.Context, platform.Command) ([]byte, error) {
	return nil, errors.New("controller transport tests forbid Capture")
}

// controllerTestPinnedTransport pairs a decorated process runner with its original simulator ownership.
func controllerTestPinnedTransport(r *controllerTestRunner) (*controllerTestTransport, *controllerTestPinnedBoundary) {
	boundary := &controllerTestPinnedBoundary{runner: r}
	return &controllerTestTransport{runner: &controllerTestPinnedRunner{boundary}, underlying: r, engine: r.engine, version: r.version}, boundary
}

// controllerTestAssertPinned requires every dependency, source and recovery command to traverse the binding.
func controllerTestAssertPinned(t *testing.T, r *controllerTestRunner, boundary *controllerTestPinnedBoundary) {
	t.Helper()
	if len(boundary.calls) == 0 || len(boundary.calls) != len(r.calls) {
		t.Fatalf("a dependency/source/recovery command bypassed the binding: pinned=%d legacy=%d", len(boundary.calls), len(r.calls))
	}
	for _, call := range boundary.calls {
		if !call.deadline || call.name != "/fixture/docker" || !reflect.DeepEqual(call.args[:len(controllerTestPinnedPrefix)], controllerTestPinnedPrefix) || !reflect.DeepEqual(call.env, controllerTestPinnedEnv) {
			t.Fatalf("frozen fake transport command differs: %+v", call)
		}
	}
	controllerTestAssertNoDeletion(t, r)
}

// TestControllerTransportIsNotBoundForSafeRefusals rejects invalid requests before transport observations.
func TestControllerTransportIsNotBoundForSafeRefusals(t *testing.T) {
	for _, kind := range []string{"unsupported", "invalid", "no-pull"} {
		t.Run(kind, func(t *testing.T) {
			r, request := newControllerTestRunner(t), controllerTestParents(t)
			m := controllerTestManager(r)
			bindings := 0
			m.bindTransport = func(context.Context, platform.Runner, hostcap.Host) (sourceTransportBinding, error) {
				bindings++
				return nil, errors.New("binding must not be attempted")
			}
			switch kind {
			case "unsupported":
				m.host = hostcap.Host{GOOS: "linux", GOARCH: "amd64"}
			case "invalid":
				request.Jobs = -1
			case "no-pull":
				request.NoPull = true
			}
			if result, err := m.Run(context.Background(), request); err == nil || result.BuildID != "" || result.Publication.Published {
				t.Fatalf("safe refusal executed: %+v, %v", result, err)
			}
			if bindings != 0 || m.transport != nil {
				t.Fatal("safe refusal acquired transport authority")
			}
			controllerTestNoEffects(t, r, request)
		})
	}
}

// TestControllerTransportDryRunDoesNotBindOptIn keeps authenticated planning independent of daemon readiness.
func TestControllerTransportDryRunDoesNotBindOptIn(t *testing.T) {
	request := approvedControllerRequest(t)
	request.DryRun = true
	r := newControllerTestRunner(t)
	m := controllerTestManager(r)
	bindings := 0
	m.bindTransport = func(context.Context, platform.Runner, hostcap.Host) (sourceTransportBinding, error) {
		bindings++
		return nil, errors.New("dry-run must not resolve Docker")
	}
	if result, err := m.Run(context.Background(), request); err != nil || !result.Plan.Executable || result.BuildID != "" {
		t.Fatalf("approved dry-run differs: %+v, %v", result, err)
	}
	if bindings != 0 || m.transport != nil {
		t.Fatal("dry-run acquired transport authority")
	}
	controllerTestNoEffects(t, r, request)
}

// TestControllerTransportFailurePrecedesDependencyEffectsOptIn refuses an invalid binding before image preparation.
func TestControllerTransportFailurePrecedesDependencyEffectsOptIn(t *testing.T) {
	for _, kind := range []string{"factory", "verification"} {
		t.Run(kind, func(t *testing.T) {
			request := approvedControllerRequest(t)
			r := newControllerTestRunner(t)
			m := controllerTestManager(r)
			originalDependencies := m.dependencies
			binding, _ := controllerTestPinnedTransport(r)
			bindings := 0
			m.bindTransport = func(context.Context, platform.Runner, hostcap.Host) (sourceTransportBinding, error) {
				bindings++
				if kind == "factory" {
					return nil, errors.New("simulated untrusted local transport")
				}
				binding.verifyError = errors.New("simulated failed binding verification")
				return binding, nil
			}
			result, err := m.Run(context.Background(), request)
			if err == nil || bindings != 1 || result.BuildID != "" || m.transport != nil || m.prepared != nil || m.runner != r || m.dependencies != originalDependencies || m.dependencies.Runner != r {
				t.Fatalf("failed binding partially committed or reached dependencies: %+v, %v", result, err)
			}
			controllerTestNoEffects(t, r, request)
		})
	}
}

// TestControllerTransportRetainsOriginAndPinsReuseOptIn preserves the original dependency producer and transport.
func TestControllerTransportRetainsOriginAndPinsReuseOptIn(t *testing.T) {
	for _, firstSourceFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("first-source-fails-%t", firstSourceFails), func(t *testing.T) {
			request := approvedControllerRequest(t)
			r := newControllerTestRunner(t)
			m := controllerTestManager(r)
			originalDependencies := m.dependencies
			binding, boundary := controllerTestPinnedTransport(r)
			bindings := 0
			m.bindTransport = func(_ context.Context, runner platform.Runner, _ hostcap.Host) (sourceTransportBinding, error) {
				bindings++
				if runner != r || m.dependencies != originalDependencies || len(r.calls) != 0 {
					t.Fatal("binding was not resolved from the original runner before dependency effects")
				}
				return binding, nil
			}
			r.before = func(_ context.Context, _ platform.Command) (bool, error) {
				if bindings != 1 || m.transport != binding || m.dependencies != originalDependencies || m.dependencies.Runner != binding.Runner() || m.runner != binding.Runner() {
					t.Fatal("dependency/source command preceded atomic binding commitment")
				}
				return false, nil
			}
			if !firstSourceFails {
				r.copyBytes = controllerTestArchive(t, request.RepositoryRoot, 2)
			}
			first, err := m.Run(context.Background(), request)
			if firstSourceFails {
				controllerTestWithheld(t, r, first, err)
			} else if err != nil || !first.Publication.Published {
				t.Fatalf("first source chain failed: %+v, %v", first, err)
			}
			prepared := m.prepared
			if prepared == nil || prepared != first.Dependencies.Prepared || bindings != 1 || binding.verifications == 0 {
				t.Fatal("first chain did not preserve exact dependency readiness and binding")
			}
			// These are deliberately hostile ambient changes. Only the fake
			// wrapper routing is proved here; real selector resolution has its
			// separate transport-unit tests and native qualification boundary.
			t.Setenv("DOCKER_HOST", "tcp://untrusted.invalid:2375")
			t.Setenv("DOCKER_CONTEXT", "changed-after-binding")
			t.Setenv("DOCKER_CONFIG", filepath.Join(t.TempDir(), "changed-config"))
			r.copyBytes = controllerTestArchive(t, request.RepositoryRoot, 2)
			request.NoPull = true
			second, err := m.Run(context.Background(), request)
			if err != nil || !second.Publication.Published || bindings != 1 || m.dependencies != originalDependencies || m.prepared != prepared || second.Dependencies.Prepared != prepared || controllerTestCount(r, "pull") != 1 || controllerTestCount(r, "commit") != 1 || first.BuildID == second.BuildID || first.VolumeName == second.VolumeName {
				t.Fatalf("reuse rebound transport, changed Prepared origin or reused resources: %+v, %v", second, err)
			}
			controllerTestAssertPinned(t, r, boundary)
		})
	}
}

// TestControllerTransportCancellationRecoveryStaysBoundOptIn uses the admitted endpoint for fresh recovery work.
func TestControllerTransportCancellationRecoveryStaysBoundOptIn(t *testing.T) {
	request := approvedControllerRequest(t)
	r := newControllerTestRunner(t)
	m := controllerTestManager(r)
	binding, boundary := controllerTestPinnedTransport(r)
	bindings := 0
	m.bindTransport = func(context.Context, platform.Runner, hostcap.Host) (sourceTransportBinding, error) {
		bindings++
		return binding, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.after = func(_ context.Context, c platform.Command) error {
		if c.Args[0] == "start" {
			actual := r.containers[c.Args[2]]
			if controllerTestPhase(*actual) == "build" {
				actual.State.Status, actual.State.Running, actual.State.Pid, actual.State.FinishedAt = "running", true, 321, ""
				cancel()
			}
		}
		return nil
	}
	result, err := m.Run(ctx, request)
	controllerTestWithheld(t, r, result, err)
	if !errors.Is(err, context.Canceled) || bindings != 1 || result.Source.TerminalUnknown || controllerTestCount(r, "stop") != 1 {
		t.Fatalf("fresh-context recovery lost bound ownership: %+v, %v", result, err)
	}
	for _, call := range boundary.calls {
		operation := call.args[len(controllerTestPinnedPrefix):]
		if operation[0] == "stop" && (call.canceled || len(operation) != 4 || operation[3] != result.Source.ContainerID) {
			t.Fatalf("recovery used a canceled context or another CID: %+v", call)
		}
	}
	controllerTestAssertPinned(t, r, boundary)
}

// TestControllerTransportIdentityChangeWithholdsPublicationOptIn refuses success after daemon identity drift.
func TestControllerTransportIdentityChangeWithholdsPublicationOptIn(t *testing.T) {
	for _, field := range []string{"engine", "version"} {
		t.Run(field, func(t *testing.T) {
			request := approvedControllerRequest(t)
			r := newControllerTestRunner(t)
			m := controllerTestManager(r)
			binding, boundary := controllerTestPinnedTransport(r)
			bindings := 0
			m.bindTransport = func(context.Context, platform.Runner, hostcap.Host) (sourceTransportBinding, error) {
				bindings++
				return binding, nil
			}
			r.copyBytes = controllerTestArchive(t, request.RepositoryRoot, 2)
			r.after = func(_ context.Context, c platform.Command) error {
				if c.Args[0] == "cp" && strings.HasSuffix(c.Args[1], "/"+ArchiveName) {
					if field == "engine" {
						r.engine = "controller-fixture-other-engine-0002"
					} else {
						r.version = "29.1.4"
					}
				}
				return nil
			}
			result, err := m.Run(context.Background(), request)
			controllerTestWithheld(t, r, result, err)
			if bindings != 1 || m.prepared == nil || m.transport != binding {
				t.Fatal("identity refusal discarded readiness or silently rebound")
			}
			volumes, calls := len(r.volumes), len(r.calls)
			request.NoPull = true
			second, err := m.Run(context.Background(), request)
			controllerTestWithheld(t, r, second, err)
			if bindings != 1 || len(r.volumes) != volumes || len(r.calls) != calls || second.BuildID != "" {
				t.Fatal("failed binding verification reached dependency effects or a new endpoint")
			}
			controllerTestAssertPinned(t, r, boundary)
		})
	}
}

// TestControllerTransportDarwinStoredMountMappingOptIn keeps host and daemon coordinates distinct in controller policy.
func TestControllerTransportDarwinStoredMountMappingOptIn(t *testing.T) {
	for _, corruption := range []string{"none", "requested-host-path", "effective-host-path", "requested-wrong-suffix", "effective-double-prefix"} {
		t.Run(corruption, func(t *testing.T) {
			request := approvedControllerRequest(t)
			r := newControllerTestRunner(t)
			m := controllerTestManager(r)
			m.host = hostcap.Host{GOOS: "darwin", GOARCH: "arm64"}
			binding, boundary := controllerTestPinnedTransport(r)
			binding.daemonPrefix = "/host_mnt"
			m.bindTransport = func(context.Context, platform.Runner, hostcap.Host) (sourceTransportBinding, error) {
				return binding, nil
			}
			r.copyBytes = controllerTestArchive(t, request.RepositoryRoot, 2)
			var hostPath, daemonPath string
			r.after = func(_ context.Context, c platform.Command) error {
				if c.Args[0] != "create" {
					return nil
				}
				for _, container := range r.containers {
					if controllerTestPhase(*container) != "build" || container.State.Status != "created" {
						continue
					}
					for i := range container.HostConfig.Mounts {
						mount := &container.HostConfig.Mounts[i]
						if mount.Target != "/source" {
							continue
						}
						hostPath, daemonPath = mount.Source, "/host_mnt"+mount.Source
						mount.Source = daemonPath
						if corruption == "requested-host-path" {
							mount.Source = hostPath
						} else if corruption == "requested-wrong-suffix" {
							mount.Source = daemonPath + "-other"
						}
					}
					for i := range container.Mounts {
						mount := &container.Mounts[i]
						if mount.Destination == "/source" {
							mount.Source = daemonPath
							if corruption == "effective-host-path" {
								mount.Source = hostPath
							} else if corruption == "effective-double-prefix" {
								mount.Source = "/host_mnt" + daemonPath
							}
						}
					}
				}
				return nil
			}
			result, err := m.Run(context.Background(), request)
			if hostPath == "" || hostPath != result.SnapshotDirectory || daemonPath != "/host_mnt"+result.SnapshotDirectory || len(binding.hostPaths) == 0 {
				t.Fatalf("host staging path was confused with daemon coordinates: %+v, %v", result, err)
			}
			for _, host := range binding.hostPaths {
				if host != hostPath {
					t.Fatalf("mapping received a rewritten or unrelated host path: %q", host)
				}
			}
			foundHostArg := false
			for _, call := range r.calls {
				for _, arg := range call.args {
					if arg == "type=bind,src="+hostPath+",dst=/source,readonly,bind-propagation=rprivate" {
						foundHostArg = true
					}
				}
			}
			if !foundHostArg {
				t.Fatal("Docker create did not retain the original host snapshot path")
			}
			if corruption == "none" {
				if err != nil || !result.Publication.Published || result.SourceBind == nil || result.SourceBind.HostSnapshotPath != hostPath || result.SourceBind.DaemonSnapshotPath != daemonPath || result.SourceBind.EngineID != binding.engine || result.SourceBind.ServerVersion != binding.version {
					t.Fatalf("exact independently rewritten mount records were refused: %+v, %v", result, err)
				}
			} else {
				controllerTestWithheld(t, r, result, err)
				for _, call := range r.calls {
					if call.args[0] == "start" && controllerTestPhase(*r.containers[call.args[2]]) == "build" {
						t.Fatal("source started after a mismatched requested/effective daemon bind")
					}
				}
			}
			controllerTestAssertPinned(t, r, boundary)
		})
	}
}
