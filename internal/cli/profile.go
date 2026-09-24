package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/profile"
)

// completeProfiles offers canonical profile names without loading configuration.
func completeProfiles(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	var names []string
	for _, candidate := range profile.List() {
		names = append(names, candidate.ID+"\t"+candidate.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// newProfileCommand makes hardware choices discoverable before initialisation.
func (a *application) newProfileCommand() *cobra.Command {
	command := &cobra.Command{
		Use: "profile", Short: "Discover hardware profiles",
		// Discovery must remain available to repair an invalid saved profile.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	}
	var asJSON bool
	list := &cobra.Command{
		Use: "list", Short: "List hardware profile IDs and model names", Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if asJSON {
				return a.writeJSON(profile.List())
			}
			writer := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(writer, "PROFILE\tHARDWARE")
			for _, candidate := range profile.List() {
				_, _ = fmt.Fprintf(writer, "%s\t%s\n", candidate.ID, candidate.Name)
			}
			return writer.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "write machine-readable JSON")
	command.AddCommand(list)
	return command
}

// profileIndependentBuild identifies artefact production whose platform scope
// comes from source and bundle contracts, never a workstation's saved profile.
func profileIndependentBuild(command *cobra.Command) bool {
	for current := command; current.HasParent(); current = current.Parent() {
		if current.Name() == "build" || current.Name() == "release" {
			return true
		}
	}
	return false
}

// hardwareTarget declares which commands need a hardware identity. Other
// commands inspect catalogues, hosts, files or exact receipts, so they validate
// a supplied profile without inventing a hardware target when none is needed.
func hardwareTarget(command *cobra.Command) (root string, required bool) {
	switch command.CommandPath() {
	case "lexr image create", "lexr wizard":
		return "", true // Offline destinations cannot be inferred from this host.
	case "lexr doctor boot", "lexr doctor hardware", "lexr doctor userspace",
		"lexr userspace status", "lexr userspace install",
		"lexr kernel preflight", "lexr kernel install", "lexr kernel boot refresh",
		"lexr clean scan", "lexr clean plan", "lexr clean apply":
		root, _ := command.Flags().GetString("root")
		if root == "" {
			root = "/"
		}
		return root, true
	case "lexr userspace camera capture":
		return "/", true
	case "lexr handoff apply":
		root, _ := command.Flags().GetString("identity-root")
		return root, true
	}
	return "", false
}

// selectProfile resolves the command's target once after flag/config precedence.
// The top-level profile setting, including "auto", is the only selection
// mechanism; build and release commands keep their own platform inventory.
func (a *application) selectProfile(command *cobra.Command) error {
	profileFlag := command.Flags().Lookup("profile")
	explicit := profileFlag != nil && profileFlag.Changed
	if profileIndependentBuild(command) {
		if explicit {
			return fmt.Errorf("%s uses its source or bundle's platform inventory; omit --profile for build and release commands", command.CommandPath())
		}
		return nil
	}
	name := a.configuration.Profile
	if explicit {
		name = a.profileName
		if name == "" {
			return fmt.Errorf("--profile must not be empty; use a profile ID or auto")
		}
	}
	var selected profile.Profile
	var err error
	if name != "" && name != "auto" {
		selected, err = profile.Resolve(name)
		if err != nil {
			return err
		}
	}
	root, required := hardwareTarget(command)
	if command == command.Root() && isTerminalReader(a.in) {
		required = true
	}
	if selected.ID == "" && required {
		if root == "" || filepath.Clean(root) != "/" {
			return fmt.Errorf("%s needs a hardware profile for this offline target; run 'lexr init <profile>' or pass --profile (see 'lexr profile list')", command.CommandPath())
		}
		selected, err = profile.Detect(root)
		if err != nil {
			return fmt.Errorf("cannot detect a hardware profile: %w; run 'lexr profile list', then 'lexr init <profile>' or pass --profile", err)
		}
	}
	if required && root != "" && selected.ID != "" {
		// A selected profile describes intent; it must not override contradictory
		// physical evidence. Missing offline evidence remains a domain preflight.
		if observed, detectErr := profile.Detect(root); detectErr == nil && observed.ID != selected.ID {
			return fmt.Errorf("selected profile %q conflicts with target hardware %q", selected.ID, observed.ID)
		}
	}
	a.profile = selected
	return nil
}

// profileNames returns the canonical IDs for positional init completion.
func profileNames() []string {
	names, _ := completeProfiles(nil, nil, "")
	for index := range names {
		names[index], _, _ = strings.Cut(names[index], "\t")
	}
	return names
}

// checkKernelProfile keeps platform intent subordinate to the verified bundle
// inventory. It does not replace native installation or boot-binding preflight.
func (a *application) checkKernelProfile(bundle kernel.Bundle) error {
	if a.profile.ID == "" {
		return nil
	}
	for _, platformID := range bundle.BootPlatforms() {
		if platformID == a.profile.Platform {
			return nil
		}
	}
	return fmt.Errorf("kernel bundle does not declare profile %q", a.profile.ID)
}

// checkImageProfile records profile mismatch as a failed structural check so
// JSON remains a complete report and a USB write fails before device planning.
func (a *application) checkImageProfile(report *imagecontract.ValidationReport) error {
	if a.profile.ID == "" || !report.Valid {
		return nil
	}
	for _, platformID := range report.BootProfiles {
		if platformID == a.profile.Platform {
			return nil
		}
	}
	report.Valid = false
	err := fmt.Errorf("image does not declare profile %q", a.profile.ID)
	report.Checks = append(report.Checks, imagecontract.ValidationCheck{Name: "hardware-profile", Passed: false, Details: err.Error()})
	return err
}
