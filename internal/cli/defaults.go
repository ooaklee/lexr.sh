package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// commandSettings follows the command names into the YAML namespaces. Keeping
// the translation here avoids duplicating precedence rules in every handler.
func commandSettings(command *cobra.Command, values map[string]any) map[string]any {
	var names []string
	for current := command; current.HasParent(); current = current.Parent() {
		names = append(names, current.Name())
	}
	for index := len(names) - 1; index >= 0; index-- {
		values, _ = values[names[index]].(map[string]any)
	}
	return values
}

// applyConfiguration binds configured values before command preflight. Explicit
// flags always win, including false and empty values. Consent remains per run:
// the schema accepts no YAML key that could confer permission from a file.
func (a *application) applyConfiguration(command *cobra.Command) error {
	if global, ok := a.configurationValues["global"].(map[string]any); ok {
		if err := applyFlagDefaults(command, global); err != nil {
			return err
		}
	}
	return applyFlagDefaults(command, commandSettings(command, a.configurationValues))
}

// applyFlagDefaults uses pflag's own decoders and slice replacement semantics.
// Changed also satisfies required-input checks, but only for ordinary inputs.
func applyFlagDefaults(command *cobra.Command, values map[string]any) error {
	for key, value := range values {
		if value == nil {
			continue
		}
		name := strings.ReplaceAll(key, "_", "-")
		if name == "profile" {
			continue // Hardware selection has one shared resolver.
		}
		flag := command.Flags().Lookup(name)
		if flag == nil || flag.Changed {
			continue
		}
		// Host directory resolvers deliberately treat an empty YAML path as
		// unset, while preserving an explicitly empty command-line flag.
		if value == "" && flag.Annotations["lexr.host-path"] != nil {
			continue
		}
		var err error
		if slice, ok := value.([]string); ok {
			if target, ok := flag.Value.(pflag.SliceValue); ok {
				err = target.Replace(slice)
			} else {
				err = fmt.Errorf("expected a scalar, received a list")
			}
		} else if _, nested := value.(map[string]any); nested {
			continue
		} else {
			err = flag.Value.Set(fmt.Sprint(value))
		}
		if err != nil {
			return fmt.Errorf("configuration for %s.%s: %w", command.CommandPath(), key, err)
		}
		flag.Changed = true
	}
	return nil
}
