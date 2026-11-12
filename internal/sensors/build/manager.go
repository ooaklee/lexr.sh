package build

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ooaklee/lexr.sh/internal/hostcap"
	"github.com/ooaklee/lexr.sh/internal/platform"
	"github.com/ooaklee/lexr.sh/internal/sensors/builderimage"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// buildTokenPattern fixes fresh operation tokens to the generated 96-bit form.
var buildTokenPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)

// buildContainerIDPattern requires complete Docker IDs before ownership checks.
var buildContainerIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// buildHostRequirement refuses implicit emulation or unsupported controller hosts.
var buildHostRequirement = hostcap.Requirement{Operation: "native offline sensor source build", OperatingSystems: []string{"linux", "darwin"}, Architectures: []string{"arm64"}}

// plan checks arguments, host support, disjoint parents and authenticated source
// without acquiring dependencies, contacting Docker or creating build outputs.
func (m *Manager) plan(ctx context.Context, r Request) (Plan, error) {
	var p Plan
	if m == nil || !m.profile.valid() || m.runner == nil || m.dependencies == nil || m.bindTransport == nil || m.token == nil || m.timeout <= 0 || m.initTimeout <= 0 || m.sourceTimeout <= 0 || m.recoveryTimeout <= 0 {
		return p, errors.New("sensor source manager is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return p, err
	}
	if r.Jobs == 0 {
		r.Jobs = 2
	}
	if r.Jobs < 1 || r.Jobs > 8 {
		return p, errors.New("sensor jobs must be from 1 through 8")
	}
	for _, name := range []string{r.RepositoryRoot, r.WorkParent, r.OutputParent} {
		if strings.TrimSpace(name) == "" || !utf8.ValidString(name) || strings.ContainsAny(name, ",\"") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return p, errors.New("sensor source/work/output paths are required and must not contain CSV quotes, commas or controls")
		}
	}
	p = Plan{Metadata048: m.profile == producer048, RepositoryRoot: r.RepositoryRoot, WorkParent: r.WorkParent, OutputParent: r.OutputParent, Jobs: r.Jobs, DryRun: r.DryRun, NoPull: r.NoPull, InitRecipeSHA256: InitRecipeSHA256(), SourceRecipeSHA256: m.profile.sourceRecipeSHA256()}
	availability := buildHostRequirement.Evaluate(m.host)
	p.Executable, p.ExecutionBlocker = availability.Executable, availability.ExecutionBlocker
	if !p.Executable {
		return p, nil
	}
	if r.NoPull && m.prepared == nil {
		p.Executable = false
		p.ExecutionBlocker = "NoPull requires this Manager's own prepared dependency image; persistent cache authority is not implemented"
		return p, nil
	}
	canonical := func(name string) (string, error) {
		name, err := filepath.Abs(name)
		if err != nil {
			return "", err
		}
		name, err = filepath.EvalSymlinks(name)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(name)
		if err != nil || !info.IsDir() {
			return "", errors.New("sensor paths must name existing directories")
		}
		if !utf8.ValidString(name) || strings.ContainsAny(name, ",\"") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return "", errors.New("canonical sensor path is not mount-safe")
		}
		return name, nil
	}
	var err error
	if p.RepositoryRoot, err = canonical(r.RepositoryRoot); err != nil {
		return p, err
	}
	if p.WorkParent, err = canonical(r.WorkParent); err != nil {
		return p, err
	}
	if p.OutputParent, err = canonical(r.OutputParent); err != nil {
		return p, err
	}
	if err = inspectWorkParent(p.WorkParent); err != nil {
		return p, err
	}
	for _, pair := range [][2]string{{p.RepositoryRoot, p.WorkParent}, {p.WorkParent, p.RepositoryRoot}, {p.RepositoryRoot, p.OutputParent}, {p.OutputParent, p.RepositoryRoot}, {p.WorkParent, p.OutputParent}, {p.OutputParent, p.WorkParent}} {
		inside, err := buildDirectoryContains(pair[0], pair[1])
		if err != nil {
			return p, err
		}
		if inside {
			return p, errors.New("sensor source, work and output parents must not overlap")
		}
	}
	if err = verifyBuildMountTopology([]string{p.RepositoryRoot, p.WorkParent, p.OutputParent}); err != nil {
		return p, err
	}
	p.Source, err = m.profile.verifySource(p.RepositoryRoot)
	if err != nil {
		return p, err
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	if p.Source.SourceCommit != m.profile.sourceCommit() {
		return p, errors.New("authenticated source and build policy lineage differ")
	}
	p.SourceAuthenticated = true
	return p, nil
}

// Run prepares its own dependencies, supervises two separate owned phases, and
// publishes only fully decoded outputs after all terminal/identity rechecks.
// It never installs, discovers sensors, removes resources or touches a Surface.
func (m *Manager) Run(ctx context.Context, r Request) (result Result, resultErr error) {
	p, err := m.plan(ctx, r)
	if err != nil {
		return result, err
	}
	result.Plan = p
	if p.DryRun {
		return result, nil
	}
	if !p.Executable {
		return result, errors.New(p.ExecutionBlocker)
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	// Resolve and bind local transport before dependency acquisition or resource
	// creation. Later runs and recovery retain it rather than re-resolving an
	// ambient context which could now point at another daemon.
	if err = m.ensureTransport(ctx); err != nil {
		return result, err
	}
	depRequest := builderimage.Request{NoPull: r.NoPull}
	if r.NoPull {
		depRequest.Prepared = m.prepared
	}
	result.Dependencies, err = m.dependencies.Prepare(ctx, depRequest)
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if result.Dependencies.Prepared == nil || result.Dependencies.Provenance == nil {
		return result, errors.New("dependency producer returned no readiness authority")
	}
	m.prepared = result.Dependencies.Prepared
	engine, version, image, env, err := m.readyImage(ctx)
	if err != nil {
		return result, err
	}
	token, err := m.token()
	if err != nil || !buildTokenPattern.MatchString(token) {
		return result, errors.New("sensor build token is invalid")
	}
	result.BuildID = token
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = inspectWorkParent(p.WorkParent); err != nil {
		return result, err
	}
	if err = verifyBuildMountTopology([]string{p.RepositoryRoot, p.WorkParent, p.OutputParent}); err != nil {
		return result, err
	}
	result.WorkDirectory = filepath.Join(p.WorkParent, "sensor-build-"+token)
	if err = os.Mkdir(result.WorkDirectory, 0o700); err != nil {
		return result, fmt.Errorf("exclusively create retained source work directory: %w", err)
	}
	result.SnapshotDirectory = filepath.Join(result.WorkDirectory, "repository")
	if err = ctx.Err(); err != nil {
		return result, err
	}
	snapshot, err := m.profile.stageRepository(p.RepositoryRoot, result.SnapshotDirectory)
	if err != nil {
		return result, err
	}
	defer snapshot.Close()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if !reflect.DeepEqual(snapshot.Composition(), p.Source) {
		return result, errors.New("source composition changed after planning")
	}
	result.VolumeName = sourceVolumePrefix + token
	initSpec := sourceDockerSpec{profile: m.profile, Phase: sourceInit, Name: "lexr-sensors-init-" + token, Token: token, ImageID: image, VolumeName: result.VolumeName, Jobs: p.Jobs, Env: env}
	daemonSnapshotPath, err := m.transport.DaemonSnapshotPath(snapshot.Path())
	if err != nil {
		return result, err
	}
	result.SourceBind = &SourceBindObservation{HostSnapshotPath: snapshot.Path(), DaemonSnapshotPath: daemonSnapshotPath, EngineID: engine, ServerVersion: version}
	sourceSpec := sourceDockerSpec{profile: m.profile, Phase: sourceBuild, Name: "lexr-sensors-build-" + token, Token: token, ImageID: image, VolumeName: result.VolumeName, SnapshotPath: snapshot.Path(), DaemonSnapshotPath: daemonSnapshotPath, Jobs: p.Jobs, Env: env}
	result.Init.Name, result.Source.Name = initSpec.Name, sourceSpec.Name
	// Journal every resource name before a client can create it remotely. Files
	// are fresh and exclusive; partials and diagnostics are retained on failure.
	intent := struct {
		Schema, BuildID, EngineID, ImageID, VolumeName, InitName, SourceName, InitRecipeSHA256, SourceRecipeSHA256 string
		Jobs                                                                                                       int
	}{"lexr-sensors-build-intent-v1", token, engine, image, result.VolumeName, initSpec.Name, sourceSpec.Name, InitRecipeSHA256(), m.profile.sourceRecipeSHA256(), p.Jobs}
	if err = writeBuildRecord(result.WorkDirectory, "00-intent.json", intent); err != nil {
		return result, err
	}
	var initAttempted, sourceAttempted bool
	defer func() {
		if resultErr == nil {
			return
		}
		for _, phase := range []struct {
			attempted bool
			spec      sourceDockerSpec
			result    *PhaseResult
		}{{sourceAttempted, sourceSpec, &result.Source}, {initAttempted, initSpec, &result.Init}} {
			if !phase.attempted {
				continue
			}
			// A later terminal recheck may invalidate an earlier stopped
			// observation. Always re-observe attempted phases on error.
			if err := m.recoverPhase(phase.spec, phase.result); err != nil {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = snapshot.Verify(); err != nil {
		return result, err
	}
	args := volumeCreateArgsForProfile(result.VolumeName, token, m.profile)
	if len(args) == 0 {
		return result, errors.New("invalid owned source volume identity")
	}
	// Docker volume create reuses an existing name. Refuse one explicitly;
	// cryptographic names plus trusted daemon ownership bound the race here.
	existing, _, err := m.command(ctx, 4096, "volume", "ls", "--format", "{{.Name}}", "--filter", "name=^"+result.VolumeName+"$")
	if err != nil {
		return result, err
	}
	if strings.TrimSpace(string(existing)) != "" {
		return result, errors.New("source work volume already exists; it will not be reused")
	}
	if _, _, err = m.command(ctx, 4096, args...); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = m.verifyVolume(ctx, result.VolumeName, token); err != nil {
		return result, err
	}
	result.VolumeVerified = true
	initAttempted = true
	_, err = m.runPhase(ctx, initSpec, &result.Init, m.initTimeout)
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = snapshot.Verify(); err != nil {
		return result, err
	}
	if err = m.verifyVolume(ctx, result.VolumeName, token); err != nil {
		return result, err
	}
	sourceAttempted = true
	_, err = m.runPhase(ctx, sourceSpec, &result.Source, m.sourceTimeout)
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = snapshot.Verify(); err != nil {
		return result, err
	}
	if err = m.verifyCompletedPhase(ctx, initSpec, result.Init.Terminal); err != nil {
		return result, err
	}
	if err = m.verifyCompletedPhase(ctx, sourceSpec, result.Source.Terminal); err != nil {
		return result, err
	}
	copyBytes, _, err := m.command(ctx, MaximumArchiveBytes+(64<<10), "cp", result.Source.ContainerID+":/work/job/"+ArchiveName, "-")
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	result.TransportArchive = filepath.Join(result.WorkDirectory, "source-artifacts.docker-copy.tar")
	if err = writeBuildBytes(result.TransportArchive, copyBytes); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	verified, err := m.profile.decodeCopy(copyBytes, p.Jobs)
	if err != nil {
		return result, fmt.Errorf("source artifacts withheld: %w", err)
	}
	result.Artifacts = verified.Files()
	report := verified.Report()
	result.BuildReport = &report
	if err = snapshot.Verify(); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	// Revalidate through the original producer, never via caller-constructed
	// labels/provenance. Its dependency terminal is historical, not phase proof.
	if _, err = m.dependencies.Prepare(ctx, builderimage.Request{NoPull: true, Prepared: m.prepared}); err != nil {
		return result, err
	}
	currentEngine, currentVersion, currentImage, _, err := m.readyImage(ctx)
	if err != nil {
		return result, err
	}
	if currentEngine != engine || currentVersion != version || currentImage != image {
		return result, errors.New("source engine/image identity changed before publication")
	}
	if err = m.verifyVolume(ctx, result.VolumeName, token); err != nil {
		return result, err
	}
	if err = m.verifyCompletedPhase(ctx, initSpec, result.Init.Terminal); err != nil {
		return result, err
	}
	if err = m.verifyCompletedPhase(ctx, sourceSpec, result.Source.Terminal); err != nil {
		return result, err
	}
	if err = snapshot.Verify(); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	// This public receipt contains no local checkout/cache paths or captured
	// diagnostics. Inventories and exact software outputs are descriptive proof.
	receipt := struct {
		Metadata048        bool                    `json:"metadata_048,omitempty"`
		Schema             string                  `json:"schema"`
		BuildID            string                  `json:"build_id"`
		Scope              string                  `json:"scope"`
		Source             source.Composition      `json:"source"`
		SourceMetadata     []source.File           `json:"source_metadata"`
		Dependencies       builderimage.Provenance `json:"dependencies"`
		EngineID           string                  `json:"engine_id"`
		ServerVersion      string                  `json:"server_version"`
		InitRecipeSHA256   string                  `json:"init_recipe_sha256"`
		SourceRecipeSHA256 string                  `json:"source_recipe_sha256"`
		Init               Terminal                `json:"init"`
		Build              Terminal                `json:"build"`
		Products           []Artifact              `json:"products"`
	}{m.profile == producer048, "lexr-sensors-source-receipt-v1", token, "unqualified source build; not an installable bundle or physical sensor qualification", snapshot.Composition(), snapshot.Composition().Metadata(), m.prepared.Provenance(), engine, version, InitRecipeSHA256(), m.profile.sourceRecipeSHA256(), *result.Init.Terminal, *result.Source.Terminal, verified.Files()}
	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return result, err
	}
	receiptBytes = append(receiptBytes, '\n')
	if err = ctx.Err(); err != nil {
		return result, err
	}
	result.Publication, err = publishArtifactsWithHooksForProfile(p.OutputParent, token, verified, receiptBytes, m.profile, publicationHooks{
		beforeRename: func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := m.transport.Verify(ctx); err != nil {
				return err
			}
			if err := verifyBuildMountTopology([]string{p.RepositoryRoot, p.WorkParent, p.OutputParent}); err != nil {
				return err
			}
			if err := snapshot.Verify(); err != nil {
				return err
			}
			return ctx.Err()
		},
		afterRename: ctx.Err,
	})
	if err != nil {
		return result, err
	}
	if !result.Publication.Published || !result.Publication.Durable || !result.Publication.LocationVerified {
		return result, errors.New("source publication returned incomplete outcome")
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

// writeBuildBytes retains one new private byte stream with a durability barrier.
func writeBuildBytes(name string, data []byte) error {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if n, err := f.Write(data); err != nil || n != len(data) {
		return errors.Join(err, errors.New("retained transport archive write incomplete"))
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// writeBuildRecord writes a new private JSON checkpoint without replacing evidence.
func writeBuildRecord(directory, name string, value any) error {
	f, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = json.NewEncoder(f).Encode(value); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// readyImage rechecks the bound engine and exact image from this owner's opaque
// Prepared handle before allowing either source phase to use it.
func (m *Manager) readyImage(ctx context.Context) (string, string, string, []string, error) {
	if m.prepared == nil {
		return "", "", "", nil, errors.New("source dependency image is not prepared")
	}
	if m.transport == nil {
		return "", "", "", nil, errors.New("source Docker transport is not bound")
	}
	b, _, err := m.command(ctx, 64<<10, "info", "--format", "{{json .}}")
	if err != nil {
		return "", "", "", nil, err
	}
	engine, version, err := validateSourceEngine(b)
	if err != nil {
		return "", "", "", nil, err
	}
	boundEngine, boundVersion := m.transport.EngineIdentity()
	if engine != boundEngine || version != boundVersion {
		return "", "", "", nil, errors.New("source Docker engine differs from its bound local transport")
	}
	if engine != m.prepared.Provenance().EngineID {
		return "", "", "", nil, errors.New("prepared source image belongs to another engine")
	}
	image := m.prepared.ImageID()
	b, _, err = m.command(ctx, 256<<10, "image", "inspect", "--format", "{{json .}}", image)
	if err != nil {
		return "", "", "", nil, err
	}
	var inspected sourceImageInspection
	if err = decodeSourceInspection(b, &inspected); err != nil {
		return "", "", "", nil, err
	}
	if err = validateSourceImage(inspected, image); err != nil {
		return "", "", "", nil, err
	}
	env, err := sourceEnvironment(inspected.Config.Env)
	return engine, version, image, env, err
}

// verifyVolume requires the owned local volume and unchanged recipe labels.
func (m *Manager) verifyVolume(ctx context.Context, name, token string) error {
	b, _, err := m.command(ctx, 256<<10, "volume", "inspect", name)
	if err != nil {
		return err
	}
	v, err := decodeVolumeInspection(b)
	if err != nil {
		return err
	}
	return validateOwnedVolumeForProfile(v, name, token, m.profile)
}

// inspectPhase rechecks the transport and exact container ownership before state
// observations are used by execution, post-build checks or recovery.
func (m *Manager) inspectPhase(ctx context.Context, spec sourceDockerSpec, id string) (sourceContainerInspection, error) {
	var c sourceContainerInspection
	if m.transport == nil {
		return c, errors.New("source Docker transport is not bound")
	}
	if err := m.transport.Verify(ctx); err != nil {
		return c, err
	}
	ref := id
	if ref == "" {
		ref = spec.Name
	}
	b, _, err := m.command(ctx, 256<<10, "container", "inspect", "--format", "{{json .}}", ref)
	if err != nil {
		return c, err
	}
	if err = decodeSourceInspection(b, &c); err != nil {
		return c, err
	}
	return c, exactContainerOwnership(c, spec, id)
}

// runPhase creates and supervises one bounded owned container, then separately
// observes stopped/successful state rather than trusting client exit alone.
func (m *Manager) runPhase(parent context.Context, spec sourceDockerSpec, result *PhaseResult, deadline time.Duration) (stopped bool, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if m.transport == nil {
		return false, errors.New("source Docker transport is not bound")
	}
	if err := m.transport.Verify(ctx); err != nil {
		return false, err
	}
	args, err := sourceCreateArgs(spec)
	if err != nil {
		return false, err
	}
	b, _, err := m.command(ctx, 4096, args...)
	if err != nil {
		return false, err
	}
	result.ContainerID = strings.TrimSpace(string(b))
	if !buildContainerIDPattern.MatchString(result.ContainerID) {
		result.ContainerID = ""
		return false, errors.New("source phase create returned no complete container ID")
	}
	c, err := m.inspectPhase(ctx, spec, result.ContainerID)
	if err != nil {
		return false, err
	}
	if err = validateSourceContainerPolicy(c, spec); err != nil {
		return false, err
	}
	t, err := observedSourceTerminal(c)
	if err != nil || t.Status != "created" {
		return false, errors.New("source phase was not freshly created and stopped")
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	out, diagnostics, runErr := m.command(ctx, 32<<20, "start", "--attach", result.ContainerID)
	result.Stdout, result.Stderr = string(out), string(diagnostics)
	observation, done := context.WithTimeout(context.Background(), m.recoveryTimeout)
	defer done()
	c, err = m.inspectPhase(observation, spec, result.ContainerID)
	if err != nil {
		return false, errors.Join(runErr, err)
	}
	if err = validateSourceContainerPolicy(c, spec); err != nil {
		return false, errors.Join(runErr, err)
	}
	t, terminalErr := observedSourceTerminal(c)
	result.Terminal = &t
	stopped = terminalErr == nil
	if err = errors.Join(runErr, ctx.Err(), terminalErr); err != nil {
		return stopped, err
	}
	if !successfulSourceTerminal(t) {
		return stopped, errors.New("source phase failed; outputs withheld")
	}
	return stopped, nil
}

// recoverPhase stops only a freshly re-identified owned container and retains
// uncertainty when its final stopped state cannot be verified.
func (m *Manager) recoverPhase(spec sourceDockerSpec, result *PhaseResult) error {
	ctx, cancel := context.WithTimeout(context.Background(), m.recoveryTimeout)
	defer cancel()
	c, err := m.inspectPhase(ctx, spec, result.ContainerID)
	if err == nil {
		result.ContainerID = c.ID
		if c.State.Running || c.State.Paused || c.State.Restarting {
			_, _, stopErr := m.command(ctx, 64<<10, "stop", "--time", "10", c.ID)
			if stopErr != nil {
				result.RecoveryError = stopErr.Error()
			}
			c, err = m.inspectPhase(ctx, spec, c.ID)
		}
		if err == nil {
			var t Terminal
			t, err = observedSourceTerminal(c)
			result.Terminal = &t
		}
	}
	result.TerminalUnknown = err != nil
	if err != nil {
		result.RecoveryError = err.Error()
		return fmt.Errorf("owned %s termination is unverified; evidence retained: %w", spec.Phase, err)
	}
	return nil
}

// verifyCompletedPhase requires unchanged policy and exact successful terminal
// observations before completed work can contribute to publication.
func (m *Manager) verifyCompletedPhase(ctx context.Context, spec sourceDockerSpec, want *Terminal) error {
	if want == nil || !successfulSourceTerminal(*want) {
		return errors.New("source phase has no successful terminal proof")
	}
	c, err := m.inspectPhase(ctx, spec, want.ContainerID)
	if err != nil {
		return err
	}
	if err = validateSourceContainerPolicy(c, spec); err != nil {
		return err
	}
	current, err := observedSourceTerminal(c)
	if err != nil {
		return err
	}
	if current != *want || !successfulSourceTerminal(current) {
		return errors.New("source phase terminal state changed")
	}
	return nil
}

// buildBoundedBuffer cancels the owning command instead of truncating excess data.
type buildBoundedBuffer struct {
	data     bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

// Write rejects bytes beyond the fixed stream bound and requests cancellation.
func (b *buildBoundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.data.Len() {
		b.exceeded = true
		b.cancel()
		return 0, errors.New("sensor Docker output exceeds fixed bound")
	}
	return b.data.Write(p)
}

// command bounds stdout/stderr independently and preserves diagnostics with
// cancellation or runner failures from the manager's selected Docker runner.
func (m *Manager) command(parent context.Context, limit int, args ...string) ([]byte, []byte, error) {
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	out := &buildBoundedBuffer{limit: limit, cancel: cancel}
	diagnostics := &buildBoundedBuffer{limit: limit, cancel: cancel}
	err := m.runner.Run(ctx, platform.Command{Name: "docker", Args: args, Stdout: out, Stderr: diagnostics})
	if out.exceeded || diagnostics.exceeded {
		err = errors.Join(err, errors.New("sensor Docker output exceeded its fixed bound"))
	}
	err = errors.Join(err, ctx.Err())
	if err != nil && diagnostics.data.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, diagnostics.data.String())
	}
	return out.data.Bytes(), diagnostics.data.Bytes(), err
}
