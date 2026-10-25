package build

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ooaklee/lexr.sh/internal/camera/jsonstrict"
	"github.com/ooaklee/lexr.sh/internal/sensors/builderimage"
)

// These helpers describe policy only. They do not acquire an image, create a
// volume/container, inspect a daemon, or grant authority from adjacent labels.
type sourcePhase string

// Source phases select only the fixed initialisation and offline build recipes.
const (
	sourceInit       sourcePhase = "init"
	sourceBuild      sourcePhase = "build"
	sourcePhaseLabel             = "io.lexr.sensors.phase"
)

// Docker identity patterns bound the names accepted by source policy checks.
var (
	sourceObjectID      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	sourceContainerID   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	sourceTokenPattern  = regexp.MustCompile(`^[0-9a-f]{24}$`)
	sourceEngineID      = regexp.MustCompile(`^[A-Za-z0-9:.-]{16,128}$`)
	sourceServerVersion = regexp.MustCompile(`^[0-9][0-9A-Za-z.+~_-]{0,127}$`)
)

// sourceDockerSpec is assembled only after the controller has obtained its
// own opaque builderimage.Prepared result and authenticated source snapshot.
// A matching spec is necessary policy, not proof of either prerequisite.
type sourceDockerSpec struct {
	profile            producerProfile
	Phase              sourcePhase
	Name               string
	Token              string
	ImageID            string
	VolumeName         string
	SnapshotPath       string // Host coordinate used only in the create request.
	DaemonSnapshotPath string // Exact expected inspection coordinate from the private verified binding.
	Jobs               int
	Env                []string
}

// sourceHealthcheck retains image healthcheck fields so hidden execution is refused.
type sourceHealthcheck struct {
	Test          []string
	Interval      int64
	Timeout       int64
	StartPeriod   int64
	StartInterval int64
	Retries       int
}

// sourceImageConfig captures command, environment and implicit-resource settings.
type sourceImageConfig struct {
	Image                     string
	User                      string
	WorkingDir                string
	Entrypoint                []string
	Cmd                       []string
	Env                       []string
	Labels                    map[string]string
	Volumes                   map[string]json.RawMessage
	ExposedPorts              map[string]json.RawMessage
	Healthcheck               *sourceHealthcheck
	OpenStdin, StdinOnce, Tty bool
}

// sourceImageInspection binds source execution to an immutable native image.
type sourceImageInspection struct {
	ID           string `json:"Id"`
	OS           string `json:"Os"`
	Architecture string
	Config       sourceImageConfig
}

// sourceBindOptions records requested bind propagation and recursive mount policy.
type sourceBindOptions struct {
	Propagation            string
	NonRecursive           bool
	CreateMountpoint       bool
	ReadOnlyNonRecursive   bool
	ReadOnlyForceRecursive bool
}

// sourceVolumeOptions records volume settings that could change work ownership.
type sourceVolumeOptions struct {
	NoCopy       bool
	Labels       map[string]string
	Subpath      string
	DriverConfig json.RawMessage `json:",omitempty"`
}

// sourceHostMount captures the daemon's recorded mount request for comparison.
type sourceHostMount struct {
	Type          string
	Source        string
	Target        string
	ReadOnly      bool
	Consistency   string
	BindOptions   *sourceBindOptions
	VolumeOptions *sourceVolumeOptions
	TmpfsOptions  json.RawMessage `json:",omitempty"`
}

// sourceEffectiveMount captures an independently checked effective mount route.
type sourceEffectiveMount struct {
	Type        string
	Name        string
	Source      string
	Destination string
	Driver      string
	Mode        string
	Propagation string
	RW          bool
}

// sourceContainerInspection retains ownership, isolation, mounts and state from
// one owned container's typed inspection without granting trust from labels alone.
type sourceContainerInspection struct {
	ID           string `json:"Id"`
	Name         string
	Image        string
	Config       sourceImageConfig
	RestartCount int
	Mounts       []sourceEffectiveMount
	HostConfig   struct {
		Privileged                   bool
		ReadonlyRootfs               bool
		AutoRemove                   bool
		NetworkMode                  string
		Binds                        []string
		VolumesFrom                  []string
		Devices                      []json.RawMessage
		DeviceRequests               []json.RawMessage
		DeviceCgroupRules            []string
		CapAdd, CapDrop, SecurityOpt []string
		PidsLimit                    int64
		Memory, NanoCpus             int64
		PidMode, IpcMode             string
		UTSMode, UsernsMode          string
		CgroupnsMode                 string
		GroupAdd                     []string
		Links                        []string
		ExtraHosts                   []string
		Dns, DnsOptions, DnsSearch   []string
		PortBindings                 map[string]json.RawMessage
		PublishAllPorts              bool
		Sysctls                      map[string]string
		Tmpfs                        map[string]string
		Mounts                       []sourceHostMount
		RestartPolicy                struct {
			Name              string
			MaximumRetryCount int
		}
	}
	State struct {
		Status                                       string
		Running, Paused, Restarting, Dead, OOMKilled bool
		Pid                                          int64
		ExitCode                                     int
		Error, StartedAt, FinishedAt                 string
	}
}

// sourceDockerTerminal describes the exact inspected owned container. It is
// historical evidence, not a claim that the software is installable or tested
// on physical sensors.
type sourceDockerTerminal struct {
	ContainerID string `json:"container_id"`
	Status      string `json:"status"`
	Running     bool   `json:"running"`
	PID         int64  `json:"pid"`
	ExitCode    int    `json:"exit_code"`
	OOMKilled   bool   `json:"oom_killed"`
	Error       string `json:"error,omitempty"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at"`
}

// decodeSourceInspection accepts one bounded, canonically typed JSON object and
// refuses duplicate names, missing observations and trailing data.
func decodeSourceInspection(data []byte, into any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' || len(data) > 256<<10 || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return errors.New("source Docker inspection is not bounded UTF-8 JSON")
	}
	if err := jsonstrict.RejectDuplicateNames(data); err != nil {
		return errors.New("source Docker inspection has duplicate JSON names")
	}
	output := reflect.ValueOf(into)
	if !output.IsValid() || output.Kind() != reflect.Pointer || output.IsNil() {
		return errors.New("source Docker inspection requires a non-nil typed output")
	}
	if err := validateSourceInspectionShape(data, output.Type().Elem(), "inspection"); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(into); err != nil {
		return errors.New("invalid source Docker inspection JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("source Docker inspection has trailing data")
	}
	return nil
}

// encoding/json accepts case-insensitive struct fields and null primitive
// values. Neither is an adequate observation of a safety-critical Docker
// field. Check canonical known struct names before typed decoding, while
// leaving arbitrary map keys and unknown Docker extension fields opaque.
func validateSourceInspectionShape(raw json.RawMessage, model reflect.Type, location string) error {
	if model == reflect.TypeOf(json.RawMessage(nil)) {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		switch model.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
			return nil
		default:
			return fmt.Errorf("source Docker inspection field %s must not be null", location)
		}
	}
	for model.Kind() == reflect.Pointer {
		model = model.Elem()
	}
	switch model.Kind() {
	case reflect.Struct:
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil || values == nil {
			return fmt.Errorf("source Docker inspection field %s must be an object", location)
		}
		if model == reflect.TypeOf(sourceContainerInspection{}) {
			if err := requireSourceContainerObservations(values, location); err != nil {
				return err
			}
		}
		fields := make(map[string]reflect.Type, model.NumField())
		for i := 0; i < model.NumField(); i++ {
			field := model.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			fields[name] = field.Type
		}
		for name, value := range values {
			fieldType, known := fields[name]
			if !known {
				for canonical := range fields {
					if strings.EqualFold(name, canonical) {
						return fmt.Errorf("source Docker inspection field %s.%s has noncanonical spelling", location, canonical)
					}
				}
				continue
			}
			if err := validateSourceInspectionShape(value, fieldType, location+"."+name); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return fmt.Errorf("source Docker inspection field %s must be an array", location)
		}
		for i, value := range values {
			if err := validateSourceInspectionShape(value, model.Elem(), location+"["+strconv.Itoa(i)+"]"); err != nil {
				return err
			}
		}
	}
	return nil
}

// requireSourceContainerObservations prevents absent primitive fields from
// silently becoming safe-looking Go zero values during typed decoding.
func requireSourceContainerObservations(container map[string]json.RawMessage, location string) error {
	for section, required := range map[string][]string{
		"State":      {"Status", "Running", "Paused", "Restarting", "Dead", "OOMKilled", "Pid", "ExitCode", "Error", "StartedAt", "FinishedAt"},
		"HostConfig": {"Privileged", "ReadonlyRootfs", "AutoRemove"},
	} {
		var values map[string]json.RawMessage
		raw, present := container[section]
		if !present || json.Unmarshal(raw, &values) != nil || values == nil {
			return fmt.Errorf("source Docker inspection requires observed %s.%s", location, section)
		}
		for _, name := range required {
			value, present := values[name]
			if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return fmt.Errorf("source Docker inspection requires observed %s.%s.%s", location, section, name)
			}
		}
	}
	return nil
}

// validateSourceEngine requires an identified Linux ARM64 engine observation.
func validateSourceEngine(raw []byte) (string, string, error) {
	var engine struct{ ID, ServerVersion, OSType, Architecture string }
	if err := decodeSourceInspection(raw, &engine); err != nil {
		return "", "", err
	}
	if !sourceEngineID.MatchString(engine.ID) || !sourceServerVersion.MatchString(engine.ServerVersion) || engine.OSType != "linux" || (engine.Architecture != "arm64" && engine.Architecture != "aarch64") {
		return "", "", errors.New("source Docker engine must be an identified native Linux ARM64 daemon")
	}
	return engine.ID, engine.ServerVersion, nil
}

// validateSourceImage verifies the exact dependency image and its inert defaults.
func validateSourceImage(image sourceImageInspection, exactID string) error {
	if !sourceObjectID.MatchString(exactID) || image.ID != exactID || image.OS != "linux" || image.Architecture != "arm64" {
		return errors.New("source builder must be the exact immutable Linux ARM64 image")
	}
	c := image.Config
	if c.User != "65532:65532" || c.WorkingDir != "/" || !reflect.DeepEqual(c.Entrypoint, []string{"/bin/false"}) || !reflect.DeepEqual(c.Cmd, []string{"lexr-explicit-offline-runner-required"}) || len(c.Volumes) != 0 || len(c.ExposedPorts) != 0 || !emptySourceHealthcheck(c.Healthcheck) || c.OpenStdin || c.StdinOnce || c.Tty {
		return errors.New("source builder image configuration differs")
	}
	// These labels corroborate the owned Prepared chain; they never create it.
	if c.Labels["io.lexr.sensors.builder"] != "dependencies-v1" || c.Labels["io.lexr.sensors.recipe"] != builderimage.RecipeSHA256() {
		return errors.New("source builder dependency provenance labels differ")
	}
	_, err := sourceEnvironment(c.Env)
	return err
}

// emptySourceHealthcheck accepts only an absent or entirely inert healthcheck.
func emptySourceHealthcheck(h *sourceHealthcheck) bool {
	return h == nil || len(h.Test) == 0 && h.Interval == 0 && h.Timeout == 0 && h.StartPeriod == 0 && h.StartInterval == 0 && h.Retries == 0
}

// fixedSourceEnvironment returns the compiled deterministic execution variables.
func fixedSourceEnvironment() []string {
	return []string{
		"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC",
		"PYTHONDONTWRITEBYTECODE=1", "DEBIAN_FRONTEND=noninteractive",
		"HOME=/work/job/home", "XDG_CACHE_HOME=/work/job/cache", "TMPDIR=/work/job/tmp",
	}
}

// sourceEnvironment validates, but never adopts, inherited values. Prepared
// images retain the pinned Ubuntu base's PATH and the dependency producer's
// controlled overrides. Other variables (notably loader/Python/proxy knobs)
// fail closed rather than being passed through to source execution.
func sourceEnvironment(imageEnv []string) ([]string, error) {
	allowed := map[string]bool{"PATH": true, "LANG": true, "LC_ALL": true, "TZ": true, "PYTHONDONTWRITEBYTECODE": true, "DEBIAN_FRONTEND": true, "HOME": true, "XDG_CACHE_HOME": true, "TMPDIR": true}
	seen := map[string]bool{}
	for _, value := range imageEnv {
		key, _, ok := strings.Cut(value, "=")
		if !ok || !allowed[key] || seen[key] || len(value) > 4096 || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return nil, errors.New("source builder environment contains an unknown, duplicate or malformed variable")
		}
		seen[key] = true
	}
	return fixedSourceEnvironment(), nil
}

// validateSourceSpec checks phase identities, jobs and explicit mount coordinates
// before any Docker creation command can be assembled.
func validateSourceSpec(spec sourceDockerSpec) error {
	if !spec.profile.valid() || spec.Phase != sourceInit && spec.Phase != sourceBuild || !sourceTokenPattern.MatchString(spec.Token) || spec.Name != "lexr-sensors-"+string(spec.Phase)+"-"+spec.Token || !sourceObjectID.MatchString(spec.ImageID) || spec.VolumeName != sourceVolumePrefix+spec.Token || spec.Jobs < 1 || spec.Jobs > 8 {
		return errors.New("source Docker identity or job policy differs")
	}
	if !sourceEqualSet(spec.Env, fixedSourceEnvironment()) {
		return errors.New("source Docker environment differs from fixed policy")
	}
	if spec.Phase == sourceInit {
		if spec.SnapshotPath != "" || spec.DaemonSnapshotPath != "" {
			return errors.New("source initialization must not receive a source mount")
		}
	} else {
		for _, name := range []string{spec.SnapshotPath, spec.DaemonSnapshotPath} {
			if !sourceCanonicalPath(name) || strings.ContainsAny(name, ",\"") {
				return errors.New("source snapshot host and daemon coordinates must have canonical absolute comma/quote-free mount paths")
			}
		}
	}
	return nil
}

// sourceCommand selects a bounded fixed recipe, never a caller-supplied script.
func sourceCommand(spec sourceDockerSpec) []string {
	if !spec.profile.valid() || spec.Phase != sourceInit && spec.Phase != sourceBuild {
		return nil
	}
	if spec.Phase == sourceInit {
		return []string{"--signal=TERM", "--kill-after=5s", "2m", "/bin/bash", "-euo", "pipefail", "-c", InitScript}
	}
	return []string{"--signal=TERM", "--kill-after=5s", "45m", "/bin/bash", "-euo", "pipefail", "-c", spec.profile.sourceScript(), "lexr-sensors-source-build", strconv.Itoa(spec.Jobs)}
}

// sourceCreateArgs encodes the validated phase's exact isolation and resource
// policy, without permitting an image pull or automatic resource removal.
func sourceCreateArgs(spec sourceDockerSpec) ([]string, error) {
	if err := validateSourceSpec(spec); err != nil {
		return nil, err
	}
	user, pids, memory, cpus := "65532:65532", "512", "4g", strconv.Itoa(spec.Jobs)
	if spec.Phase == sourceInit {
		user, pids, memory, cpus = "0:0", "64", "256m", "2"
	}
	args := []string{"create", "--pull=never", "--name", spec.Name, "--platform", "linux/arm64", "--network", "none", "--read-only", "--user", user, "--workdir", "/", "--entrypoint", "/usr/bin/timeout", "--cap-drop", "ALL"}
	if spec.Phase == sourceInit {
		args = append(args, "--cap-add", "CHOWN")
	}
	args = append(args, "--security-opt", "no-new-privileges", "--pids-limit", pids, "--memory", memory, "--cpus", cpus, "--ipc", "private", "--cgroupns", "private", "--restart", "no", "--tmpfs", "/tmp:rw,nosuid,nodev,size=1g,mode=1777", "--mount", "type=volume,src="+spec.VolumeName+",dst=/work,volume-nocopy")
	if spec.Phase == sourceBuild {
		args = append(args, "--mount", "type=bind,src="+spec.SnapshotPath+",dst=/source,readonly,bind-propagation=rprivate")
	}
	for _, label := range []string{sourceOwnerLabel + "=" + sourceOwnerValue, sourceTokenLabel + "=" + spec.Token, sourceInitRecipeLabel + "=" + InitRecipeSHA256(), sourceRecipeLabel + "=" + spec.profile.sourceRecipeSHA256(), sourcePhaseLabel + "=" + string(spec.Phase)} {
		args = append(args, "--label", label)
	}
	for _, value := range spec.Env {
		args = append(args, "--env", value)
	}
	args = append(args, spec.ImageID)
	return append(args, sourceCommand(spec)...), nil
}

// exactContainerOwnership requires the selected ID, image, name and recipe labels
// before supervision or recovery may act on a container.
func exactContainerOwnership(c sourceContainerInspection, spec sourceDockerSpec, exactID string) error {
	if err := validateSourceSpec(spec); err != nil {
		return err
	}
	if !sourceContainerID.MatchString(c.ID) || exactID != "" && c.ID != exactID || c.Name != "/"+spec.Name || c.Image != spec.ImageID || c.Config.Image != spec.ImageID {
		return errors.New("owned source container identity differs")
	}
	for key, value := range map[string]string{sourceOwnerLabel: sourceOwnerValue, sourceTokenLabel: spec.Token, sourceInitRecipeLabel: InitRecipeSHA256(), sourceRecipeLabel: spec.profile.sourceRecipeSHA256(), sourcePhaseLabel: string(spec.Phase)} {
		if c.Config.Labels[key] != value {
			return errors.New("owned source container recipe/phase labels differ")
		}
	}
	return nil
}

// validateSourceContainerPolicy checks observed isolation, commands and limits
// against compiled phase policy before treating a container as admissible.
func validateSourceContainerPolicy(c sourceContainerInspection, spec sourceDockerSpec) error {
	if err := validateSourceSpec(spec); err != nil {
		return err
	}
	h, config := c.HostConfig, c.Config
	if h.Privileged || !h.ReadonlyRootfs || h.AutoRemove || h.NetworkMode != "none" || len(h.Binds) != 0 || len(h.VolumesFrom) != 0 || len(h.Devices) != 0 || len(h.DeviceRequests) != 0 || len(h.DeviceCgroupRules) != 0 || len(config.Volumes) != 0 || len(config.ExposedPorts) != 0 || !emptySourceHealthcheck(config.Healthcheck) || config.OpenStdin || config.StdinOnce || config.Tty {
		return errors.New("source container isolation differs")
	}
	if h.PidMode != "" || h.IpcMode != "private" || h.CgroupnsMode != "private" || h.UTSMode != "" || h.UsernsMode != "" || len(h.GroupAdd) != 0 || len(h.Links) != 0 || len(h.ExtraHosts) != 0 || len(h.Dns) != 0 || len(h.DnsOptions) != 0 || len(h.DnsSearch) != 0 || len(h.PortBindings) != 0 || h.PublishAllPorts || len(h.Sysctls) != 0 {
		return errors.New("source container namespaces or network additions differ")
	}
	user, pids, memory, cpus := "65532:65532", int64(512), int64(4<<30), int64(spec.Jobs)*1_000_000_000
	var caps []string
	if spec.Phase == sourceInit {
		user, pids, memory, cpus = "0:0", 64, 256<<20, 2_000_000_000
		caps = []string{"CHOWN"}
	}
	if h.PidsLimit != pids || h.Memory != memory || h.NanoCpus != cpus || h.RestartPolicy.Name != "no" || h.RestartPolicy.MaximumRetryCount != 0 || c.RestartCount != 0 {
		return errors.New("source container resource or restart policy differs")
	}
	if !sourceEqualSet(sourceNormalizeCaps(h.CapDrop), []string{"ALL"}) || !sourceEqualSet(sourceNormalizeCaps(h.CapAdd), caps) || len(h.SecurityOpt) != 1 || h.SecurityOpt[0] != "no-new-privileges" && h.SecurityOpt[0] != "no-new-privileges=true" {
		return errors.New("source container capability policy differs")
	}
	if !reflect.DeepEqual(h.Tmpfs, map[string]string{"/tmp": "rw,nosuid,nodev,size=1g,mode=1777"}) {
		return errors.New("source container temporary filesystem differs")
	}
	if config.User != user || config.WorkingDir != "/" || !reflect.DeepEqual(config.Entrypoint, []string{"/usr/bin/timeout"}) || !reflect.DeepEqual(config.Cmd, sourceCommand(spec)) || !sourceEqualSet(config.Env, spec.Env) {
		return errors.New("source container command or environment differs")
	}
	return validateSourceMounts(c, spec)
}

// validateSourceMounts separately verifies requested and effective mount records,
// allowing only the owned workspace and selected read-only source coordinates.
func validateSourceMounts(c sourceContainerInspection, spec sourceDockerSpec) error {
	wanted := 1
	if spec.Phase == sourceBuild {
		wanted++
	}
	if len(c.HostConfig.Mounts) != wanted {
		return errors.New("source container mount request count differs")
	}
	requested := map[string]bool{}
	for _, mount := range c.HostConfig.Mounts {
		if requested[mount.Target] || mount.Consistency != "" || len(mount.TmpfsOptions) != 0 {
			return errors.New("source container mount request is duplicate or has extra options")
		}
		requested[mount.Target] = true
		switch mount.Target {
		case "/work":
			v := mount.VolumeOptions
			if mount.Type != "volume" || mount.Source != spec.VolumeName || mount.ReadOnly || mount.BindOptions != nil || v == nil || !v.NoCopy || len(v.Labels) != 0 || v.Subpath != "" || len(v.DriverConfig) != 0 {
				return errors.New("source workspace must be the exact owned non-copying volume")
			}
		case "/source":
			b := mount.BindOptions
			if spec.Phase != sourceBuild || mount.Type != "bind" || mount.Source != spec.DaemonSnapshotPath || !mount.ReadOnly || mount.VolumeOptions != nil || b == nil || b.Propagation != "rprivate" || b.NonRecursive || b.CreateMountpoint || b.ReadOnlyNonRecursive || b.ReadOnlyForceRecursive {
				return errors.New("source snapshot bind differs from the exact read-only private mount")
			}
		default:
			return errors.New("source container has an unexpected mount destination")
		}
	}
	if !requested["/work"] || spec.Phase == sourceBuild && !requested["/source"] {
		return errors.New("source container is missing a required mount request")
	}
	// Docker versions may omit the tmpfs from effective Mounts while preserving
	// HostConfig.Tmpfs. If present, it must be the same unique fixed destination.
	seen := map[string]bool{}
	for _, mount := range c.Mounts {
		if seen[mount.Destination] {
			return errors.New("source container has duplicate effective mounts")
		}
		seen[mount.Destination] = true
		switch mount.Destination {
		case "/work":
			// Docker may report the exact mode "z" for a local named volume
			// whose requested mode was empty. This does not permit relabeling
			// modes or combinations on source binds, tmpfs, or other volumes.
			if mount.Type != "volume" || mount.Name != spec.VolumeName || mount.Driver != "local" || !mount.RW || !sourceCanonicalPath(mount.Source) || mount.Mode != "" && mount.Mode != "rw" && mount.Mode != "z" || mount.Propagation != "" {
				return errors.New("source workspace effective mount differs")
			}
		case "/source":
			if spec.Phase != sourceBuild || mount.Type != "bind" || mount.Name != "" || mount.Driver != "" || mount.Source != spec.DaemonSnapshotPath || mount.RW || !sourceReadOnlyBindMode(mount.Mode) || mount.Propagation != "rprivate" {
				return errors.New("source snapshot effective mount differs")
			}
		case "/tmp":
			if mount.Type != "tmpfs" || mount.Name != "" || mount.Driver != "" || mount.Source != "" || !mount.RW || mount.Mode != "" && mount.Mode != "rw" || mount.Propagation != "" {
				return errors.New("source temporary effective mount differs")
			}
		default:
			return errors.New("source container has an unexpected effective mount")
		}
	}
	if !seen["/work"] || spec.Phase == sourceBuild && !seen["/source"] {
		return errors.New("source container is missing a required effective mount")
	}
	return nil
}

// sourceCanonicalPath requires a non-root absolute path without control bytes.
func sourceCanonicalPath(name string) bool {
	return filepath.IsAbs(name) && filepath.Clean(name) == name && name != "/" && utf8.ValidString(name) && strings.IndexFunc(name, unicode.IsControl) < 0
}

// sourceReadOnlyBindMode recognises only the admitted read-only/private spellings.
func sourceReadOnlyBindMode(mode string) bool {
	return mode == "" || mode == "ro" || mode == "ro,rprivate" || mode == "rprivate,ro"
}

// sourceNormalizeCaps compares Docker capability names without case/prefix noise.
func sourceNormalizeCaps(values []string) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = strings.TrimPrefix(strings.ToUpper(value), "CAP_")
	}
	return result
}

// sourceEqualSet compares sorted copies, retaining duplicate multiplicity.
func sourceEqualSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// observedSourceTerminal distinguishes verified stopped state from uncertain
// execution; a never-started created container is stopped but not successful.
func observedSourceTerminal(c sourceContainerInspection) (sourceDockerTerminal, error) {
	s := c.State
	terminal := sourceDockerTerminal{ContainerID: c.ID, Status: s.Status, Running: s.Running, PID: s.Pid, ExitCode: s.ExitCode, OOMKilled: s.OOMKilled, Error: s.Error, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt}
	if !sourceContainerID.MatchString(c.ID) || s.Running || s.Paused || s.Restarting || s.Dead || s.Pid != 0 || s.Status != "created" && s.Status != "exited" {
		return terminal, errors.New("source container has no verified stopped state")
	}
	return terminal, nil
}

// successfulSourceTerminal requires a clean exit with valid ordered timestamps,
// no live PID and no daemon or out-of-memory failure.
func successfulSourceTerminal(terminal sourceDockerTerminal) bool {
	start, startErr := time.Parse(time.RFC3339Nano, terminal.StartedAt)
	finish, finishErr := time.Parse(time.RFC3339Nano, terminal.FinishedAt)
	return sourceContainerID.MatchString(terminal.ContainerID) && terminal.Status == "exited" && !terminal.Running && terminal.PID == 0 && terminal.ExitCode == 0 && !terminal.OOMKilled && terminal.Error == "" && startErr == nil && finishErr == nil && !start.IsZero() && !finish.IsZero() && !finish.Before(start)
}
