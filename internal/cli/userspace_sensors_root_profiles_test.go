package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	sensorbuild "github.com/ooaklee/lexr.sh/internal/sensors/build"
	sensorsource "github.com/ooaklee/lexr.sh/internal/sensors/source"
	userspacebuild "github.com/ooaklee/lexr.sh/internal/userspace/build"
)

// TestSensorSourceProfileRealRootMetadata048ReadOnlyPlan exercises persistent
// catalogue parsing, normal root preflight and the actual selected producer's
// plan. It creates only test fixtures; no native producer or Docker is invoked.
func TestSensorSourceProfileRealRootMetadata048ReadOnlyPlan(t *testing.T) {
	repository := os.Getenv("LEXR_TEST_SENSOR_METADATA048_OE_ROOT")
	if repository == "" {
		t.Skip("set LEXR_TEST_SENSOR_METADATA048_OE_ROOT for exact-source root-command plan")
	}
	// A root-command plan must not require any executable lookup or a usable
	// Docker endpoint. Neither setting modifies the user's Docker configuration.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
	config := configFixture(t, "version: 1\n")
	catalog := filepath.Join(t.TempDir(), "source-only-catalog.json")
	if err := os.WriteFile(catalog, []byte(sensorBuildCatalogFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, placement := range []string{"global-before-command", "global-after-command", "missing-source-only-override"} {
		t.Run(placement, func(t *testing.T) {
			work, output := t.TempDir(), t.TempDir()
			for _, parent := range []string{work, output} {
				if err := os.Chmod(parent, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"--config", config}
			if placement == "global-before-command" {
				args = append(args, "--userspace-catalog", catalog)
			}
			args = append(args, "userspace", "build", "sensors", "--repository-root", repository, "--sensor-work-parent", work, "--output-dir", output, "--sensor-source-profile", userspacebuild.SensorSourceMetadata048, "--dry-run", "--json")
			if placement == "global-after-command" {
				args = append(args, "--userspace-catalog", catalog)
			}
			stdout, stderr, err := executeCLI(t, args...)
			if stderr != "" {
				t.Fatalf("root-command diagnostics are not clean JSON delivery: %q", stderr)
			}
			decoder := json.NewDecoder(strings.NewReader(stdout))
			var report sensorBuildCLIReport
			if decodeErr := decoder.Decode(&report); decodeErr != nil {
				t.Fatalf("root-command JSON=%s: %v", stdout, decodeErr)
			}
			var extra any
			if decodeErr := decoder.Decode(&extra); decodeErr != io.EOF {
				t.Fatalf("more than one JSON document: %v", decodeErr)
			}
			if placement == "missing-source-only-override" {
				if err == nil || !strings.Contains(err.Error(), "does not support a source build") || report.Error != err.Error() || report.Component != userspacebuild.ComponentSensors || report.Sensors != nil {
					t.Fatalf("metadata selector bypassed the shipped catalogue: %v %s", err, stdout)
				}
			} else {
				if err != nil || report.Error != "" || report.Component != userspacebuild.ComponentSensors || report.Sensors == nil {
					t.Fatalf("persistent source-only override failed: %v %s", err, stdout)
				}
				plan := report.Sensors.Plan
				if !plan.Metadata048 || !plan.DryRun || plan.NoPull || plan.Jobs != 2 || plan.InitRecipeSHA256 != sensorbuild.InitRecipeSHA256() || plan.SourceRecipeSHA256 != sensorbuild.SourceRecipeSHA256048() {
					t.Fatalf("root plan selected a different producer contract: %+v", plan)
				}
				if runtime.GOARCH == "arm64" && (runtime.GOOS == "darwin" || runtime.GOOS == "linux") {
					if !plan.Executable || !plan.SourceAuthenticated || plan.ExecutionBlocker != "" || plan.Source.SourceCommit != sensorbuild.SourceCommit048 || plan.Source.SourceFileCount != sensorsource.Metadata048SourceFileCount || plan.Source.SourceSymlinkCount != 6 {
						t.Fatalf("supported root plan did not authenticate exact048 source: %+v", plan)
					}
				} else if plan.Executable || plan.SourceAuthenticated || plan.ExecutionBlocker == "" || plan.Source.SourceCommit != "" {
					t.Fatalf("unsupported root plan invented source/execution proof: %+v", plan)
				}
				// Run's dry-run branch returns its plan before transport binding,
				// dependency acquisition, resource creation or publication.
				expected := userspacebuild.Result{Component: userspacebuild.ComponentSensors, Sensors: &sensorbuild.Result{Plan: plan}}
				if !reflect.DeepEqual(report.Result, expected) {
					t.Fatalf("dry run returned execution or publication state: %s", stdout)
				}
			}
			for _, parent := range []string{work, output} {
				entries, readErr := os.ReadDir(parent)
				if readErr != nil || len(entries) != 0 {
					t.Fatalf("root dry run/refusal changed %s: %v %v", parent, entries, readErr)
				}
			}
		})
	}
}
