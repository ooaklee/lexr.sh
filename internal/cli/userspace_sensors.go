package cli

import (
	"fmt"

	sensorbuild "github.com/ooaklee/lexr.sh/internal/sensors/build"
	userspacebuild "github.com/ooaklee/lexr.sh/internal/userspace/build"
	userspacecatalog "github.com/ooaklee/lexr.sh/internal/userspace/catalog"
	userspacemanager "github.com/ooaklee/lexr.sh/internal/userspace/manager"
)

// A catalogue override can select a compiled build, never provide source,
// execution or installation authority. Check the exact source-only contract
// rather than treating a matching component ID as sufficient.
func validateSensorBuildCatalogComponent(component userspacecatalog.Component) error {
	if component.ID != userspacemanager.SensorsComponent ||
		component.Capability != userspacecatalog.CapabilitySensors ||
		component.Level != userspacecatalog.LevelExperimental ||
		component.CompatibilityEvidence != userspacecatalog.EvidenceUnqualifiedSource ||
		component.SupportActions != (userspacecatalog.SupportActions{Build: true}) ||
		component.Release != nil || component.KernelCompatibility != nil {
		return fmt.Errorf("native sensors require the experimental, unqualified_source, build-only catalogue contract")
	}
	return nil
}

// sensorSourceBuildComplete requires publication and clean observed build phases.
func sensorSourceBuildComplete(result *sensorbuild.Result) bool {
	return sensorSourceBuildCompleteForProfile(result, userspacebuild.SensorSourceLegacy047)
}

// sensorSourceBuildMatchesRequest never takes its expected profile from a result.
func sensorSourceBuildMatchesRequest(result *sensorbuild.Result, request userspacebuild.Request) bool {
	if request.SensorSourceProfile != "" && request.SensorSourceProfile != userspacebuild.SensorSourceLegacy047 && request.SensorSourceProfile != userspacebuild.SensorSourceMetadata048 {
		return false
	}
	if result == nil || result.Plan.DryRun != request.DryRun || result.Plan.Metadata048 != (request.SensorSourceProfile == userspacebuild.SensorSourceMetadata048) {
		return false
	}
	if !request.DryRun {
		return sensorSourceBuildCompleteForProfile(result, request.SensorSourceProfile)
	}
	commit := sensorbuild.SourceCommit
	if request.SensorSourceProfile == userspacebuild.SensorSourceMetadata048 {
		commit = sensorbuild.SourceCommit048
	}
	return !result.Plan.SourceAuthenticated || result.Plan.Source.SourceCommit == commit
}

// sensorSourceBuildCompleteForProfile binds the request-selected contract to
// plan, artifact names and in-band report before accepting terminal publication.
func sensorSourceBuildCompleteForProfile(result *sensorbuild.Result, selected string) bool {
	commit, version, recipe, names := sensorbuild.SourceCommit, "0.4.7", sensorbuild.SourceRecipeSHA256(), sensorbuild.ArtifactFiles()
	metadata048 := selected == userspacebuild.SensorSourceMetadata048
	if metadata048 {
		commit, version, recipe, names = sensorbuild.SourceCommit048, "0.4.8", sensorbuild.SourceRecipeSHA256048(), sensorbuild.ArtifactFiles048()
	} else if selected != "" && selected != userspacebuild.SensorSourceLegacy047 {
		return false
	}
	if result == nil || !result.Publication.Published || !result.Publication.Durable ||
		!result.Publication.LocationVerified || result.BuildReport == nil ||
		len(result.Artifacts) != len(names) || result.Plan.Metadata048 != metadata048 ||
		!result.Plan.SourceAuthenticated || !result.Plan.Executable || result.Plan.DryRun ||
		result.Plan.Jobs < 1 || result.Plan.Jobs > 8 ||
		result.Plan.Source.SourceCommit != commit || result.Plan.InitRecipeSHA256 != sensorbuild.InitRecipeSHA256() || result.Plan.SourceRecipeSHA256 != recipe ||
		result.BuildReport.Schema != "lexr-sensors-source-build-v1" || result.BuildReport.SourceCommit != commit || result.BuildReport.Jobs != result.Plan.Jobs {
		return false
	}
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	artifacts := make(map[string]sensorbuild.Artifact, len(names))
	for _, artifact := range result.Artifacts {
		if !wanted[artifact.Name] {
			return false
		}
		if _, duplicate := artifacts[artifact.Name]; duplicate {
			return false
		}
		artifacts[artifact.Name] = artifact
	}
	if len(result.BuildReport.Products) != len(names)-1 {
		return false
	}
	products := make(map[string]bool, len(names)-1)
	for _, product := range result.BuildReport.Products {
		if !wanted[product.Name] || product.Name == sensorbuild.ReportName || products[product.Name] || artifacts[product.Name] != product {
			return false
		}
		products[product.Name] = true
	}
	sscProviders := 0
	for _, provider := range result.BuildReport.Providers {
		if provider.Name == "libssc" {
			sscProviders++
			if provider.Version != version {
				return false
			}
		}
	}
	if sscProviders != 1 {
		return false
	}
	dependency, terminal := result.Dependencies, result.Dependencies.Terminal
	if dependency.TerminalUnknown || dependency.RecoveryError != "" || dependency.ContainerID == "" || terminal == nil ||
		terminal.ContainerID != dependency.ContainerID || terminal.Status != "exited" || terminal.Running || terminal.PID != 0 || terminal.ExitCode != 0 || terminal.OOMKilled || terminal.Error != "" {
		return false
	}
	for _, phase := range []sensorbuild.PhaseResult{result.Init, result.Source} {
		terminal := phase.Terminal
		if phase.TerminalUnknown || phase.RecoveryError != "" || phase.ContainerID == "" || terminal == nil ||
			terminal.ContainerID != phase.ContainerID || terminal.Status != "exited" || terminal.Running || terminal.PID != 0 || terminal.ExitCode != 0 || terminal.OOMKilled || terminal.Error != "" {
			return false
		}
	}
	return true
}

// writeSensorSourceBuildResult distinguishes a plan, a retained partial and a
// completed development build. Historical terminal observations do not resolve
// a later TerminalUnknown. None of these states qualifies physical sensors.
func (a *application) writeSensorSourceBuildResult(result *sensorbuild.Result, buildErr error) error {
	return a.writeSensorSourceBuildResultForProfile(result, buildErr, userspacebuild.SensorSourceLegacy047)
}

// writeSensorSourceBuildResultForProfile receives its expected profile from the
// request routing; source or returned report fields cannot opt into a contract.
func (a *application) writeSensorSourceBuildResultForProfile(result *sensorbuild.Result, buildErr error, selected string) error {
	if result == nil {
		if buildErr != nil {
			return nil // Preserve the producer error, not a secondary render error.
		}
		return fmt.Errorf("sensor build returned no source-build result")
	}
	if result.Plan.DryRun && buildErr == nil {
		if _, err := fmt.Fprintln(a.out, "sensor source-build dry run"); err != nil {
			return err
		}
		if result.Plan.SourceAuthenticated {
			if _, err := fmt.Fprintf(a.out, "source composition: authenticated (%s)\n", result.Plan.Source.SourceCommit); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintln(a.out, "source authentication: not completed"); err != nil {
			return err
		}
		if !result.Plan.Executable {
			if _, err := fmt.Fprintf(a.out, "execution unavailable: %s\n", result.Plan.ExecutionBlocker); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintln(a.out, "controller host supported; Docker readiness and build capacity were not checked"); err != nil {
			return err
		}
		_, err := fmt.Fprintln(a.out, "no Docker contact or outputs created; no installation or hardware qualification")
		return err
	}

	complete := buildErr == nil && sensorSourceBuildCompleteForProfile(result, selected)
	if complete {
		if _, err := fmt.Fprintf(a.out, "sensor development source build completed\noutput: %s\nverified artifacts: %d\nreceipt SHA-256: %s\n",
			result.Publication.Directory, len(result.Artifacts), result.Publication.ReceiptSHA256); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(a.out, "sensor source build incomplete; retained resources have not been deleted"); err != nil {
			return err
		}
		if result.Publication.Published {
			if _, err := fmt.Fprintf(a.out, "publication exists but final validation did not complete: %s\ndurable: %t; location verified: %t\n",
				result.Publication.Directory, result.Publication.Durable, result.Publication.LocationVerified); err != nil {
				return err
			}
		}
	}
	for _, retained := range [][2]string{{"work", result.WorkDirectory}, {"volume", result.VolumeName}, {"transport archive", result.TransportArchive}, {"publication staging", result.Publication.StagingDirectory}} {
		if retained[1] != "" {
			if _, err := fmt.Fprintf(a.out, "%s: %s\n", retained[0], retained[1]); err != nil {
				return err
			}
		}
	}
	dependencyState := "not observed"
	if result.Dependencies.TerminalUnknown {
		dependencyState = "UNKNOWN (historical terminal data is not current proof)"
	} else if result.Dependencies.Terminal != nil {
		dependencyState = fmt.Sprintf("%s; exit %d", result.Dependencies.Terminal.Status, result.Dependencies.Terminal.ExitCode)
	}
	if result.Dependencies.ContainerID != "" || result.Dependencies.ContainerName != "" {
		id := result.Dependencies.ContainerID
		if id == "" {
			id = "ID not observed"
		}
		if _, err := fmt.Fprintf(a.out, "dependencies container: %s; name: %s; terminal: %s\n", id, result.Dependencies.ContainerName, dependencyState); err != nil {
			return err
		}
		if result.Dependencies.RecoveryError != "" {
			if _, err := fmt.Fprintf(a.out, "dependencies recovery error: %s\n", result.Dependencies.RecoveryError); err != nil {
				return err
			}
		}
	}
	for _, entry := range []struct {
		label string
		phase sensorbuild.PhaseResult
	}{{"init", result.Init}, {"source", result.Source}} {
		phase := entry.phase
		if phase.ContainerID == "" && phase.Name == "" {
			continue
		}
		id := phase.ContainerID
		if id == "" {
			id = "ID not observed"
		}
		state := "not observed"
		if phase.TerminalUnknown {
			state = "UNKNOWN (historical terminal data is not current proof)"
		} else if phase.Terminal != nil {
			state = fmt.Sprintf("%s; exit %d", phase.Terminal.Status, phase.Terminal.ExitCode)
		}
		if _, err := fmt.Fprintf(a.out, "%s container: %s; name: %s; terminal: %s\n", entry.label, id, phase.Name, state); err != nil {
			return err
		}
		if phase.RecoveryError != "" {
			if _, err := fmt.Fprintf(a.out, "%s recovery error: %s\n", entry.label, phase.RecoveryError); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintln(a.out, "development artifacts only; not an installable release, desktop integration or physical sensor qualification"); err != nil {
		return err
	}
	if !complete && buildErr == nil {
		return fmt.Errorf("sensor source build returned incomplete publication proof")
	}
	return nil
}
