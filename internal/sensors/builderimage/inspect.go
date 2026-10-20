package builderimage

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
)

var objectID = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var containerID = regexp.MustCompile(`^[0-9a-f]{64}$`)
var tokenPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)

const ownerLabel = "io.lexr.sensors.builder"
const recipeLabel = "io.lexr.sensors.recipe"
const tokenLabel = "io.lexr.sensors.token"
const ownerValue = "dependencies-v1"
const containerPrefix = "lexr-sensors-deps-"

type imageConfig struct {
	User       string
	WorkingDir string
	Entrypoint []string
	Cmd        []string
	Env        []string
	Labels     map[string]string
	Volumes    map[string]json.RawMessage
}
type imageInspection struct {
	ID           string `json:"Id"`
	OS           string `json:"Os"`
	Architecture string
	Config       imageConfig
}
type containerInspection struct {
	ID           string `json:"Id"`
	Name         string
	Image        string
	Config       imageConfig
	RestartCount int
	Mounts       []struct{ Type, Source, Destination string }
	HostConfig   struct {
		Privileged                   bool
		ReadonlyRootfs               bool
		AutoRemove                   bool
		NetworkMode                  string
		Binds                        []string
		VolumesFrom                  []string
		Devices                      []json.RawMessage
		DeviceRequests               []json.RawMessage
		CapAdd, CapDrop, SecurityOpt []string
		PidsLimit                    int64
		Memory, NanoCpus             int64
		PidMode, IpcMode             string
		Tmpfs                        map[string]string
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

func decodeObject(data []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(into); err != nil {
		return errors.New("invalid Docker inspection JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("Docker inspection has trailing data")
	}
	return nil
}

func validateImage(image imageInspection) error {
	if !objectID.MatchString(image.ID) || image.OS != "linux" || image.Architecture != "arm64" {
		return errors.New("image must be an immutable Linux ARM64 object")
	}
	if len(image.Config.Volumes) != 0 {
		return errors.New("image must not declare implicit volumes")
	}
	return nil
}

func ownership(c containerInspection, name, id, token, baseID string) error {
	if !containerID.MatchString(c.ID) || id != "" && c.ID != id || c.Name != "/"+name || c.Image != baseID || c.Config.Labels[ownerLabel] != ownerValue || c.Config.Labels[recipeLabel] != RecipeSHA256() || c.Config.Labels[tokenLabel] != token {
		return errors.New("owned dependency container identity differs")
	}
	return nil
}

func validateContainerPolicy(c containerInspection, env []string) error {
	h := c.HostConfig
	if h.Privileged || h.ReadonlyRootfs || h.AutoRemove || h.NetworkMode != "bridge" || len(h.Binds) != 0 || len(h.VolumesFrom) != 0 || len(h.Devices) != 0 || len(h.DeviceRequests) != 0 || len(c.Config.Volumes) != 0 {
		return errors.New("dependency container isolation differs")
	}
	if h.PidsLimit != 256 || h.Memory != 4<<30 || h.NanoCpus != 2_000_000_000 || h.PidMode != "" || h.IpcMode != "private" || h.RestartPolicy.Name != "no" || h.RestartPolicy.MaximumRetryCount != 0 || c.RestartCount != 0 {
		return errors.New("dependency container limits or namespaces differ")
	}
	if !equalSet(normalizeCaps(h.CapAdd), []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID"}) || !equalSet(normalizeCaps(h.CapDrop), []string{"ALL"}) || len(h.SecurityOpt) != 1 || (h.SecurityOpt[0] != "no-new-privileges" && h.SecurityOpt[0] != "no-new-privileges=true") {
		return errors.New("dependency container capability policy differs")
	}
	if !reflect.DeepEqual(h.Tmpfs, dependencyTmpfs()) {
		return errors.New("dependency container temporary mounts differ")
	}
	for _, mount := range c.Mounts {
		if mount.Type != "tmpfs" || (mount.Destination != "/tmp" && mount.Destination != "/run") || mount.Source != "" {
			return errors.New("dependency container has an unexpected mount")
		}
	}
	if c.Config.User != "0:0" || c.Config.WorkingDir != "/" || !reflect.DeepEqual(c.Config.Entrypoint, []string{"/usr/bin/timeout"}) || !reflect.DeepEqual(c.Config.Cmd, []string{"--signal=TERM", "--kill-after=5s", "18m", "/bin/bash", "-euo", "pipefail", "-c", DependencyScript}) || !equalSet(c.Config.Env, env) {
		return errors.New("dependency command or environment differs")
	}
	return nil
}

func normalizeCaps(values []string) []string {
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = strings.TrimPrefix(strings.ToUpper(v), "CAP_")
	}
	return result
}
func equalSet(a, b []string) bool {
	a = slices.Clone(a)
	b = slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
func dependencyTmpfs() map[string]string {
	return map[string]string{"/tmp": "rw,nosuid,nodev,size=1g,mode=1777", "/run": "rw,nosuid,nodev,size=64m,mode=0755"}
}

func terminal(c containerInspection) (Terminal, error) {
	s := c.State
	t := Terminal{ContainerID: c.ID, Status: s.Status, Running: s.Running, PID: s.Pid, ExitCode: s.ExitCode, OOMKilled: s.OOMKilled, Error: s.Error, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt}
	if s.Running || s.Paused || s.Restarting || s.Dead || s.Pid != 0 || (s.Status != "exited" && s.Status != "created") {
		return t, errors.New("dependency container has no verified stopped state")
	}
	return t, nil
}

func successful(t Terminal) bool {
	start, startErr := time.Parse(time.RFC3339Nano, t.StartedAt)
	finish, finishErr := time.Parse(time.RFC3339Nano, t.FinishedAt)
	return t.Status == "exited" && !t.Running && t.PID == 0 && t.ExitCode == 0 && !t.OOMKilled && t.Error == "" && startErr == nil && finishErr == nil && !start.IsZero() && !finish.IsZero() && !finish.Before(start)
}

// Docker cp streams a tar archive. Accept only one bounded regular member with
// the requested basename; never extract a pathname or follow a member link.
func inventoryMember(data []byte, name string) (string, error) {
	r := tar.NewReader(bytes.NewReader(data))
	header, err := r.Next()
	if err != nil || header == nil || header.Name != path.Base(name) || header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > 1<<20 || header.Linkname != "" {
		return "", errors.New("inventory copy must contain exactly the fixed regular member")
	}
	b, err := io.ReadAll(io.LimitReader(r, header.Size+1))
	if err != nil || int64(len(b)) != header.Size {
		return "", errors.New("inventory copy is truncated")
	}
	if _, err := r.Next(); err != io.EOF {
		return "", errors.New("inventory copy contains additional members")
	}
	if bytes.IndexByte(b, 0) >= 0 || !bytes.HasSuffix(b, []byte("\n")) {
		return "", errors.New("inventory text is malformed")
	}
	return string(b), nil
}

var packageLine = regexp.MustCompile(`^[a-z0-9][a-z0-9.+:-]*=[^\x00-\x20\x7f]+$`)

func validateInventories(packages, tools string) error {
	lines := strings.Split(strings.TrimSuffix(packages, "\n"), "\n")
	if len(lines) < 27 || !slices.IsSorted(lines) {
		return errors.New("package inventory is incomplete or unsorted")
	}
	for i, line := range lines {
		if !packageLine.MatchString(line) || i > 0 && line == lines[i-1] {
			return errors.New("package inventory has invalid or duplicate rows")
		}
	}
	const identity = "[build-identity]\n" +
		"lexr-sensor-build:x:65532:65532:Lexr sensor source builder:/work/job/home:/usr/sbin/nologin\n" +
		"lexr-sensor-build:x:65532:\npassword_status=L\n"
	if strings.Count(tools, "[build-identity]\n") != 1 || !strings.Contains(tools, identity) {
		return errors.New("toolchain inventory lacks the exact locked sensor build identity")
	}
	for _, required := range []string{"lexr-sensors-builder-toolchain-v1\n", "os_id=ubuntu\nos_version=26.04\narchitecture=arm64\n", "[gcc-target]\naarch64-linux-gnu\n", "[gcc-version]\n", "[gcc-details]\n", "[linker]\n", "[readelf]\n", "[meson]\n", "[ninja]\n", "[python]\n", "[pkgconf]\n", "[protoc]\n", "[protoc-c]\n", "[development-api-versions:"} {
		if !strings.Contains(tools, required) {
			return fmt.Errorf("toolchain inventory lacks required section %q", required)
		}
	}
	return nil
}
