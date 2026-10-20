package build

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ooaklee/lexr.sh/internal/hostcap"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// This private binding is authority established before dependency effects, not
// a user-selectable namespace alias or an interpretation of inspected mounts.
type sourceTransportBinding interface {
	// Runner returns the operation runner pinned to the admitted local transport.
	Runner() platform.Runner
	// EngineIdentity returns the engine ID/version captured during binding.
	EngineIdentity() (string, string)
	// DaemonSnapshotPath derives the admitted daemon coordinate for a host root.
	DaemonSnapshotPath(string) (string, error)
	// Verify rechecks local identities and the same engine without rebinding.
	Verify(context.Context) error
}

// sourceTransportPathInfo retains inode, owner and type observations for pins.
type sourceTransportPathInfo struct {
	mode          fs.FileMode
	uid           uint32
	device, inode uint64
}

// Per-binding observations make refusals testable without real Docker or Unix
// sockets. Production uses real read-only OS observations, never global mocks.
type sourceTransportObservations struct {
	environ   func() []string
	home      func() (string, error)
	lookPath  func(string) (string, error)
	canonical func(string) (string, error)
	lstat     func(string) (sourceTransportPathInfo, error)
	uid       func() int
}

// sourceTransportInfoFormat limits engine observations to the required fields.
const sourceTransportInfoFormat = `{"ID":{{json .ID}},"ServerVersion":{{json .ServerVersion}},"OSType":{{json .OSType}},"Architecture":{{json .Architecture}},"OperatingSystem":{{json .OperatingSystem}}}`

// sourceTransportContextName bounds a read-only Docker context selection name.
var sourceTransportContextName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// sourceTransportEngineID accepts only bounded observed daemon identities.
var sourceTransportEngineID = regexp.MustCompile(`^[A-Za-z0-9:.-]{16,128}$`)

// sourceTransportVersion accepts a bounded, nonempty daemon version token.
var sourceTransportVersion = regexp.MustCompile(`^[0-9][0-9A-Za-z.+~_-]{0,127}$`)

// sourceTransportClearedEnv lists ambient controls neutralised on bound commands.
var sourceTransportClearedEnv = []string{
	"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS",
	"DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_API_VERSION", "DOCKER_CUSTOM_HEADERS",
}

// sourceTransportEngine combines the native platform and daemon identity pins.
type sourceTransportEngine struct {
	ID, ServerVersion, OSType, Architecture, OperatingSystem string
}

// localSourceDockerTransport retains one admitted executable/config/socket and
// engine binding for dependency preparation, source execution and recovery.
type localSourceDockerTransport struct {
	underlying                                         platform.Runner
	observed                                           sourceTransportObservations
	host                                               hostcap.Host
	uid                                                uint32
	executable, config, configAnchor, socket, endpoint string
	executableInfo, configInfo, socketInfo             sourceTransportPathInfo
	configMissing                                      bool
	engine                                             sourceTransportEngine
}

// bindSourceDockerTransport establishes a local binding from real host metadata.
func bindSourceDockerTransport(ctx context.Context, runner platform.Runner, host hostcap.Host) (sourceTransportBinding, error) {
	return bindSourceDockerTransportObserved(ctx, runner, host, sourceTransportObservations{
		environ: os.Environ, home: os.UserHomeDir, lookPath: exec.LookPath,
		canonical: filepath.EvalSymlinks, lstat: sourceTransportLstat, uid: sourceTransportCurrentUID,
	})
}

// sourceTransportCleanPath checks canonical absolute mount-safe path syntax.
func sourceTransportCleanPath(name string) bool {
	return filepath.IsAbs(name) && filepath.Clean(name) == name && utf8.ValidString(name) &&
		!strings.ContainsAny(name, ",\"") && strings.IndexFunc(name, unicode.IsControl) < 0
}

// sourceTransportEnvironment refuses ambiguous selectors and unsupported Docker
// controls before any daemon request or dependency acquisition is attempted.
func sourceTransportEnvironment(values []string) (map[string]string, error) {
	out := make(map[string]string)
	for _, value := range values {
		key, text, found := strings.Cut(value, "=")
		if !strings.HasPrefix(key, "DOCKER_") {
			continue
		}
		if !found || strings.ContainsRune(text, 0) {
			return nil, errors.New("malformed Docker environment")
		}
		if _, duplicate := out[key]; duplicate {
			return nil, errors.New("duplicate Docker environment selection")
		}
		out[key] = text
		known := false
		for _, name := range sourceTransportClearedEnv {
			known = known || key == name
		}
		if !known && text != "" {
			return nil, fmt.Errorf("unsupported Docker environment control %s", key)
		}
	}
	for _, key := range sourceTransportClearedEnv[3:] {
		if out[key] != "" {
			return nil, fmt.Errorf("Docker TLS/API/header override %s is unsupported for local source transport", key)
		}
	}
	if out["DOCKER_HOST"] != "" && out["DOCKER_CONTEXT"] != "" {
		return nil, errors.New("simultaneous DOCKER_HOST and DOCKER_CONTEXT is ambiguous for source transport")
	}
	return out, nil
}

// bindSourceDockerTransportObserved resolves one local endpoint and pins its
// filesystem/engine identities using the selected read-only observations.
func bindSourceDockerTransportObserved(ctx context.Context, runner platform.Runner, host hostcap.Host, observed sourceTransportObservations) (sourceTransportBinding, error) {
	if ctx == nil || runner == nil || observed.environ == nil || observed.home == nil || observed.lookPath == nil || observed.canonical == nil || observed.lstat == nil || observed.uid == nil {
		return nil, errors.New("source Docker transport observations are incomplete")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uid := observed.uid()
	if host.GOARCH != "arm64" || host.GOOS != "linux" && host.GOOS != "darwin" || uid < 0 {
		return nil, errors.New("local source Docker transport requires a Linux or macOS ARM64 host")
	}
	ambient, err := sourceTransportEnvironment(observed.environ())
	if err != nil {
		return nil, err
	}
	t := &localSourceDockerTransport{underlying: runner, observed: observed, host: host, uid: uint32(uid)}
	home, err := observed.home()
	if err != nil || !sourceTransportCleanPath(home) {
		return nil, errors.New("source Docker transport requires an absolute clean home directory")
	}
	home, err = observed.canonical(home)
	if err != nil || !sourceTransportCleanPath(home) {
		return nil, errors.New("source Docker transport home cannot be canonicalized")
	}
	executable, err := observed.lookPath("docker")
	if err != nil || !filepath.IsAbs(executable) {
		return nil, errors.New("Docker CLI must resolve to an absolute executable")
	}
	t.executable, err = observed.canonical(executable)
	if err != nil || !sourceTransportCleanPath(t.executable) {
		return nil, errors.New("Docker CLI executable cannot be canonicalized")
	}
	t.executableInfo, err = observed.lstat(t.executable)
	if err != nil || !t.executableInfo.mode.IsRegular() || t.executableInfo.mode.Perm()&0o111 == 0 || t.executableInfo.mode&(fs.ModeSetuid|fs.ModeSetgid) != 0 || !t.trusted(t.executableInfo) {
		return nil, errors.New("Docker CLI must be a trusted regular executable")
	}
	config := ambient["DOCKER_CONFIG"]
	if config == "" {
		config = filepath.Join(home, ".docker")
	}
	if !sourceTransportCleanPath(config) {
		return nil, errors.New("Docker config path must be absolute and clean")
	}
	info, statErr := observed.lstat(config)
	if errors.Is(statErr, fs.ErrNotExist) {
		// The default Linux CLI also works without ~/.docker. Pin its existing
		// trusted parent and continued absence; never create a directory here.
		t.configAnchor, err = observed.canonical(filepath.Dir(config))
		if err != nil || !sourceTransportCleanPath(t.configAnchor) {
			return nil, errors.New("absent Docker config needs an existing canonical parent")
		}
		t.configInfo, err = observed.lstat(t.configAnchor)
		if err != nil || !t.configInfo.mode.IsDir() || !t.trusted(t.configInfo) {
			return nil, errors.New("Docker config parent is not trusted")
		}
		t.config, t.configMissing = filepath.Join(t.configAnchor, filepath.Base(config)), true
	} else {
		if statErr != nil || !info.mode.IsDir() || !t.trusted(info) {
			return nil, errors.New("Docker config must be a trusted directory, not a symlink")
		}
		t.config, err = observed.canonical(config)
		if err != nil || !sourceTransportCleanPath(t.config) {
			return nil, errors.New("Docker config cannot be canonicalized")
		}
		t.configInfo, err = observed.lstat(t.config)
		if err != nil || !t.configInfo.mode.IsDir() || !t.trusted(t.configInfo) {
			return nil, errors.New("canonical Docker config is not trusted")
		}
		t.configAnchor = t.config
	}
	endpoint := ambient["DOCKER_HOST"]
	if endpoint == "" {
		selected := ambient["DOCKER_CONTEXT"]
		if selected == "" {
			out, captureErr := t.capture(ctx, false, "context", "show")
			if captureErr != nil {
				return nil, captureErr
			}
			selected = strings.TrimSpace(string(out))
		}
		if !sourceTransportContextName.MatchString(selected) {
			return nil, errors.New("Docker context selection is not a safe literal name")
		}
		out, captureErr := t.capture(ctx, false, "context", "inspect", selected, "--format", "{{json .}}")
		if captureErr != nil {
			return nil, captureErr
		}
		var contextInfo struct {
			Name      string
			Endpoints map[string]json.RawMessage
		}
		if err = sourceTransportDecode(out, &contextInfo); err != nil || contextInfo.Name != selected || len(contextInfo.Endpoints) != 1 {
			return nil, errors.New("Docker context must select exactly one non-TLS Docker endpoint")
		}
		var selectedEndpoint struct {
			Host          string
			SkipTLSVerify bool
		}
		if err = sourceTransportDecode(contextInfo.Endpoints["docker"], &selectedEndpoint); err != nil || selectedEndpoint.SkipTLSVerify {
			return nil, errors.New("Docker context endpoint must have canonical non-TLS observations")
		}
		endpoint = selectedEndpoint.Host
	}
	if !strings.HasPrefix(endpoint, "unix:///") {
		return nil, errors.New("source Docker transport supports local Unix sockets only")
	}
	rawSocket := strings.TrimPrefix(endpoint, "unix://")
	if !sourceTransportCleanPath(rawSocket) || strings.ContainsAny(rawSocket, "%?#") {
		return nil, errors.New("Docker Unix socket path is malformed or aliased")
	}
	leaf, err := observed.lstat(rawSocket)
	if err != nil || leaf.mode&fs.ModeSocket == 0 || !t.trusted(leaf) {
		return nil, errors.New("Docker endpoint must be an owned local Unix socket, not a symlink")
	}
	t.socket, err = observed.canonical(rawSocket)
	if err != nil || !sourceTransportCleanPath(t.socket) {
		return nil, errors.New("Docker socket cannot be canonicalized")
	}
	t.socketInfo, err = observed.lstat(t.socket)
	if err != nil || t.socketInfo != leaf {
		return nil, errors.New("Docker socket identity changed during canonicalization")
	}
	if host.GOOS == "darwin" && (t.socket != filepath.Join(home, ".docker/run/docker.sock") || t.socketInfo.uid != t.uid) {
		return nil, errors.New("macOS source transport requires the current user's exact local Docker Desktop socket")
	}
	t.endpoint = "unix://" + t.socket
	t.engine, err = t.inspectEngine(ctx)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// trusted requires root/current-user ownership and no world-writable entry.
func (t *localSourceDockerTransport) trusted(info sourceTransportPathInfo) bool {
	return (info.uid == 0 || info.uid == t.uid) && info.mode.Perm()&0o002 == 0
}

// checkLocal refuses replacement of the executable/config/socket or the later
// appearance of a configuration location that was absent during binding.
func (t *localSourceDockerTransport) checkLocal() error {
	for name, expected := range map[string]sourceTransportPathInfo{t.executable: t.executableInfo, t.configAnchor: t.configInfo} {
		actual, err := t.observed.lstat(name)
		if err != nil || actual != expected {
			return errors.New("bound Docker executable/config identity changed")
		}
	}
	if t.configMissing {
		if _, err := t.observed.lstat(t.config); !errors.Is(err, fs.ErrNotExist) {
			return errors.New("previously absent Docker config appeared or became unobservable")
		}
	}
	if t.socket != "" {
		actual, err := t.observed.lstat(t.socket)
		if err != nil || actual != t.socketInfo {
			return errors.New("bound local Docker socket identity changed")
		}
	}
	return nil
}

// command fixes executable/configuration and optionally endpoint arguments while
// neutralising inherited Docker selectors and refusing per-command overrides.
func (t *localSourceDockerTransport) command(command platform.Command, endpoint bool) (platform.Command, error) {
	if t == nil || t.underlying == nil || t.executable == "" || endpoint && t.endpoint == "" {
		return command, errors.New("source Docker transport is unbound")
	}
	if err := t.checkLocal(); err != nil {
		return command, err
	}
	for _, value := range command.Env {
		key, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "DOCKER_") {
			return command, errors.New("per-command Docker transport environment is forbidden")
		}
	}
	environment := append([]string(nil), command.Env...)
	for _, name := range sourceTransportClearedEnv {
		environment = append(environment, name+"=")
	}
	args := []string{"--config", t.config}
	if endpoint {
		args = append(args, "--host", t.endpoint)
	}
	command.Name, command.Args, command.Env = t.executable, append(args, command.Args...), environment
	return command, nil
}

// sourceTransportDecode bounds read-only Docker metadata before strict decoding.
func sourceTransportDecode(data []byte, target any) error {
	if len(data) > 64<<10 {
		return errors.New("Docker transport observation exceeds fixed bound")
	}
	return decodeSourceInspection(data, target)
}

// capture bounds both metadata streams and observation time; unexpected stderr
// or cancellation remains an error rather than usable transport evidence.
func (t *localSourceDockerTransport) capture(parent context.Context, endpoint bool, args ...string) ([]byte, error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	stdout := &buildBoundedBuffer{limit: 64 << 10, cancel: cancel}
	stderr := &buildBoundedBuffer{limit: 64 << 10, cancel: cancel}
	command, err := t.command(platform.Command{Name: "docker", Args: args, Stdout: stdout, Stderr: stderr}, endpoint)
	if err != nil {
		return nil, err
	}
	err = t.underlying.Run(ctx, command)
	if stdout.exceeded || stderr.exceeded {
		err = errors.Join(err, errors.New("Docker transport observation exceeds fixed bound"))
	}
	if stderr.data.Len() != 0 {
		err = errors.Join(err, errors.New("Docker transport observation returned diagnostics"))
	}
	err = errors.Join(err, ctx.Err())
	return stdout.data.Bytes(), err
}

// inspectEngine requires native Linux ARM64 identity and the platform marker
// appropriate to the already admitted local Linux or Desktop transport.
func (t *localSourceDockerTransport) inspectEngine(ctx context.Context) (sourceTransportEngine, error) {
	var engine sourceTransportEngine
	data, err := t.capture(ctx, true, "info", "--format", sourceTransportInfoFormat)
	if err != nil {
		return engine, err
	}
	if err = sourceTransportDecode(data, &engine); err != nil {
		return engine, err
	}
	if !sourceTransportEngineID.MatchString(engine.ID) || !sourceTransportVersion.MatchString(engine.ServerVersion) || engine.OSType != "linux" || engine.Architecture != "arm64" && engine.Architecture != "aarch64" {
		return engine, errors.New("bound Docker engine must identify native Linux ARM64")
	}
	if len(engine.OperatingSystem) > 256 || !utf8.ValidString(engine.OperatingSystem) || strings.IndexFunc(engine.OperatingSystem, unicode.IsControl) >= 0 ||
		t.host.GOOS == "darwin" && engine.OperatingSystem != "Docker Desktop" || t.host.GOOS == "linux" && (engine.OperatingSystem == "" || strings.Contains(strings.ToLower(engine.OperatingSystem), "docker desktop")) {
		return engine, errors.New("Docker engine operating-system marker does not corroborate the local host transport")
	}
	return engine, nil
}

// Runner exposes this binding's checked forwarding boundary to its owner.
func (t *localSourceDockerTransport) Runner() platform.Runner { return t }

// EngineIdentity returns captured daemon identity, or empty values if unbound.
func (t *localSourceDockerTransport) EngineIdentity() (string, string) {
	if t == nil {
		return "", ""
	}
	return t.engine.ID, t.engine.ServerVersion
}

// Verify re-observes the same local daemon and refuses any identity/platform drift.
func (t *localSourceDockerTransport) Verify(ctx context.Context) error {
	if ctx == nil || t == nil || t.engine.ID == "" {
		return errors.New("source Docker transport is unbound or has no context")
	}
	engine, err := t.inspectEngine(ctx)
	if err != nil {
		return err
	}
	if engine != t.engine {
		return errors.New("bound Docker engine identity/version/platform changed")
	}
	return nil
}

// DaemonSnapshotPath derives only the fixed mapping admitted by this binding,
// after checking the selected host directory's canonical identity and ownership.
func (t *localSourceDockerTransport) DaemonSnapshotPath(name string) (string, error) {
	if t == nil || t.engine.ID == "" {
		return "", errors.New("source Docker transport is unbound")
	}
	if err := t.checkLocal(); err != nil {
		return "", err
	}
	if !sourceTransportCleanPath(name) || name == "/" || name == "/host_mnt" || strings.HasPrefix(name, "/host_mnt/") {
		return "", errors.New("source snapshot path is not an unaliased canonical host directory")
	}
	canonical, err := t.observed.canonical(name)
	if err != nil || canonical != name {
		return "", errors.New("source snapshot directory must already be canonical")
	}
	info, err := t.observed.lstat(name)
	if err != nil || !info.mode.IsDir() || info.uid != t.uid || info.mode.Perm()&0o002 != 0 {
		return "", errors.New("source snapshot must be a current-user-owned directory, not a symlink")
	}
	if t.host.GOOS == "darwin" {
		return "/host_mnt" + name, nil
	}
	return name, nil
}

// sourceTransportCommandAllowed confines forwarding to workflow operations and
// refuses transport-selection flags hidden in caller arguments.
func sourceTransportCommandAllowed(command platform.Command) error {
	if command.Name != "docker" || len(command.Args) == 0 || strings.HasPrefix(command.Args[0], "-") {
		return errors.New("bound runner accepts only operation-first Docker commands")
	}
	switch command.Args[0] {
	case "info", "pull", "image", "container", "volume", "create", "start", "stop", "commit", "cp":
	default:
		return errors.New("Docker operation is outside the bound source workflow")
	}
	for i, arg := range command.Args {
		for _, flag := range []string{"--host", "--context", "--config", "--tls", "--tlsverify", "--tlscacert", "--tlscert", "--tlskey"} {
			if arg == flag || strings.HasPrefix(arg, flag+"=") {
				return errors.New("caller Docker transport flags are forbidden")
			}
		}
		// Only the compiled post-image timeout/bash payload may contain -c;
		// never admit Docker's context shorthand in a caller-selected position.
		if strings.HasPrefix(arg, "-c") && !(arg == "-c" && sourceTransportBashPayload(command.Args, i)) {
			return errors.New("caller Docker context shorthand is forbidden")
		}
		if len(arg) > 1 && arg[0] == '-' && arg[1] != '-' {
			payload := arg == "-c" && sourceTransportBashPayload(command.Args, i) || arg == "-euo" && sourceTransportBashPayload(command.Args, i+2)
			if !payload {
				return errors.New("caller Docker transport shorthand is forbidden")
			}
		}
	}
	return nil
}

// sourceTransportBashPayload distinguishes the compiled post-image bash position
// from Docker's similarly named context-selection shorthand.
func sourceTransportBashPayload(args []string, index int) bool {
	return len(args) > index+1 && index >= 8 && args[0] == "create" && sourceObjectID.MatchString(args[index-7]) &&
		args[index-6] == "--signal=TERM" && args[index-5] == "--kill-after=5s" &&
		(args[index-4] == "2m" || args[index-4] == "18m" || args[index-4] == "45m") &&
		args[index-3] == "/bin/bash" && args[index-2] == "-euo" && args[index-1] == "pipefail"
}

// Run validates an operation and forwards it through the unchanged local binding.
func (t *localSourceDockerTransport) Run(ctx context.Context, command platform.Command) error {
	if ctx == nil {
		return errors.New("source Docker command context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sourceTransportCommandAllowed(command); err != nil {
		return err
	}
	bound, err := t.command(command, true)
	if err != nil {
		return err
	}
	return t.underlying.Run(ctx, bound)
}

// Capture applies the same bound command policy to captured-output operations.
func (t *localSourceDockerTransport) Capture(ctx context.Context, command platform.Command) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("source Docker command context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := sourceTransportCommandAllowed(command); err != nil {
		return nil, err
	}
	bound, err := t.command(command, true)
	if err != nil {
		return nil, err
	}
	return t.underlying.Capture(ctx, bound)
}
