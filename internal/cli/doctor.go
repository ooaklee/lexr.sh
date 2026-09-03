package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ooaklee/lexr.sh/internal/bootdoctor"
	"github.com/ooaklee/lexr.sh/internal/doctor"
	"github.com/ooaklee/lexr.sh/internal/hardwaredoctor"
)

// bootDoctorWorkflow is the delivery layer's narrow view of static boot inspection.
type bootDoctorWorkflow interface {
	// Inspect returns one typed, path-redacted, read-only boot report.
	Inspect(context.Context, bootdoctor.Options) (bootdoctor.Report, error)
}

// bootDoctorFactory constructs a read-only boot inspector for command tests.
type bootDoctorFactory func() bootDoctorWorkflow

// hardwareDoctorWorkflow is the delivery layer's narrow view of live hardware
// inspection, keeping command tests independent of the host's devices.
type hardwareDoctorWorkflow interface {
	// Inspect returns one typed, redacted live-hardware report.
	Inspect(context.Context, hardwaredoctor.Options) (hardwaredoctor.Report, error)
}

// hardwareDoctorFactory constructs an inspector over the selected procfs and
// sysfs root only when the hardware command runs.
type hardwareDoctorFactory func(string) (hardwareDoctorWorkflow, error)

// alternateRootProbeRunner makes process-based evidence unavailable rather
// than mixing an alternate filesystem snapshot with the current host.
type alternateRootProbeRunner struct{}

// Run rejects every external probe for an alternate diagnostic root.
func (alternateRootProbeRunner) Run(ctx context.Context, _ hardwaredoctor.Probe, _ int64) (hardwaredoctor.ProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return hardwaredoctor.ProbeResult{}, err
	}
	return hardwaredoctor.ProbeResult{}, fmt.Errorf("external hardware probes are unavailable for an alternate root")
}

// newDoctorCommand checks host image-building prerequisites and provides the
// nested static userspace and live hardware diagnostic commands.
func (a *application) newDoctorCommand() *cobra.Command {
	var workspace string
	var asJSON bool
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Check whether this host can build and validate images",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			report := doctor.New(nil).Run(command.Context(), workspace)
			if asJSON {
				if err := a.writeJSON(report); err != nil {
					return err
				}
			} else {
				for _, check := range report.Checks {
					_, _ = fmt.Fprintf(a.out, "%-4s  %-20s %s\n", check.Status, check.Name, check.Details)
				}
			}
			if !report.Ready {
				return fmt.Errorf("required host checks failed")
			}
			return nil
		},
	}
	command.Flags().StringVar(&workspace, "workspace", ".", "directory that will hold temporary image data")
	command.Flags().BoolVar(&asJSON, "json", false, "write machine-readable JSON")
	command.AddCommand(
		a.newUserspaceStatusDeliveryCommand("userspace", "Report missing Surface Pro 11 userspace support"),
		a.newHardwareDoctorCommand(nil),
		a.newBootDoctorCommand(nil),
	)
	return command
}

// newBootDoctorCommand builds a static, scriptable boot diagnostic that never
// executes hooks, rewrites DTBs, changes defaults, or elevates privileges.
func (a *application) newBootDoctorCommand(factory bootDoctorFactory) *cobra.Command {
	var root string
	var device string
	var targetABI string
	var fallbackABI string
	var asJSON bool
	command := &cobra.Command{
		Use:   "boot",
		Short: "Report static Surface kernel boot and DTB evidence",
		Long: "Inspect bounded GRUB, embedded or external boot DTB, boot artefact, firmware DTB, default-selection, and retired-hook evidence read-only. " +
			"The command never executes package hooks or GRUB tools, changes defaults, rewrites DTBs, or proves physical bootability.",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			selectedFactory := factory
			if selectedFactory == nil {
				selectedFactory = func() bootDoctorWorkflow { return bootdoctor.New() }
			}
			report, err := selectedFactory().Inspect(command.Context(), bootdoctor.Options{
				Root:        root,
				Device:      device,
				TargetABI:   targetABI,
				FallbackABI: fallbackABI,
			})
			if err != nil {
				return fmt.Errorf("inspect static boot evidence: %w", err)
			}
			if asJSON {
				if err := a.writeJSON(report); err != nil {
					return err
				}
			} else if err := a.writeBootDoctorReport(report); err != nil {
				return err
			}
			if !report.Ready {
				return fmt.Errorf("required static boot checks failed; physical bootability remains unproven")
			}
			return nil
		},
	}
	command.Flags().StringVar(&root, "root", "/", "Linux filesystem root containing boot evidence")
	command.Flags().StringVar(&device, "device", "", "Surface hardware variant: x1e-oled or x1p-lcd")
	command.Flags().StringVar(&targetABI, "target-abi", "", "optional target ABI that must be ready")
	command.Flags().StringVar(&fallbackABI, "fallback-abi", "", "optional fallback ABI that must be ready")
	command.Flags().BoolVar(&asJSON, "json", false, "write machine-readable JSON")
	return command
}

// writeBootDoctorReport renders only recognised boot paths, exact DTB digests,
// and bounded diagnostic conclusions.
func (a *application) writeBootDoctorReport(report bootdoctor.Report) error {
	writer := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintf(writer, "GRUB default\t%s\nsaved_entry\t%s\ndevice\t%s\n", report.Default.Effective, report.Default.SavedEntry, report.Device); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "\nTYPE\tINDEX\tABI\tCOMMAND\tPATH\tEXISTS"); err != nil {
		return err
	}
	for _, entry := range report.Entries {
		kind := "normal"
		if entry.Recovery {
			kind = "recovery"
		}
		for _, token := range entry.Linux {
			if _, err := fmt.Fprintf(writer, "%s\t%d\t%s\t%s\t%s\t%t\n", kind, entry.Index, entry.ABI, token.Command, token.Path, entry.KernelExists); err != nil {
				return err
			}
		}
		for _, token := range entry.Initrd {
			if _, err := fmt.Fprintf(writer, "%s\t%d\t%s\t%s\t%s\t%t\n", kind, entry.Index, entry.ABI, token.Command, token.Path, entry.InitramfsExists); err != nil {
				return err
			}
		}
		for _, token := range entry.DeviceTrees {
			if _, err := fmt.Fprintf(writer, "%s\t%d\t%s\t%s\t%s\t%t\n", kind, entry.Index, entry.ABI, token.Command, token.Path, entry.BootDTBSHA256 != ""); err != nil {
				return err
			}
		}
		if entry.DeviceTreeBoot != nil {
			if _, err := fmt.Fprintf(writer, "dtb-binding\t%d\t%s\t%s\t%s\t\n", entry.Index, entry.ABI,
				entry.DeviceTreeBoot.Mode, entry.DeviceTreeBoot.SHA256); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(writer, "dtb-digest\t%d\t%s\tboot-side\t%s\t\n", entry.Index, entry.ABI, entry.BootDTBSHA256); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "dtb-digest\t%d\t%s\tinstalled\t%s\t\n", entry.Index, entry.ABI, entry.InstalledDTBSHA256); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(writer, "\nSTATE\tREQUIRED\tABI\tCHECK\tDETAIL"); err != nil {
		return err
	}
	for _, check := range report.Checks {
		if _, err := fmt.Fprintf(writer, "%s\t%t\t%s\t%s\t%s\n", check.State, check.Required, check.ABI, check.ID, check.Detail); err != nil {
			return err
		}
	}
	readiness := "ready"
	if !report.Ready {
		readiness = "not ready"
	}
	bootMode := ""
	bootEntries := 0
	if report.Attribution.DeviceTreeBoot != nil {
		bootMode = string(report.Attribution.DeviceTreeBoot.Mode)
		bootEntries = report.Attribution.DeviceTreeBoot.GRUBEntryCount
	}
	if _, err := fmt.Fprintf(writer,
		"\npatch-line-first DTB ABI\t%s\nattributed boot DTB ABI\t%s\nboot DTB mode\t%s\nboot DTB GRUB entries verified\t%d\nboot-side SHA-256\t%s\ninstalled SHA-256\t%s\nobserved readiness\t%s\nphysical bootability\t%s\n",
		report.Attribution.SelectedABI, report.Attribution.AttributedABI,
		bootMode, bootEntries, report.Attribution.BootSHA256,
		report.Attribution.InstalledSHA256, readiness, report.PhysicalBootability); err != nil {
		return err
	}
	return writer.Flush()
}

// newHardwareDoctorCommand builds scriptable live hardware diagnostics with
// positional feature selection and deterministic human or JSON delivery.
func (a *application) newHardwareDoctorCommand(factory hardwareDoctorFactory) *cobra.Command {
	var root string
	var asJSON bool
	command := &cobra.Command{
		Use:       "hardware [wifi|bluetooth|audio|touchscreen ...]",
		Short:     "Report redacted live Surface Pro 11 hardware state",
		ValidArgs: []string{"wifi", "bluetooth", "audio", "touchscreen"},
		Long: "Report bounded, read-only live hardware evidence without changing devices, radio blocks, services, networking, or audio routing. " +
			"An alternate root supplies filesystem evidence only; process-based checks are reported unavailable rather than querying the current host.",
		Args: cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, args []string) error {
			features := make([]hardwaredoctor.Feature, 0, len(args))
			for _, value := range args {
				feature, err := hardwaredoctor.ParseFeature(value)
				if err != nil {
					return err
				}
				features = append(features, feature)
			}
			selectedFactory := factory
			if selectedFactory == nil {
				selectedFactory = newHardwareDoctorWorkflow
			}
			workflow, err := selectedFactory(root)
			if err != nil {
				return fmt.Errorf("construct hardware doctor: %w", err)
			}
			report, err := workflow.Inspect(command.Context(), hardwaredoctor.Options{Features: features})
			if err != nil {
				return fmt.Errorf("inspect live hardware: %w", err)
			}
			if asJSON {
				if err := a.writeJSON(report); err != nil {
					return err
				}
			} else if err := a.writeHardwareDoctorReport(report); err != nil {
				return err
			}
			if !report.Ready {
				return fmt.Errorf("required live hardware checks failed; physical hardware qualification remains unproven")
			}
			return nil
		},
	}
	command.Flags().StringVar(&root, "root", "/", "Linux runtime root containing procfs and sysfs evidence")
	command.Flags().BoolVar(&asJSON, "json", false, "write machine-readable JSON")
	return command
}

// newHardwareDoctorWorkflow constructs the default read-only live inspector.
func newHardwareDoctorWorkflow(root string) (hardwareDoctorWorkflow, error) {
	if strings.TrimSpace(root) == "" {
		root = string(filepath.Separator)
	}
	filesystem, err := hardwaredoctor.NewOSFileSystem(root)
	if err != nil {
		return nil, err
	}
	var runner hardwaredoctor.ProbeRunner
	if !isLiveHardwareRoot(root) {
		runner = alternateRootProbeRunner{}
	}
	return hardwaredoctor.New(filesystem, runner)
}

// isLiveHardwareRoot reports whether root resolves to the current system root.
func isLiveHardwareRoot(root string) bool {
	if strings.TrimSpace(root) == "" {
		root = string(filepath.Separator)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return false
	}
	systemRoot, err := filepath.EvalSymlinks(string(filepath.Separator))
	return err == nil && filepath.Clean(resolved) == filepath.Clean(systemRoot)
}

// writeHardwareDoctorReport renders only the domain's redacted scalar fields
// and keeps observable readiness distinct from physical qualification.
func (a *application) writeHardwareDoctorReport(report hardwaredoctor.Report) error {
	writer := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "STATE\tEVIDENCE\tFEATURE\tREQUIRED\tCHECK\tDETAIL"); err != nil {
		return err
	}
	for _, check := range report.Checks {
		feature := string(check.Feature)
		if feature == "" {
			feature = "platform"
		}
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%t\t%s\t%s\n",
			check.State, check.Evidence, feature, check.Required, check.ID, check.Detail); err != nil {
			return err
		}
		if check.Remediation != "" {
			if _, err := fmt.Fprintf(writer, "\t\t\t\tremediation\t%s\n", check.Remediation); err != nil {
				return err
			}
		}
	}
	readiness := "ready"
	if !report.Ready {
		readiness = "not ready"
	}
	if _, err := fmt.Fprintf(writer, "\nobserved readiness:\t%s\nhardware qualification:\tnot proven by this read-only command\n", readiness); err != nil {
		return err
	}
	return writer.Flush()
}
