package cli

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/ooaklee/lexr.sh/internal/bootmenu"
	"github.com/spf13/cobra"
)

// newArchGRUBRegistrationCommand exposes an optional, repeatable post-install
// action usable from the existing Linux OS or explicitly mounted live targets.
func (a *application) newArchGRUBRegistrationCommand() *cobra.Command {
	options := bootmenu.ArchOptions{}
	var yes bool
	command := &cobra.Command{
		Use:   "register-arch",
		Short: "Add an installed Surface Arch loader to an existing GRUB menu",
		Long:  "Add an installed Surface Arch loader to an existing GRUB custom.cfg. Run from the existing Linux OS, or specify its mounted GRUB directory from the live USB. Paths must already be mounted. No partitions, firmware variables, EFI files or generated grub.cfg are changed. Do not run alongside a GRUB update.",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if !options.DryRun && !yes {
				return errors.New("preview with --dry-run, or explicitly apply with --yes")
			}
			if options.DryRun && yes {
				return errors.New("choose --dry-run or --yes, not both")
			}
			if runtime.GOOS != "linux" {
				return errors.New("GRUB registration requires Linux")
			}
			if !options.DryRun && os.Geteuid() != 0 {
				return errors.New("applying GRUB registration requires sudo")
			}
			result, err := bootmenu.RegisterArch(command.Context(), options, a.kernelBootRunnerForCommand())
			if err != nil {
				return err
			}
			if result.AlreadyPresent {
				fmt.Fprintln(a.out, "Arch is already registered; no files changed.")
			} else if options.DryRun {
				fmt.Fprintf(a.out, "Would append to %s (no files changed):\n%s", result.Path, result.Entry)
			} else {
				fmt.Fprintf(a.out, "Registered Arch in %s. Select 'Arch Linux ARM (Surface Pro 11)' at your existing GRUB menu.\n", result.Path)
				if result.Backup != "" {
					fmt.Fprintf(a.out, "Previous custom menu: %s\n", result.Backup)
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&options.ArchRoot, "arch-root", "", "mounted installed Arch root containing its Lexr receipt")
	command.Flags().StringVar(&options.GRUBDirectory, "grub-directory", "", "existing OS's mounted GRUB directory (including a separate /boot if used)")
	command.Flags().StringVar(&options.ESP, "esp", "/boot/efi", "mounted shared EFI System Partition")
	command.Flags().BoolVar(&options.DryRun, "dry-run", false, "verify the loader and preview the entry without writing")
	command.Flags().BoolVar(&yes, "yes", false, "apply the verified entry to the selected existing menu")
	_ = command.MarkFlagRequired("arch-root")
	_ = command.MarkFlagRequired("grub-directory")
	return command
}
