package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	lexrconfig "github.com/ooaklee/lexr.sh/internal/config"
	"github.com/ooaklee/lexr.sh/internal/profile"
)

// pflag normally discards empty StringSlice occurrences; reject them before
// that happens so --config= cannot silently select another file.
type nonemptyConfigFlag struct{ pflag.Value }

// Set rejects an empty occurrence before delegating CSV parsing to pflag.
func (value nonemptyConfigFlag) Set(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("configuration path %q is empty", raw)
	}
	return value.Value.Set(raw)
}

// newConfigCommand provides configuration inspection and primary-file editing.
func (a *application) newConfigCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "config", Short: "Check, edit or show Lexr configuration",
		// Management commands load files themselves. In particular, edit must
		// remain available when the configuration is missing or invalid.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	}
	command.AddCommand(&cobra.Command{
		Use: "check", Short: "Validate all selected configuration files and their merge",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			paths, err := lexrconfig.ResolvePaths(a.configPaths)
			if err != nil {
				return err
			}
			for _, path := range paths {
				if _, err := fmt.Fprintf(a.out, "Checking configuration: %s\n", path); err != nil {
					return err
				}
			}
			configuration, err := lexrconfig.LoadFiles(paths)
			if err != nil {
				return err
			}
			if configuration.Profile != "" && configuration.Profile != "auto" {
				if _, err := profile.Resolve(configuration.Profile); err != nil {
					return fmt.Errorf("configuration %q: %w", paths, err)
				}
			}
			_, err = fmt.Fprintln(a.out, "Configuration valid.")
			return err
		},
	}, &cobra.Command{
		Use: "show", Short: "Print merged YAML and its source paths",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			paths, err := lexrconfig.ResolvePaths(a.configPaths)
			if err != nil {
				return err
			}
			for _, path := range paths {
				if _, err := fmt.Fprintf(a.out, "# Source: %q\n", path); err != nil {
					return err
				}
			}
			return lexrconfig.WriteMerged(a.out, paths)
		},
	}, &cobra.Command{
		Use: "edit", Short: "Open the primary configuration in VISUAL, EDITOR or vi",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			paths, err := lexrconfig.ResolvePaths(a.configPaths)
			if err != nil {
				return err
			}
			if err := lexrconfig.EnsureTemplate(paths[0]); err != nil {
				return err
			}
			editor := strings.TrimSpace(os.Getenv("VISUAL"))
			if editor == "" {
				editor = strings.TrimSpace(os.Getenv("EDITOR"))
			}
			if editor == "" {
				editor = "vi"
			}
			// Accept an executable path (including spaces), or a command with
			// whitespace-separated arguments. Never interpret shell syntax.
			arguments := []string{editor}
			if _, err := exec.LookPath(editor); err != nil {
				arguments = strings.Fields(editor)
			}
			process := exec.CommandContext(command.Context(), arguments[0], append(arguments[1:], paths[0])...)
			process.Stdin, process.Stdout, process.Stderr = a.in, a.out, a.errOut
			if err := process.Run(); err != nil {
				return fmt.Errorf("edit configuration %q with %q: %w", paths[0], editor, err)
			}
			return nil
		},
	})
	return command
}

// newInitCommand sets the primary profile with explicit overwrite confirmation.
func (a *application) newInitCommand() *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use: "init <profile>", Short: "Set the primary configuration's hardware profile",
		ValidArgs:         profileNames(),
		Args:              cobra.ExactArgs(1),
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(_ *cobra.Command, args []string) error {
			selected, err := profile.Resolve(args[0])
			if err != nil {
				return err
			}
			paths, err := lexrconfig.ResolvePaths(a.configPaths)
			if err != nil {
				return err
			}
			// Validate selected overlays before changing the primary file. An
			// absent primary is allowed on first use; a broken overlay is not.
			if len(paths) > 1 {
				if _, err := lexrconfig.LoadFiles(paths[1:]); err != nil {
					return err
				}
			}
			if err := lexrconfig.SetProfile(paths[0], selected.ID, force); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(a.out, "Set profile %q in %s\n", selected.ID, paths[0]); err != nil {
				return err
			}
			merged, err := lexrconfig.LoadFiles(paths)
			if err != nil {
				return err
			}
			if merged.Profile != selected.ID {
				_, err = fmt.Fprintf(a.out, "A later configuration file overrides the saved profile; merged profile: %q.\n", merged.Profile)
			}
			return err
		},
	}
	command.Flags().BoolVar(&force, "force", false, "replace a different non-empty profile without prompting")
	return command
}
