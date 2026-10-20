package build

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/hostcap"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

const (
	transportTestHome              = "/home/test"
	transportTestConfig            = transportTestHome + "/.docker"
	transportTestCLI               = "/usr/bin/docker"
	transportTestEngine            = "transport-fixture-engine-00000001"
	transportTestVersion           = "29.8.2"
	transportTestSource            = "/home/test/source snapshot"
	transportTestInfoFormat        = `{"ID":{{json .ID}},"ServerVersion":{{json .ServerVersion}},"OSType":{{json .OSType}},"Architecture":{{json .Architecture}},"OperatingSystem":{{json .OperatingSystem}}}`
	transportTestEngineObservation = `{"ID":"transport-fixture-engine-00000001","ServerVersion":"29.8.2","OSType":"linux","Architecture":"aarch64","OperatingSystem":"Ubuntu"}`
)

type transportTestCall struct {
	ctx      context.Context
	command  platform.Command
	deadline bool
}

// Every observation and process result is supplied in memory. These tests do
// not create sockets, consult the host environment, or execute a Docker client.
type transportTestFixture struct {
	host       hostcap.Host
	env        []string
	home       string
	executable string
	paths      map[string]sourceTransportPathInfo
	aliases    map[string]string
	pathErrors map[string]error
	envReads   int
	homeReads  int
	lookups    int
	runner     *transportTestRunner
}

type transportTestRunner struct {
	calls       []transportTestCall
	captures    int
	engine      string
	version     string
	engineOS    string
	arch        string
	contextName string
	contextHost string
	contextTLS  bool
	contextRaw  []byte
	infoRaw     []byte
	before      func(context.Context, platform.Command) (bool, error)
}

func newTransportTestFixture(t *testing.T, goos string) *transportTestFixture {
	t.Helper()
	f := &transportTestFixture{
		host: hostcap.Host{GOOS: goos, GOARCH: "arm64"},
		home: transportTestHome, executable: transportTestCLI,
		paths: map[string]sourceTransportPathInfo{
			transportTestCLI:    {mode: 0755, uid: 0, device: 1, inode: 10},
			transportTestConfig: {mode: fs.ModeDir | 0700, uid: 1000, device: 1, inode: 11},
			transportTestSource: {mode: fs.ModeDir | 0700, uid: 1000, device: 1, inode: 12},
		},
		aliases: map[string]string{}, pathErrors: map[string]error{},
		runner: &transportTestRunner{
			engine: transportTestEngine, version: transportTestVersion,
			engineOS: "Ubuntu 24.04.3 LTS", arch: "aarch64", contextName: "default",
		},
	}
	socket := "/run/docker.sock"
	if goos == "darwin" {
		socket = transportTestConfig + "/run/docker.sock"
		f.runner.engineOS = "Docker Desktop"
		f.runner.contextName = "desktop-linux"
	}
	f.paths[socket] = sourceTransportPathInfo{mode: fs.ModeSocket | 0600, uid: 1000, device: 1, inode: 13}
	f.env = []string{"PATH=/original/bin", "DOCKER_HOST=unix://" + socket}
	f.runner.contextHost = "unix://" + socket
	return f
}

func (f *transportTestFixture) observations() sourceTransportObservations {
	return sourceTransportObservations{
		environ: func() []string {
			f.envReads++
			return append([]string(nil), f.env...)
		},
		home: func() (string, error) { f.homeReads++; return f.home, nil },
		lookPath: func(name string) (string, error) {
			f.lookups++
			if name != "docker" {
				return "", fmt.Errorf("unexpected executable lookup %q", name)
			}
			return f.executable, nil
		},
		canonical: func(path string) (string, error) {
			if err := f.pathErrors[path]; err != nil {
				return "", err
			}
			if canonical, ok := f.aliases[path]; ok {
				return canonical, nil
			}
			return path, nil
		},
		lstat: func(path string) (sourceTransportPathInfo, error) {
			if err := f.pathErrors[path]; err != nil {
				return sourceTransportPathInfo{}, err
			}
			if info, ok := f.paths[path]; ok {
				return info, nil
			}
			return sourceTransportPathInfo{}, fs.ErrNotExist
		},
		uid: func() int { return 1000 },
	}
}

func (f *transportTestFixture) bind(t *testing.T) sourceTransportBinding {
	t.Helper()
	binding, err := bindSourceDockerTransportObserved(context.Background(), f.runner, f.host, f.observations())
	if err != nil {
		t.Fatalf("valid local transport was refused: %v", err)
	}
	if binding == nil || binding.Runner() == nil {
		t.Fatal("binding did not retain a runner")
	}
	return binding
}

func (r *transportTestRunner) Capture(ctx context.Context, command platform.Command) ([]byte, error) {
	r.captures++
	var stdout bytes.Buffer
	command.Stdout = &stdout
	if err := r.Run(ctx, command); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

func (r *transportTestRunner) Run(ctx context.Context, command platform.Command) error {
	_, bounded := ctx.Deadline()
	copy := command
	copy.Args = append([]string(nil), command.Args...)
	copy.Env = append([]string(nil), command.Env...)
	r.calls = append(r.calls, transportTestCall{ctx: ctx, command: copy, deadline: bounded})
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.before != nil {
		if handled, err := r.before(ctx, command); handled {
			return err
		}
	}
	args := transportTestOperation(command.Args)
	if len(args) == 0 {
		return errors.New("fake runner received no operation")
	}
	if args[0] == "info" {
		if !bounded || command.Stdout == nil || command.Stderr == nil {
			return errors.New("transport info must be bounded and captured")
		}
		if !reflect.DeepEqual(args, []string{"info", "--format", transportTestInfoFormat}) {
			return fmt.Errorf("unexpected transport info grammar: %q", args)
		}
		if r.infoRaw != nil {
			_, err := command.Stdout.Write(r.infoRaw)
			return err
		}
		return json.NewEncoder(command.Stdout).Encode(map[string]string{
			"ID": r.engine, "ServerVersion": r.version, "OSType": "linux",
			"Architecture": r.arch, "OperatingSystem": r.engineOS,
		})
	}
	if args[0] == "context" {
		if !bounded || command.Stdout == nil || command.Stderr == nil {
			return errors.New("context observation must be bounded and captured")
		}
		if reflect.DeepEqual(args, []string{"context", "show"}) {
			_, err := fmt.Fprintln(command.Stdout, r.contextName)
			return err
		}
		if reflect.DeepEqual(args, []string{"context", "inspect", r.contextName, "--format", "{{json .}}"}) {
			if r.contextRaw != nil {
				_, err := command.Stdout.Write(r.contextRaw)
				return err
			}
			return json.NewEncoder(command.Stdout).Encode(map[string]any{
				"Name":      r.contextName,
				"Endpoints": map[string]any{"docker": map[string]any{"Host": r.contextHost, "SkipTLSVerify": r.contextTLS}},
			})
		}
		return fmt.Errorf("unexpected context operation: %q", args)
	}
	return nil
}

func transportTestOperation(args []string) []string {
	for len(args) >= 2 && (args[0] == "--config" || args[0] == "--host") {
		args = args[2:]
	}
	return args
}

func transportTestAssertBoundCommand(t *testing.T, command platform.Command, endpoint string) {
	t.Helper()
	if command.Name != transportTestCLI {
		t.Fatalf("executable was not frozen: %q", command.Name)
	}
	want := []string{"--config", transportTestConfig, "--host", endpoint}
	if len(command.Args) < len(want) || !reflect.DeepEqual(command.Args[:len(want)], want) {
		t.Fatalf("Docker selection was not frozen: %q, want prefix %q", command.Args, want)
	}
	for _, name := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_API_VERSION", "DOCKER_CUSTOM_HEADERS"} {
		found := false
		for _, value := range command.Env {
			if strings.HasPrefix(value, name+"=") {
				found = true
				if value != name+"=" {
					t.Fatalf("bound command retained %s selection: %q", name, value)
				}
			}
		}
		if !found {
			t.Fatalf("bound command did not neutralize inherited %s", name)
		}
	}
}

func TestSourceTransportBindsLocalSocketAndFreezesCommand(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			f := newTransportTestFixture(t, goos)
			endpoint := f.runner.contextHost
			binding := f.bind(t)
			if id, version := binding.EngineIdentity(); id != transportTestEngine || version != transportTestVersion {
				t.Fatalf("wrong frozen engine identity: %q %q", id, version)
			}
			if len(f.runner.calls) == 0 {
				t.Fatal("transport bound without observing an engine")
			}
			for _, call := range f.runner.calls {
				transportTestAssertBoundCommand(t, call.command, endpoint)
				if !call.deadline {
					t.Fatal("engine observation had no deadline")
				}
			}
			if f.runner.captures != 0 {
				t.Fatal("transport used unbounded Runner.Capture")
			}
			if f.envReads != 1 || f.homeReads != 1 || f.lookups != 1 {
				t.Fatalf("environment/home/executable selection was not observed once: %d/%d/%d", f.envReads, f.homeReads, f.lookups)
			}

			// Later selection changes must not redirect commands or cause a new
			// context or PATH lookup. Stable filesystem identities remain valid.
			f.env = []string{"DOCKER_HOST=ssh://other", "DOCKER_CONTEXT=other", "PATH=/other/bin"}
			f.home, f.executable = "/other/home", "/other/bin/docker"
			var stdout, stderr bytes.Buffer
			stdin := strings.NewReader("input")
			type contextKey struct{}
			ctx := context.WithValue(context.Background(), contextKey{}, "preserved")
			command := platform.Command{Name: "docker", Args: []string{"pull", "--platform", "linux/arm64", "fixture:tag"}, Dir: "/work", Env: []string{"TRANSPORT_TEST=kept"}, Stdin: stdin, Stdout: &stdout, Stderr: &stderr}
			if err := binding.Runner().Run(ctx, command); err != nil {
				t.Fatal(err)
			}
			got := f.runner.calls[len(f.runner.calls)-1]
			transportTestAssertBoundCommand(t, got.command, endpoint)
			if !reflect.DeepEqual(transportTestOperation(got.command.Args), command.Args) {
				t.Fatal("wrapper changed operation arguments")
			}
			if got.ctx != ctx || got.ctx.Value(contextKey{}) != "preserved" || got.command.Dir != command.Dir || got.command.Stdin != stdin || got.command.Stdout != &stdout || got.command.Stderr != &stderr {
				t.Fatal("wrapper failed to preserve command context, working directory, or streams")
			}
			if !slices.Contains(got.command.Env, "TRANSPORT_TEST=kept") {
				t.Fatal("wrapper discarded an ordinary per-command environment entry")
			}
			if f.envReads != 1 || f.homeReads != 1 || f.lookups != 1 {
				t.Fatal("bound command reconsidered mutable environment, home, or PATH selection")
			}
		})
	}
}

func TestSourceTransportResolvesContextReadOnlyOnce(t *testing.T) {
	t.Parallel()
	f := newTransportTestFixture(t, "darwin")
	f.env = []string{"PATH=/original/bin"}
	binding := f.bind(t)
	var contextCalls []platform.Command
	for _, call := range f.runner.calls {
		if operation := transportTestOperation(call.command.Args); len(operation) > 0 && operation[0] == "context" {
			contextCalls = append(contextCalls, call.command)
		}
	}
	want := [][]string{
		{"--config", transportTestConfig, "context", "show"},
		{"--config", transportTestConfig, "context", "inspect", "desktop-linux", "--format", "{{json .}}"},
	}
	if len(contextCalls) != len(want) {
		t.Fatalf("context resolution was not the two readonly observations: %v", contextCalls)
	}
	for i, command := range contextCalls {
		if command.Name != transportTestCLI || !reflect.DeepEqual(command.Args, want[i]) {
			t.Fatalf("context lookup %d was not explicitly configured: %q %q", i, command.Name, command.Args)
		}
	}
	f.runner.contextName, f.runner.contextHost = "other", "ssh://other"
	before := len(f.runner.calls)
	if err := binding.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.runner.calls[before:] {
		transportTestAssertBoundCommand(t, call.command, "unix://"+transportTestConfig+"/run/docker.sock")
		if operation := transportTestOperation(call.command.Args); len(operation) == 0 || operation[0] != "info" {
			t.Fatalf("verification re-resolved a mutable context: %q", call.command.Args)
		}
	}
}

func TestSourceTransportUsesExplicitContextWithoutChangingSelection(t *testing.T) {
	t.Parallel()
	f := newTransportTestFixture(t, "darwin")
	f.env = []string{"DOCKER_CONTEXT=desktop-linux"}
	f.bind(t)
	var operations [][]string
	for _, call := range f.runner.calls {
		operations = append(operations, transportTestOperation(call.command.Args))
	}
	want := [][]string{
		{"context", "inspect", "desktop-linux", "--format", "{{json .}}"},
		{"info", "--format", transportTestInfoFormat},
	}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("explicit context did not resolve directly with readonly operations: %q", operations)
	}
}

func TestSourceTransportRefusesUntrustedContextObservation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*transportTestRunner)
	}{
		{"remote-endpoint", func(r *transportTestRunner) { r.contextHost = "ssh://other" }},
		{"tls-skip", func(r *transportTestRunner) { r.contextTLS = true }},
		{"invalid-name", func(r *transportTestRunner) { r.contextName = "desktop-linux\nother" }},
		{"null", func(r *transportTestRunner) { r.contextRaw = []byte("null") }},
		{"mismatched-name", func(r *transportTestRunner) {
			r.contextRaw = []byte(`{"Name":"other","Endpoints":{"docker":{"Host":"unix:///home/test/.docker/run/docker.sock","SkipTLSVerify":false}}}`)
		}},
		{"missing-endpoint", func(r *transportTestRunner) { r.contextRaw = []byte(`{"Name":"desktop-linux","Endpoints":{}}`) }},
		{"extra-endpoint", func(r *transportTestRunner) {
			r.contextRaw = []byte(`{"Name":"desktop-linux","Endpoints":{"docker":{"Host":"unix:///home/test/.docker/run/docker.sock","SkipTLSVerify":false},"other":{"Host":"unix:///run/other.sock"}}}`)
		}},
		{"case-name", func(r *transportTestRunner) {
			r.contextRaw = []byte(`{"name":"desktop-linux","Endpoints":{"docker":{"Host":"unix:///home/test/.docker/run/docker.sock","SkipTLSVerify":false}}}`)
		}},
		{"case-name-alias", func(r *transportTestRunner) {
			r.contextRaw = []byte(`{"Name":"desktop-linux","name":"desktop-linux","Endpoints":{"docker":{"Host":"unix:///home/test/.docker/run/docker.sock","SkipTLSVerify":false}}}`)
		}},
		{"case-endpoint-host", func(r *transportTestRunner) {
			r.contextRaw = []byte(`{"Name":"desktop-linux","Endpoints":{"docker":{"host":"unix:///home/test/.docker/run/docker.sock","SkipTLSVerify":false}}}`)
		}},
		{"case-endpoint-tls", func(r *transportTestRunner) {
			r.contextRaw = []byte(`{"Name":"desktop-linux","Endpoints":{"docker":{"Host":"unix:///home/test/.docker/run/docker.sock","skipTLSVerify":false}}}`)
		}},
		{"null-endpoint-tls", func(r *transportTestRunner) {
			r.contextRaw = []byte(`{"Name":"desktop-linux","Endpoints":{"docker":{"Host":"unix:///home/test/.docker/run/docker.sock","SkipTLSVerify":null}}}`)
		}},
		{"null-name", func(r *transportTestRunner) {
			r.contextRaw = []byte(`{"Name":null,"Endpoints":{"docker":{"Host":"unix:///home/test/.docker/run/docker.sock","SkipTLSVerify":false}}}`)
		}},
		{"trailing-json", func(r *transportTestRunner) {
			r.contextRaw = []byte(`{"Name":"desktop-linux","Endpoints":{"docker":{"Host":"unix:///home/test/.docker/run/docker.sock","SkipTLSVerify":false}}}{}`)
		}},
		{"oversized", func(r *transportTestRunner) { r.contextRaw = bytes.Repeat([]byte(" "), 1<<20) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTransportTestFixture(t, "darwin")
			f.env = nil
			test.edit(f.runner)
			if binding, err := bindSourceDockerTransportObserved(context.Background(), f.runner, f.host, f.observations()); err == nil || binding != nil {
				t.Fatal("untrusted context observation produced a binding")
			}
			for _, call := range f.runner.calls {
				operation := transportTestOperation(call.command.Args)
				if len(operation) < 2 || operation[0] != "context" || operation[1] != "show" && operation[1] != "inspect" {
					t.Fatalf("untrusted context observation caused another operation: %q", operation)
				}
			}
		})
	}
}

func TestSourceTransportRejectsUntrustedSelectionBeforeEffects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(*transportTestFixture)
	}{
		{"ambiguous-host-context", func(f *transportTestFixture) { f.env = append(f.env, "DOCKER_CONTEXT=desktop-linux") }},
		{"tls", func(f *transportTestFixture) { f.env = append(f.env, "DOCKER_TLS=1") }},
		{"tls-verify", func(f *transportTestFixture) { f.env = append(f.env, "DOCKER_TLS_VERIFY=1") }},
		{"cert-path", func(f *transportTestFixture) { f.env = append(f.env, "DOCKER_CERT_PATH=/certs") }},
		{"api-version", func(f *transportTestFixture) { f.env = append(f.env, "DOCKER_API_VERSION=1.1") }},
		{"custom-headers", func(f *transportTestFixture) { f.env = append(f.env, "DOCKER_CUSTOM_HEADERS=x=y") }},
		{"unknown-control", func(f *transportTestFixture) { f.env = append(f.env, "DOCKER_UNKNOWN=1") }},
		{"malformed-environment", func(f *transportTestFixture) { f.env = append(f.env, "DOCKER_HOST") }},
		{"duplicate-environment", func(f *transportTestFixture) { f.env = append(f.env, f.env[1]) }},
		{"tcp", func(f *transportTestFixture) { f.env[1] = "DOCKER_HOST=tcp://127.0.0.1:2375" }},
		{"ssh", func(f *transportTestFixture) { f.env[1] = "DOCKER_HOST=ssh://host" }},
		{"http", func(f *transportTestFixture) { f.env[1] = "DOCKER_HOST=http://localhost" }},
		{"npipe", func(f *transportTestFixture) { f.env[1] = "DOCKER_HOST=npipe:////./pipe/docker_engine" }},
		{"unix-authority", func(f *transportTestFixture) { f.env[1] = "DOCKER_HOST=unix://host/run/docker.sock" }},
		{"unix-relative", func(f *transportTestFixture) { f.env[1] = "DOCKER_HOST=unix:run/docker.sock" }},
		{"unix-query", func(f *transportTestFixture) { f.env[1] += "?host=other" }},
		{"unix-fragment", func(f *transportTestFixture) { f.env[1] += "#other" }},
		{"unix-escaped", func(f *transportTestFixture) { f.env[1] = "DOCKER_HOST=unix:///run/%64ocker.sock" }},
		{"unix-unclean", func(f *transportTestFixture) { f.env[1] = "DOCKER_HOST=unix:///run/../run/docker.sock" }},
		{"unix-trailing-slash", func(f *transportTestFixture) { f.env[1] += "/" }},
		{"unix-control", func(f *transportTestFixture) { f.env[1] += "\n" }},
		{"unknown-host", func(f *transportTestFixture) { f.host.GOOS = "windows" }},
		{"unknown-arch", func(f *transportTestFixture) { f.host.GOARCH = "amd64" }},
		{"empty-host", func(f *transportTestFixture) { f.host = hostcap.Host{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newTransportTestFixture(t, "linux")
			test.edit(f)
			binding, err := bindSourceDockerTransportObserved(context.Background(), f.runner, f.host, f.observations())
			if err == nil || binding != nil {
				t.Fatal("untrusted selection produced a binding")
			}
			if len(f.runner.calls) != 0 {
				t.Fatalf("untrusted selection crossed the process boundary: %v", f.runner.calls)
			}
		})
	}
}

func TestSourceTransportRequiresTrustedLocalIdentities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		goos string
		edit func(*transportTestFixture)
	}{
		{"socket-missing", "linux", func(f *transportTestFixture) { delete(f.paths, "/run/docker.sock") }},
		{"socket-regular", "linux", func(f *transportTestFixture) {
			p := f.paths["/run/docker.sock"]
			p.mode = 0600
			f.paths["/run/docker.sock"] = p
		}},
		{"socket-symlink", "linux", func(f *transportTestFixture) {
			p := f.paths["/run/docker.sock"]
			p.mode = fs.ModeSymlink | 0777
			f.paths["/run/docker.sock"] = p
		}},
		{"socket-other-owner", "linux", func(f *transportTestFixture) {
			p := f.paths["/run/docker.sock"]
			p.uid = 2000
			f.paths["/run/docker.sock"] = p
		}},
		{"socket-world-write", "linux", func(f *transportTestFixture) {
			p := f.paths["/run/docker.sock"]
			p.mode |= 0002
			f.paths["/run/docker.sock"] = p
		}},
		{"desktop-root-owned-socket", "darwin", func(f *transportTestFixture) {
			p := f.paths[transportTestConfig+"/run/docker.sock"]
			p.uid = 0
			f.paths[transportTestConfig+"/run/docker.sock"] = p
		}},
		{"desktop-other-socket", "darwin", func(f *transportTestFixture) {
			f.env[1] = "DOCKER_HOST=unix:///run/docker.sock"
			f.paths["/run/docker.sock"] = sourceTransportPathInfo{mode: fs.ModeSocket | 0600, uid: 1000, device: 1, inode: 14}
		}},
		{"cli-directory", "linux", func(f *transportTestFixture) {
			p := f.paths[transportTestCLI]
			p.mode = fs.ModeDir | 0755
			f.paths[transportTestCLI] = p
		}},
		{"cli-not-executable", "linux", func(f *transportTestFixture) {
			p := f.paths[transportTestCLI]
			p.mode = 0644
			f.paths[transportTestCLI] = p
		}},
		{"cli-other-owner", "linux", func(f *transportTestFixture) {
			p := f.paths[transportTestCLI]
			p.uid = 2000
			f.paths[transportTestCLI] = p
		}},
		{"cli-world-write", "linux", func(f *transportTestFixture) {
			p := f.paths[transportTestCLI]
			p.mode |= 0002
			f.paths[transportTestCLI] = p
		}},
		{"cli-setuid", "linux", func(f *transportTestFixture) {
			p := f.paths[transportTestCLI]
			p.mode |= fs.ModeSetuid
			f.paths[transportTestCLI] = p
		}},
		{"cli-setgid", "linux", func(f *transportTestFixture) {
			p := f.paths[transportTestCLI]
			p.mode |= fs.ModeSetgid
			f.paths[transportTestCLI] = p
		}},
		{"config-missing", "linux", func(f *transportTestFixture) { delete(f.paths, transportTestConfig) }},
		{"config-regular", "linux", func(f *transportTestFixture) {
			p := f.paths[transportTestConfig]
			p.mode = 0600
			f.paths[transportTestConfig] = p
		}},
		{"config-symlink", "linux", func(f *transportTestFixture) {
			p := f.paths[transportTestConfig]
			p.mode = fs.ModeSymlink | 0777
			f.paths[transportTestConfig] = p
		}},
		{"config-other-owner", "linux", func(f *transportTestFixture) {
			p := f.paths[transportTestConfig]
			p.uid = 2000
			f.paths[transportTestConfig] = p
		}},
		{"config-world-write", "linux", func(f *transportTestFixture) {
			p := f.paths[transportTestConfig]
			p.mode |= 0002
			f.paths[transportTestConfig] = p
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newTransportTestFixture(t, test.goos)
			test.edit(f)
			binding, err := bindSourceDockerTransportObserved(context.Background(), f.runner, f.host, f.observations())
			if err == nil || binding != nil {
				t.Fatal("untrusted filesystem identity produced a binding")
			}
			if len(f.runner.calls) != 0 {
				t.Fatal("untrusted filesystem identity crossed the process boundary")
			}
		})
	}
}

func TestSourceTransportRechecksIdentitiesBeforeForwarding(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/run/docker.sock", transportTestCLI, transportTestConfig} {
		for _, field := range []string{"inode", "device", "mode", "owner", "missing"} {
			t.Run(path+"/"+field, func(t *testing.T) {
				f := newTransportTestFixture(t, "linux")
				binding := f.bind(t)
				p := f.paths[path]
				switch field {
				case "inode":
					p.inode++
				case "device":
					p.device++
				case "mode":
					p.mode ^= 0010
				case "owner":
					p.uid = 0
					if path == transportTestCLI {
						p.uid = 1000
					}
				case "missing":
					delete(f.paths, path)
				}
				if field != "missing" {
					f.paths[path] = p
				}
				before := len(f.runner.calls)
				if err := binding.Runner().Run(context.Background(), platform.Command{Name: "docker", Args: []string{"pull", "fixture:tag"}}); err == nil {
					t.Fatal("changed local identity forwarded an effectful command")
				}
				if len(f.runner.calls) != before {
					t.Fatal("changed local identity reached the underlying runner")
				}
			})
		}
	}
}

func TestSourceTransportRejectsSocketReplacementDuringResolution(t *testing.T) {
	t.Parallel()
	f := newTransportTestFixture(t, "linux")
	observed := f.observations()
	canonical := observed.canonical
	observed.canonical = func(path string) (string, error) {
		if path == "/run/docker.sock" {
			p := f.paths[path]
			p.inode++
			f.paths[path] = p
		}
		return canonical(path)
	}
	if binding, err := bindSourceDockerTransportObserved(context.Background(), f.runner, f.host, observed); err == nil || binding != nil {
		t.Fatal("socket replacement between observations produced a binding")
	}
	if len(f.runner.calls) != 0 {
		t.Fatal("socket replacement reached the process boundary")
	}
}

func TestSourceTransportCaptureUsesTheSameFrozenBinding(t *testing.T) {
	t.Parallel()
	f := newTransportTestFixture(t, "linux")
	binding := f.bind(t)
	f.runner.before = func(ctx context.Context, command platform.Command) (bool, error) {
		if !reflect.DeepEqual(transportTestOperation(command.Args), []string{"image", "inspect", "--format", "fixture", "fixture:image"}) {
			return false, nil
		}
		_, err := command.Stdout.Write([]byte("captured"))
		return true, err
	}
	got, err := binding.Runner().Capture(context.Background(), platform.Command{Name: "docker", Args: []string{"image", "inspect", "--format", "fixture", "fixture:image"}})
	if err != nil || string(got) != "captured" {
		t.Fatalf("bound capture lost output: %q, %v", got, err)
	}
	if f.runner.captures != 1 {
		t.Fatal("bound capture did not use the underlying capture boundary")
	}
	transportTestAssertBoundCommand(t, f.runner.calls[len(f.runner.calls)-1].command, "unix:///run/docker.sock")
}

func TestSourceTransportPinsAbsentConfigAndItsExistingParent(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"none", "config-appears", "parent-replaced", "parent-unobservable"} {
		t.Run(change, func(t *testing.T) {
			f := newTransportTestFixture(t, "linux")
			delete(f.paths, transportTestConfig)
			f.paths[transportTestHome] = sourceTransportPathInfo{mode: fs.ModeDir | 0700, uid: 1000, device: 1, inode: 14}
			binding := f.bind(t)
			switch change {
			case "config-appears":
				f.paths[transportTestConfig] = sourceTransportPathInfo{mode: fs.ModeDir | 0700, uid: 1000, device: 1, inode: 15}
			case "parent-replaced":
				p := f.paths[transportTestHome]
				p.inode++
				f.paths[transportTestHome] = p
			case "parent-unobservable":
				f.pathErrors[transportTestHome] = fs.ErrPermission
			}
			before := len(f.runner.calls)
			err := binding.Runner().Run(context.Background(), platform.Command{Name: "docker", Args: []string{"pull", "fixture:tag"}})
			if change == "none" {
				if err != nil {
					t.Fatalf("stable absent config was refused: %v", err)
				}
				transportTestAssertBoundCommand(t, f.runner.calls[len(f.runner.calls)-1].command, "unix:///run/docker.sock")
			} else {
				if err == nil || len(f.runner.calls) != before {
					t.Fatal("changed absent-config authority reached the underlying runner")
				}
			}
		})
	}
}

func TestSourceTransportRefusesUnknownForwardedOperation(t *testing.T) {
	t.Parallel()
	for _, command := range []platform.Command{
		{Name: "other", Args: []string{"pull", "fixture:tag"}},
		{Name: transportTestCLI, Args: []string{"pull", "fixture:tag"}},
		{Name: "docker"},
		{Name: "docker", Args: []string{"context", "use", "other"}},
		{Name: "docker", Args: []string{"system", "prune"}},
		{Name: "docker", Args: []string{"compose", "up"}},
		{Name: "docker", Args: []string{"pull", "-c", "other", "fixture:tag"}},
		{Name: "docker", Args: []string{"create", "-c", "other"}},
		{Name: "docker", Args: []string{"create", "-cNAME"}},
	} {
		t.Run(command.Name+" "+strings.Join(command.Args, " "), func(t *testing.T) {
			f := newTransportTestFixture(t, "linux")
			binding := f.bind(t)
			before := len(f.runner.calls)
			if err := binding.Runner().Run(context.Background(), command); err == nil || len(f.runner.calls) != before {
				t.Fatal("unknown command or operation reached the underlying runner")
			}
		})
	}
}

func TestSourceTransportAllowsOnlyTheCompiledCreateBashPayload(t *testing.T) {
	t.Parallel()
	for _, phase := range []sourcePhase{sourceInit, sourceBuild} {
		t.Run(string(phase), func(t *testing.T) {
			f := newTransportTestFixture(t, "linux")
			binding := f.bind(t)
			args, err := sourceCreateArgs(policyTestSpec(phase))
			if err != nil {
				t.Fatal(err)
			}
			if err := binding.Runner().Run(context.Background(), platform.Command{Name: "docker", Args: args}); err != nil {
				t.Fatalf("exact compiled create payload was refused: %v", err)
			}
			for _, edit := range []string{"joined-c", "extra-c", "foreign-bash", "foreign-image"} {
				t.Run(edit, func(t *testing.T) {
					modified := append([]string(nil), args...)
					bash := slices.Index(modified, "/bin/bash")
					if bash < 1 || bash+3 >= len(modified) || modified[bash+1] != "-euo" || modified[bash+2] != "pipefail" || modified[bash+3] != "-c" {
						t.Fatal("compiled Bash payload no longer has the expected immutable grammar")
					}
					switch edit {
					case "joined-c":
						modified[bash+3] = "-cNAME"
					case "extra-c":
						modified = append(modified, "-c")
					case "foreign-bash":
						modified[bash] = "/other/bash"
					case "foreign-image":
						modified[bash-4] = "fixture:foreign"
					}
					before := len(f.runner.calls)
					if err := binding.Runner().Run(context.Background(), platform.Command{Name: "docker", Args: modified}); err == nil || len(f.runner.calls) != before {
						t.Fatal("uncompiled create short-flag payload reached the runner")
					}
				})
			}
		})
	}
}

func TestSourceTransportRefusesCommandSelectionOverrides(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--host", "unix:///other", "info"}, {"-H", "unix:///other", "info"}, {"-Hunix:///other", "info"},
		{"--host=unix:///other", "info"}, {"--context", "other", "info"}, {"--context=other", "info"},
		{"--config", "/other", "info"}, {"--config=/other", "info"}, {"--tls", "info"}, {"--tlsverify", "info"},
		{"--tlscacert", "/other", "info"}, {"--tlscert=/other", "info"}, {"--tlskey", "/other", "info"},
		{"pull", "--host", "unix:///other", "fixture:tag"}, {"pull", "--context=other", "fixture:tag"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newTransportTestFixture(t, "linux")
			binding := f.bind(t)
			before := len(f.runner.calls)
			if err := binding.Runner().Run(context.Background(), platform.Command{Name: "docker", Args: args}); err == nil {
				t.Fatal("caller supplied a competing Docker selection")
			}
			if len(f.runner.calls) != before {
				t.Fatal("competing selection reached the underlying runner")
			}
		})
	}
	for _, env := range []string{"DOCKER_HOST=unix:///other", "DOCKER_CONTEXT=other", "DOCKER_CONFIG=/other", "DOCKER_TLS=", "DOCKER_API_VERSION=1.1", "DOCKER_CUSTOM_HEADERS=x=y", "DOCKER_UNKNOWN=1"} {
		t.Run(env, func(t *testing.T) {
			f := newTransportTestFixture(t, "linux")
			binding := f.bind(t)
			before := len(f.runner.calls)
			if err := binding.Runner().Run(context.Background(), platform.Command{Name: "docker", Args: []string{"pull", "fixture:tag"}, Env: []string{env}}); err == nil {
				t.Fatal("per-command Docker environment override was accepted")
			}
			if len(f.runner.calls) != before {
				t.Fatal("per-command Docker environment override reached the underlying runner")
			}
		})
	}
}

func TestSourceTransportVerifiesExactEngineIdentity(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"engine", "version", "architecture", "operating-system"} {
		t.Run(field, func(t *testing.T) {
			f := newTransportTestFixture(t, "darwin")
			binding := f.bind(t)
			switch field {
			case "engine":
				f.runner.engine = "other-engine-0000000000000001"
			case "version":
				f.runner.version = "29.8.3"
			case "architecture":
				f.runner.arch = "amd64"
			case "operating-system":
				f.runner.engineOS = "Ubuntu 24.04.3 LTS"
			}
			if err := binding.Verify(context.Background()); err == nil {
				t.Fatal("changed or incompatible engine passed verification")
			}
			if id, version := binding.EngineIdentity(); id != transportTestEngine || version != transportTestVersion {
				t.Fatal("failed verification replaced the original engine identity")
			}
		})
	}
}

func TestSourceTransportRejectsIncompatibleOrMalformedEngine(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		goos string
		edit func(*transportTestRunner)
	}{
		{"linux-desktop", "linux", func(r *transportTestRunner) { r.engineOS = "Docker Desktop" }},
		{"darwin-nondesktop", "darwin", func(r *transportTestRunner) { r.engineOS = "Ubuntu" }},
		{"wrong-architecture", "linux", func(r *transportTestRunner) { r.arch = "amd64" }},
		{"invalid-id", "linux", func(r *transportTestRunner) { r.engine = "short" }},
		{"invalid-version", "linux", func(r *transportTestRunner) { r.version = "latest" }},
		{"empty-info", "linux", func(r *transportTestRunner) { r.infoRaw = []byte{} }},
		{"null-info", "linux", func(r *transportTestRunner) { r.infoRaw = []byte("null") }},
		{"case-id", "linux", func(r *transportTestRunner) {
			r.infoRaw = []byte(strings.Replace(transportTestEngineObservation, `"ID":`, `"id":`, 1))
		}},
		{"case-id-alias", "linux", func(r *transportTestRunner) {
			r.infoRaw = []byte(strings.Replace(transportTestEngineObservation, `"ServerVersion":`, `"id":"transport-fixture-engine-00000001","ServerVersion":`, 1))
		}},
		{"case-server-version", "linux", func(r *transportTestRunner) {
			r.infoRaw = []byte(strings.Replace(transportTestEngineObservation, `"ServerVersion":`, `"serverversion":`, 1))
		}},
		{"null-id", "linux", func(r *transportTestRunner) {
			r.infoRaw = []byte(strings.Replace(transportTestEngineObservation, `"ID":"transport-fixture-engine-00000001"`, `"ID":null`, 1))
		}},
		{"null-operating-system", "linux", func(r *transportTestRunner) {
			r.infoRaw = []byte(strings.Replace(transportTestEngineObservation, `"OperatingSystem":"Ubuntu"`, `"OperatingSystem":null`, 1))
		}},
		{"trailing-json", "linux", func(r *transportTestRunner) {
			r.infoRaw = []byte(`{"ID":"transport-fixture-engine-00000001","ServerVersion":"29.8.2","OSType":"linux","Architecture":"aarch64","OperatingSystem":"Ubuntu"}{}`)
		}},
		{"duplicate-field", "linux", func(r *transportTestRunner) {
			r.infoRaw = []byte(`{"ID":"transport-fixture-engine-00000001","ID":"other-engine-0000000000001","ServerVersion":"29.8.2","OSType":"linux","Architecture":"aarch64","OperatingSystem":"Ubuntu"}`)
		}},
		{"oversized-info", "linux", func(r *transportTestRunner) { r.infoRaw = bytes.Repeat([]byte(" "), 1<<20) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTransportTestFixture(t, test.goos)
			test.edit(f.runner)
			binding, err := bindSourceDockerTransportObserved(context.Background(), f.runner, f.host, f.observations())
			if err == nil || binding != nil {
				t.Fatal("incompatible or malformed engine produced a binding")
			}
			for _, call := range f.runner.calls {
				if operation := transportTestOperation(call.command.Args); len(operation) == 0 || operation[0] != "info" {
					t.Fatal("engine refusal caused an effectful command")
				}
			}
		})
	}
}

func TestSourceTransportMapsOnlyCanonicalOwnedSnapshotDirectory(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			f := newTransportTestFixture(t, goos)
			binding := f.bind(t)
			want := transportTestSource
			if goos == "darwin" {
				want = "/host_mnt" + want
			}
			before := len(f.runner.calls)
			if got, err := binding.DaemonSnapshotPath(transportTestSource); err != nil || got != want {
				t.Fatalf("wrong daemon coordinate: %q, want %q: %v", got, want, err)
			}
			if len(f.runner.calls) != before {
				t.Fatal("snapshot mapping executed a process")
			}
			for _, path := range []string{"", "/", "relative", transportTestSource + "/", "/home/test/a/../source snapshot", transportTestSource + ",readonly=false", transportTestSource + `"`, transportTestSource + "\n", "/host_mnt" + transportTestSource, "/host_mnt/host_mnt" + transportTestSource} {
				if got, err := binding.DaemonSnapshotPath(path); err == nil || got != "" {
					t.Fatalf("unsafe snapshot coordinate was mapped: %q => %q, %v", path, got, err)
				}
			}
			for _, change := range []string{"missing", "file", "symlink", "alias", "foreign-owner"} {
				t.Run(change, func(t *testing.T) {
					original := f.paths[transportTestSource]
					defer func() { f.paths[transportTestSource] = original; delete(f.aliases, transportTestSource) }()
					p := original
					switch change {
					case "missing":
						delete(f.paths, transportTestSource)
					case "file":
						p.mode = 0600
						f.paths[transportTestSource] = p
					case "symlink":
						p.mode = fs.ModeSymlink | 0777
						f.paths[transportTestSource] = p
					case "alias":
						f.aliases[transportTestSource] = "/other/source"
					case "foreign-owner":
						p.uid = 2000
						f.paths[transportTestSource] = p
					}
					if got, err := binding.DaemonSnapshotPath(transportTestSource); err == nil || got != "" {
						t.Fatalf("untrusted source directory was mapped: %q, %v", got, err)
					}
				})
			}
		})
	}
}

func TestSourceTransportRefusesMappingAfterLocalIdentityChanges(t *testing.T) {
	t.Parallel()
	f := newTransportTestFixture(t, "darwin")
	binding := f.bind(t)
	socket := transportTestConfig + "/run/docker.sock"
	p := f.paths[socket]
	p.inode++
	f.paths[socket] = p
	before := len(f.runner.calls)
	if got, err := binding.DaemonSnapshotPath(transportTestSource); err == nil || got != "" {
		t.Fatal("snapshot mapping retained authority after its bound socket changed")
	}
	if len(f.runner.calls) != before {
		t.Fatal("refused mapping executed a process")
	}
}

func TestSourceTransportCancellationStopsObservationAndForwarding(t *testing.T) {
	t.Parallel()
	f := newTransportTestFixture(t, "linux")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if binding, err := bindSourceDockerTransportObserved(ctx, f.runner, f.host, f.observations()); !errors.Is(err, context.Canceled) || binding != nil {
		t.Fatalf("canceled binding returned %v, %v", binding, err)
	}
	if len(f.runner.calls) != 0 {
		t.Fatal("canceled binding crossed the process boundary")
	}
	binding := f.bind(t)
	before := len(f.runner.calls)
	if err := binding.Runner().Run(ctx, platform.Command{Name: "docker", Args: []string{"pull", "fixture:tag"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled command returned %v", err)
	}
	if err := binding.Verify(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled verification returned %v", err)
	}
	if len(f.runner.calls) != before {
		t.Fatal("canceled bound operation reached the underlying runner")
	}
}

func TestSourceTransportPropagatesCancellationDuringObservation(t *testing.T) {
	t.Parallel()
	f := newTransportTestFixture(t, "linux")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.runner.before = func(commandContext context.Context, command platform.Command) (bool, error) {
		cancel()
		return true, commandContext.Err()
	}
	if binding, err := bindSourceDockerTransportObserved(ctx, f.runner, f.host, f.observations()); !errors.Is(err, context.Canceled) || binding != nil {
		t.Fatalf("cancellation during observation returned %v, %v", binding, err)
	}
	if len(f.runner.calls) != 1 || transportTestOperation(f.runner.calls[0].command.Args)[0] != "info" {
		t.Fatal("canceled observation continued to another operation")
	}
}

func TestSourceTransportInfoHonorsEarlierCallerDeadline(t *testing.T) {
	t.Parallel()
	f := newTransportTestFixture(t, "linux")
	deadline := time.Now().Add(5 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if _, err := bindSourceDockerTransportObserved(ctx, f.runner, f.host, f.observations()); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.runner.calls {
		got, ok := call.ctx.Deadline()
		if !ok || got.After(deadline) {
			t.Fatal("transport observation dropped or extended the caller deadline")
		}
	}
}
