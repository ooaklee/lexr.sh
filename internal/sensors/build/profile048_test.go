package build

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/hostcap"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// These are host-only software fixtures. A fake runner simulates Docker;
// authenticated source snapshots and archive decoders remain production code.
func metadata048Controller(t *testing.T) (*Manager, *controllerTestRunner) {
	t.Helper()
	r := newControllerTestRunner(t)
	r.profile = producer048
	return configureControllerTestManager(NewMetadata048(r), r), r
}

// metadata048Request requires an explicit authenticated successor source fixture.
func metadata048Request(t *testing.T) Request {
	t.Helper()
	root := os.Getenv("LEXR_TEST_SENSOR_METADATA048_OE_ROOT")
	if root == "" {
		t.Skip("set LEXR_TEST_SENSOR_METADATA048_OE_ROOT for host-only authenticated 0.4.8 orchestration fixtures")
	}
	if runtime.GOARCH != "arm64" || runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("dependency manager requires an ARM64 Linux/macOS host test binary")
	}
	composition, err := source.Verify048(root)
	if err != nil || composition.SourceCommit != SourceCommit048 || composition.SourceFileCount != source.Metadata048SourceFileCount || composition.SourceSymlinkCount != 6 {
		t.Fatalf("explicit successor source authority differs: %+v, %v", composition, err)
	}
	r := controllerTestParents(t)
	r.RepositoryRoot = root
	return r
}

// metadata048Archive assembles a non-executed archive with authenticated notices.
func metadata048Archive(t *testing.T, repository string) []byte {
	t.Helper()
	if _, err := source.Verify048(repository); err != nil {
		t.Fatal(err)
	}
	files, _ := artifactFixture048(t)
	for target, member := range map[string]string{"COPYING.libqmi": "libqmi/source/COPYING", "COPYING.LIB.libqmi": "libqmi/source/COPYING.LIB", "LICENSE.libssc": "libssc/source/LICENSE", "COPYING.iio-sensor-proxy": "iio-sensor-proxy/source/COPYING"} {
		data, err := os.ReadFile(filepath.Join(repository, "userspace", "sensors", filepath.FromSlash(member)))
		if err != nil {
			t.Fatal(err)
		}
		files[target] = data
	}
	rewriteFixtureReport048(t, files, nil)
	data := wrapFixtureCopy(t, writeFixtureTar(t, fixtureMembers048(files)))
	if _, err := DecodeDockerCopy048(data, 2); err != nil {
		t.Fatalf("production successor decoder refused nonexecuted fixture: %v", err)
	}
	return data
}

// TestProducerConstructorsSelectOnlyTheirCompiledContract rejects unknown profiles.
func TestProducerConstructorsSelectOnlyTheirCompiledContract(t *testing.T) {
	for _, metadata := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "metadata048"}[metadata], func(t *testing.T) {
			r, request := newControllerTestRunner(t), controllerTestParents(t)
			m := New(r)
			if metadata {
				m = NewMetadata048(r)
			}
			m.host = hostcap.Host{GOOS: "windows", GOARCH: "arm64"}
			p, err := m.Plan(context.Background(), request)
			if err != nil || p.Metadata048 != metadata || p.Executable || p.SourceAuthenticated || p.SourceRecipeSHA256 != m.profile.sourceRecipeSHA256() {
				t.Fatalf("constructor plan selection differs: %+v, %v", p, err)
			}
			raw, err := json.Marshal(p)
			if err != nil || bytes.Contains(raw, []byte(`"metadata_048"`)) != metadata {
				t.Fatal("legacy JSON changed or successor selection was omitted")
			}
			controllerTestNoEffects(t, r, request)
		})
	}
	for _, invalid := range []producerProfile{2, 255} {
		r, request := newControllerTestRunner(t), controllerTestParents(t)
		m := configureControllerTestManager(newManager(r, invalid), r)
		if _, err := m.Plan(context.Background(), request); err == nil {
			t.Fatal("unknown profile produced a plan")
		}
		if _, err := m.Run(context.Background(), request); err == nil {
			t.Fatal("unknown profile ran")
		}
		controllerTestNoEffects(t, r, request)
	}
}

// TestProducerProfilesBindSpecVolumeAndRecoveryOwnership refuses crossed policies.
func TestProducerProfilesBindSpecVolumeAndRecoveryOwnership(t *testing.T) {
	for _, profile := range []producerProfile{producer047, producer048} {
		for _, phase := range []sourcePhase{sourceInit, sourceBuild} {
			spec := policyTestSpec(phase)
			spec.profile = profile
			container := policyTestContainer(phase)
			container.Config.Cmd = sourceCommand(spec)
			container.Config.Labels[sourceRecipeLabel] = profile.sourceRecipeSHA256()
			if err := validateSourceContainerPolicy(container, spec); err != nil {
				t.Fatalf("selected profile policy refused: %v", err)
			}
			args, err := sourceCreateArgs(spec)
			if err != nil || !strings.Contains(strings.Join(args, "\x00"), profile.sourceRecipeSHA256()) || phase == sourceBuild && !strings.Contains(strings.Join(args, "\x00"), profile.sourceScript()) {
				t.Fatal("creation omitted compiled recipe binding", err)
			}
			other := producer048
			if profile == producer048 {
				other = producer047
			}
			foreign := clonePolicyContainer(t, container)
			foreign.Config.Labels[sourceRecipeLabel] = other.sourceRecipeSHA256()
			if exactContainerOwnership(foreign, spec, policyTestCID) == nil {
				t.Fatal("cross-profile labels granted supervision/recovery authority")
			}
			foreignSpec := spec
			foreignSpec.profile = other
			foreign.Config.Cmd = sourceCommand(foreignSpec)
			if exactContainerOwnership(foreign, spec, policyTestCID) == nil && validateSourceContainerPolicy(foreign, spec) == nil {
				t.Fatal("coherently relabelled other profile was adopted")
			}
			if phase == sourceBuild {
				foreign = clonePolicyContainer(t, container)
				foreign.Config.Cmd = sourceCommand(foreignSpec)
				if validateSourceContainerPolicy(foreign, spec) == nil {
					t.Fatal("other profile script accepted with matching labels")
				}
			}
		}
		v := volumeTestInspection()
		v.Labels[sourceRecipeLabel] = profile.sourceRecipeSHA256()
		if validateOwnedVolumeForProfile(v, volumeTestName, volumeTestToken, profile) != nil {
			t.Fatal("selected volume profile refused")
		}
		other := producer048
		if profile == producer048 {
			other = producer047
		}
		if validateOwnedVolumeForProfile(v, volumeTestName, volumeTestToken, other) == nil {
			t.Fatal("other profile volume was adopted")
		}
		if profile == producer048 && validateOwnedVolume(v, volumeTestName, volumeTestToken) == nil {
			t.Fatal("legacy wrapper adopted successor volume")
		}
	}
	invalid := producerProfile(255)
	spec := policyTestSpec(sourceBuild)
	spec.profile = invalid
	if a, err := sourceCreateArgs(spec); err == nil || a != nil || sourceCommand(spec) != nil {
		t.Fatal("invalid profile assembled a runnable recipe")
	}
	if volumeCreateArgsForProfile(volumeTestName, volumeTestToken, invalid) != nil || validateOwnedVolumeForProfile(volumeTestInspection(), volumeTestName, volumeTestToken, invalid) == nil {
		t.Fatal("invalid profile assembled/adopted a volume")
	}
}

// metadata048PublicationArtifacts creates sealed, non-executed publication fixtures.
func metadata048PublicationArtifacts() *VerifiedArtifacts {
	v := &VerifiedArtifacts{valid: true, profile: artifact048, files: map[string][]byte{}, links: ArtifactLinks()}
	for _, name := range ArtifactFiles048() {
		data := []byte("nonexecuted publication fixture048: " + name + "\n")
		v.files[name] = data
		v.artifacts = append(v.artifacts, Artifact{Name: name, Bytes: int64(len(data)), SHA256: publicationSHA256(data), Mode: expectedFiles048()[name].mode})
	}
	return v
}

// TestProducer048PublicationRequiresExplicitProfileAndOpaqueSeal checks publication refusals.
func TestProducer048PublicationRequiresExplicitProfileAndOpaqueSeal(t *testing.T) {
	for _, test := range []struct {
		name      string
		profile   producerProfile
		artifacts *VerifiedArtifacts
	}{
		{"legacy-refuses-successor", producer047, metadata048PublicationArtifacts()},
		{"successor-refuses-legacy", producer048, publicationTestArtifacts()},
		{"unknown", producerProfile(2), metadata048PublicationArtifacts()},
		{"nil", producer048, nil},
		{"zero", producer048, &VerifiedArtifacts{}},
		{"forged-in-band-profile", producer048, publicationTestArtifacts()},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := publicationTestParent(t)
			p, err := publishArtifactsWithHooksForProfile(parent, publicationTestID, test.artifacts, []byte(`{"metadata_048":true}`), test.profile, publicationHooks{})
			if err == nil || p.Published || p.StagingDirectory != "" {
				t.Fatal("wrong profile gained publication authority")
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatal("wrong profile wrote output")
			}
		})
	}
	for _, mutation := range []string{"missing", "extra", "wrong-seal", "changed-bytes", "wrong-mode", "duplicate", "missing-link"} {
		t.Run(mutation, func(t *testing.T) {
			v := metadata048PublicationArtifacts()
			switch mutation {
			case "missing":
				v.artifacts = v.artifacts[1:]
			case "extra":
				v.artifacts = append(v.artifacts, Artifact{Name: "extra"})
			case "wrong-seal":
				v.profile = artifactProfile(3)
			case "changed-bytes":
				v.files[v.artifacts[0].Name][0] ^= 1
			case "wrong-mode":
				v.artifacts[0].Mode = 0o777
			case "duplicate":
				v.artifacts[1] = v.artifacts[0]
			case "missing-link":
				v.links = v.links[1:]
			}
			parent := publicationTestParent(t)
			if p, err := publishArtifactsWithHooksForProfile(parent, publicationTestID, v, []byte(`{}`), producer048, publicationHooks{}); err == nil || p.Published || p.StagingDirectory != "" {
				t.Fatal("altered successor publication accepted")
			}
		})
	}
	for _, failure := range []string{"none", "file", "parent"} {
		t.Run("durability-"+failure, func(t *testing.T) {
			parent, hooks := publicationTestParent(t), publicationHooks{}
			if failure == "file" {
				hooks.syncFile = func(*os.File) error { return errors.New("fixture sync failure") }
			}
			if failure == "parent" {
				hooks.syncDirectory = func(f *os.File, phase string) error {
					if phase == "parent" {
						return errors.New("fixture sync failure")
					}
					return f.Sync()
				}
			}
			p, err := publishArtifactsWithHooksForProfile(parent, publicationTestID, metadata048PublicationArtifacts(), []byte(`{"metadata_048":true}`), producer048, hooks)
			if failure == "none" {
				if err != nil || !p.Published || !p.Durable || !p.LocationVerified {
					t.Fatal("successor publication incomplete", p, err)
				}
				if files, err := os.ReadDir(p.Directory); err != nil || len(files) != 22 {
					t.Fatal("successor publication closure differs")
				}
				collision, err := publishArtifactsWithHooksForProfile(parent, publicationTestID, metadata048PublicationArtifacts(), []byte(`{}`), producer048, publicationHooks{})
				if err == nil || collision.Published || collision.StagingDirectory == "" {
					t.Fatal("successor output was replaced or collision partial removed")
				}
			} else if err == nil || p.Durable || failure == "parent" && (!p.Published || !p.LocationVerified) || failure == "file" && (p.Published || p.StagingDirectory == "") {
				t.Fatal("successor durability failure lost retained state", p, err)
			}
		})
	}
}

// TestControllerMetadata048AuthenticatedPublicationOptIn exercises fixture orchestration.
func TestControllerMetadata048AuthenticatedPublicationOptIn(t *testing.T) {
	request := metadata048Request(t)
	m, r := metadata048Controller(t)
	r.copyBytes = metadata048Archive(t, request.RepositoryRoot)
	request.DryRun = true
	plan, err := m.Plan(context.Background(), request)
	if err != nil || !plan.Metadata048 || !plan.SourceAuthenticated || plan.Source.SourceCommit != SourceCommit048 || plan.SourceRecipeSHA256 != SourceRecipeSHA256048() {
		t.Fatalf("successor plan differs: %+v, %v", plan, err)
	}
	if p2, err := m.Plan(context.Background(), request); err != nil || !reflect.DeepEqual(plan, p2) {
		t.Fatal("successor plan is not deterministic")
	}
	result, err := m.Run(context.Background(), request)
	if err != nil || result.BuildID != "" || result.Publication.Published {
		t.Fatal("dry-run executed", err)
	}
	controllerTestNoEffects(t, r, request)
	request.DryRun = false
	result, err = m.Run(context.Background(), request)
	if err != nil || !result.Plan.Metadata048 || !result.VolumeVerified || !result.Publication.Published || !result.Publication.Durable || !result.Publication.LocationVerified || result.BuildReport == nil || result.BuildReport.SourceCommit != SourceCommit048 || len(result.Artifacts) != 18 {
		t.Fatalf("mocked successor chain incomplete: %+v, %v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(result.Publication.Directory, publicationReceiptName))
	if err != nil || bytesDigest(data) != result.Publication.ReceiptSHA256 {
		t.Fatal("successor receipt unavailable", err)
	}
	var receipt struct {
		Metadata048    bool               `json:"metadata_048"`
		Source         source.Composition `json:"source"`
		SourceMetadata []source.File      `json:"source_metadata"`
		Recipe         string             `json:"source_recipe_sha256"`
		Products       []Artifact         `json:"products"`
		Scope          string             `json:"scope"`
	}
	if json.Unmarshal(data, &receipt) != nil || !receipt.Metadata048 || receipt.Source.SourceCommit != SourceCommit048 || !reflect.DeepEqual(receipt.SourceMetadata, source.ApprovedMetadata048()) || receipt.Recipe != SourceRecipeSHA256048() || len(receipt.Products) != 18 || !strings.Contains(receipt.Scope, "not an installable bundle") {
		t.Fatal("successor publication receipt lost fixed lineage/recipe/scope")
	}
	for _, path := range []string{request.RepositoryRoot, request.WorkParent, request.OutputParent} {
		if bytes.Contains(data, []byte(path)) {
			t.Fatal("public receipt contains a host path")
		}
	}
	for _, artifact := range result.Artifacts {
		b, err := os.ReadFile(filepath.Join(result.Publication.Directory, artifact.Name))
		if err != nil || bytesDigest(b) != artifact.SHA256 {
			t.Fatal("published successor artifact differs", artifact.Name)
		}
	}
	for _, c := range r.containers {
		if controllerTestPhase(*c) == "init" || controllerTestPhase(*c) == "build" {
			if c.Config.Labels[sourceRecipeLabel] != SourceRecipeSHA256048() || controllerTestPhase(*c) == "build" && !strings.Contains(strings.Join(c.Config.Cmd, "\x00"), SourceScript048()) {
				t.Fatal("successor used legacy container recipe")
			}
		}
	}
	request.NoPull = true
	second, err := m.Run(context.Background(), request)
	if err != nil || !second.Publication.Durable || second.VolumeName == result.VolumeName || second.SnapshotDirectory == result.SnapshotDirectory || second.BuildID == result.BuildID || controllerTestCount(r, "pull") != 1 || controllerTestCount(r, "commit") != 1 {
		t.Fatal("successor repeat reused build resources or lost its prepared dependency", err)
	}
	if current, err := source.Verify048(request.RepositoryRoot); err != nil || !reflect.DeepEqual(current, plan.Source) {
		t.Fatal("approved successor source changed")
	}
	controllerTestAssertNoDeletion(t, r)
}

// TestControllerMetadata048RefusesMixedSourcesWithoutEffects rejects crossed sources.
func TestControllerMetadata048RefusesMixedSourcesWithoutEffects(t *testing.T) {
	request := metadata048Request(t)
	r := newControllerTestRunner(t)
	if p, err := controllerTestManager(r).Run(context.Background(), request); err == nil || p.Plan.SourceAuthenticated {
		t.Fatal("default producer adopted successor source")
	}
	controllerTestNoEffects(t, r, request)
	legacy := os.Getenv("LEXR_TEST_SENSOR_OE_ROOT")
	if legacy == "" {
		t.Skip("set LEXR_TEST_SENSOR_OE_ROOT for inverse source refusal")
	}
	if _, err := source.Verify(legacy); err != nil {
		t.Fatal(err)
	}
	request.RepositoryRoot = legacy
	m, r := metadata048Controller(t)
	if p, err := m.Run(context.Background(), request); err == nil || p.Plan.SourceAuthenticated {
		t.Fatal("successor producer adopted legacy source")
	}
	controllerTestNoEffects(t, r, request)
}

// TestControllerMetadata048WithholdsAcrossDriftAndUncertainty retains unresolved outcomes.
func TestControllerMetadata048WithholdsAcrossDriftAndUncertainty(t *testing.T) {
	_ = metadata048Request(t)
	for _, kind := range []string{"volume-recipe", "init-recipe", "build-recipe", "build-command", "nonzero", "running-recovered", "lost-recovery-ownership", "legacy-copy", "truncated-copy", "late-volume-recipe"} {
		t.Run(kind, func(t *testing.T) {
			request := metadata048Request(t)
			m, r := metadata048Controller(t)
			r.copyBytes = metadata048Archive(t, request.RepositoryRoot)
			switch kind {
			case "volume-recipe":
				r.mutateVolume = func(v *volumeInspection) { v.Labels[sourceRecipeLabel] = SourceRecipeSHA256() }
			case "init-recipe", "build-recipe":
				wanted := strings.TrimSuffix(kind, "-recipe")
				r.mutateContainer = func(phase string, _ int, c *sourceContainerInspection) {
					if phase == wanted {
						c.Config.Labels[sourceRecipeLabel] = SourceRecipeSHA256()
					}
				}
			case "build-command":
				r.mutateContainer = func(phase string, _ int, c *sourceContainerInspection) {
					if phase == "build" {
						c.Config.Cmd = append([]string(nil), c.Config.Cmd...)
						c.Config.Cmd[7] = SourceScript
					}
				}
			case "nonzero":
				r.onStart = func(phase string, c *sourceContainerInspection) {
					if phase == "build" {
						c.State.ExitCode = 9
					}
				}
			case "running-recovered", "lost-recovery-ownership":
				r.onStart = func(phase string, c *sourceContainerInspection) {
					if phase == "build" {
						c.State.Status, c.State.Running, c.State.Pid, c.State.FinishedAt = "running", true, 123, ""
						if kind == "lost-recovery-ownership" {
							c.Config.Labels[sourceRecipeLabel] = SourceRecipeSHA256()
						}
					}
				}
			case "legacy-copy":
				legacy := os.Getenv("LEXR_TEST_SENSOR_OE_ROOT")
				if legacy == "" {
					t.Skip("legacy source opt-in required for cross-decoder inverse")
				}
				r.copyBytes = controllerTestArchive(t, legacy, 2)
			case "truncated-copy":
				r.copyBytes = r.copyBytes[:100]
			case "late-volume-recipe":
				r.mutateVolume = func(v *volumeInspection) {
					for _, c := range r.containers {
						if controllerTestPhase(*c) == "build" && c.State.Status == "exited" {
							v.Labels[sourceRecipeLabel] = SourceRecipeSHA256()
						}
					}
				}
			}
			result, err := m.Run(context.Background(), request)
			controllerTestWithheld(t, r, result, err)
			if kind == "running-recovered" && (controllerTestCount(r, "stop") != 1 || result.Source.TerminalUnknown || result.Source.Terminal == nil || result.Source.Terminal.ExitCode != 137) {
				t.Fatal("exact successor recovery failed to stop only its owned running phase")
			}
			if kind == "lost-recovery-ownership" && (controllerTestCount(r, "stop") != 0 || !result.Source.TerminalUnknown || result.Source.RecoveryError == "") {
				t.Fatal("recipe drift authorized a stop or erased uncertainty")
			}
		})
	}
}
