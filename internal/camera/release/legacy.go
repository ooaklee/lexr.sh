package release

import (
	"errors"

	camerabuild "github.com/ooaklee/lexr.sh/internal/camera/build"
)

// LegacySchemaVersion identifies pre-manifest local preparations. New release
// preparation always emits SchemaVersion and requires a manifest-aware build.
const LegacySchemaVersion = 1

// validateReleaseSchema refuses mixed or relabelled authority families.
func validateReleaseSchema(manifest Manifest) error {
	switch manifest.SchemaVersion {
	case LegacySchemaVersion:
		if manifest.Compatibility != nil || manifest.Build.Compatibility != nil || manifest.Build.SchemaVersion != camerabuild.LegacySchemaVersion {
			return errors.New("legacy camera release cannot contain manifest-aware build authority")
		}
	case SchemaVersion:
		if manifest.Compatibility == nil || manifest.Build.Compatibility == nil || manifest.Build.SchemaVersion != camerabuild.SchemaVersion {
			return errors.New("camera release requires manifest-aware build authority")
		}
	default:
		return errors.New("unsupported camera release manifest schema")
	}
	return nil
}
