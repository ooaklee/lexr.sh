package manager

import (
	"errors"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	"github.com/ooaklee/lexr.sh/internal/userspace/producer"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/userspace/assessment"
	"github.com/ooaklee/lexr.sh/internal/userspace/catalog"
	userspacestatus "github.com/ooaklee/lexr.sh/internal/userspace/status"
)

// assessStatus reuses the mutation evaluator read-only and offline. Legacy
// diagnostics remain explicit until a reviewed catalogue selects a new release.
func assessStatus(report userspacestatus.Report, components *catalog.Catalog, options userspacestatus.Options) userspacestatus.Report {
	for _, contract := range statusContracts {
		component, found := components.Get(contract.id)
		if !found || component.Release == nil || len(options.Features) > 0 && !slices.Contains(options.Features, contract.feature) {
			continue
		}
		directory := options.BundleDirectory
		if directory == "" {
			directory = filepath.Join(report.Root, "var/lib/lexr/userspace", component.ID)
		}
		reference, release := component.Compatibility, component.Release.Tag
		if reference == nil {
			// Native preparations can be diagnosed before catalogue adoption, using
			// their independently compiled producer pin. Absence preserves legacy status.
			if _, err := os.Lstat(filepath.Join(directory, compatibility.Filename)); errors.Is(err, os.ErrNotExist) {
				continue
			}
			policy, err := producer.PolicyFor(component.ID)
			if err != nil {
				continue
			}
			reference, release = &policy.Manifest, policy.Release
		}
		filtered := report.Checks[:0]
		for _, check := range report.Checks {
			if check.ID != "kernel-compatibility-"+component.ID {
				filtered = append(filtered, check)
			}
		}
		report.Checks = filtered
		check := userspacestatus.Check{ID: "component-compatibility-" + component.ID, Feature: contract.feature, ComponentID: component.ID, SupportLevel: userspacestatus.SupportLevel(component.Level), Required: len(options.Features) > 0 || component.Level == catalog.LevelRequired, State: userspacestatus.StateUnavailable}
		selected := options.CompatibilityTarget
		selected.KernelABI = report.KernelABI
		target, err := assessment.ObserveRoot(report.Root, selected)
		if err != nil {
			check.Detail = err.Error()
		} else {
			record, err := assessment.Evaluate(directory, *reference, component.ID, release, target, false)
			if err != nil {
				check.Detail = err.Error()
			} else {
				check.Compatibility = &record
				check.Detail = strings.Join(record.Decision.Reasons, "; ")
				switch record.Decision.Status {
				case "tested":
					check.State = userspacestatus.StatePass
				case "unverified":
					check.State = userspacestatus.StateWarn
				case "incompatible":
					check.State = userspacestatus.StateFail
				}
			}
		}
		report.Checks = append(report.Checks, check)
	}
	report.Ready = true
	for _, check := range report.Checks {
		if check.Required && (check.State == userspacestatus.StateFail || check.State == userspacestatus.StateUnavailable) {
			report.Ready = false
		}
	}
	return report
}
