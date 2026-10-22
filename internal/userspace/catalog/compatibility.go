package catalog

import (
	"errors"
	"regexp"
	"slices"

	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// manifestDigest accepts only the canonical SHA-256 spelling.
var manifestDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// legacyReleases is the closed migration registry; new releases cannot opt out
// of authenticated compatibility by labelling themselves legacy.
var legacyReleases = map[string]string{
	"audio-fullio-v19c":   "sp11-audio-v19c",
	"iptsd-v1":            "sp11-iptsd-v2",
	"imx681-libcamera-v1": "sp11-imx681-libcamera-v1",
}

// validateCompatibilityContract separates exact historical contracts from
// schema v3 manifests without duplicating release-specific compatibility ranges.
func validateCompatibilityContract(add func(string, string, ...any), prefix string, schema int, component Component) {
	if schema == 2 {
		if component.Compatibility != nil || component.LegacyProfile != "" {
			add(prefix, "schema 2 cannot contain schema 3 compatibility fields")
		}
		return
	}
	if component.Compatibility != nil {
		ref := component.Compatibility
		if ref.Size <= 0 || ref.Size > compatibility.MaxBytes || !manifestDigest.MatchString(ref.SHA256) {
			add(prefix+".compatibility", "requires an exact bounded size and lowercase SHA-256")
		}
		if component.LegacyProfile != "" || component.KernelCompatibility != nil {
			add(prefix, "manifest references cannot duplicate legacy compatibility policy")
		}
		if component.Release == nil || !slices.Contains(component.Release.AssetAllowlist, compatibility.Filename) {
			add(prefix+".release", "must include the referenced compatibility manifest asset")
		}
	} else if component.Release != nil {
		if legacyReleases[component.ID] != component.Release.Tag || component.LegacyProfile != component.Release.Tag {
			add(prefix+".compatibility", "new releases require a compatibility manifest; legacy profiles are exact compiled identities")
		}
		if slices.Contains(component.Release.AssetAllowlist, compatibility.Filename) {
			add(prefix+".compatibility", "a compatibility asset requires an independent reference")
		}
	} else if component.LegacyProfile != "" {
		add(prefix+".legacy_profile", "requires its exact historical release")
	}
}

// UnsupportedSchema reports a version mismatch without confusing malformed
// catalogue content with an unavailable diagnostic implementation.
func UnsupportedSchema(err error) bool {
	var validation *ValidationError
	if !errors.As(err, &validation) {
		return false
	}
	for _, issue := range validation.Issues {
		if issue.Field == "schema_version" {
			return true
		}
	}
	return false
}
