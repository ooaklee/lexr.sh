package builderimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/hostcap"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

var nativeRequirement = hostcap.Requirement{Operation: "native sensor dependency preparation", OperatingSystems: []string{"linux", "darwin"}, Architectures: []string{"arm64"}}
var engineIdentity = regexp.MustCompile(`^[A-Za-z0-9:.-]{16,128}$`)
var serverVersion = regexp.MustCompile(`^[0-9][0-9A-Za-z.+~_-]{0,127}$`)

func (m *Manager) plan(ctx context.Context, r Request) (Plan, error) {
	if m == nil || m.Runner == nil || m.token == nil || m.timeout <= 0 || m.recoveryTimeout <= 0 {
		return Plan{}, errors.New("dependency producer is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	if r.Prepared != nil && (!r.NoPull || r.Prepared.origin != m || r.Prepared.provenance.RecipeSHA256 != RecipeSHA256() || !objectID.MatchString(r.Prepared.ImageID())) {
		return Plan{}, errors.New("prepared handle must come from this exact producer and be reused with NoPull")
	}
	availability := nativeRequirement.Evaluate(m.host)
	p := Plan{DryRun: r.DryRun, NoPull: r.NoPull, Executable: availability.Executable, ExecutionBlocker: availability.ExecutionBlocker, BaseImage: BaseImage, RecipeSHA256: RecipeSHA256(), NetworkPhase: "dependency-acquisition-only; no sources or host mounts", RetainsContainer: true}
	if r.NoPull && r.Prepared == nil {
		p.Executable = false
		p.ExecutionBlocker = "NoPull requires this process's own prepared dependency image; persistent cache authority is not implemented"
	}
	return p, nil
}

// Prepare runs a controlled fresh dependency producer or rechecks its own
// in-process immutable handle. No persistent cache/tag/label-only authority is
// accepted. Failure retains owned containers and never returns a Prepared.
func (m *Manager) Prepare(ctx context.Context, request Request) (result Result, resultErr error) {
	plan, err := m.plan(ctx, request)
	if err != nil {
		return result, err
	}
	result.Plan = plan
	if plan.DryRun {
		return result, nil
	}
	if !plan.Executable {
		return result, errors.New(plan.ExecutionBlocker)
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	engine, version, err := m.engine(ctx)
	if err != nil {
		return result, err
	}
	if request.NoPull {
		p := request.Prepared.provenance
		if p.EngineID != engine {
			return result, errors.New("prepared handle belongs to a different Docker engine")
		}
		image, err := m.image(ctx, p.ImageID)
		if err != nil {
			return result, err
		}
		if err := validatePreparedImage(image, p.ImageID); err != nil {
			return result, err
		}
		result.Prepared = request.Prepared
		result.Provenance = &p
		result.ObservedImageID = p.ImageID
		return result, nil
	}
	if _, _, err := m.command(ctx, 8<<20, "pull", "--platform", "linux/arm64", BaseImage); err != nil {
		return result, fmt.Errorf("acquire pinned dependency base: %w", err)
	}
	base, err := m.image(ctx, BaseImage)
	if err != nil {
		return result, err
	}
	if len(base.Config.Entrypoint) != 0 || base.Config.User != "" && base.Config.User != "0" && base.Config.User != "0:0" {
		return result, errors.New("base image entrypoint/user differs from the compiled preparation policy")
	}
	token, err := m.token()
	if err != nil || !tokenPattern.MatchString(token) {
		return result, errors.New("dependency producer token is invalid")
	}
	name := containerPrefix + token
	result.ContainerName = name
	env, err := dependencyEnvironment(base.Config.Env)
	if err != nil {
		return result, err
	}
	created := false
	stopped := false
	defer func() {
		if resultErr == nil || !created || stopped {
			return
		}
		recovery, done := context.WithTimeout(context.Background(), m.recoveryTimeout)
		defer done()
		ref := result.ContainerID
		if ref == "" {
			ref = name
		}
		c, e := m.container(recovery, ref)
		if e == nil {
			e = ownership(c, name, result.ContainerID, token, base.ID)
		}
		if e == nil {
			result.ContainerID = c.ID
			if c.State.Running || c.State.Paused || c.State.Restarting {
				_, _, stopErr := m.command(recovery, 64<<10, "stop", "--time", "10", c.ID)
				if stopErr != nil {
					result.RecoveryError = stopErr.Error()
				}
				c, e = m.container(recovery, c.ID)
				if e == nil {
					e = ownership(c, name, result.ContainerID, token, base.ID)
				}
			}
			if e == nil {
				t, terminalErr := terminal(c)
				result.Terminal = &t
				e = terminalErr
			}
		}
		if e != nil {
			result.TerminalUnknown = true
			result.RecoveryError = e.Error()
			resultErr = errors.Join(resultErr, errors.New("owned dependency container termination is unverified; evidence retained"))
		}
	}()
	created = true // A failed client can still have created the owned name remotely.
	output, _, err := m.command(ctx, 4096, createArgs(name, token, base.ID, env)...)
	if err != nil {
		return result, fmt.Errorf("create dependency container: %w", err)
	}
	result.ContainerID = strings.TrimSpace(string(output))
	if !containerID.MatchString(result.ContainerID) {
		result.ContainerID = ""
		return result, errors.New("create did not return a complete container identity")
	}
	c, err := m.container(ctx, result.ContainerID)
	if err != nil {
		return result, err
	}
	if err := ownership(c, name, result.ContainerID, token, base.ID); err != nil {
		return result, err
	}
	if err := validateContainerPolicy(c, env); err != nil {
		return result, err
	}
	if c.State.Status != "created" || c.State.Running || c.State.Pid != 0 {
		return result, errors.New("fresh dependency container is not in its expected created state")
	}
	stdout, stderr, runErr := m.command(ctx, 8<<20, "start", "--attach", result.ContainerID)
	result.DependencyStdout, result.DependencyStderr = string(stdout), string(stderr)
	// A fresh context observes the daemon, even when the Docker client timed out.
	observation, done := context.WithTimeout(context.Background(), m.recoveryTimeout)
	c, inspectErr := m.container(observation, result.ContainerID)
	done()
	if inspectErr != nil {
		return result, errors.Join(runErr, inspectErr)
	}
	if err := ownership(c, name, result.ContainerID, token, base.ID); err != nil {
		return result, err
	}
	if err := validateContainerPolicy(c, env); err != nil {
		return result, err
	}
	t, terminalErr := terminal(c)
	result.Terminal = &t
	if terminalErr == nil {
		stopped = true
	}
	if err := errors.Join(runErr, ctx.Err(), terminalErr); err != nil {
		return result, err
	}
	if !successful(t) {
		return result, errors.New("dependency container did not exit successfully; image withheld")
	}
	packages, err := m.inventory(ctx, c.ID, PackageInventoryPath)
	if err != nil {
		return result, err
	}
	tools, err := m.inventory(ctx, c.ID, ToolchainInventoryPath)
	if err != nil {
		return result, err
	}
	if err := validateInventories(packages, tools); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// Only an already exited container can reach commit. Keep Docker's default
	// pause policy: the old --pause flag emits a stdout warning on Docker 29,
	// which must not be mistaken for part of an immutable image identity.
	// An empty ENTRYPOINT change can retain the container's timeout entrypoint.
	// Replace it explicitly with a fail-closed default; a future offline source
	// runner must supply its own reviewed command and entrypoint unconditionally.
	output, _, err = m.command(ctx, 4096, "commit", "--change", `ENTRYPOINT ["/bin/false"]`, "--change", `CMD ["lexr-explicit-offline-runner-required"]`, "--change", "USER 65532:65532", "--change", "WORKDIR /", c.ID)
	if err != nil {
		return result, fmt.Errorf("commit verified dependency container: %w", err)
	}
	result.ObservedImageID = strings.TrimSpace(string(output))
	if !objectID.MatchString(result.ObservedImageID) {
		return result, errors.New("commit did not return a complete immutable image identity")
	}
	image, err := m.image(ctx, result.ObservedImageID)
	if err != nil {
		return result, err
	}
	if err := validatePreparedImage(image, result.ObservedImageID); err != nil {
		return result, err
	}
	final, err := m.container(ctx, c.ID)
	if err != nil {
		return result, err
	}
	if err := ownership(final, name, c.ID, token, base.ID); err != nil {
		return result, err
	}
	end, err := terminal(final)
	if err != nil || end != t || final.RestartCount != 0 {
		return result, errors.New("dependency terminal state changed during image commit")
	}
	p := Provenance{EngineID: engine, ServerVersion: version, BaseImage: BaseImage, BaseImageID: base.ID, ImageID: image.ID, RecipeSHA256: RecipeSHA256(), PackageInventory: packages, PackageInventorySHA256: digest(packages), ToolchainInventory: tools, ToolchainInventorySHA256: digest(tools), Terminal: t}
	result.Prepared = &Prepared{origin: m, provenance: p}
	result.Provenance = &p
	return result, nil
}

func (m *Manager) engine(ctx context.Context) (string, string, error) {
	b, _, err := m.command(ctx, 4096, "info", "--format", "{{.ID}}|{{.OSType}}|{{.Architecture}}|{{.ServerVersion}}")
	if err != nil {
		return "", "", err
	}
	fields := strings.Split(strings.TrimSpace(string(b)), "|")
	if len(fields) != 4 || !engineIdentity.MatchString(fields[0]) || fields[1] != "linux" || (fields[2] != "arm64" && fields[2] != "aarch64") || !serverVersion.MatchString(fields[3]) {
		return "", "", errors.New("Docker engine must have an identity and run native Linux ARM64")
	}
	return fields[0], fields[3], nil
}
func (m *Manager) image(ctx context.Context, ref string) (imageInspection, error) {
	var image imageInspection
	b, _, err := m.command(ctx, 256<<10, "image", "inspect", "--format", "{{json .}}", ref)
	if err != nil {
		return image, err
	}
	if err := decodeObject(b, &image); err != nil {
		return image, err
	}
	return image, validateImage(image)
}
func (m *Manager) container(ctx context.Context, ref string) (containerInspection, error) {
	var c containerInspection
	b, _, err := m.command(ctx, 256<<10, "container", "inspect", "--format", "{{json .}}", ref)
	if err != nil {
		return c, err
	}
	return c, decodeObject(b, &c)
}
func (m *Manager) inventory(ctx context.Context, cid, name string) (string, error) {
	b, _, err := m.command(ctx, (1<<20)+16384, "cp", cid+":"+name, "-")
	if err != nil {
		return "", err
	}
	return inventoryMember(b, name)
}

func validatePreparedImage(image imageInspection, id string) error {
	if image.ID != id || image.Config.User != "65532:65532" || image.Config.WorkingDir != "/" || !equalSet(image.Config.Entrypoint, []string{"/bin/false"}) || !equalSet(image.Config.Cmd, []string{"lexr-explicit-offline-runner-required"}) || image.Config.Labels[ownerLabel] != ownerValue || image.Config.Labels[recipeLabel] != RecipeSHA256() {
		return errors.New("committed dependency image configuration differs")
	}
	return nil
}
func digest(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }

func dependencyEnvironment(base []string) ([]string, error) {
	seen := map[string]bool{}
	env := make([]string, 0, len(base)+6)
	for _, value := range base {
		key, _, ok := strings.Cut(value, "=")
		if !ok || key == "" || seen[key] || strings.ContainsAny(value, "\x00\n\r") || len(value) > 4096 {
			return nil, errors.New("base environment is malformed")
		}
		seen[key] = true
		switch key {
		case "DEBIAN_FRONTEND", "LANG", "LC_ALL", "TZ", "PYTHONDONTWRITEBYTECODE":
		default:
			env = append(env, value)
		}
	}
	env = append(env, "DEBIAN_FRONTEND=noninteractive", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "TZ=UTC", "PYTHONDONTWRITEBYTECODE=1")
	return env, nil
}

func createArgs(name, token, image string, env []string) []string {
	args := []string{"create", "--pull=never", "--name", name, "--platform", "linux/arm64", "--network", "bridge", "--user", "0:0", "--workdir", "/", "--entrypoint", "/usr/bin/timeout", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER", "--cap-add", "SETUID", "--cap-add", "SETGID", "--security-opt", "no-new-privileges", "--pids-limit", "256", "--memory", "4g", "--cpus", "2", "--ipc", "private", "--restart", "no", "--tmpfs", "/tmp:rw,nosuid,nodev,size=1g,mode=1777", "--tmpfs", "/run:rw,nosuid,nodev,size=64m,mode=0755", "--label", ownerLabel + "=" + ownerValue, "--label", recipeLabel + "=" + RecipeSHA256(), "--label", tokenLabel + "=" + token}
	for _, value := range env {
		args = append(args, "--env", value)
	}
	return append(args, image, "--signal=TERM", "--kill-after=5s", "18m", "/bin/bash", "-euo", "pipefail", "-c", DependencyScript)
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.exceeded = true
		if b.cancel != nil {
			b.cancel()
		}
		return 0, errors.New("Docker output exceeds its fixed bound")
	}
	return b.buffer.Write(p)
}

// Do not embed bytes.Buffer: its promoted WriteString/ReadFrom methods would
// bypass Write's bound through io.WriteString/io.Copy interface shortcuts.
func (b *boundedBuffer) Len() int       { return b.buffer.Len() }
func (b *boundedBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *boundedBuffer) String() string { return b.buffer.String() }
func (m *Manager) command(ctx context.Context, limit int, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &boundedBuffer{limit: limit, cancel: cancel}
	stderr := &boundedBuffer{limit: limit, cancel: cancel}
	err := m.Runner.Run(ctx, platform.Command{Name: "docker", Args: args, Stdout: stdout, Stderr: stderr, Stdin: io.Reader(nil)})
	if stdout.exceeded || stderr.exceeded {
		err = errors.Join(err, errors.New("Docker output exceeded its fixed bound"))
	}
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, stderr.String())
	}
	return stdout.Bytes(), stderr.Bytes(), err
}
