package build

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/hostcap"
	"github.com/ooaklee/lexr.sh/internal/platform"
	"github.com/ooaklee/lexr.sh/internal/sensors/builderimage"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// controllerTestEngine fixes the simulated daemon identity for controller lifecycle tests.
const controllerTestEngine = "controller-fixture-engine-00000001"

// Controller fixture image references are immutable synthetic identities, not local images.
var (
	controllerTestBase  = "sha256:" + strings.Repeat("a", 64)
	controllerTestImage = "sha256:" + strings.Repeat("b", 64)
)

// controllerTestCall records the operation arguments and context state at the simulated boundary.
type controllerTestCall struct {
	args     []string
	deadline bool
	canceled bool
}

// Only the Docker process/inspection boundary is simulated. The tests never
// execute a Docker command, a shell, source code, sensor code or a transport.
// Prepared, StageRepository and DecodeDockerCopy still use production authority.
type controllerTestRunner struct {
	t               *testing.T
	profile         producerProfile // fixture-selected expected policy, never read from argv
	calls           []controllerTestCall
	captures        int
	engine          string
	version         string
	containers      map[string]*sourceContainerInspection
	names           map[string]string
	volumes         map[string]volumeInspection
	inspections     map[string]int
	copyBytes       []byte
	before          func(context.Context, platform.Command) (bool, error)
	after           func(context.Context, platform.Command) error
	mutateContainer func(string, int, *sourceContainerInspection)
	mutateImage     func(*sourceImageInspection)
	mutateVolume    func(*volumeInspection)
	onStart         func(string, *sourceContainerInspection)
}

// newControllerTestRunner creates an isolated in-memory daemon and resource registry.
func newControllerTestRunner(t *testing.T) *controllerTestRunner {
	t.Helper()
	return &controllerTestRunner{t: t, engine: controllerTestEngine, version: "29.1.3", containers: map[string]*sourceContainerInspection{}, names: map[string]string{}, volumes: map[string]volumeInspection{}, inspections: map[string]int{}}
}

// controllerTestManager pairs production orchestration with bounded fake process and transport observations.
func controllerTestManager(r *controllerTestRunner) *Manager {
	return configureControllerTestManager(New(r), r)
}

// configureControllerTestManager installs bounded fixture transport and timing.
func configureControllerTestManager(m *Manager, r *controllerTestRunner) *Manager {
	// Existing controller tests deliberately simulate execution/transport. The
	// dedicated transport tests exercise endpoint attestation and frozen argv.
	m.bindTransport = controllerTestBindTransport
	m.host = hostcap.Host{GOOS: "linux", GOARCH: "arm64"}
	n := 0
	m.token = func() (string, error) { n++; return fmt.Sprintf("%024x", n), nil }
	m.timeout, m.initTimeout, m.sourceTimeout, m.recoveryTimeout = time.Minute, 10*time.Second, 10*time.Second, 5*time.Second
	return m
}

// controllerTestWriteJSON emits one simulated Docker observation through the supplied output stream.
func controllerTestWriteJSON(w io.Writer, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// controllerTestClone isolates nested fixture state before an inspection mutation.
func controllerTestClone[T any](t *testing.T, value T) T {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var copy T
	if err := json.Unmarshal(b, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

// Capture refuses the unbounded process shortcut so controller commands must use bounded Run calls.
func (r *controllerTestRunner) Capture(context.Context, platform.Command) ([]byte, error) {
	r.captures++
	return nil, errors.New("unbounded Capture is forbidden in controller tests")
}

// Run simulates only the declared Docker lifecycle while retaining all resources for assertions.
func (r *controllerTestRunner) Run(ctx context.Context, command platform.Command) (err error) {
	_, bounded := ctx.Deadline()
	r.calls = append(r.calls, controllerTestCall{append([]string(nil), command.Args...), bounded, ctx.Err() != nil})
	if command.Name != "docker" || command.Stdin != nil || command.Stdout == nil || command.Stderr == nil || command.Dir != "" || len(command.Env) != 0 || !bounded {
		return errors.New("controller crossed an unbounded or unexpected process boundary")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if r.before != nil {
		if handled, e := r.before(ctx, command); handled {
			return e
		}
	}
	defer func() {
		if err == nil && r.after != nil {
			err = r.after(ctx, command)
		}
	}()
	a := command.Args
	if len(a) == 0 {
		return errors.New("empty Docker operation")
	}
	switch a[0] {
	case "info":
		if len(a) != 3 || a[1] != "--format" {
			return errors.New("unexpected info grammar")
		}
		if a[2] == "{{json .}}" {
			return controllerTestWriteJSON(command.Stdout, map[string]string{"ID": r.engine, "OSType": "linux", "Architecture": "aarch64", "ServerVersion": r.version})
		}
		if a[2] != "{{.ID}}|{{.OSType}}|{{.Architecture}}|{{.ServerVersion}}" {
			return errors.New("unexpected dependency info grammar")
		}
		_, err = fmt.Fprintf(command.Stdout, "%s|linux|aarch64|%s\n", r.engine, r.version)
		return err
	case "pull":
		if !reflect.DeepEqual(a, []string{"pull", "--platform", "linux/arm64", builderimage.BaseImage}) {
			return errors.New("unexpected image acquisition")
		}
		return nil
	case "image":
		if len(a) != 5 || a[1] != "inspect" || a[2] != "--format" || a[3] != "{{json .}}" {
			return errors.New("unexpected image inspection")
		}
		image := sourceImageInspection{ID: controllerTestImage, OS: "linux", Architecture: "arm64"}
		image.Config = sourceImageConfig{User: "65532:65532", WorkingDir: "/", Entrypoint: []string{"/bin/false"}, Cmd: []string{"lexr-explicit-offline-runner-required"}, Env: controllerTestDependencyEnv(), Labels: map[string]string{"io.lexr.sensors.builder": "dependencies-v1", "io.lexr.sensors.recipe": builderimage.RecipeSHA256()}}
		if a[4] == builderimage.BaseImage {
			image.ID = controllerTestBase
			image.Config = sourceImageConfig{Env: []string{controllerTestDependencyEnv()[0]}}
		} else if a[4] != controllerTestImage {
			return errors.New("unexpected foreign image reference")
		} else if r.mutateImage != nil {
			r.mutateImage(&image)
		}
		return controllerTestWriteJSON(command.Stdout, image)
	case "create":
		container, err := r.createContainer(a)
		if err != nil {
			return err
		}
		r.containers[container.ID] = &container
		r.names[strings.TrimPrefix(container.Name, "/")] = container.ID
		_, err = fmt.Fprintln(command.Stdout, container.ID)
		return err
	case "container":
		if len(a) != 5 || a[1] != "inspect" || a[2] != "--format" || a[3] != "{{json .}}" {
			return errors.New("unexpected container inspection")
		}
		id := a[4]
		if named, ok := r.names[id]; ok {
			id = named
		}
		actual, ok := r.containers[id]
		if !ok {
			return errors.New("fixture has no such owned container")
		}
		c := controllerTestClone(r.t, *actual)
		phase := controllerTestPhase(c)
		r.inspections[phase]++
		if r.mutateContainer != nil {
			r.mutateContainer(phase, r.inspections[phase], &c)
		}
		return controllerTestWriteJSON(command.Stdout, c)
	case "start":
		if len(a) != 3 || a[1] != "--attach" {
			return errors.New("unexpected start grammar")
		}
		c, ok := r.containers[a[2]]
		if !ok || c.State.Status != "created" {
			return errors.New("start did not identify a fresh owned container")
		}
		c.State.Status, c.State.StartedAt, c.State.FinishedAt = "exited", "2026-10-20T01:00:00Z", "2026-10-20T01:00:01Z"
		if r.onStart != nil {
			r.onStart(controllerTestPhase(*c), c)
		}
		if _, err = io.WriteString(command.Stdout, "simulated process boundary only\n"); err != nil {
			return err
		}
		_, err = io.WriteString(command.Stderr, "fixture diagnostics; not native execution\n")
		return err
	case "stop":
		if len(a) != 4 || a[1] != "--time" || a[2] != "10" {
			return errors.New("unexpected stop grammar")
		}
		c, ok := r.containers[a[3]]
		if !ok {
			return errors.New("stop did not identify an exact owned CID")
		}
		c.State.Status, c.State.Running, c.State.Paused, c.State.Restarting, c.State.Pid = "exited", false, false, false, 0
		c.State.ExitCode, c.State.FinishedAt = 137, "2026-10-20T01:00:02Z"
		return nil
	case "commit":
		want := []string{"commit", "--change", `ENTRYPOINT ["/bin/false"]`, "--change", `CMD ["lexr-explicit-offline-runner-required"]`, "--change", "USER 65532:65532", "--change", "WORKDIR /"}
		if len(a) != len(want)+1 || !reflect.DeepEqual(a[:len(want)], want) || r.containers[a[len(want)]] == nil || controllerTestPhase(*r.containers[a[len(want)]]) != "dependencies" {
			return errors.New("unexpected dependency commit grammar")
		}
		_, err = fmt.Fprintln(command.Stdout, controllerTestImage)
		return err
	case "cp":
		if len(a) != 3 || a[2] != "-" {
			return errors.New("archive must be streamed, not extracted")
		}
		id, name, found := strings.Cut(a[1], ":")
		c, ok := r.containers[id]
		if !found || !ok || c.State.Status != "exited" || c.State.Running || c.State.ExitCode != 0 {
			return errors.New("copy did not identify a successful stopped owned CID")
		}
		var data []byte
		switch name {
		case builderimage.PackageInventoryPath:
			data = controllerTestInventory(r.t, true)
		case builderimage.ToolchainInventoryPath:
			data = controllerTestInventory(r.t, false)
		case "/work/job/" + ArchiveName:
			if controllerTestPhase(*c) != "build" {
				return errors.New("source archive copied from another phase")
			}
			data = r.copyBytes
		default:
			return errors.New("unexpected copy path")
		}
		_, err = command.Stdout.Write(data)
		return err
	case "volume":
		if len(a) == 6 && a[1] == "ls" && a[2] == "--format" && a[3] == "{{.Name}}" && a[4] == "--filter" {
			name := strings.TrimSuffix(strings.TrimPrefix(a[5], "name=^"), "$")
			if a[5] != "name=^"+name+"$" || !validSourceVolumeIdentity(name, strings.TrimPrefix(name, sourceVolumePrefix)) {
				return errors.New("volume collision listing was not exact")
			}
			if _, exists := r.volumes[name]; exists {
				_, err = fmt.Fprintln(command.Stdout, name)
			}
			return err
		}
		if len(a) >= 2 && a[1] == "create" {
			name := a[len(a)-1]
			token := strings.TrimPrefix(name, sourceVolumePrefix)
			if !reflect.DeepEqual(a, volumeCreateArgsForProfile(name, token, r.profile)) {
				return errors.New("unexpected volume creation grammar")
			}
			if _, exists := r.volumes[name]; exists {
				return errors.New("fixture volume name was reused")
			}
			r.volumes[name] = volumeInspection{Name: name, Driver: "local", Scope: "local", Mountpoint: "/var/lib/docker/volumes/" + name + "/_data", Labels: map[string]string{sourceOwnerLabel: sourceOwnerValue, sourceTokenLabel: token, sourceInitRecipeLabel: InitRecipeSHA256(), sourceRecipeLabel: r.profile.sourceRecipeSHA256()}, Options: nil}
			_, err = fmt.Fprintln(command.Stdout, name)
			return err
		}
		if len(a) != 3 || a[1] != "inspect" {
			return errors.New("unexpected volume operation")
		}
		v, ok := r.volumes[a[2]]
		if !ok {
			return errors.New("volume was not created")
		}
		v = controllerTestClone(r.t, v)
		if r.mutateVolume != nil {
			r.mutateVolume(&v)
		}
		return controllerTestWriteJSON(command.Stdout, []volumeInspection{v})
	default:
		return fmt.Errorf("forbidden/unexpected Docker operation %q", a[0])
	}
}

// controllerTestDependencyEnv supplies the exact fixed environment expected by dependency preparation.
func controllerTestDependencyEnv() []string {
	return []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "DEBIAN_FRONTEND=noninteractive", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC", "PYTHONDONTWRITEBYTECODE=1"}
}

// controllerTestPhase identifies the simulated dependency, initialisation or source phase.
func controllerTestPhase(c sourceContainerInspection) string {
	if strings.HasPrefix(c.Name, "/lexr-sensors-deps-") {
		return "dependencies"
	}
	return c.Config.Labels[sourcePhaseLabel]
}

// createContainer decodes the closed fixture command grammar into requested and effective observations.
func (r *controllerTestRunner) createContainer(args []string) (sourceContainerInspection, error) {
	var c sourceContainerInspection
	flags := map[string][]string{}
	i := 1
	for i < len(args) && strings.HasPrefix(args[i], "--") {
		key := args[i]
		if key == "--pull=never" || key == "--read-only" {
			flags[key] = append(flags[key], "true")
			i++
			continue
		}
		if i+1 >= len(args) {
			return c, errors.New("create has no option value")
		}
		flags[key] = append(flags[key], args[i+1])
		i += 2
	}
	allowed := map[string]bool{"--pull=never": true, "--read-only": true, "--name": true, "--platform": true, "--network": true, "--user": true, "--workdir": true, "--entrypoint": true, "--cap-drop": true, "--cap-add": true, "--security-opt": true, "--pids-limit": true, "--memory": true, "--cpus": true, "--ipc": true, "--cgroupns": true, "--restart": true, "--tmpfs": true, "--mount": true, "--label": true, "--env": true}
	for name := range flags {
		if !allowed[name] {
			return c, fmt.Errorf("unexpected create option %s", name)
		}
	}
	if i >= len(args) || len(flags["--name"]) != 1 {
		return c, errors.New("create has no image or unique name")
	}
	get := func(name string) string {
		if len(flags[name]) == 1 {
			return flags[name][0]
		}
		return ""
	}
	c.ID, c.Name, c.Image = fmt.Sprintf("%064x", len(r.containers)+1), "/"+get("--name"), args[i]
	c.Config = sourceImageConfig{Image: c.Image, User: get("--user"), WorkingDir: get("--workdir"), Entrypoint: []string{get("--entrypoint")}, Cmd: append([]string(nil), args[i+1:]...), Env: append([]string(nil), flags["--env"]...), Labels: map[string]string{}}
	for _, entry := range flags["--label"] {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			return c, errors.New("malformed label")
		}
		c.Config.Labels[key] = value
	}
	h := &c.HostConfig
	h.ReadonlyRootfs, h.NetworkMode, h.IpcMode, h.CgroupnsMode = get("--read-only") == "true", get("--network"), get("--ipc"), get("--cgroupns")
	h.CapAdd, h.CapDrop, h.SecurityOpt = flags["--cap-add"], flags["--cap-drop"], flags["--security-opt"]
	h.PidsLimit, _ = strconv.ParseInt(get("--pids-limit"), 10, 64)
	h.NanoCpus, _ = strconv.ParseInt(get("--cpus"), 10, 64)
	h.NanoCpus *= 1_000_000_000
	switch get("--memory") {
	case "4g":
		h.Memory = 4 << 30
	case "256m":
		h.Memory = 256 << 20
	default:
		return c, errors.New("unexpected memory limit")
	}
	h.RestartPolicy.Name = get("--restart")
	h.Tmpfs = map[string]string{}
	for _, entry := range flags["--tmpfs"] {
		name, value, ok := strings.Cut(entry, ":")
		if !ok {
			return c, errors.New("malformed tmpfs")
		}
		h.Tmpfs[name] = value
	}
	for _, text := range flags["--mount"] {
		fields := map[string]string{}
		for _, field := range strings.Split(text, ",") {
			key, value, _ := strings.Cut(field, "=")
			fields[key] = value
		}
		mount := sourceHostMount{Type: fields["type"], Source: fields["src"], Target: fields["dst"]}
		_, mount.ReadOnly = fields["readonly"]
		effective := sourceEffectiveMount{Type: mount.Type, Source: mount.Source, Destination: mount.Target, RW: !mount.ReadOnly}
		if mount.Type == "volume" {
			_, noCopy := fields["volume-nocopy"]
			mount.VolumeOptions = &sourceVolumeOptions{NoCopy: noCopy}
			effective.Name, effective.Driver, effective.Source = mount.Source, "local", "/var/lib/docker/volumes/"+mount.Source+"/_data"
		} else {
			mount.BindOptions = &sourceBindOptions{Propagation: fields["bind-propagation"]}
			effective.Mode, effective.Propagation = "ro", fields["bind-propagation"]
		}
		h.Mounts = append(h.Mounts, mount)
		c.Mounts = append(c.Mounts, effective)
	}
	c.State.Status = "created"
	if controllerTestPhase(c) == "build" {
		initID := r.names["lexr-sensors-init-"+c.Config.Labels[sourceTokenLabel]]
		if initID == "" || r.containers[initID].State.Status != "exited" || r.containers[initID].State.ExitCode != 0 {
			return c, errors.New("source phase created before successful init")
		}
	}
	return c, nil
}

// controllerTestInventory wraps explicitly synthetic package or toolchain rows in a copy archive.
func controllerTestInventory(t *testing.T, packages bool) []byte {
	t.Helper()
	name := filepath.Base(builderimage.ToolchainInventoryPath)
	text := "lexr-sensors-builder-toolchain-v1\nos_id=ubuntu\nos_version=26.04\narchitecture=arm64\n[build-identity]\nlexr-sensor-build:x:65532:65532:Lexr sensor source builder:/work/job/home:/usr/sbin/nologin\nlexr-sensor-build:x:65532:\npassword_status=L\n[gcc-target]\naarch64-linux-gnu\n[gcc-version]\n14.2.0\n[gcc-details]\nfixture gcc\n[linker]\nfixture ld\n[readelf]\nfixture readelf\n[meson]\n1.7\n[ninja]\n1.12\n[python]\n3.13\n[pkgconf]\n1.8\n[protoc]\n3.21\n[protoc-c]\n1.5\n[development-api-versions:fixture]\n1.0\n"
	if packages {
		name = filepath.Base(builderimage.PackageInventoryPath)
		var rows []string
		for i := 0; i < 30; i++ {
			rows = append(rows, fmt.Sprintf("fixture-package-%02d=1.0", i))
		}
		text = strings.Join(rows, "\n") + "\n"
	}
	return writeFixtureTar(t, []fixtureTarMember{{tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(text)), Format: tar.FormatUSTAR}, []byte(text)}})
}

// controllerTestParents creates fresh private repository, work and output parents for filesystem fixtures.
func controllerTestParents(t *testing.T) Request {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// StageRepository intentionally makes snapshots read-only. Restore only
	// this test's fresh temporary directories for testing.T's ordinary cleanup.
	t.Cleanup(func() {
		_ = filepath.WalkDir(parent, func(name string, entry fs.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				return os.Chmod(name, 0o700)
			}
			return err
		})
	})
	r := Request{RepositoryRoot: filepath.Join(parent, "repository"), WorkParent: filepath.Join(parent, "work"), OutputParent: filepath.Join(parent, "output")}
	for _, name := range []string{r.RepositoryRoot, r.WorkParent, r.OutputParent} {
		if err := os.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// controllerTestNoEffects requires planning refusals to leave process and filesystem boundaries untouched.
func controllerTestNoEffects(t *testing.T, r *controllerTestRunner, request Request) {
	t.Helper()
	if len(r.calls) != 0 || r.captures != 0 {
		t.Fatalf("safe refusal contacted a process boundary: %+v", r.calls)
	}
	for _, name := range []string{request.WorkParent, request.OutputParent} {
		entries, err := os.ReadDir(name)
		if err != nil || len(entries) != 0 {
			t.Fatalf("safe refusal modified parent %q: %v, %v", name, entries, err)
		}
	}
}

// TestControllerSafeBlockedPlansAndDryRunsHaveNoEffects keeps unsupported plans non-executing.
func TestControllerSafeBlockedPlansAndDryRunsHaveNoEffects(t *testing.T) {
	for _, host := range []hostcap.Host{{GOOS: "linux", GOARCH: "amd64"}, {GOOS: "windows", GOARCH: "arm64"}, {}} {
		t.Run(host.GOOS+"-"+host.GOARCH, func(t *testing.T) {
			r, request := newControllerTestRunner(t), controllerTestParents(t)
			m := controllerTestManager(r)
			m.host = host
			// The source deliberately has no approved inputs: blocked-host plans
			// must remain truthful without opening source data or Docker.
			p, err := m.Plan(context.Background(), request)
			if err != nil || p.Executable || p.ExecutionBlocker == "" || p.SourceAuthenticated || p.Jobs != 2 {
				t.Fatalf("unsupported host plan differs: %+v, %v", p, err)
			}
			result, err := m.Run(context.Background(), request)
			if err == nil || result.BuildID != "" || result.Publication.Published {
				t.Fatalf("blocked host executed: %+v, %v", result, err)
			}
			request.DryRun = true
			result, err = m.Run(context.Background(), request)
			if err != nil || result.Plan.Executable || result.Plan.SourceAuthenticated {
				t.Fatalf("blocked dry-run became execution authority: %+v, %v", result, err)
			}
			controllerTestNoEffects(t, r, request)
		})
	}
	t.Run("no-pull-needs-private-handle", func(t *testing.T) {
		r, request := newControllerTestRunner(t), controllerTestParents(t)
		m := controllerTestManager(r)
		request.NoPull = true
		p, err := m.Plan(context.Background(), request)
		if err != nil || p.Executable || p.ExecutionBlocker == "" || p.SourceAuthenticated {
			t.Fatalf("NoPull fabricated dependency readiness: %+v, %v", p, err)
		}
		if result, err := m.Run(context.Background(), request); err == nil || result.BuildID != "" {
			t.Fatalf("NoPull ran without private handle: %+v, %v", result, err)
		}
		controllerTestNoEffects(t, r, request)
	})
}

// TestControllerInvalidRequestsAndUnauthenticatedSourceHaveNoEffects rejects invalid authority before work.
func TestControllerInvalidRequestsAndUnauthenticatedSourceHaveNoEffects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Manager, *Request)
	}{
		{"negative-jobs", func(_ *Manager, r *Request) { r.Jobs = -1 }},
		{"too-many-jobs", func(_ *Manager, r *Request) { r.Jobs = 9 }},
		{"blank-source", func(_ *Manager, r *Request) { r.RepositoryRoot = " " }},
		{"comma-source", func(_ *Manager, r *Request) { r.RepositoryRoot += ",other" }},
		{"invalid-utf8-source", func(_ *Manager, r *Request) { r.RepositoryRoot += string([]byte{0xff}) }},
		{"quote-output", func(_ *Manager, r *Request) { r.OutputParent += `"` }},
		{"control-work", func(_ *Manager, r *Request) { r.WorkParent += "\n" }},
		{"missing-source", func(_ *Manager, r *Request) { r.RepositoryRoot += "-missing" }},
		{"overlap-roots", func(_ *Manager, r *Request) { r.OutputParent = r.RepositoryRoot }},
		{"work-not-private", func(_ *Manager, r *Request) {
			if err := os.Chmod(r.WorkParent, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"uninitialized-timeout", func(m *Manager, _ *Request) { m.timeout = 0 }},
		{"uninitialized-dependencies", func(m *Manager, _ *Request) { m.dependencies = nil }},
		{"unapproved-source", func(_ *Manager, _ *Request) {}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, initial := newControllerTestRunner(t), controllerTestParents(t)
			request, m := initial, controllerTestManager(r)
			tc.mutate(m, &request)
			if _, err := m.Plan(context.Background(), request); err == nil {
				t.Fatal("invalid plan accepted")
			}
			if result, err := m.Run(context.Background(), request); err == nil || result.Publication.Published || result.BuildID != "" {
				t.Fatalf("invalid request executed: %+v, %v", result, err)
			}
			controllerTestNoEffects(t, r, initial)
		})
	}
	r, request := newControllerTestRunner(t), controllerTestParents(t)
	m := controllerTestManager(r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Plan(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled plan ignored context: %v", err)
	}
	if _, err := m.Run(ctx, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run ignored context: %v", err)
	}
	var absent *Manager
	if _, err := absent.Plan(context.Background(), request); err == nil {
		t.Fatal("nil Manager accepted")
	}
	controllerTestNoEffects(t, r, request)
}

// approvedControllerRequest requires explicit authenticated OE sources before enabling composition fixtures.
func approvedControllerRequest(t *testing.T) Request {
	t.Helper()
	root := os.Getenv("LEXR_TEST_SENSOR_OE_ROOT")
	if root == "" {
		t.Skip("set LEXR_TEST_SENSOR_OE_ROOT for compiled-authority controller tests; no Docker/source execution")
	}
	if runtime.GOARCH != "arm64" || runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("dependency Manager truthfully requires an ARM64 Linux/macOS test binary")
	}
	composition, err := source.Verify(root)
	if err != nil {
		t.Fatalf("explicit approved source opt-in is invalid: %v", err)
	}
	if composition.SourceCommit != SourceCommit || composition.SourceFileCount != 949 || composition.SourceSymlinkCount != 6 {
		t.Fatal("approved source composition differs from compiled lineage")
	}
	r := controllerTestParents(t)
	r.RepositoryRoot = root
	return r
}

// The archive contains serialised, nonexecuted ELF metadata fixtures. Actual
// approved licence bytes are read only after the full source.Verify gate. The
// unchanged public decoder supplies all authority; the fixture policy seam and
// VerifiedArtifacts private fields are never used by this controller fixture.
func controllerTestArchive(t *testing.T, repository string, jobs int) []byte {
	t.Helper()
	if _, err := source.Verify(repository); err != nil {
		t.Fatal(err)
	}
	files, _ := artifactFixture(t)
	for target, member := range map[string]string{"COPYING.libqmi": "libqmi/source/COPYING", "COPYING.LIB.libqmi": "libqmi/source/COPYING.LIB", "LICENSE.libssc": "libssc/source/LICENSE", "COPYING.iio-sensor-proxy": "iio-sensor-proxy/source/COPYING"} {
		data, err := os.ReadFile(filepath.Join(repository, "userspace", "sensors", filepath.FromSlash(member)))
		if err != nil {
			t.Fatal(err)
		}
		files[target] = data
	}
	rewriteFixtureReport(t, files, func(r *BuildReport) { r.Jobs = jobs })
	copyBytes := wrapFixtureCopy(t, writeFixtureTar(t, fixtureMembers(files)))
	if _, err := DecodeDockerCopy(copyBytes, jobs); err != nil {
		t.Fatalf("production decoder refused compiled-authority metadata fixture: %v", err)
	}
	return copyBytes
}

// controllerTestAssertNoDeletion rejects unbounded, destructive or unowned simulator operations.
func controllerTestAssertNoDeletion(t *testing.T, r *controllerTestRunner) {
	t.Helper()
	if r.captures != 0 {
		t.Fatal("controller used unbounded Capture")
	}
	for _, call := range r.calls {
		if !call.deadline {
			t.Fatalf("controller used an unbounded command: %+v", call)
		}
		if len(call.args) > 0 && call.args[0] == "stop" && call.canceled {
			t.Fatal("recovery stop reused an already-cancelled context")
		}
		if len(call.args) == 0 || call.args[0] == "rm" || call.args[0] == "prune" || call.args[0] == "run" || call.args[0] == "build" || call.args[0] == "exec" || call.args[0] == "volume" && len(call.args) > 1 && call.args[1] != "create" && call.args[1] != "inspect" && call.args[1] != "ls" {
			t.Fatalf("controller deleted or used an unowned execution path: %+v", call.args)
		}
	}
}

// controllerTestCount counts a named operation across the recorded simulated command stream.
func controllerTestCount(r *controllerTestRunner, operation string) int {
	n := 0
	for _, call := range r.calls {
		if len(call.args) > 0 && call.args[0] == operation {
			n++
		}
	}
	return n
}

// controllerTestWithheld checks that a failed chain publishes nothing and retains its resources.
func controllerTestWithheld(t *testing.T, r *controllerTestRunner, result Result, err error) {
	t.Helper()
	if err == nil || result.Publication.Published || result.Publication.Directory != "" {
		t.Fatalf("failed source chain published outputs: %+v, %v", result, err)
	}
	entries, readErr := os.ReadDir(result.Plan.OutputParent)
	if result.Plan.OutputParent != "" && (readErr != nil || len(entries) != 0) {
		t.Fatalf("failed source chain wrote publication output: %v, %v", entries, readErr)
	}
	controllerTestAssertNoDeletion(t, r)
}

// TestControllerApprovedPlanAndPublicationOptIn composes real source authority and decoding with simulated execution.
func TestControllerApprovedPlanAndPublicationOptIn(t *testing.T) {
	request := approvedControllerRequest(t)
	r := newControllerTestRunner(t)
	m := controllerTestManager(r)
	r.copyBytes = controllerTestArchive(t, request.RepositoryRoot, 2)
	request.DryRun = true
	a, err := m.Plan(context.Background(), request)
	if err != nil || !a.Executable || !a.SourceAuthenticated || a.Jobs != 2 || a.InitRecipeSHA256 != InitRecipeSHA256() || a.SourceRecipeSHA256 != SourceRecipeSHA256() {
		t.Fatalf("approved plan differs: %+v, %v", a, err)
	}
	b, err := m.Plan(context.Background(), request)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatal("approved plan is not deterministic")
	}
	result, err := m.Run(context.Background(), request)
	if err != nil || result.BuildID != "" || result.Publication.Published {
		t.Fatalf("approved dry-run executed: %+v, %v", result, err)
	}
	controllerTestNoEffects(t, r, request)
	request.DryRun = false
	result, err = m.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("simulated controller chain failed: %v", err)
	}
	if !result.VolumeVerified || result.Init.TerminalUnknown || result.Source.TerminalUnknown || result.Init.Terminal == nil || result.Source.Terminal == nil || !successfulSourceTerminal(*result.Init.Terminal) || !successfulSourceTerminal(*result.Source.Terminal) || !result.Publication.Published || !result.Publication.LocationVerified || !result.Publication.Durable || len(result.Artifacts) != 16 || result.BuildReport == nil || result.BuildReport.SourceCommit != SourceCommit {
		t.Fatalf("successful controller receipt incomplete: %+v", result)
	}
	if result.Dependencies.Prepared == nil || m.prepared != result.Dependencies.Prepared || result.Dependencies.Provenance == nil || result.Dependencies.Provenance.ImageID != controllerTestImage {
		t.Fatal("controller did not preserve its own real dependency Prepared chain")
	}
	data, err := os.ReadFile(filepath.Join(result.Publication.Directory, publicationReceiptName))
	if err != nil || bytesDigest(data) != result.Publication.ReceiptSHA256 {
		t.Fatalf("published receipt bytes differ: %v", err)
	}
	var receipt map[string]any
	if err := json.Unmarshal(data, &receipt); err != nil || receipt["schema"] != "lexr-sensors-source-receipt-v1" || receipt["build_id"] != result.BuildID || !strings.Contains(fmt.Sprint(receipt["scope"]), "not an installable bundle") {
		t.Fatalf("publication overclaims scope: %v, %v", receipt, err)
	}
	for _, local := range []string{request.RepositoryRoot, request.WorkParent, request.OutputParent, result.SnapshotDirectory} {
		if bytes.Contains(data, []byte(local)) {
			t.Fatalf("public receipt leaked local path %q", local)
		}
	}
	for _, artifact := range result.Artifacts {
		actual, err := os.ReadFile(filepath.Join(result.Publication.Directory, artifact.Name))
		if err != nil || bytesDigest(actual) != artifact.SHA256 {
			t.Fatalf("published %s differs: %v", artifact.Name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(result.WorkDirectory, "00-intent.json")); err != nil {
		t.Fatalf("retained resource intent missing: %v", err)
	}
	transport, err := os.ReadFile(result.TransportArchive)
	if err != nil || !bytes.Equal(transport, r.copyBytes) {
		t.Fatalf("successful raw transport evidence differs: %v", err)
	}
	request.NoPull = true
	second, err := m.Run(context.Background(), request)
	if err != nil || !second.Publication.Published || second.BuildID == result.BuildID || second.VolumeName == result.VolumeName || second.SnapshotDirectory == result.SnapshotDirectory || controllerTestCount(r, "pull") != 1 || controllerTestCount(r, "commit") != 1 {
		t.Fatalf("NoPull reused resources or lost exact owned dependency readiness: %+v, %v", second, err)
	}
	if second.Dependencies.Terminal != nil || second.Init.Terminal == nil || second.Source.Terminal == nil {
		t.Fatal("historical dependency terminal was confused with fresh phase proof")
	}
	if current, err := source.Verify(request.RepositoryRoot); err != nil || !reflect.DeepEqual(current, a.Source) {
		t.Fatalf("original approved source changed: %v", err)
	}
	controllerTestAssertNoDeletion(t, r)
}

// TestControllerApprovedRefusalsAndTerminalLifecycleOptIn withholds publication across phase and terminal failures.
func TestControllerApprovedRefusalsAndTerminalLifecycleOptIn(t *testing.T) {
	_ = approvedControllerRequest(t)
	cases := []struct {
		name      string
		configure func(*controllerTestRunner, *Manager, Request)
		noSource  bool
		unknown   bool
		wantStop  bool
	}{
		{"dependency-acquisition-failure", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.before = func(_ context.Context, c platform.Command) (bool, error) {
				return c.Args[0] == "pull", errors.New("fixture acquisition refusal")
			}
		}, true, false, false},
		{"invalid-token", func(_ *controllerTestRunner, m *Manager, _ Request) {
			m.token = func() (string, error) { return "not-a-token", nil }
		}, true, false, false},
		{"existing-volume-not-reused", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.before = func(_ context.Context, c platform.Command) (bool, error) {
				if len(c.Args) == 6 && c.Args[0] == "volume" && c.Args[1] == "ls" {
					name := strings.TrimSuffix(strings.TrimPrefix(c.Args[5], "name=^"), "$")
					_, err := fmt.Fprintln(c.Stdout, name)
					return true, err
				}
				return false, nil
			}
		}, true, false, false},
		{"volume-options", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.mutateVolume = func(v *volumeInspection) {
				v.Options = map[string]string{"type": "none", "device": "/host", "o": "bind"}
			}
		}, true, false, false},
		{"init-nonzero", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.onStart = func(p string, c *sourceContainerInspection) {
				if p == "init" {
					c.State.ExitCode = 9
				}
			}
		}, true, false, false},
		{"init-oom", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.onStart = func(p string, c *sourceContainerInspection) {
				if p == "init" {
					c.State.OOMKilled = true
				}
			}
		}, true, false, false},
		{"init-policy-after-start", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.mutateContainer = func(p string, n int, c *sourceContainerInspection) {
				if p == "init" && n > 1 {
					c.HostConfig.NetworkMode = "bridge"
				}
			}
		}, true, false, false},
		{"source-nonzero", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.onStart = func(p string, c *sourceContainerInspection) {
				if p == "build" {
					c.State.ExitCode = 7
				}
			}
		}, false, false, false},
		{"source-running-recovers-exact-cid", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.onStart = func(p string, c *sourceContainerInspection) {
				if p == "build" {
					c.State.Status, c.State.Running, c.State.Pid, c.State.FinishedAt = "running", true, 321, ""
				}
			}
		}, false, false, true},
		{"source-create-client-error-recovers-owned-name", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.after = func(_ context.Context, c platform.Command) error {
				if c.Args[0] == "create" {
					for _, actual := range r.containers {
						if controllerTestPhase(*actual) == "build" {
							actual.State.Status, actual.State.Running, actual.State.Pid = "running", true, 321
							actual.State.StartedAt = "2026-10-20T01:00:00Z"
							return errors.New("fixture client failed after daemon created the owned name")
						}
					}
				}
				return nil
			}
		}, false, false, true},
		{"source-foreign-owner-never-stopped", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.mutateContainer = func(p string, _ int, c *sourceContainerInspection) {
				if p == "build" {
					c.Config.Labels[sourceTokenLabel] = strings.Repeat("f", 24)
					c.State.Status, c.State.Running, c.State.Pid = "running", true, 321
				}
			}
		}, false, true, false},
		{"source-terminal-recheck", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.mutateContainer = func(p string, n int, c *sourceContainerInspection) {
				if p == "build" && n >= 3 {
					c.State.FinishedAt = "2026-10-20T01:00:09Z"
				}
			}
		}, false, false, false},
		{"source-previous-success-then-running-recovered", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.mutateContainer = func(p string, n int, c *sourceContainerInspection) {
				if p == "build" && n == 3 {
					// Change the simulated daemon's actual state, not merely one
					// inspection response. Earlier stopped proof is now stale.
					actual := r.containers[c.ID]
					actual.State.Status, actual.State.Running, actual.State.Pid, actual.State.FinishedAt = "running", true, 321, ""
					*c = controllerTestClone(r.t, *actual)
				}
			}
		}, false, false, true},
		{"init-terminal-recheck", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.mutateContainer = func(p string, n int, c *sourceContainerInspection) {
				if p == "init" && n >= 3 {
					c.State.ExitCode = 8
				}
			}
		}, false, false, false},
		{"archive-empty", func(r *controllerTestRunner, _ *Manager, _ Request) { r.copyBytes = nil }, false, false, false},
		{"archive-truncated", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.copyBytes = r.copyBytes[:len(r.copyBytes)-1024]
		}, false, false, false},
		{"engine-changed-after-copy", func(r *controllerTestRunner, _ *Manager, _ Request) {
			r.after = func(_ context.Context, c platform.Command) error {
				if c.Args[0] == "cp" && strings.HasSuffix(c.Args[1], "/"+ArchiveName) {
					r.engine = "controller-fixture-other-engine-0001"
				}
				return nil
			}
			// Earlier terminal observations cannot establish current state after
			// the bound engine identity changes. Recovery must refuse, not rebind.
		}, false, true, false},
		{"image-changed-after-copy", func(r *controllerTestRunner, _ *Manager, _ Request) {
			changed := false
			r.after = func(_ context.Context, c platform.Command) error {
				if c.Args[0] == "cp" && strings.HasSuffix(c.Args[1], "/"+ArchiveName) {
					changed = true
				}
				return nil
			}
			r.mutateImage = func(i *sourceImageInspection) {
				if changed {
					i.Config.User = "0:0"
				}
			}
		}, false, false, false},
		{"snapshot-changed-after-init", func(r *controllerTestRunner, _ *Manager, request Request) {
			r.after = func(_ context.Context, c platform.Command) error {
				if c.Args[0] == "start" {
					actual := r.containers[c.Args[2]]
					if controllerTestPhase(*actual) == "init" {
						target := filepath.Join(request.WorkParent, "sensor-build-"+actual.Config.Labels[sourceTokenLabel], "repository", "userspace", "sensors", "libssc", "source", "LICENSE")
						if err := os.Chmod(target, 0o644); err != nil {
							return err
						}
						return os.WriteFile(target, []byte("changed test snapshot only\n"), 0o644)
					}
				}
				return nil
			}
		}, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := approvedControllerRequest(t)
			r := newControllerTestRunner(t)
			m := controllerTestManager(r)
			r.copyBytes = controllerTestArchive(t, request.RepositoryRoot, 2)
			tc.configure(r, m, request)
			result, err := m.Run(context.Background(), request)
			controllerTestWithheld(t, r, result, err)
			if tc.noSource && result.Source.ContainerID != "" {
				t.Fatal("source phase ran before required gate")
			}
			if result.Source.TerminalUnknown != tc.unknown {
				t.Fatalf("unknown terminal receipt differs: %+v, %v", result.Source, err)
			}
			if (controllerTestCount(r, "stop") != 0) != tc.wantStop {
				t.Fatalf("unexpected recovery stop count: %d", controllerTestCount(r, "stop"))
			}
			if tc.name == "engine-changed-after-copy" {
				for _, phase := range []PhaseResult{result.Init, result.Source} {
					if !phase.TerminalUnknown || !strings.Contains(phase.RecoveryError, "engine identity changed") || phase.Terminal == nil || !successfulSourceTerminal(*phase.Terminal) || phase.Terminal.ContainerID != phase.ContainerID {
						t.Fatalf("engine change lost historical proof or claimed current terminal authority: %+v", phase)
					}
				}
				copySeen := false
				for _, call := range r.calls {
					if call.args[0] == "cp" && strings.HasSuffix(call.args[1], "/"+ArchiveName) {
						if copySeen {
							t.Fatal("engine-change fixture copied the archive more than once")
						}
						copySeen = true
						continue
					}
					if copySeen && (call.args[0] == "stop" || call.args[0] == "container" && call.args[1] == "inspect") {
						t.Fatal("engine change allowed inspection or recovery stop against an unbound engine")
					}
				}
				if !copySeen {
					t.Fatal("engine-change fixture never reached the copy boundary")
				}
			}
			if tc.wantStop {
				for _, call := range r.calls {
					if call.args[0] == "stop" && call.args[3] != result.Source.ContainerID {
						t.Fatal("recovery stopped a different phase/container ID")
					}
				}
			}
			if result.TransportArchive != "" {
				data, readErr := os.ReadFile(result.TransportArchive)
				info, statErr := os.Stat(result.TransportArchive)
				if readErr != nil || statErr != nil || !bytes.Equal(data, r.copyBytes) || info.Mode().Perm() != 0o600 {
					t.Fatalf("private raw transport evidence was not retained exactly: %v, %v", readErr, statErr)
				}
			}
			if result.Source.ContainerID == "" || result.Source.Terminal == nil || !successfulSourceTerminal(*result.Source.Terminal) {
				for _, call := range r.calls {
					if call.args[0] == "cp" && strings.HasSuffix(call.args[1], "/"+ArchiveName) {
						t.Fatal("archive copied without source successful terminal")
					}
				}
			}
		})
	}
}

// TestControllerApprovedCancellationUsesFreshRecoveryOptIn observes owned termination through a fresh bounded context.
func TestControllerApprovedCancellationUsesFreshRecoveryOptIn(t *testing.T) {
	for _, boundary := range []string{"dependency-commit", "volume-create", "init-start", "source-start", "source-copy"} {
		t.Run(boundary, func(t *testing.T) {
			request := approvedControllerRequest(t)
			r := newControllerTestRunner(t)
			m := controllerTestManager(r)
			r.copyBytes = controllerTestArchive(t, request.RepositoryRoot, 2)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r.after = func(_ context.Context, c platform.Command) error {
				match := boundary == "dependency-commit" && c.Args[0] == "commit" || boundary == "volume-create" && len(c.Args) > 1 && c.Args[0] == "volume" && c.Args[1] == "create" || boundary == "source-copy" && c.Args[0] == "cp" && strings.HasSuffix(c.Args[1], "/"+ArchiveName)
				if c.Args[0] == "start" {
					phase := controllerTestPhase(*r.containers[c.Args[2]])
					match = match || boundary == "init-start" && phase == "init" || boundary == "source-start" && phase == "build"
					if match {
						actual := r.containers[c.Args[2]]
						actual.State.Status, actual.State.Running, actual.State.Pid, actual.State.FinishedAt = "running", true, 321, ""
					}
				}
				if match {
					cancel() // A malicious/buggy runner still returns nil afterward.
				}
				return nil
			}
			result, err := m.Run(ctx, request)
			controllerTestWithheld(t, r, result, err)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation did not propagate: %v", err)
			}
			for _, call := range r.calls {
				if call.canceled {
					// The imported dependency producer may attempt this inspect
					// after a fake commit returns nil despite parent cancellation.
					// The runner refuses immediately. It is neither source/init
					// work nor a recovery command; no Prepared is granted.
					want := []string{"image", "inspect", "--format", "{{json .}}", controllerTestImage}
					if boundary != "dependency-commit" || !reflect.DeepEqual(call.args, want) {
						t.Fatalf("source or recovery operation used a cancelled context: %v", call.args)
					}
				}
			}
			wantStops := 0
			if boundary == "init-start" || boundary == "source-start" {
				wantStops = 1
			}
			if controllerTestCount(r, "stop") != wantStops || result.Init.TerminalUnknown || result.Source.TerminalUnknown {
				t.Fatalf("fresh-context exact-owned recovery differs: %+v, %v", result, err)
			}
			if wantStops != 0 {
				wantID := result.Source.ContainerID
				if boundary == "init-start" {
					wantID = result.Init.ContainerID
				}
				for _, call := range r.calls {
					if call.args[0] == "stop" && call.args[3] != wantID {
						t.Fatal("cancel recovery stopped another phase's container")
					}
				}
			}
		})
	}
}

// TestControllerApprovedRetainsAndRevalidatesDependencyAfterSourceFailureOptIn preserves readiness without trusting a changed daemon.
func TestControllerApprovedRetainsAndRevalidatesDependencyAfterSourceFailureOptIn(t *testing.T) {
	request := approvedControllerRequest(t)
	r := newControllerTestRunner(t)
	m := controllerTestManager(r)
	r.copyBytes = nil
	failed, err := m.Run(context.Background(), request)
	controllerTestWithheld(t, r, failed, err)
	if m.prepared == nil || failed.Dependencies.Prepared == nil {
		t.Fatal("successful dependency readiness was discarded after source failure")
	}
	r.copyBytes = controllerTestArchive(t, request.RepositoryRoot, 2)
	request.NoPull = true
	result, err := m.Run(context.Background(), request)
	if err != nil || !result.Publication.Published || controllerTestCount(r, "pull") != 1 || controllerTestCount(r, "commit") != 1 {
		t.Fatalf("same-manager readiness was not revalidated and reused: %+v, %v", result, err)
	}
	r.engine = "controller-fixture-other-engine-0002"
	previousVolumes := len(r.volumes)
	result, err = m.Run(context.Background(), request)
	if err == nil || result.BuildID != "" || len(r.volumes) != previousVolumes || result.Publication.Published {
		t.Fatalf("foreign engine reused owned Prepared authority: %+v, %v", result, err)
	}
	controllerTestAssertNoDeletion(t, r)
}

// TestControllerBoundedBufferRejectsWriteShortcuts prevents writer interfaces bypassing the output limit.
func TestControllerBoundedBufferRejectsWriteShortcuts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &buildBoundedBuffer{limit: 3, cancel: cancel}
	if n, err := io.WriteString(b, "abcd"); err == nil || n != 0 || !b.exceeded || ctx.Err() == nil || b.data.Len() != 0 {
		t.Fatal("io.WriteString bypassed bounded Docker output")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	b = &buildBoundedBuffer{limit: 3, cancel: cancel}
	if _, err := io.Copy(b, strings.NewReader("abcd")); err == nil || !b.exceeded || ctx.Err() == nil || b.data.Len() != 0 {
		t.Fatal("io.Copy bypassed bounded Docker output")
	}
}

// Keep this test's simulated dependency package accounting visibly sorted.
func TestControllerSyntheticPackageInventoryIsNotNativeEvidence(t *testing.T) {
	archive := controllerTestInventory(t, true)
	r := tar.NewReader(bytes.NewReader(archive))
	h, err := r.Next()
	if err != nil || h.Name != filepath.Base(builderimage.PackageInventoryPath) {
		t.Fatal("synthetic dependency inventory is malformed")
	}
	data, err := io.ReadAll(r)
	rows := strings.Split(strings.TrimSpace(string(data)), "\n")
	if err != nil || len(rows) != 30 || !sort.StringsAreSorted(rows) || !strings.HasPrefix(rows[0], "fixture-package-") {
		t.Fatal("synthetic package records must not masquerade as observed host packages")
	}
}
