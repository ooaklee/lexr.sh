package compatibility

import (
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/ooaklee/lexr.sh/internal/kernel/identity"
)

// componentPattern bounds declarative stable component identifiers.
var componentPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

// releasePattern limits releases to flat portable tags.
var releasePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Validate applies semantic and closed-registry policy to schema v1.
func Validate(m *Manifest) error {
	if m == nil || m.SchemaVersion != 1 || m.Kind != "lexr.userspace-component-compatibility" {
		return errors.New("unsupported component compatibility schema or kind")
	}
	if len(m.ComponentID) > 128 || !componentPattern.MatchString(m.ComponentID) || len(m.Release) > 128 || !releasePattern.MatchString(m.Release) {
		return errors.New("invalid component or release identity")
	}
	if err := uniqueValues(m.Capabilities, []string{"audio", "pen", "camera", "firmware", "networking", "bluetooth", "power", "touchscreen"}); err != nil {
		return fmt.Errorf("capabilities: %w", err)
	}
	if err := validateVersionRange(m.Lexr); err != nil {
		return err
	}
	if len(m.Targets) == 0 || len(m.Targets) > 32 {
		return errors.New("targets must contain 1 to 32 complete rules")
	}
	for _, target := range m.Targets {
		if err := uniqueValues(target.Architectures, []string{"arm64"}); err != nil {
			return fmt.Errorf("architectures: %w", err)
		}
		if err := uniqueValues(target.DeviceProfiles, []string{"surface-pro-11-x1e-oled"}); err != nil {
			return fmt.Errorf("device profiles: %w", err)
		}
		if len(target.OperatingSystems) == 0 || len(target.OperatingSystems) > 16 || len(target.Kernels) == 0 || len(target.Kernels) > 32 {
			return errors.New("target OS or kernel count outside bounds")
		}
		osIDs := map[string]bool{}
		for _, system := range target.OperatingSystems {
			if osIDs[system.ID] {
				return errors.New("duplicate operating system")
			}
			osIDs[system.ID] = true
			if len(system.VersionRanges) == 0 || len(system.VersionRanges) > 32 || system.TestedVersions == nil || len(system.TestedVersions) > 64 {
				return errors.New("invalid OS range or evidence count")
			}
			for _, r := range system.VersionRanges {
				if !osVersion(system.ID, r.MinimumInclusive) || r.MaximumExclusive != "" && (!osVersion(system.ID, r.MaximumExclusive) || compareOS(system.ID, r.MinimumInclusive, r.MaximumExclusive) >= 0) {
					return errors.New("invalid registered OS identity or range")
				}
			}
			seen := map[string]bool{}
			for _, tested := range system.TestedVersions {
				if seen[tested] || !osVersion(system.ID, tested) || !withinOS(system, tested) {
					return errors.New("duplicate, malformed, or out-of-range OS evidence")
				}
				seen[tested] = true
			}
		}
		kernels := map[string]bool{}
		for _, kernel := range target.Kernels {
			key := kernel.PatchLine + "/" + kernel.PlatformFlavour + "/" + kernel.Scope
			if kernels[key] || !identity.ValidDomain(kernel.PatchLine, kernel.PlatformFlavour, kernel.Scope) {
				return errors.New("duplicate or invalid kernel domain")
			}
			kernels[key] = true
			r := kernel.ABIGeneration
			if r.MinimumInclusive < 1 || r.TestedThroughInclusive < r.MinimumInclusive || r.MaximumExclusive < 0 || r.MaximumExclusive > 0 && (r.MaximumExclusive <= r.MinimumInclusive || r.TestedThroughInclusive >= r.MaximumExclusive) {
				return errors.New("invalid kernel generation range")
			}
		}
	}
	return nil
}

// validateVersionRange requires canonical persisted bounds and honest evidence.
func validateVersionRange(r VersionRange) error {
	for _, v := range []string{r.MinimumInclusive, r.TestedThroughInclusive} {
		if _, err := semverParts(v); err != nil {
			return err
		}
	}
	if compareVersion(r.MinimumInclusive, r.TestedThroughInclusive) > 0 {
		return errors.New("Lexr evidence precedes minimum")
	}
	if r.MaximumExclusive != "" {
		if _, err := semverParts(r.MaximumExclusive); err != nil {
			return err
		}
		if compareVersion(r.MinimumInclusive, r.MaximumExclusive) >= 0 || compareVersion(r.TestedThroughInclusive, r.MaximumExclusive) >= 0 {
			return errors.New("invalid Lexr upper bound")
		}
	}
	return nil
}

// uniqueValues enforces nonempty, bounded sets from a compiled registry.
func uniqueValues(values, registry []string) error {
	if len(values) == 0 || len(values) > 32 {
		return errors.New("list must contain 1 to 32 values")
	}
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] || !slices.Contains(registry, value) {
			return fmt.Errorf("duplicate or unregistered value %q", value)
		}
		seen[value] = true
	}
	return nil
}

// withinOS tests the union of hard intervals for an already validated OS ID.
func withinOS(system OperatingSystem, version string) bool {
	for _, r := range system.VersionRanges {
		if compareOS(system.ID, version, r.MinimumInclusive) >= 0 && (r.MaximumExclusive == "" || compareOS(system.ID, version, r.MaximumExclusive) < 0) {
			return true
		}
	}
	return false
}
