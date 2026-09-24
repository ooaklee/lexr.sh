package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ooaklee/lexr.sh/internal/cleanup"
	lexrconfig "github.com/ooaklee/lexr.sh/internal/config"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	kernelinstall "github.com/ooaklee/lexr.sh/internal/kernel/install"
)

// TestKernelInstallationReceivesGlobalProfile exercises the shared resolver and
// actual handlers so a profile cannot stop at a superficial bundle check again.
func TestKernelInstallationReceivesGlobalProfile(t *testing.T) {
	const lcd = "x1p64100-microsoft-denali"
	for _, operation := range []string{"preflight", "install"} {
		for _, override := range []bool{false, true} {
			stub := &stubKernelInstallationManager{installReceipt: kernelinstall.Receipt{Plan: kernelinstall.Plan{DryRun: true}}}
			app := &application{
				in: strings.NewReader(""), out: &bytes.Buffer{}, errOut: &bytes.Buffer{},
				configuration: lexrconfig.Config{Profile: lcd}, kernelInstaller: stub,
			}
			root := &cobra.Command{Use: "lexr", PersistentPreRunE: func(command *cobra.Command, _ []string) error { return app.selectProfile(command) }}
			root.PersistentFlags().StringVar(&app.profileName, "profile", "", "hardware profile")
			root.AddCommand(app.newKernelCommand())
			args := []string{"kernel", operation, kernelCLIBundleDirectory(t), "--root", t.TempDir(), "--fallback-abi", kernelCLIFallbackABI}
			if operation == "install" {
				args = append(args, "--dry-run")
			}
			if override {
				app.configuration.Profile = "x1e80100-microsoft-denali-oled"
				args = append(args, "--profile", lcd)
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			requests := append(stub.preflightRequests, stub.installRequests...)
			if len(requests) != 1 || requests[0].Profile != lcd {
				t.Fatalf("%s override=%t lost LCD intent: %#v", operation, override, requests)
			}
		}
	}
}

// TestFailedKernelInstallPrintsBootHookRecovery keeps recovery paths visible
// without requiring JSON output, including partially completed cleanup.
func TestFailedKernelInstallPrintsBootHookRecovery(t *testing.T) {
	for _, state := range []string{"complete", "prepared"} {
		var output bytes.Buffer
		app := &application{out: &output, errOut: &bytes.Buffer{}}
		failure := errors.New("package installation failed")
		receipt := kernelinstall.Receipt{
			BootHookCleanup:   &cleanup.Receipt{State: state, Backup: "/var/lib/lexr/backups/example"},
			BootHooksRestored: true,
		}
		err := app.writeKernelInstallReceipt(receipt, kernel.LocalPackageSetAll, false, failure)
		if !errors.Is(err, failure) {
			t.Fatalf("lost installation failure: %v", err)
		}
		name := "receipt.json"
		if state != "complete" {
			name = "receipt.pending.json"
		}
		for _, want := range []string{"/var/lib/lexr/backups/example/" + name, "retired boot hooks restored: true"} {
			if !strings.Contains(output.String(), want) {
				t.Fatalf("failed install hides recovery evidence %q: %s", want, output.String())
			}
		}
	}
}
