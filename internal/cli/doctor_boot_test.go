package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ooaklee/lexr.sh/internal/bootdoctor"
)

// bootDoctorStub returns a deterministic report and records command options.
type bootDoctorStub struct {
	// report is the complete static report returned to delivery.
	report bootdoctor.Report
	// err is an optional inspection failure.
	err error
	// options records the delivered root, device, and ABI filters.
	options bootdoctor.Options
}

// Inspect records command options and returns the configured report or error.
func (stub *bootDoctorStub) Inspect(ctx context.Context, options bootdoctor.Options) (bootdoctor.Report, error) {
	if err := ctx.Err(); err != nil {
		return bootdoctor.Report{}, err
	}
	stub.options = options
	return stub.report, stub.err
}

// executeBootDoctorCommand runs the isolated child and captures both streams.
func executeBootDoctorCommand(t *testing.T, stub *bootDoctorStub, args ...string) (string, string, error) {
	t.Helper()
	var output bytes.Buffer
	var errorOutput bytes.Buffer
	app := &application{in: strings.NewReader(""), out: &output, errOut: &errorOutput}
	root := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(app.out)
	root.SetErr(app.errOut)
	root.AddCommand(app.newBootDoctorCommand(func() bootDoctorWorkflow { return stub }))
	root.SetArgs(append([]string{"boot"}, args...))
	err := root.ExecuteContext(context.Background())
	return output.String(), errorOutput.String(), err
}

// failingBootDoctorReport returns one complete not-ready fixture with digests.
func failingBootDoctorReport() bootdoctor.Report {
	return bootdoctor.Report{
		Ready:               false,
		Device:              "x1e-oled",
		PhysicalBootability: "static evidence cannot prove physical bootability",
		Checks: []bootdoctor.Check{{
			ID:       "grub-entry-dtb",
			State:    bootdoctor.StateFail,
			Required: true,
			ABI:      "7.2.2-jg-0sp11v2-qcom-x1e",
			Detail:   "the boot-side DTB does not match the installed same-ABI firmware DTB",
		}},
	}
}

// TestBootDoctorJSONFailureRemainsMachineReadable verifies a complete JSON
// report is emitted before the command returns its non-zero readiness result.
func TestBootDoctorJSONFailureRemainsMachineReadable(t *testing.T) {
	stub := &bootDoctorStub{report: failingBootDoctorReport()}
	output, errorOutput, err := executeBootDoctorCommand(t, stub,
		"--root", "/target", "--device", "x1e-oled", "--target-abi", "7.2.2-jg-0sp11v2-qcom-x1e", "--fallback-abi", "7.2.0-jg-0sp11v20-qcom-x1e", "--json")
	if err == nil || !strings.Contains(err.Error(), "required static boot checks failed") {
		t.Fatalf("boot doctor error = %v", err)
	}
	if errorOutput != "" {
		t.Fatalf("stderr = %q", errorOutput)
	}
	var report bootdoctor.Report
	if decodeErr := json.Unmarshal([]byte(output), &report); decodeErr != nil {
		t.Fatalf("JSON report cannot be decoded: %v\n%s", decodeErr, output)
	}
	if report.Ready || len(report.Checks) != 1 {
		t.Fatalf("decoded report = %#v", report)
	}
	if stub.options.Root != "/target" || stub.options.Device != "x1e-oled" || stub.options.TargetABI == "" || stub.options.FallbackABI == "" {
		t.Fatalf("delivered boot options = %#v", stub.options)
	}
}

// TestBootDoctorHumanReportStatesStaticLimit verifies text delivery includes
// readiness and the explicit physical-bootability limitation.
func TestBootDoctorHumanReportStatesStaticLimit(t *testing.T) {
	report := failingBootDoctorReport()
	report.Ready = true
	report.Checks[0].State = bootdoctor.StateWarn
	report.Checks[0].Required = false
	stub := &bootDoctorStub{report: report}
	output, errorOutput, err := executeBootDoctorCommand(t, stub, "--device", "x1e-oled")
	if err != nil || errorOutput != "" {
		t.Fatalf("human boot doctor error=%v stderr=%q", err, errorOutput)
	}
	for _, expected := range []string{"STATE", "grub-entry-dtb", "observed readiness", "ready", "static evidence cannot prove physical bootability"} {
		if !strings.Contains(output, expected) {
			t.Errorf("human report does not contain %q:\n%s", expected, output)
		}
	}
}

// TestBootDoctorRedactsAlternateRootFatalError verifies a rejected GRUB
// symlink cannot expose the host-side alternate-root prefix through the CLI.
func TestBootDoctorRedactsAlternateRootFatalError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic link creation is not reliably available on Windows")
	}
	rootPath := t.TempDir()
	grubDirectory := filepath.Join(rootPath, "boot/grub")
	if err := os.MkdirAll(grubDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "grub.cfg")
	if err := os.WriteFile(outside, []byte("menuentry 'outside' {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(grubDirectory, "grub.cfg")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	var errorOutput bytes.Buffer
	app := &application{in: strings.NewReader(""), out: &output, errOut: &errorOutput}
	command := &cobra.Command{Use: "test", SilenceErrors: true, SilenceUsage: true}
	command.SetOut(app.out)
	command.SetErr(app.errOut)
	command.AddCommand(app.newBootDoctorCommand(nil))
	command.SetArgs([]string{"boot", "--root", rootPath, "--device", "x1e-oled"})
	err := command.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("symlinked GRUB configuration unexpectedly passed inspection")
	}
	delivered := err.Error() + errorOutput.String()
	if strings.Contains(delivered, rootPath) || !strings.Contains(delivered, "[redacted alternate-root value]") {
		t.Fatalf("CLI error did not redact alternate root: %s", delivered)
	}
}
