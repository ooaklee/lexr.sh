package builderimage

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/hostcap"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// managerTestEngine is the synthetic engine identity used by controlled observations.
const managerTestEngine = "fixture-engine-0000000000000001"

// managerTestToken fixes ownership names without using real Docker resources.
const managerTestToken = "dddddddddddddddddddddddd"

// managerTestCID is the complete synthetic container identity.
var managerTestCID = strings.Repeat("c", 64)

// managerTestBase identifies the synthetic immutable dependency base.
var managerTestBase = "sha256:" + strings.Repeat("a", 64)

// managerTestImage identifies the synthetic committed dependency image.
var managerTestImage = "sha256:" + strings.Repeat("b", 64)

// managerTestCall records a simulated Docker command and its context state.
type managerTestCall struct {
	args         []string
	contextError error
	deadline     bool
}

// This runner substitutes only Docker's process/inspection boundary. It never
// executes a shell, dependency script, native source, Docker, or a transport.
type managerTestRunner struct {
	calls                     []managerTestCall
	captures                  int
	inspections               int
	started, running, stopped bool
	base                      imageInspection
	image                     imageInspection
	packages, tools           []byte
	hook                      func(context.Context, platform.Command) (bool, error)
	mutateContainer           func(int, *containerInspection)
}

// managerTestEnvironment returns the fixed environment expected by dependency fixtures.
func managerTestEnvironment() []string {
	return []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "DEBIAN_FRONTEND=noninteractive", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC", "PYTHONDONTWRITEBYTECODE=1"}
}

// newManagerTestRunner supplies complete successful observations entirely in memory.
func newManagerTestRunner(t *testing.T) *managerTestRunner {
	t.Helper()
	f := &managerTestRunner{}
	f.base = imageInspection{ID: managerTestBase, OS: "linux", Architecture: "arm64", Config: imageConfig{Env: []string{managerTestEnvironment()[0], "LANG=old", "DEBIAN_FRONTEND=old"}}}
	f.image = imageInspection{ID: managerTestImage, OS: "linux", Architecture: "arm64", Config: imageConfig{User: "65532:65532", WorkingDir: "/", Entrypoint: []string{"/bin/false"}, Cmd: []string{"lexr-explicit-offline-runner-required"}, Labels: map[string]string{"io.lexr.sensors.builder": "dependencies-v1", "io.lexr.sensors.recipe": RecipeSHA256()}}}
	// Synthetic sorted records exercise accounting, not actual package presence.
	var rows []string
	for i := 0; i < 30; i++ {
		rows = append(rows, fmt.Sprintf("fixture-package-%02d=1.0", i))
	}
	packages := strings.Join(rows, "\n") + "\n"
	tools := "lexr-sensors-builder-toolchain-v1\nos_id=ubuntu\nos_version=26.04\narchitecture=arm64\n[build-identity]\nlexr-sensor-build:x:65532:65532:Lexr sensor source builder:/work/job/home:/usr/sbin/nologin\nlexr-sensor-build:x:65532:\npassword_status=L\n[gcc-target]\naarch64-linux-gnu\n[gcc-version]\n14.2.0\n[gcc-details]\nfixture gcc\n[linker]\nfixture ld\n[readelf]\nfixture readelf\n[meson]\n1.7\n[ninja]\n1.12\n[python]\n3.13\n[pkgconf]\n1.8\n[protoc]\n3.21\n[protoc-c]\n1.5\n[development-api-versions:fixture]\n1.0\n"
	f.packages = managerTestTar(t, []tar.Header{{Name: path.Base(PackageInventoryPath), Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(packages))}}, [][]byte{[]byte(packages)})
	f.tools = managerTestTar(t, []tar.Header{{Name: path.Base(ToolchainInventoryPath), Mode: 0o644, Typeflag: tar.TypeReg, Size: int64(len(tools))}}, [][]byte{[]byte(tools)})
	return f
}

// newManagerUnderTest fixes the synthetic host and ownership token for one manager.
func newManagerUnderTest(r *managerTestRunner) *Manager {
	m := New(r)
	m.host = hostcap.Host{GOOS: "linux", GOARCH: "arm64"}
	m.token = func() (string, error) { return managerTestToken, nil }
	m.timeout, m.recoveryTimeout = 5*time.Second, time.Second
	return m
}

// managerTestContainer returns a container matching the fixed dependency policy.
func managerTestContainer() containerInspection {
	c := containerInspection{ID: managerTestCID, Name: "/lexr-sensors-deps-" + managerTestToken, Image: managerTestBase}
	c.Config = imageConfig{User: "0:0", WorkingDir: "/", Entrypoint: []string{"/usr/bin/timeout"}, Cmd: []string{"--signal=TERM", "--kill-after=5s", "18m", "/bin/bash", "-euo", "pipefail", "-c", DependencyScript}, Env: managerTestEnvironment(), Labels: map[string]string{"io.lexr.sensors.builder": "dependencies-v1", "io.lexr.sensors.recipe": RecipeSHA256(), "io.lexr.sensors.token": managerTestToken}}
	c.HostConfig.NetworkMode = "bridge"
	c.HostConfig.PidsLimit, c.HostConfig.Memory, c.HostConfig.NanoCpus = 256, 4<<30, 2_000_000_000
	c.HostConfig.IpcMode = "private"
	c.HostConfig.RestartPolicy.Name = "no"
	c.HostConfig.CapAdd = []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETUID", "SETGID"}
	c.HostConfig.CapDrop = []string{"ALL"}
	c.HostConfig.SecurityOpt = []string{"no-new-privileges"}
	c.HostConfig.Tmpfs = map[string]string{"/tmp": "rw,nosuid,nodev,size=1g,mode=1777", "/run": "rw,nosuid,nodev,size=64m,mode=0755"}
	c.State.Status = "created"
	return c
}

// observedContainer projects the fixture's current running, created or exited state.
func (f *managerTestRunner) observedContainer() containerInspection {
	c := managerTestContainer()
	if f.started {
		c.State.Status, c.State.StartedAt, c.State.FinishedAt = "exited", "2026-10-20T00:00:00Z", "2026-10-20T00:00:02Z"
	}
	if f.running {
		c.State.Status, c.State.Running, c.State.Pid, c.State.FinishedAt = "running", true, 1234, ""
	}
	if f.stopped {
		c.State.ExitCode = 137
	}
	if f.mutateContainer != nil {
		f.mutateContainer(f.inspections, &c)
	}
	return c
}

// managerTestJSON writes one synthetic inspection response to the supplied stream.
func managerTestJSON(w io.Writer, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// Capture refuses the alternate runner API so fixtures exercise streamed bounds.
func (f *managerTestRunner) Capture(context.Context, platform.Command) ([]byte, error) {
	f.captures++
	return nil, errors.New("Capture must not be used by the bounded producer")
}

// Run simulates dependency operations and records effects without contacting Docker.
func (f *managerTestRunner) Run(ctx context.Context, c platform.Command) error {
	_, deadline := ctx.Deadline()
	f.calls = append(f.calls, managerTestCall{append([]string(nil), c.Args...), ctx.Err(), deadline})
	if c.Name != "docker" || c.Stdout == nil || c.Stderr == nil || c.Stdin != nil || c.Dir != "" || len(c.Env) != 0 || !deadline {
		return errors.New("fixture observed an unbounded or unexpected process boundary")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(c.Args) == 0 {
		return errors.New("empty Docker arguments")
	}
	if len(c.Args) >= 2 && c.Args[0] == "container" && c.Args[1] == "inspect" {
		f.inspections++
	}
	if f.hook != nil {
		if handled, err := f.hook(ctx, c); handled {
			return err
		}
	}
	switch c.Args[0] {
	case "info":
		_, err := io.WriteString(c.Stdout, managerTestEngine+"|linux|aarch64|29.1.3\n")
		return err
	case "pull":
		return nil
	case "image":
		if len(c.Args) != 5 || c.Args[1] != "inspect" || c.Args[2] != "--format" || c.Args[3] != "{{json .}}" {
			return errors.New("unexpected image inspection grammar")
		}
		if c.Args[4] == BaseImage {
			return managerTestJSON(c.Stdout, f.base)
		}
		if c.Args[4] == managerTestImage {
			return managerTestJSON(c.Stdout, f.image)
		}
		return errors.New("unexpected mutable or foreign image reference")
	case "create":
		_, err := io.WriteString(c.Stdout, managerTestCID+"\n")
		return err
	case "container":
		if len(c.Args) != 5 || c.Args[2] != "--format" || c.Args[3] != "{{json .}}" || c.Args[4] != managerTestCID && c.Args[4] != "lexr-sensors-deps-"+managerTestToken {
			return errors.New("unexpected container inspection reference")
		}
		return managerTestJSON(c.Stdout, f.observedContainer())
	case "start":
		if !reflect.DeepEqual(c.Args, []string{"start", "--attach", managerTestCID}) {
			return errors.New("unexpected start grammar")
		}
		f.started = true
		if _, err := io.WriteString(c.Stdout, "fixture dependency stdout\n"); err != nil {
			return err
		}
		_, err := io.WriteString(c.Stderr, "fixture dependency advisory\n")
		return err
	case "stop":
		if !reflect.DeepEqual(c.Args, []string{"stop", "--time", "10", managerTestCID}) {
			return errors.New("stop was not directed at the exact owned CID")
		}
		f.running, f.stopped = false, true
		return nil
	case "cp":
		if len(c.Args) != 3 || c.Args[2] != "-" {
			return errors.New("inventory was not streamed")
		}
		var data []byte
		switch c.Args[1] {
		case managerTestCID + ":" + PackageInventoryPath:
			data = f.packages
		case managerTestCID + ":" + ToolchainInventoryPath:
			data = f.tools
		default:
			return errors.New("unexpected inventory path")
		}
		_, err := c.Stdout.Write(data)
		return err
	case "commit":
		_, err := io.WriteString(c.Stdout, managerTestImage+"\n")
		return err
	default:
		return fmt.Errorf("unexpected Docker operation %q", c.Args[0])
	}
}

// managerTestTar serialises inventory-copy fixture members without filesystem extraction.
func managerTestTar(t *testing.T, headers []tar.Header, bodies [][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for i := range headers {
		if err := w.WriteHeader(&headers[i]); err != nil {
			t.Fatal(err)
		}
		if len(bodies[i]) > 0 {
			if _, err := w.Write(bodies[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// managerTestCount counts one operation in the fixture's recorded Docker calls.
func managerTestCount(f *managerTestRunner, operation string) int {
	n := 0
	for _, c := range f.calls {
		if c.args[0] == operation {
			n++
		}
	}
	return n
}

// managerTestNoGrant requires refusal without returning usable prepared-image authority.
func managerTestNoGrant(t *testing.T, f *managerTestRunner, result Result, err error, allowCommit bool) {
	t.Helper()
	if err == nil || result.Prepared != nil || result.Provenance != nil {
		t.Fatalf("failed chain granted prepared authority: %+v, %v", result, err)
	}
	if f.captures != 0 {
		t.Fatal("unbounded Capture was used")
	}
	if !allowCommit && managerTestCount(f, "commit") != 0 {
		t.Fatal("failed preparation committed an image")
	}
	for _, call := range f.calls {
		if call.args[0] == "rm" || call.args[0] == "build" || call.args[0] == "run" {
			t.Fatalf("unexpected deletion/unowned producer operation: %v", call.args)
		}
	}
}

// TestManagerPlansAndEarlyRefusalsHaveNoEffects checks read-only plans and invalid request boundaries.
func TestManagerPlansAndEarlyRefusalsHaveNoEffects(t *testing.T) {
	for _, host := range []hostcap.Host{{GOOS: "linux", GOARCH: "arm64"}, {GOOS: "darwin", GOARCH: "arm64"}, {GOOS: "linux", GOARCH: "amd64"}, {GOOS: "windows", GOARCH: "arm64"}, {}} {
		t.Run(host.GOOS+"-"+host.GOARCH, func(t *testing.T) {
			f := newManagerTestRunner(t)
			m := newManagerUnderTest(f)
			m.host = host
			tokens := 0
			m.token = func() (string, error) { tokens++; return managerTestToken, nil }
			request := Request{DryRun: true}
			a, err := m.Plan(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			b, err := m.Plan(context.Background(), request)
			if err != nil || a != b {
				t.Fatal("plan is not deterministic")
			}
			result, err := m.Prepare(context.Background(), request)
			if err != nil || result.Prepared != nil || result.Provenance != nil || result.ContainerID != "" {
				t.Fatalf("dry run executed or granted authority: %+v, %v", result, err)
			}
			want := host.GOARCH == "arm64" && (host.GOOS == "linux" || host.GOOS == "darwin")
			if a.Executable != want || !want && a.ExecutionBlocker == "" || a.BaseImage != BaseImage || a.RecipeSHA256 != RecipeSHA256() || !a.RetainsContainer {
				t.Fatalf("untruthful plan: %+v", a)
			}
			if !want {
				_, err := m.Prepare(context.Background(), Request{})
				if err == nil {
					t.Fatal("unsupported host executed")
				}
			}
			if len(f.calls) != 0 || f.captures != 0 || tokens != 0 {
				t.Fatal("plan/dry-run/static refusal had external effects")
			}
		})
	}
	f := newManagerTestRunner(t)
	m := newManagerUnderTest(f)
	plan, err := m.Plan(context.Background(), Request{NoPull: true})
	if err != nil || plan.Executable || !strings.Contains(plan.ExecutionBlocker, "NoPull") {
		t.Fatalf("missing handle plan: %+v,%v", plan, err)
	}
	result, err := m.Prepare(context.Background(), Request{NoPull: true})
	managerTestNoGrant(t, f, result, err, false)
	if len(f.calls) != 0 {
		t.Fatal("NoPull without handle touched Docker")
	}
	for _, request := range []Request{{NoPull: true, Prepared: &Prepared{}}, {Prepared: &Prepared{}}} {
		if _, err := m.Prepare(context.Background(), request); err == nil {
			t.Fatal("zero handle accepted")
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Prepare(cancelled, Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled request: %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatal("invalid request touched Docker")
	}
}

// TestManagerControlledPreparationExactOrderAndOpaqueReuse checks the full synthetic preparation and handle-reuse sequence.
func TestManagerControlledPreparationExactOrderAndOpaqueReuse(t *testing.T) {
	f := newManagerTestRunner(t)
	m := newManagerUnderTest(f)
	result, err := m.Prepare(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Prepared == nil || result.Provenance == nil || result.Prepared.ImageID() != managerTestImage || result.Terminal == nil || result.Terminal.Status != "exited" || result.Terminal.ExitCode != 0 || result.TerminalUnknown {
		t.Fatalf("incomplete successful result: %+v", result)
	}
	if result.DependencyStdout != "fixture dependency stdout\n" || result.DependencyStderr != "fixture dependency advisory\n" {
		t.Fatal("bounded dependency diagnostics were lost")
	}
	wantCreate := []string{"create", "--pull=never", "--name", "lexr-sensors-deps-" + managerTestToken, "--platform", "linux/arm64", "--network", "bridge", "--user", "0:0", "--workdir", "/", "--entrypoint", "/usr/bin/timeout", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER", "--cap-add", "SETUID", "--cap-add", "SETGID", "--security-opt", "no-new-privileges", "--pids-limit", "256", "--memory", "4g", "--cpus", "2", "--ipc", "private", "--restart", "no", "--tmpfs", "/tmp:rw,nosuid,nodev,size=1g,mode=1777", "--tmpfs", "/run:rw,nosuid,nodev,size=64m,mode=0755", "--label", "io.lexr.sensors.builder=dependencies-v1", "--label", "io.lexr.sensors.recipe=" + RecipeSHA256(), "--label", "io.lexr.sensors.token=" + managerTestToken}
	for _, env := range managerTestEnvironment() {
		wantCreate = append(wantCreate, "--env", env)
	}
	wantCreate = append(wantCreate, managerTestBase, "--signal=TERM", "--kill-after=5s", "18m", "/bin/bash", "-euo", "pipefail", "-c", DependencyScript)
	inspectContainer := []string{"container", "inspect", "--format", "{{json .}}", managerTestCID}
	want := [][]string{{"info", "--format", "{{.ID}}|{{.OSType}}|{{.Architecture}}|{{.ServerVersion}}"}, {"pull", "--platform", "linux/arm64", BaseImage}, {"image", "inspect", "--format", "{{json .}}", BaseImage}, wantCreate, inspectContainer, {"start", "--attach", managerTestCID}, inspectContainer, {"cp", managerTestCID + ":" + PackageInventoryPath, "-"}, {"cp", managerTestCID + ":" + ToolchainInventoryPath, "-"}, {"commit", "--change", `ENTRYPOINT ["/bin/false"]`, "--change", `CMD ["lexr-explicit-offline-runner-required"]`, "--change", "USER 65532:65532", "--change", "WORKDIR /", managerTestCID}, {"image", "inspect", "--format", "{{json .}}", managerTestImage}, inspectContainer}
	var got [][]string
	for _, call := range f.calls {
		got = append(got, call.args)
		if call.contextError != nil || !call.deadline {
			t.Fatal("unbounded/cancelled observation")
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("producer command grammar/order differs:\ngot %#v\nwant %#v", got, want)
	}
	p := result.Prepared.Provenance()
	sum := sha256.Sum256([]byte(p.PackageInventory))
	if p.PackageInventorySHA256 != hex.EncodeToString(sum[:]) || p.EngineID != managerTestEngine || p.BaseImageID != managerTestBase || p.RecipeSHA256 != RecipeSHA256() {
		t.Fatalf("incomplete provenance: %+v", p)
	}
	p.ImageID = "changed"
	result.Provenance.ImageID = "changed"
	if result.Prepared.ImageID() != managerTestImage {
		t.Fatal("returned provenance mutated opaque authority")
	}
	f.calls = nil
	reused, err := m.Prepare(context.Background(), Request{NoPull: true, Prepared: result.Prepared})
	if err != nil || reused.Prepared != result.Prepared {
		t.Fatalf("same-manager reuse failed: %v", err)
	}
	if len(f.calls) != 2 || f.calls[0].args[0] != "info" || !reflect.DeepEqual(f.calls[1].args, []string{"image", "inspect", "--format", "{{json .}}", managerTestImage}) {
		t.Fatalf("NoPull performed acquisition/mutation: %v", f.calls)
	}
	foreign := newManagerUnderTest(f)
	f.calls = nil
	if _, err := foreign.Prepare(context.Background(), Request{NoPull: true, Prepared: result.Prepared}); err == nil || len(f.calls) != 0 {
		t.Fatal("foreign-manager handle was accepted or touched Docker")
	}
	if _, err := m.Prepare(context.Background(), Request{Prepared: result.Prepared}); err == nil || len(f.calls) != 0 {
		t.Fatal("handle without NoPull was accepted")
	}
	if f.captures != 0 {
		t.Fatal("Capture was used")
	}
}

// TestManagerRejectsEngineAndBaseBeforeCreate rejects unsupported observations before container creation.
func TestManagerRejectsEngineAndBaseBeforeCreate(t *testing.T) {
	for _, value := range []string{"short|linux|arm64|29.1.3", managerTestEngine + "|windows|arm64|29.1.3", managerTestEngine + "|linux|amd64|29.1.3", managerTestEngine + "|linux|arm64|29.1\x00.3", managerTestEngine + "|linux|arm64|29.1\n.3", managerTestEngine + "|linux|arm64|29.1\r.3"} {
		t.Run(value, func(t *testing.T) {
			f := newManagerTestRunner(t)
			f.hook = func(_ context.Context, c platform.Command) (bool, error) {
				if c.Args[0] == "info" {
					_, err := io.WriteString(c.Stdout, value)
					return true, err
				}
				return false, nil
			}
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, false)
			if len(f.calls) != 1 {
				t.Fatal("invalid engine reached acquisition")
			}
		})
	}
	cases := []struct {
		name   string
		mutate func(*imageInspection)
	}{{"architecture", func(i *imageInspection) { i.Architecture = "amd64" }}, {"os", func(i *imageInspection) { i.OS = "windows" }}, {"identity", func(i *imageInspection) { i.ID = "mutable-tag" }}, {"volume", func(i *imageInspection) {
		i.Config.Volumes = map[string]json.RawMessage{"/source": json.RawMessage(`{}`)}
	}}, {"entrypoint", func(i *imageInspection) { i.Config.Entrypoint = []string{"/untrusted"} }}, {"user", func(i *imageInspection) { i.Config.User = "1000" }}, {"duplicate-env", func(i *imageInspection) { i.Config.Env = append(i.Config.Env, "PATH=duplicate") }}, {"control-env", func(i *imageInspection) { i.Config.Env = append(i.Config.Env, "OTHER=bad\nvalue") }}}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			f := newManagerTestRunner(t)
			row.mutate(&f.base)
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, false)
			if managerTestCount(f, "create") != 0 {
				t.Fatal("invalid base reached container creation")
			}
		})
	}
}

// TestManagerRejectsAdversarialCreatedInspection withholds execution for changed ownership or isolation policy.
func TestManagerRejectsAdversarialCreatedInspection(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*containerInspection)
	}{
		{"id", func(c *containerInspection) { c.ID = strings.Repeat("e", 64) }}, {"name", func(c *containerInspection) { c.Name = "/foreign" }}, {"base", func(c *containerInspection) { c.Image = managerTestImage }}, {"owner", func(c *containerInspection) { c.Config.Labels[ownerLabel] = "other" }}, {"recipe", func(c *containerInspection) { c.Config.Labels[recipeLabel] = strings.Repeat("0", 64) }}, {"token", func(c *containerInspection) { c.Config.Labels[tokenLabel] = strings.Repeat("0", 24) }},
		{"privileged", func(c *containerInspection) { c.HostConfig.Privileged = true }}, {"network", func(c *containerInspection) { c.HostConfig.NetworkMode = "host" }}, {"autoremove", func(c *containerInspection) { c.HostConfig.AutoRemove = true }}, {"readonly-root", func(c *containerInspection) { c.HostConfig.ReadonlyRootfs = true }}, {"bind", func(c *containerInspection) { c.HostConfig.Binds = []string{"/host:/source"} }}, {"volumes-from", func(c *containerInspection) { c.HostConfig.VolumesFrom = []string{"foreign"} }}, {"devices", func(c *containerInspection) { c.HostConfig.Devices = []json.RawMessage{json.RawMessage(`{}`)} }}, {"device-requests", func(c *containerInspection) { c.HostConfig.DeviceRequests = []json.RawMessage{json.RawMessage(`{}`)} }}, {"implicit-volume", func(c *containerInspection) {
			c.Config.Volumes = map[string]json.RawMessage{"/host": json.RawMessage(`{}`)}
		}},
		{"mount", func(c *containerInspection) {
			c.Mounts = append(c.Mounts, struct{ Type, Source, Destination string }{"bind", "/host", "/source"})
		}}, {"cap-add", func(c *containerInspection) { c.HostConfig.CapAdd = append(c.HostConfig.CapAdd, "SYS_ADMIN") }}, {"cap-drop", func(c *containerInspection) { c.HostConfig.CapDrop = nil }}, {"security", func(c *containerInspection) { c.HostConfig.SecurityOpt = []string{"apparmor=unconfined"} }}, {"pids", func(c *containerInspection) { c.HostConfig.PidsLimit = 0 }}, {"memory", func(c *containerInspection) { c.HostConfig.Memory = 0 }}, {"cpus", func(c *containerInspection) { c.HostConfig.NanoCpus = 0 }}, {"pid-mode", func(c *containerInspection) { c.HostConfig.PidMode = "host" }}, {"ipc-mode", func(c *containerInspection) { c.HostConfig.IpcMode = "host" }}, {"restart", func(c *containerInspection) { c.HostConfig.RestartPolicy.Name = "always" }}, {"restart-count", func(c *containerInspection) { c.RestartCount = 1 }}, {"tmpfs", func(c *containerInspection) { c.HostConfig.Tmpfs["/tmp"] = "rw,exec" }}, {"command-ttl", func(c *containerInspection) { c.Config.Cmd[2] = "999m" }}, {"entrypoint", func(c *containerInspection) { c.Config.Entrypoint = []string{"/bin/bash"} }}, {"env", func(c *containerInspection) { c.Config.Env = append(c.Config.Env, "UNEXPECTED=1") }}, {"state", func(c *containerInspection) { c.State.Status = "running"; c.State.Running = true; c.State.Pid = 9 }},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			f := newManagerTestRunner(t)
			f.mutateContainer = func(_ int, c *containerInspection) { row.mutate(c) }
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, false)
			if managerTestCount(f, "start") != 0 {
				t.Fatal("unvalidated created container was started")
			}
			for _, c := range f.calls {
				if c.args[0] == "stop" && !reflect.DeepEqual(c.args, []string{"stop", "--time", "10", managerTestCID}) {
					t.Fatal("foreign container stopped")
				}
			}
		})
	}
}

// TestManagerWithholdsImageForTerminalFailures rejects incomplete or unsuccessful lifecycle observations.
func TestManagerWithholdsImageForTerminalFailures(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*containerInspection)
	}{{"nonzero", func(c *containerInspection) { c.State.ExitCode = 1 }}, {"daemon-ttl", func(c *containerInspection) { c.State.ExitCode = 124 }}, {"oom", func(c *containerInspection) { c.State.OOMKilled = true }}, {"state-error", func(c *containerInspection) { c.State.Error = "daemon error" }}, {"created-not-exited", func(c *containerInspection) { c.State.Status = "created" }}, {"missing-start", func(c *containerInspection) { c.State.StartedAt = "" }}, {"zero-finish", func(c *containerInspection) { c.State.FinishedAt = "0001-01-01T00:00:00Z" }}, {"paused", func(c *containerInspection) { c.State.Paused = true }}, {"dead", func(c *containerInspection) { c.State.Dead = true }}, {"restarting", func(c *containerInspection) { c.State.Restarting = true }}}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			f := newManagerTestRunner(t)
			f.mutateContainer = func(n int, c *containerInspection) {
				if n >= 2 {
					row.mutate(c)
				}
			}
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, false)
		})
	}
}

// TestManagerCancellationObservesAndStopsOnlyOwnedCID limits recovery effects to the exact owned container.
func TestManagerCancellationObservesAndStopsOnlyOwnedCID(t *testing.T) {
	f := newManagerTestRunner(t)
	m := newManagerUnderTest(f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.hook = func(_ context.Context, c platform.Command) (bool, error) {
		if c.Args[0] == "start" {
			f.started, f.running = true, true
			cancel()
			_, _ = io.WriteString(c.Stdout, "interrupted fixture output\n")
			return true, context.Canceled
		}
		return false, nil
	}
	result, err := m.Prepare(ctx, Request{})
	managerTestNoGrant(t, f, result, err, false)
	if !errors.Is(err, context.Canceled) || result.TerminalUnknown || result.Terminal == nil || result.Terminal.Running || result.Terminal.PID != 0 || managerTestCount(f, "stop") != 1 {
		t.Fatalf("cancellation recovery not verified: %+v, %v", result, err)
	}
	if f.inspections != 4 {
		t.Fatalf("expected created/post-client/pre-stop/post-stop observations, got%d", f.inspections)
	}
	for _, call := range f.calls {
		if call.args[0] == "container" || call.args[0] == "stop" {
			if call.contextError != nil || !call.deadline {
				t.Fatal("recovery reused cancelled or unbounded context")
			}
		}
	}
	if result.DependencyStdout != "interrupted fixture output\n" {
		t.Fatal("interrupted diagnostics were lost")
	}
}

// TestManagerRecoveryRetainsUncertaintyAndDoesNotStopForeignObjects preserves unknown outcomes and foreign resources.
func TestManagerRecoveryRetainsUncertaintyAndDoesNotStopForeignObjects(t *testing.T) {
	for _, mode := range []string{"stop-failure", "foreign-owner", "observation-failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newManagerTestRunner(t)
			f.hook = func(_ context.Context, c platform.Command) (bool, error) {
				switch c.Args[0] {
				case "start":
					f.started, f.running = true, true
					return true, errors.New("lost Docker client")
				case "stop":
					if mode == "stop-failure" {
						return true, errors.New("stop fixture failed")
					}
				case "container":
					if mode == "observation-failure" && f.inspections >= 2 {
						return true, errors.New("inspection fixture failed")
					}
				}
				return false, nil
			}
			if mode == "foreign-owner" {
				f.mutateContainer = func(n int, c *containerInspection) {
					if n >= 2 {
						c.Config.Labels[ownerLabel] = "foreign"
					}
				}
			}
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, false)
			if !result.TerminalUnknown || result.RecoveryError == "" {
				t.Fatalf("uncertain termination hidden: %+v", result)
			}
			wantStop := 0
			if mode == "stop-failure" {
				wantStop = 1
			}
			if managerTestCount(f, "stop") != wantStop {
				t.Fatal("unowned/unobserved object was stopped")
			}
		})
	}
	f := newManagerTestRunner(t)
	f.hook = func(_ context.Context, c platform.Command) (bool, error) {
		if c.Args[0] == "create" {
			return true, errors.New("create client disconnected")
		}
		return false, nil
	}
	result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
	managerTestNoGrant(t, f, result, err, false)
	if result.ContainerID != managerTestCID || result.Terminal == nil || result.Terminal.Status != "created" || result.TerminalUnknown {
		t.Fatalf("owned name recovery lost: %+v", result)
	}
	if managerTestCount(f, "start") != 0 || managerTestCount(f, "stop") != 0 {
		t.Fatal("never-started container was activated/stopped")
	}
}

// TestManagerOutputBoundsCancelChildAndWithholdAuthority checks bounded stdout and stderr refusal paths.
func TestManagerOutputBoundsCancelChildAndWithholdAuthority(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			f := newManagerTestRunner(t)
			cancelled := false
			f.hook = func(ctx context.Context, c platform.Command) (bool, error) {
				if c.Args[0] != "info" {
					return false, nil
				}
				w := c.Stdout
				if stream == "stderr" {
					w = c.Stderr
				}
				_, _ = io.WriteString(w, strings.Repeat("x", 4097))
				cancelled = errors.Is(ctx.Err(), context.Canceled)
				return true, nil
			}
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, false)
			if !cancelled || len(f.calls) != 1 {
				t.Fatal("output bound did not cancel the command immediately before acquisition")
			}
		})
	}
}

// TestManagerInventoryStreamsRejectMalformedArchives covers invalid inventory members and text.
func TestManagerInventoryStreamsRejectMalformedArchives(t *testing.T) {
	good := []byte("fixture=1\n")
	name := path.Base(PackageInventoryPath)
	cases := []struct {
		name string
		data func(*testing.T) []byte
	}{
		{"not-tar", func(*testing.T) []byte { return []byte("not a tar stream") }},
		{"path", func(t *testing.T) []byte {
			return managerTestTar(t, []tar.Header{{Name: "../" + name, Typeflag: tar.TypeReg, Size: int64(len(good))}}, [][]byte{good})
		}},
		{"symlink", func(t *testing.T) []byte {
			return managerTestTar(t, []tar.Header{{Name: name, Typeflag: tar.TypeSymlink, Linkname: "outside"}}, [][]byte{nil})
		}},
		{"extra-member", func(t *testing.T) []byte {
			return managerTestTar(t, []tar.Header{{Name: name, Typeflag: tar.TypeReg, Size: int64(len(good))}, {Name: "extra", Typeflag: tar.TypeReg, Size: int64(len(good))}}, [][]byte{good, good})
		}},
		{"truncated", func(t *testing.T) []byte {
			b := managerTestTar(t, []tar.Header{{Name: name, Typeflag: tar.TypeReg, Size: int64(len(good))}}, [][]byte{good})
			return b[:515]
		}},
		{"no-newline", func(t *testing.T) []byte {
			return managerTestTar(t, []tar.Header{{Name: name, Typeflag: tar.TypeReg, Size: 3}}, [][]byte{[]byte("bad")})
		}},
		{"nul", func(t *testing.T) []byte {
			return managerTestTar(t, []tar.Header{{Name: name, Typeflag: tar.TypeReg, Size: 5}}, [][]byte{[]byte("bad\x00\n")})
		}},
	}
	for _, row := range cases {
		t.Run(row.name, func(t *testing.T) {
			f := newManagerTestRunner(t)
			f.packages = row.data(t)
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, false)
			if result.Terminal == nil || result.Terminal.Status != "exited" {
				t.Fatal("inventory checked before terminal observation")
			}
		})
	}
	for _, mode := range []string{"unsorted-packages", "duplicate-packages", "missing-tool-section"} {
		t.Run(mode, func(t *testing.T) {
			f := newManagerTestRunner(t)
			if mode == "missing-tool-section" {
				tools := "missing sections\n"
				f.tools = managerTestTar(t, []tar.Header{{Name: path.Base(ToolchainInventoryPath), Typeflag: tar.TypeReg, Size: int64(len(tools))}}, [][]byte{[]byte(tools)})
			} else {
				rows := []string{}
				for i := 0; i < 30; i++ {
					rows = append(rows, fmt.Sprintf("fixture-package-%02d=1.0", i))
				}
				if mode == "unsorted-packages" {
					sort.Sort(sort.Reverse(sort.StringSlice(rows)))
				} else {
					rows[1] = rows[0]
				}
				text := strings.Join(rows, "\n") + "\n"
				f.packages = managerTestTar(t, []tar.Header{{Name: name, Typeflag: tar.TypeReg, Size: int64(len(text))}}, [][]byte{[]byte(text)})
			}
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, false)
		})
	}
}

// TestManagerPostCommitAndReuseChangesNeverGrantAuthority rejects changed images and lifecycle identities.
func TestManagerPostCommitAndReuseChangesNeverGrantAuthority(t *testing.T) {
	for _, mode := range []string{"image-user", "image-recipe", "image-identity", "image-volume", "final-terminal"} {
		t.Run(mode, func(t *testing.T) {
			f := newManagerTestRunner(t)
			switch mode {
			case "image-user":
				f.image.Config.User = "0:0"
			case "image-recipe":
				f.image.Config.Labels[recipeLabel] = "wrong"
			case "image-identity":
				f.image.ID = managerTestBase
			case "image-volume":
				f.image.Config.Volumes = map[string]json.RawMessage{"/host": json.RawMessage(`{}`)}
			case "final-terminal":
				f.mutateContainer = func(n int, c *containerInspection) {
					if n == 3 {
						c.State.ExitCode = 7
					}
				}
			}
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, true)
			if managerTestCount(f, "commit") != 1 || result.ObservedImageID != managerTestImage {
				t.Fatal("post-commit failure did not retain observed image identity")
			}
		})
	}
	for _, mode := range []string{"engine", "image"} {
		t.Run("reuse-"+mode, func(t *testing.T) {
			f := newManagerTestRunner(t)
			m := newManagerUnderTest(f)
			first, err := m.Prepare(context.Background(), Request{})
			if err != nil {
				t.Fatal(err)
			}
			f.calls = nil
			if mode == "engine" {
				f.hook = func(_ context.Context, c platform.Command) (bool, error) {
					if c.Args[0] == "info" {
						_, err := io.WriteString(c.Stdout, "different-engine-0000000001|linux|arm64|29.1.3")
						return true, err
					}
					return false, nil
				}
			} else {
				f.image.Config.Labels[recipeLabel] = "wrong"
			}
			result, err := m.Prepare(context.Background(), Request{NoPull: true, Prepared: first.Prepared})
			managerTestNoGrant(t, f, result, err, false)
			if managerTestCount(f, "pull") != 0 || managerTestCount(f, "create") != 0 {
				t.Fatal("NoPull mismatch silently reacquired/rebuilt")
			}
		})
	}
}

// TestManagerInventoryCopyOutputBoundRetainsKnownTerminal retains stopped evidence when a copied stream exceeds its bound.
func TestManagerInventoryCopyOutputBoundRetainsKnownTerminal(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			f := newManagerTestRunner(t)
			cancelled := false
			f.hook = func(ctx context.Context, c platform.Command) (bool, error) {
				if c.Args[0] != "cp" {
					return false, nil
				}
				if !reflect.DeepEqual(c.Args, []string{"cp", managerTestCID + ":" + PackageInventoryPath, "-"}) {
					return true, errors.New("unexpected inventory-copy boundary")
				}
				writer := c.Stdout
				if stream == "stderr" {
					writer = c.Stderr
				}
				// Deliberately ignore the writer error: the manager must notice the
				// bound and cancel the command even if its Runner returns success.
				_, _ = io.WriteString(writer, strings.Repeat("x", (1<<20)+16384+1))
				cancelled = errors.Is(ctx.Err(), context.Canceled)
				return true, nil
			}
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, false)
			if !cancelled || managerTestCount(f, "cp") != 1 {
				t.Fatal("inventory output bound did not immediately cancel its child context")
			}
			if result.TerminalUnknown || result.Terminal == nil || result.Terminal.Status != "exited" || result.Terminal.Running || result.Terminal.PID != 0 || result.Terminal.ExitCode != 0 || result.Terminal.OOMKilled {
				t.Fatalf("copy failure lost the previously verified terminal: %+v", result)
			}
			if managerTestCount(f, "stop") != 0 || result.ObservedImageID != "" {
				t.Fatal("inventory-copy failure stopped a known exited container or committed an image")
			}
		})
	}
}

// TestManagerInspectRejectsTrailingJSONObject refuses additional inspection objects at every selected boundary.
func TestManagerInspectRejectsTrailingJSONObject(t *testing.T) {
	for _, phase := range []string{"base-image", "created-container"} {
		t.Run(phase, func(t *testing.T) {
			f := newManagerTestRunner(t)
			injected := false
			f.hook = func(_ context.Context, c platform.Command) (bool, error) {
				var value any
				if phase == "base-image" && c.Args[0] == "image" {
					value = f.base
				} else if phase == "created-container" && c.Args[0] == "container" && f.inspections == 1 {
					value = f.observedContainer()
				} else {
					return false, nil
				}
				injected = true
				if err := managerTestJSON(c.Stdout, value); err != nil {
					return true, err
				}
				_, err := io.WriteString(c.Stdout, "\n{}\n")
				return true, err
			}
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, false)
			if !injected || !strings.Contains(err.Error(), "trailing data") || managerTestCount(f, "start") != 0 {
				t.Fatalf("inspection trailing object was accepted or failed elsewhere: %v", err)
			}
		})
	}
}

// TestManagerCommitWarningPrefixNeverGrantsAuthority rejects image output with a warning prefix.
func TestManagerCommitWarningPrefixNeverGrantsAuthority(t *testing.T) {
	f := newManagerTestRunner(t)
	f.hook = func(_ context.Context, c platform.Command) (bool, error) {
		if c.Args[0] != "commit" {
			return false, nil
		}
		_, err := io.WriteString(c.Stdout, "warning\n"+managerTestImage+"\n")
		return true, err
	}
	result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
	managerTestNoGrant(t, f, result, err, true)
	if managerTestCount(f, "commit") != 1 || !strings.Contains(err.Error(), "commit did not return a complete immutable image identity") {
		t.Fatalf("warning-prefixed commit output did not fail at the identity gate: %v", err)
	}
	if result.ObservedImageID != "warning\n"+managerTestImage {
		t.Fatalf("commit output was parsed into a clean image identity: %q", result.ObservedImageID)
	}
	if managerTestCount(f, "image") != 1 {
		t.Fatal("warning-prefixed identity reached prepared-image inspection")
	}
	if result.TerminalUnknown || result.Terminal == nil || result.Terminal.Status != "exited" || result.Terminal.Running || result.Terminal.PID != 0 || result.Terminal.ExitCode != 0 || result.Terminal.OOMKilled {
		t.Fatalf("commit identity failure lost the verified terminal: %+v", result)
	}
}

// TestManagerCommittedImageRequiresFailClosedConfiguration checks inert image defaults before authority is granted.
func TestManagerCommittedImageRequiresFailClosedConfiguration(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*imageConfig)
	}{
		{"retained-timeout", func(c *imageConfig) { c.Entrypoint = []string{"/usr/bin/timeout"} }},
		{"nil-entrypoint", func(c *imageConfig) { c.Entrypoint = nil }},
		{"empty-entrypoint", func(c *imageConfig) { c.Entrypoint = []string{} }},
		{"extra-entrypoint", func(c *imageConfig) { c.Entrypoint = []string{"/bin/false", "extra"} }},
		{"wrong-command", func(c *imageConfig) { c.Cmd = []string{"/bin/bash"} }},
		{"nil-command", func(c *imageConfig) { c.Cmd = nil }},
		{"empty-command", func(c *imageConfig) { c.Cmd = []string{} }},
		{"extra-command", func(c *imageConfig) { c.Cmd = []string{"lexr-explicit-offline-runner-required", "extra"} }},
		{"wrong-user", func(c *imageConfig) { c.User = "0:0" }},
		{"wrong-workdir", func(c *imageConfig) { c.WorkingDir = "/tmp" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagerTestRunner(t)
			tc.mutate(&f.image.Config)
			result, err := newManagerUnderTest(f).Prepare(context.Background(), Request{})
			managerTestNoGrant(t, f, result, err, true)
			if managerTestCount(f, "commit") != 1 || managerTestCount(f, "image") != 2 || result.ObservedImageID != managerTestImage || !strings.Contains(err.Error(), "committed dependency image configuration differs") {
				t.Fatalf("unsafe committed configuration did not fail at its exact gate: %+v, %v", result, err)
			}
			if result.TerminalUnknown || result.Terminal == nil || result.Terminal.Status != "exited" || result.Terminal.Running || result.Terminal.PID != 0 || result.Terminal.ExitCode != 0 || result.Terminal.OOMKilled || managerTestCount(f, "stop") != 0 {
				t.Fatalf("configuration refusal lost or disturbed the verified terminal: %+v", result)
			}
		})
	}
}
