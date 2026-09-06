package sp11

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// ValidateManifestBundle verifies canonical version-bound delivery provenance
// and the portable media location of each embedded runtime Debian package.
func ValidateManifestBundle(bundle kernel.Bundle) error {
	canonical, err := kernel.NewBundle(kernel.BundleOptions{
		Release: bundle.Release, Repository: bundle.Repository,
		RequestedBootImageMode: bundle.RequestedBootImageMode, EffectiveDTBDelivery: bundle.EffectiveDTBDelivery,
		EmbeddedDTBCount: bundle.EmbeddedDTBCount, DTBSelectionProvenance: bundle.DTBSelectionProvenance,
		Packages: bundle.Packages, DeviceTrees: bundle.DeviceTrees,
	})
	if err != nil || !reflect.DeepEqual(bundle, canonical) {
		return errors.Join(errors.New("manifest kernel bundle delivery contract is invalid or non-canonical"), err)
	}
	for _, pkg := range bundle.Packages {
		expected := ""
		if pkg.Role == kernel.RoleImage || pkg.Role == kernel.RoleModules || pkg.Role == kernel.RoleBootSupport {
			expected = "sp11/kernel/" + pkg.Name
		}
		if pkg.Path != expected {
			return fmt.Errorf("manifest package %s has unexpected media path %q", pkg.Name, pkg.Path)
		}
	}
	return nil
}
