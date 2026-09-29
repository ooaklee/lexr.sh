package manager

import (
	"fmt"
	"reflect"

	"github.com/ooaklee/lexr.sh/internal/userspace/catalog"
)

// intersectCatalogue keeps operator catalogues below the embedded authority.
// An override can disable operations or raise a legacy kernel floor, but it
// cannot replace authenticated release identities or grant capabilities.
func intersectCatalogue(selected, compiled *catalog.Catalog) error {
	for _, component := range selected.List() {
		ceiling, found := compiled.Get(component.ID)
		if !found {
			return fmt.Errorf("userspace component %q has no compiled policy", component.ID)
		}
		a, b := component.SupportActions, ceiling.SupportActions
		if a.Status && !b.Status || a.Pull && !b.Pull || a.Build && !b.Build || a.Install && !b.Install ||
			component.Capability != ceiling.Capability || component.Redistribution != ceiling.Redistribution || component.Level != ceiling.Level {
			return fmt.Errorf("userspace component %q widens compiled capability, action, lifecycle, or redistribution policy", component.ID)
		}
		if !reflect.DeepEqual(component.Release, ceiling.Release) || !reflect.DeepEqual(component.Compatibility, ceiling.Compatibility) {
			return fmt.Errorf("userspace component %q replaces compiled release or compatibility authority", component.ID)
		}
		if ceiling.KernelCompatibility != nil {
			if component.KernelCompatibility == nil || component.KernelCompatibility.MinimumSP11Generation < ceiling.KernelCompatibility.MinimumSP11Generation ||
				component.KernelCompatibility.TestedThroughSP11Generation > ceiling.KernelCompatibility.TestedThroughSP11Generation {
				return fmt.Errorf("userspace component %q widens compiled kernel compatibility", component.ID)
			}
		}
	}
	return nil
}
