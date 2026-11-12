package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	lexr "github.com/ooaklee/lexr.sh"
	sensorbuild "github.com/ooaklee/lexr.sh/internal/sensors/build"
	"github.com/ooaklee/lexr.sh/internal/sensors/builderimage"
	sensorsource "github.com/ooaklee/lexr.sh/internal/sensors/source"
	userspacebuild "github.com/ooaklee/lexr.sh/internal/userspace/build"
	userspacecatalog "github.com/ooaklee/lexr.sh/internal/userspace/catalog"
	userspacemanager "github.com/ooaklee/lexr.sh/internal/userspace/manager"
)

// sensorBuildCatalogFixture selects the private build-only capability for CLI tests.
const sensorBuildCatalogFixture = `{"schema_version":2,"description":"Build-only development fixture, not a shipped entry.","components":[{"id":"native-sensors","name":"Native sensor development sources","level":"experimental","capability":"sensors","redistribution":"source-required","support_actions":{"status":false,"pull":false,"build":true,"install":false},"compatibility_evidence":"unqualified_source","notes":["Not hardware qualification."],"remediation":"Leave distribution services unchanged."}]}`

// recordingSensorCLIBuilder retains typed build requests and returns controlled outcomes.
type recordingSensorCLIBuilder struct {
	requests []userspacebuild.Request
	result   userspacebuild.Result
	err      error
}

// BuildWithResult records one CLI request without invoking a native producer.
func (f *recordingSensorCLIBuilder) BuildWithResult(_ context.Context, request userspacebuild.Request) (userspacebuild.Result, error) {
	f.requests = append(f.requests, request)
	return f.result, f.err
}

// sensorCLIApplication creates an isolated application and optional private catalogue.
func sensorCLIApplication(t *testing.T, catalog string) (*application, *bytes.Buffer) {
	t.Helper()
	output := &bytes.Buffer{}
	app := &application{out: output, userspace: userspacemanager.New(userspacecatalog.NewLoader(lexr.UserspaceCatalogFS(), "supported-userspace.json"), nil, nil)}
	if catalog != "" {
		app.userspaceCatalogPath = filepath.Join(t.TempDir(), "catalog.json")
		if err := os.WriteFile(app.userspaceCatalogPath, []byte(catalog), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return app, output
}

// runSensorCLI executes command routing with the selected controlled build workflow.
func runSensorCLI(t *testing.T, app *application, workflow userspaceBuildWorkflow, args ...string) error {
	t.Helper()
	command := app.newUserspaceBuildCommandWithWorkflow(workflow)
	command.SetArgs(args)
	command.SilenceUsage, command.SilenceErrors = true, true
	return command.ExecuteContext(context.Background())
}

// TestSensorBuildCLIRouteAndRequest checks sensor routing and exact typed option forwarding.
func TestSensorBuildCLIRouteAndRequest(t *testing.T) {
	for _, alias := range []string{"sensors", "native-sensors", "SENSORS"} {
		t.Run(alias, func(t *testing.T) {
			app, output := sensorCLIApplication(t, sensorBuildCatalogFixture)
			fake := &recordingSensorCLIBuilder{result: userspacebuild.Result{Component: userspacebuild.ComponentSensors, Sensors: &sensorbuild.Result{Plan: sensorbuild.Plan{DryRun: true, Executable: true, SourceAuthenticated: true, Source: sensorsource.Composition{SourceCommit: sensorbuild.SourceCommit}}}}}
			if err := runSensorCLI(t, app, fake, alias, "--repository-root", "/fixture/oe", "--sensor-work-parent", "/fixture/work", "--output-dir", "/fixture/output", "--jobs", "3", "--dry-run", "--json"); err != nil {
				t.Fatal(err)
			}
			want := userspacebuild.Request{Component: userspacebuild.ComponentSensors, RepositoryRoot: "/fixture/oe", SensorWorkParent: "/fixture/work", OutputDirectory: "/fixture/output", Jobs: 3, DryRun: true}
			if !reflect.DeepEqual(fake.requests, []userspacebuild.Request{want}) {
				t.Fatalf("requests=%+v", fake.requests)
			}
			var decoded userspacebuild.Result
			if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || decoded.Sensors == nil || !decoded.Sensors.Plan.SourceAuthenticated {
				t.Fatalf("result=%s, error=%v", output, err)
			}
		})
	}
}

// TestSensorBuildCLIDefaultCatalogStillRefuses keeps unqualified sensors out of the shipped catalogue.
func TestSensorBuildCLIDefaultCatalogStillRefuses(t *testing.T) {
	app, _ := sensorCLIApplication(t, "")
	fake := &recordingSensorCLIBuilder{}
	err := runSensorCLI(t, app, fake, "sensors", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "does not support a source build") || len(fake.requests) != 0 {
		t.Fatalf("error=%v requests=%+v", err, fake.requests)
	}
	components, err := app.userspace.LoadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	if _, found := components.Get(userspacemanager.SensorsComponent); found {
		t.Fatal("sensor entry was prematurely shipped")
	}
}

// TestSensorBuildCLIPreWorkflowRefusalsReturnOneJSONError checks a single failure envelope before delegation.
func TestSensorBuildCLIPreWorkflowRefusalsReturnOneJSONError(t *testing.T) {
	for _, catalog := range []string{"", "{broken", strings.Replace(sensorBuildCatalogFixture, `"capability":"sensors"`, `"capability":"camera"`, 1)} {
		app, output := sensorCLIApplication(t, catalog)
		fake := &recordingSensorCLIBuilder{}
		if err := runSensorCLI(t, app, fake, "sensors", "--json", "--dry-run"); err == nil || len(fake.requests) != 0 {
			t.Fatalf("catalogue refusal bypassed: %v %+v", err, fake.requests)
		}
		var report sensorBuildCLIReport
		decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
		if err := decoder.Decode(&report); err != nil || report.Error == "" || report.Component != userspacebuild.ComponentSensors || report.Sensors != nil {
			t.Fatalf("early refusal JSON=%s error=%v", output, err)
		}
		if err := decoder.Decode(&report); err != io.EOF {
			t.Fatalf("extra JSON document: %v", err)
		}
	}
}

// TestSensorBuildCLIRejectsCatalogContractDrift refuses expanded actions or invented compatibility.
func TestSensorBuildCLIRejectsCatalogContractDrift(t *testing.T) {
	var document struct {
		Components []userspacecatalog.Component `json:"components"`
	}
	if err := json.Unmarshal([]byte(sensorBuildCatalogFixture), &document); err != nil {
		t.Fatal(err)
	}
	base := document.Components[0]
	for _, test := range []struct {
		name string
		edit func(*userspacecatalog.Component)
	}{
		{"ID", func(c *userspacecatalog.Component) { c.ID = "other" }},
		{"capability", func(c *userspacecatalog.Component) { c.Capability = userspacecatalog.CapabilityCamera }},
		{"level", func(c *userspacecatalog.Component) { c.Level = userspacecatalog.LevelSupported }},
		{"evidence", func(c *userspacecatalog.Component) {
			c.CompatibilityEvidence = userspacecatalog.EvidenceSourceIntegratedPriorValidation
		}},
		{"status", func(c *userspacecatalog.Component) { c.SupportActions.Status = true }},
		{"pull", func(c *userspacecatalog.Component) { c.SupportActions.Pull = true }},
		{"install", func(c *userspacecatalog.Component) { c.SupportActions.Install = true }},
		{"build", func(c *userspacecatalog.Component) { c.SupportActions.Build = false }},
		{"release", func(c *userspacecatalog.Component) { c.Release = &userspacecatalog.Release{} }},
		{"kernel", func(c *userspacecatalog.Component) { c.KernelCompatibility = &userspacecatalog.KernelCompatibility{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.edit(&changed)
			if err := validateSensorBuildCatalogComponent(changed); err == nil {
				t.Fatal("contract drift accepted")
			}
		})
	}
	// This wrong-kind override is schema-valid: the compiled selector must
	// still refuse it before any producer is called.
	wrongKind := strings.Replace(sensorBuildCatalogFixture, `"capability":"sensors"`, `"capability":"camera"`, 1)
	app, _ := sensorCLIApplication(t, wrongKind)
	fake := &recordingSensorCLIBuilder{}
	err := runSensorCLI(t, app, fake, "sensors", "--dry-run")
	if err == nil || len(fake.requests) != 0 {
		t.Fatalf("wrong-kind override reached builder: %v %+v", err, fake.requests)
	}
}

// TestSensorBuildCLIPartialJSONPreservesErrorAndUnknown retains producer failures and terminal uncertainty.
func TestSensorBuildCLIPartialJSONPreservesErrorAndUnknown(t *testing.T) {
	app, output := sensorCLIApplication(t, sensorBuildCatalogFixture)
	failed := errors.New("fixture bound engine lost")
	fake := &recordingSensorCLIBuilder{err: failed, result: userspacebuild.Result{Component: userspacebuild.ComponentSensors, Sensors: &sensorbuild.Result{WorkDirectory: "/fixture/retained", Source: sensorbuild.PhaseResult{ContainerID: strings.Repeat("b", 64), TerminalUnknown: true, RecoveryError: "fixture observation failure"}}}}
	err := runSensorCLI(t, app, fake, "sensors", "--json")
	if !errors.Is(err, failed) {
		t.Fatalf("original error lost: %v", err)
	}
	var report struct {
		userspacebuild.Result
		Error string `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
	if err := decoder.Decode(&report); err != nil || report.Error != failed.Error() || report.Sensors == nil || !report.Sensors.Source.TerminalUnknown || report.Sensors.WorkDirectory != "/fixture/retained" {
		t.Fatalf("partial report=%s error=%v", output, err)
	}
	if err := decoder.Decode(&report); err != io.EOF {
		t.Fatalf("extra JSON output: %v", err)
	}
}

// TestSensorBuildCLIRefusesFalseSuccess rejects empty or inconsistent successful responses.
func TestSensorBuildCLIRefusesFalseSuccess(t *testing.T) {
	for _, jsonFlag := range []bool{false, true} {
		app, output := sensorCLIApplication(t, sensorBuildCatalogFixture)
		fake := &recordingSensorCLIBuilder{result: userspacebuild.Result{Component: userspacebuild.ComponentSensors, Sensors: &sensorbuild.Result{}}}
		args := []string{"sensors"}
		if jsonFlag {
			args = append(args, "--json")
		}
		err := runSensorCLI(t, app, fake, args...)
		if err == nil || !strings.Contains(err.Error(), "incomplete publication proof") || strings.Contains(output.String(), "source build completed") {
			t.Fatalf("false success: error=%v output=%s", err, output)
		}
		if jsonFlag && !strings.Contains(output.String(), `"error":`) {
			t.Fatalf("JSON lost the synthetic incomplete-result error: %s", output)
		}
	}
}

// TestSensorBuildCLIMismatchedResponseDoesNotInventSuccess rejects results for another component.
func TestSensorBuildCLIMismatchedResponseDoesNotInventSuccess(t *testing.T) {
	for _, kind := range []string{"iptsd", "camera", "dry-run"} {
		for _, format := range []string{"human", "json"} {
			t.Run(kind+"/"+format, func(t *testing.T) {
				app, output := sensorCLIApplication(t, sensorBuildCatalogFixture)
				result := userspacebuild.Result{Component: userspacebuild.ComponentIPTSD}
				if kind == "camera" {
					result.Component = userspacebuild.ComponentCamera
				}
				if kind == "dry-run" {
					result.Component = userspacebuild.ComponentSensors
					result.Sensors = &sensorbuild.Result{Plan: sensorbuild.Plan{DryRun: true}, WorkDirectory: "/fixture/unexpected-work"}
				}
				fake := &recordingSensorCLIBuilder{result: result}
				args := []string{"sensors"}
				if format == "json" {
					args = append(args, "--json")
				}
				err := runSensorCLI(t, app, fake, args...)
				if err == nil {
					t.Fatal("mismatched response accepted")
				}
				for _, falseClaim := range []string{"built and validated", "source build completed", "no Docker contact or outputs created"} {
					if strings.Contains(output.String(), falseClaim) {
						t.Fatalf("mismatched response invented %q: %s", falseClaim, output)
					}
				}
				if format == "json" && !strings.Contains(output.String(), `"error":`) {
					t.Fatalf("structured error missing: %s", output)
				}
			})
		}
	}
}

// completedSensorCLIFixture returns complete synthetic publication and lifecycle evidence.
func completedSensorCLIFixture() sensorbuild.Result {
	dependency, init, source := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	result := sensorbuild.Result{
		Plan:         sensorbuild.Plan{Jobs: 2, SourceAuthenticated: true, Executable: true, Source: sensorsource.Composition{SourceCommit: sensorbuild.SourceCommit}, InitRecipeSHA256: sensorbuild.InitRecipeSHA256(), SourceRecipeSHA256: sensorbuild.SourceRecipeSHA256()},
		Publication:  sensorbuild.Publication{Published: true, Durable: true, LocationVerified: true, Directory: "/fixture/published"},
		BuildReport:  &sensorbuild.BuildReport{Schema: "lexr-sensors-source-build-v1", SourceCommit: sensorbuild.SourceCommit, Jobs: 2, Providers: []sensorbuild.Provider{{Name: "libssc", Version: "0.4.7"}}},
		Dependencies: builderimage.Result{ContainerID: dependency, ContainerName: "retained-dependency-name", Terminal: &builderimage.Terminal{ContainerID: dependency, Status: "exited"}},
		Init:         sensorbuild.PhaseResult{ContainerID: init, Name: "retained-init-name", Terminal: &sensorbuild.Terminal{ContainerID: init, Status: "exited"}},
		Source:       sensorbuild.PhaseResult{ContainerID: source, Name: "retained-source-name", Terminal: &sensorbuild.Terminal{ContainerID: source, Status: "exited"}},
	}
	for _, name := range sensorbuild.ArtifactFiles() {
		artifact := sensorbuild.Artifact{Name: name, Bytes: 1, SHA256: strings.Repeat("a", 64), Mode: 0o644}
		result.Artifacts = append(result.Artifacts, artifact)
		if name != sensorbuild.ReportName {
			result.BuildReport.Products = append(result.BuildReport.Products, artifact)
		}
	}
	return result
}

// TestSensorBuildCLICompletionRequiresObservedSuccess checks every required completion observation.
func TestSensorBuildCLICompletionRequiresObservedSuccess(t *testing.T) {
	baseline := completedSensorCLIFixture()
	if !sensorSourceBuildComplete(&baseline) {
		t.Fatal("valid completion fixture rejected")
	}
	for _, test := range []struct {
		name string
		edit func(*sensorbuild.Result)
	}{
		{"dependency-unobserved", func(r *sensorbuild.Result) { r.Dependencies.Terminal = nil }},
		{"init-unobserved", func(r *sensorbuild.Result) { r.Init.Terminal = nil }},
		{"source-unobserved", func(r *sensorbuild.Result) { r.Source.Terminal = nil }},
		{"dependency-failed", func(r *sensorbuild.Result) { r.Dependencies.Terminal.ExitCode = 1 }},
		{"init-running", func(r *sensorbuild.Result) { r.Init.Terminal.Running = true }},
		{"source-oom", func(r *sensorbuild.Result) { r.Source.Terminal.OOMKilled = true }},
		{"source-status", func(r *sensorbuild.Result) { r.Source.Terminal.Status = "created" }},
		{"dependency-pid", func(r *sensorbuild.Result) { r.Dependencies.Terminal.PID = 11 }},
		{"source-error", func(r *sensorbuild.Result) { r.Source.Terminal.Error = "runtime failed" }},
		{"init-no-identity", func(r *sensorbuild.Result) { r.Init.ContainerID = "" }},
		{"source-other-identity", func(r *sensorbuild.Result) { r.Source.Terminal.ContainerID = "different" }},
		{"unauthenticated", func(r *sensorbuild.Result) { r.Plan.SourceAuthenticated = false }},
		{"not-executable", func(r *sensorbuild.Result) { r.Plan.Executable = false }},
		{"different-lineage", func(r *sensorbuild.Result) { r.Plan.Source.SourceCommit = "different" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := completedSensorCLIFixture()
			test.edit(&result)
			if sensorSourceBuildComplete(&result) {
				t.Fatal("incomplete observation accepted")
			}
		})
	}
}

// TestSensorBuildCLIHumanOutcomeTruth distinguishes plans, incomplete outcomes and completed software builds.
func TestSensorBuildCLIHumanOutcomeTruth(t *testing.T) {
	for _, test := range []struct {
		name   string
		result sensorbuild.Result
		err    error
		want   []string
		reject []string
	}{
		{"unsupported-plan", sensorbuild.Result{Plan: sensorbuild.Plan{DryRun: true, ExecutionBlocker: "fixture host unsupported"}}, nil, []string{"source authentication: not completed", "execution unavailable: fixture host unsupported", "no Docker contact"}, []string{"development source build completed", "source composition: authenticated"}},
		{"authenticated-plan", sensorbuild.Result{Plan: sensorbuild.Plan{DryRun: true, Executable: true, SourceAuthenticated: true, Source: sensorsource.Composition{SourceCommit: sensorbuild.SourceCommit}}}, nil, []string{"source composition: authenticated", "Docker readiness and build capacity were not checked"}, []string{"source build completed"}},
		{"lost-after-historical-success", sensorbuild.Result{Source: sensorbuild.PhaseResult{Name: "source", ContainerID: "source-id", TerminalUnknown: true, Terminal: &sensorbuild.Terminal{Status: "exited", ExitCode: 0}}, Dependencies: builderimage.Result{ContainerID: "dependency-id", TerminalUnknown: true, Terminal: &builderimage.Terminal{Status: "exited", ExitCode: 0}}}, errors.New("lost engine"), []string{"source build incomplete", "UNKNOWN (historical terminal data is not current proof)"}, []string{"terminal: exited; exit 0", "source build completed"}},
		{"published-not-durable", sensorbuild.Result{Publication: sensorbuild.Publication{Published: true, Directory: "/fixture/published", LocationVerified: true}}, errors.New("sync failed"), []string{"publication exists but final validation did not complete", "durable: false; location verified: true"}, []string{"source build completed"}},
		{"complete", completedSensorCLIFixture(), nil, []string{"development source build completed", "not an installable release", "physical sensor qualification"}, []string{"source build incomplete"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := &bytes.Buffer{}
			app := &application{out: output}
			if err := app.writeSensorSourceBuildResult(&test.result, test.err); err != nil {
				t.Fatal(err)
			}
			for _, text := range test.want {
				if !strings.Contains(output.String(), text) {
					t.Fatalf("missing %q: %s", text, output)
				}
			}
			for _, text := range test.reject {
				if strings.Contains(output.String(), text) {
					t.Fatalf("false claim %q: %s", text, output)
				}
			}
		})
	}
}

// TestSensorBuildCLINilErrorUnknownNeverSucceeds keeps current uncertainty from inheriting historical success.
func TestSensorBuildCLINilErrorUnknownNeverSucceeds(t *testing.T) {
	for _, stage := range []string{"dependencies", "init", "source"} {
		for _, field := range []string{"unknown", "recovery-error"} {
			for _, format := range []string{"human", "json"} {
				t.Run(stage+"/"+field+"/"+format, func(t *testing.T) {
					result := completedSensorCLIFixture()
					if !sensorSourceBuildComplete(&result) {
						t.Fatal("invalid baseline fixture")
					}
					unknown, recovery := field == "unknown", ""
					if field == "recovery-error" {
						recovery = "fixture recovery failed"
					}
					if stage == "dependencies" {
						result.Dependencies.TerminalUnknown, result.Dependencies.RecoveryError = unknown, recovery
					} else {
						phase := result.Source
						if stage == "init" {
							phase = result.Init
						}
						phase.TerminalUnknown, phase.RecoveryError = unknown, recovery
						if stage == "init" {
							result.Init = phase
						} else {
							result.Source = phase
						}
					}
					app, output := sensorCLIApplication(t, sensorBuildCatalogFixture)
					fake := &recordingSensorCLIBuilder{result: userspacebuild.Result{Component: userspacebuild.ComponentSensors, Sensors: &result}}
					args := []string{"sensors"}
					if format == "json" {
						args = append(args, "--json")
					}
					err := runSensorCLI(t, app, fake, args...)
					if err == nil || strings.Contains(output.String(), "development source build completed") {
						t.Fatalf("uncertain result accepted: %v %s", err, output)
					}
					if format == "human" {
						if !strings.Contains(output.String(), "name: retained-") || !strings.Contains(output.String(), "publication exists but final validation did not complete") {
							t.Fatalf("lost retained partial: %s", output)
						}
						if unknown && !strings.Contains(output.String(), "UNKNOWN (historical terminal data is not current proof)") {
							t.Fatalf("historical terminal hid uncertainty: %s", output)
						}
						if recovery != "" && !strings.Contains(output.String(), recovery) {
							t.Fatalf("recovery error missing: %s", output)
						}
					} else if !strings.Contains(output.String(), `"error":`) {
						t.Fatalf("JSON error missing: %s", output)
					}
				})
			}
		}
	}
}

// TestSensorBuildCLINameOnlyPartialsRemainVisible retains names even before container identities are observed.
func TestSensorBuildCLINameOnlyPartialsRemainVisible(t *testing.T) {
	result := sensorbuild.Result{
		Dependencies: builderimage.Result{ContainerName: "name-only-dependency", TerminalUnknown: true, RecoveryError: "dependency unknown"},
		Init:         sensorbuild.PhaseResult{Name: "name-only-init", TerminalUnknown: true, RecoveryError: "init unknown"},
		Source:       sensorbuild.PhaseResult{Name: "name-only-source", TerminalUnknown: true, RecoveryError: "source unknown"},
	}
	output := &bytes.Buffer{}
	if err := (&application{out: output}).writeSensorSourceBuildResult(&result, errors.New("creation outcome unknown")); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"dependency", "init", "source"} {
		if !strings.Contains(output.String(), "ID not observed; name: name-only-"+stage) || !strings.Contains(output.String(), stage+" unknown") {
			t.Fatalf("lost name-only stage: %s", output)
		}
	}
}

// TestSensorBuildCLIHelpRemainsUndelivered avoids advertising the gated experimental workflow.
func TestSensorBuildCLIHelpRemainsUndelivered(t *testing.T) {
	app, _ := sensorCLIApplication(t, "")
	command := app.newUserspaceBuildCommand()
	output := &bytes.Buffer{}
	command.SetOut(output)
	command.SetArgs([]string{"--help"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "sensors") || strings.Contains(output.String(), "sensor-work-parent") {
		t.Fatalf("workflow advertised before native qualification: %s", output)
	}
}

// TestSensorBuildCLIRealReadOnlyPlanOptIn checks an approved tree without Docker or output creation.
func TestSensorBuildCLIRealReadOnlyPlanOptIn(t *testing.T) {
	root := os.Getenv("LEXR_TEST_SENSOR_OE_ROOT")
	if root == "" {
		t.Skip("set LEXR_TEST_SENSOR_OE_ROOT for read-only exact-source plan")
	}
	// A plan must work even with no executable lookup path and an unusable
	// Docker selector. These are process-local test settings, not Docker config.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	work, output := t.TempDir(), t.TempDir()
	if err := os.Chmod(work, 0o700); err != nil {
		t.Fatal(err)
	}
	app, report := sensorCLIApplication(t, sensorBuildCatalogFixture)
	if err := runSensorCLI(t, app, nil, "sensors", "--repository-root", root, "--sensor-work-parent", work, "--output-dir", output, "--dry-run", "--json"); err != nil {
		t.Fatal(err)
	}
	var result userspacebuild.Result
	if err := json.Unmarshal(report.Bytes(), &result); err != nil || result.Sensors == nil || !result.Sensors.Plan.DryRun {
		t.Fatalf("read-only plan=%s error=%v", report, err)
	}
	if runtime.GOARCH == "arm64" && (runtime.GOOS == "darwin" || runtime.GOOS == "linux") {
		if !result.Sensors.Plan.SourceAuthenticated || !result.Sensors.Plan.Executable {
			t.Fatalf("supported-host plan did not authenticate source: %s", report)
		}
	} else if result.Sensors.Plan.SourceAuthenticated || result.Sensors.Plan.Executable || result.Sensors.Plan.ExecutionBlocker == "" {
		t.Fatalf("unsupported-host plan invented source/execution proof: %s", report)
	}
	for _, parent := range []string{work, output} {
		entries, err := os.ReadDir(parent)
		if err != nil || len(entries) != 0 {
			t.Fatalf("dry run changed parent %s: %v %v", parent, entries, err)
		}
	}
}
