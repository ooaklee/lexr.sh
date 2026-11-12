package cli

import (
	"strings"
	"testing"

	sensorbuild "github.com/ooaklee/lexr.sh/internal/sensors/build"
	userspacebuild "github.com/ooaklee/lexr.sh/internal/userspace/build"
)

// completedSensorCLI048Fixture is synthetic delivery evidence, not a producer run.
func completedSensorCLI048Fixture() sensorbuild.Result {
	result := completedSensorCLIFixture()
	result.Plan.Metadata048 = true
	result.Plan.Source.SourceCommit = sensorbuild.SourceCommit048
	result.Plan.SourceRecipeSHA256 = sensorbuild.SourceRecipeSHA256048()
	result.BuildReport.SourceCommit = sensorbuild.SourceCommit048
	result.BuildReport.Providers[0].Version = "0.4.8"
	result.Artifacts, result.BuildReport.Products = nil, nil
	for _, name := range sensorbuild.ArtifactFiles048() {
		artifact := sensorbuild.Artifact{Name: name, Bytes: 1, SHA256: strings.Repeat("a", 64), Mode: 0o644}
		result.Artifacts = append(result.Artifacts, artifact)
		if name != sensorbuild.ReportName {
			result.BuildReport.Products = append(result.BuildReport.Products, artifact)
		}
	}
	return result
}

// TestSensorSourceProfileCLIExplicitRouting leaves the unchanged default request empty.
func TestSensorSourceProfileCLIExplicitRouting(t *testing.T) {
	for _, selected := range []string{"", userspacebuild.SensorSourceLegacy047, userspacebuild.SensorSourceMetadata048} {
		for _, format := range []string{"human", "json"} {
			t.Run(selected+"/"+format, func(t *testing.T) {
				result := completedSensorCLIFixture()
				if selected == userspacebuild.SensorSourceMetadata048 {
					result = completedSensorCLI048Fixture()
				}
				app, output := sensorCLIApplication(t, sensorBuildCatalogFixture)
				fake := &recordingSensorCLIBuilder{result: userspacebuild.Result{Component: userspacebuild.ComponentSensors, Sensors: &result}}
				args := []string{"sensors"}
				if selected != "" {
					args = append(args, "--sensor-source-profile", selected)
				}
				if format == "json" {
					args = append(args, "--json")
				}
				if err := runSensorCLI(t, app, fake, args...); err != nil {
					t.Fatalf("explicit contract delivery failed: %v %s", err, output)
				}
				if len(fake.requests) != 1 || fake.requests[0].SensorSourceProfile != selected {
					t.Fatalf("request selector changed: %+v", fake.requests)
				}
				if format == "human" && !strings.Contains(output.String(), "development source build completed") {
					t.Fatalf("success not reported: %s", output)
				}
			})
		}
	}
}

// TestSensorSourceProfileCLIUnknownAndUnrelatedRejectBeforeCatalog fails closed early.
func TestSensorSourceProfileCLIUnknownAndUnrelatedRejectBeforeCatalog(t *testing.T) {
	for _, selected := range []string{"metadata-049", "METADATA-048", "metadata-048 ", "legacy-047,metadata-048", "", "047"} {
		app, _ := sensorCLIApplication(t, sensorBuildCatalogFixture)
		app.userspaceCatalogPath = "/missing-catalog-must-not-be-read"
		fake := &recordingSensorCLIBuilder{}
		err := runSensorCLI(t, app, fake, "sensors", "--sensor-source-profile", selected)
		if err == nil || !strings.Contains(err.Error(), "sensor-source-profile") || len(fake.requests) != 0 {
			t.Fatalf("unknown selector reached catalogue/workflow: %q %v", selected, err)
		}
	}
	for _, component := range []string{"iptsd", "camera"} {
		for _, selected := range []string{userspacebuild.SensorSourceLegacy047, userspacebuild.SensorSourceMetadata048} {
			app, _ := sensorCLIApplication(t, sensorBuildCatalogFixture)
			app.userspaceCatalogPath = "/missing-catalog-must-not-be-read"
			fake := &recordingSensorCLIBuilder{}
			err := runSensorCLI(t, app, fake, component, "--sensor-source-profile", selected)
			if err == nil || !strings.Contains(err.Error(), "sensor-source-profile") || len(fake.requests) != 0 {
				t.Fatalf("sensor selector reached unrelated workflow: %s %v", component, err)
			}
		}
	}
}

// TestSensorSourceProfileCompletionNeverSelectsFromReturnedEvidence exercises mixed profiles.
func TestSensorSourceProfileCompletionNeverSelectsFromReturnedEvidence(t *testing.T) {
	for _, selected := range []string{"", userspacebuild.SensorSourceLegacy047, userspacebuild.SensorSourceMetadata048} {
		result := completedSensorCLI048Fixture()
		if selected == userspacebuild.SensorSourceMetadata048 {
			result = completedSensorCLIFixture()
		}
		for _, format := range []string{"human", "json"} {
			app, output := sensorCLIApplication(t, sensorBuildCatalogFixture)
			fake := &recordingSensorCLIBuilder{result: userspacebuild.Result{Component: userspacebuild.ComponentSensors, Sensors: &result}}
			args := []string{"sensors"}
			if selected != "" {
				args = append(args, "--sensor-source-profile", selected)
			}
			if format == "json" {
				args = append(args, "--json")
			}
			if err := runSensorCLI(t, app, fake, args...); err == nil || strings.Contains(output.String(), "development source build completed") {
				t.Fatalf("result selected its own contract: %q %v %s", selected, err, output)
			}
		}
	}
}

// TestSensorSourceProfileCompletionBindsEveryIndependentLineageField rejects drift.
func TestSensorSourceProfileCompletionBindsEveryIndependentLineageField(t *testing.T) {
	for _, selected := range []string{userspacebuild.SensorSourceLegacy047, userspacebuild.SensorSourceMetadata048} {
		for _, change := range []struct {
			name string
			edit func(*sensorbuild.Result)
		}{
			{"profile-bit", func(r *sensorbuild.Result) { r.Plan.Metadata048 = !r.Plan.Metadata048 }},
			{"source-commit", func(r *sensorbuild.Result) { r.Plan.Source.SourceCommit = "wrong" }},
			{"init-recipe", func(r *sensorbuild.Result) { r.Plan.InitRecipeSHA256 = "wrong" }},
			{"source-recipe", func(r *sensorbuild.Result) { r.Plan.SourceRecipeSHA256 = "wrong" }},
			{"report-commit", func(r *sensorbuild.Result) { r.BuildReport.SourceCommit = "wrong" }},
			{"report-schema", func(r *sensorbuild.Result) { r.BuildReport.Schema = "wrong" }},
			{"report-jobs", func(r *sensorbuild.Result) { r.BuildReport.Jobs++ }},
			{"provider-version", func(r *sensorbuild.Result) { r.BuildReport.Providers[0].Version = "wrong" }},
			{"provider-missing", func(r *sensorbuild.Result) { r.BuildReport.Providers = nil }},
			{"provider-duplicate", func(r *sensorbuild.Result) {
				r.BuildReport.Providers = append(r.BuildReport.Providers, r.BuildReport.Providers[0])
			}},
			{"roster-name", func(r *sensorbuild.Result) { r.Artifacts[0].Name = "unexpected" }},
			{"roster-duplicate", func(r *sensorbuild.Result) { r.Artifacts[1] = r.Artifacts[0] }},
			{"roster-missing", func(r *sensorbuild.Result) { r.Artifacts = r.Artifacts[1:] }},
			{"report-product", func(r *sensorbuild.Result) { r.BuildReport.Products[0].SHA256 = "wrong" }},
			{"report-product-duplicate", func(r *sensorbuild.Result) { r.BuildReport.Products[1] = r.BuildReport.Products[0] }},
			{"report-product-unknown", func(r *sensorbuild.Result) { r.BuildReport.Products[0] = sensorbuild.Artifact{} }},
		} {
			t.Run(selected+"/"+change.name, func(t *testing.T) {
				result := completedSensorCLIFixture()
				if selected == userspacebuild.SensorSourceMetadata048 {
					result = completedSensorCLI048Fixture()
				}
				change.edit(&result)
				if sensorSourceBuildCompleteForProfile(&result, selected) {
					t.Fatal("mixed or incomplete contract accepted")
				}
			})
		}
	}
}

// TestSensorSourceProfileDryRunAndCatalogCeiling keeps the opt-in build-only.
func TestSensorSourceProfileDryRunAndCatalogCeiling(t *testing.T) {
	plan := completedSensorCLI048Fixture().Plan
	plan.DryRun = true
	result := sensorbuild.Result{Plan: plan}
	app, _ := sensorCLIApplication(t, sensorBuildCatalogFixture)
	fake := &recordingSensorCLIBuilder{result: userspacebuild.Result{Component: userspacebuild.ComponentSensors, Sensors: &result}}
	if err := runSensorCLI(t, app, fake, "sensors", "--sensor-source-profile", userspacebuild.SensorSourceMetadata048, "--dry-run", "--json"); err != nil {
		t.Fatal(err)
	}
	app, _ = sensorCLIApplication(t, "")
	fake = &recordingSensorCLIBuilder{}
	if err := runSensorCLI(t, app, fake, "sensors", "--sensor-source-profile", userspacebuild.SensorSourceMetadata048, "--dry-run"); err == nil || len(fake.requests) != 0 {
		t.Fatal("profile opt-in bypassed the source-only catalogue ceiling")
	}
	command := app.newUserspaceBuildCommandWithWorkflow(fake)
	flag := command.Flags().Lookup("sensor-source-profile")
	if flag == nil || !flag.Hidden || flag.DefValue != userspacebuild.SensorSourceLegacy047 {
		t.Fatal("experimental selector became public or changed its default")
	}
}
