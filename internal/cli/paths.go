package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// hostPathFlag defers defaults until after configuration loading. Empty flag
// registration avoids filesystem writes during help or command construction.
// Changed preserves explicit flags (even empty ones) over configured defaults.
// Resolvers must read the application's configuration when called, not when bound.
func hostPathFlag(command *cobra.Command, target *string, name, usage string, resolve func() (string, error)) {
	command.Flags().StringVar(target, name, "", usage)
	_ = command.Flags().SetAnnotation(name, "lexr.host-path", []string{"true"})
	previous := command.PreRunE
	command.PreRunE = func(command *cobra.Command, args []string) error {
		if previous != nil {
			if err := previous(command, args); err != nil {
				return err
			}
		}
		if command.Flags().Changed(name) {
			return nil
		}
		value, err := resolve()
		if err != nil {
			return fmt.Errorf("resolve --%s: %w", name, err)
		}
		*target = value
		return nil
	}
}
