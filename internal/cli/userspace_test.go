package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	lexr "github.com/ooaklee/lexr.sh"
	camerabuild "github.com/ooaklee/lexr.sh/internal/camera/build"
	userspacebuild "github.com/ooaklee/lexr.sh/internal/userspace/build"
	userspacecatalog "github.com/ooaklee/lexr.sh/internal/userspace/catalog"
	userspaceinstall "github.com/ooaklee/lexr.sh/internal/userspace/install"
	userspacemanager "github.com/ooaklee/lexr.sh/internal/userspace/manager"
)

// cliFakeInstaller records install options while returning deterministic results
// so command tests can exercise orchestration without changing the host system.
type cliFakeInstaller struct {
	calls []userspaceinstall.Options
	// iptsdResult optionally supplies an incomplete native result.
	iptsdResult userspaceinstall.Result
	// iptsdError optionally reports failed live activation.
	iptsdError error
}

// Audio records a simulated audio installation that requires a reboot.
func (f *cliFakeInstaller) Audio(_ context.Context, options userspaceinstall.Options) (userspaceinstall.Result, error) {
	return f.record(userspacemanager.AudioComponent, options, true), nil
}

// IPTSD records a simulated touchscreen userspace installation.
func (f *cliFakeInstaller) IPTSD(_ context.Context, options userspaceinstall.Options) (userspaceinstall.Result, error) {
	if f.iptsdError != nil {
		f.calls = append(f.calls, options)
		return f.iptsdResult, f.iptsdError
	}
	return f.record(userspacemanager.IPTSDComponent, options, false), nil
}

// WiFi records a simulated native distribution board-data installation.
func (f *cliFakeInstaller) WiFi(_ context.Context, options userspaceinstall.Options) (userspaceinstall.Result, error) {
	return f.record("wifi", options, false), nil
}

// Camera records a simulated camera userspace installation.
func (f *cliFakeInstaller) Camera(_ context.Context, options userspaceinstall.Options) (userspaceinstall.Result, error) {
	return f.record(userspacemanager.CameraComponent, options, false), nil
}

// record appends one simulated invocation and constructs its command-facing result.
func (f *cliFakeInstaller) record(component string, options userspaceinstall.Options, reboot bool) userspaceinstall.Result {
	f.calls = append(f.calls, options)
	return userspaceinstall.Result{
		Component: component, Root: options.Root, DryRun: options.DryRun,
		RebootRequired: reboot,
	}
}

// TestUserspaceInstallRequiresConfirmationBeforeMutation verifies that a real
// userspace install is rejected before the installer runs unless --yes is set.
func TestUserspaceInstallRequiresConfirmationBeforeMutation(t *testing.T) {
	installer := &cliFakeInstaller{}
	app, _ := newUserspaceInstallTestApplication(installer)
	command := app.newUserspaceInstallCommand()
	command.SetArgs([]string{"audio", "--from", t.TempDir()})
	command.SilenceUsage = true
	command.SilenceErrors = true

	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "pass --yes") {
		t.Fatalf("error = %v, want explicit confirmation guidance", err)
	}
	if len(installer.calls) != 0 {
		t.Fatalf("installer was called before confirmation: %#v", installer.calls)
	}
}

// TestWiFiInstallUsesDistributionInputAndExplicitActivation verifies dry runs,
// source-free native setup, confirmation and rejection of mixed release inputs.
func TestWiFiInstallUsesDistributionInputAndExplicitActivation(t *testing.T) {
	for _, test := range []struct {
		name      string
		args      []string
		wantError string
		wantCalls int
	}{
		{"preview", []string{"wifi", "--activate", "--dry-run", "--json"}, "", 1},
		{"uppercase", []string{"WIFI", "--activate", "--dry-run", "--json"}, "", 1},
		{"confirmation", []string{"wifi", "--activate"}, "pass --yes", 0},
		{"apply", []string{"wifi", "--activate", "--yes", "--json"}, "", 1},
		{"mixed-source", []string{"wifi", "--from", "/unused", "--dry-run"}, "omit --from", 0},
		{"other-activation", []string{"audio", "--from", "/unused", "--activate", "--dry-run"}, "only", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			installer := &cliFakeInstaller{}
			app, _ := newUserspaceInstallTestApplication(installer)
			command := app.newUserspaceInstallCommand()
			command.SetArgs(test.args)
			command.SilenceUsage, command.SilenceErrors = true, true
			err := command.ExecuteContext(context.Background())
			if test.wantError == "" && err != nil || test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("error=%v, want %q", err, test.wantError)
			}
			if len(installer.calls) != test.wantCalls {
				t.Fatalf("calls=%v", installer.calls)
			}
			if test.wantCalls > 0 && (!installer.calls[0].Activate || installer.calls[0].BundleDir != "" || installer.calls[0].DryRun != (test.name == "preview" || test.name == "uppercase")) {
				t.Fatalf("incorrect native options: %+v", installer.calls[0])
			}
		})
	}
}

// TestUserspaceInstallRecommendedDryRunEmitsStructuredReport verifies that the
// recommended dry run plans audio and IPTSD and emits actionable JSON guidance.
func TestUserspaceInstallRecommendedDryRunEmitsStructuredReport(t *testing.T) {
	installer := &cliFakeInstaller{}
	app, output := newUserspaceInstallTestApplication(installer)
	command := app.newUserspaceInstallCommand()
	command.SetArgs([]string{"recommended", "--from", t.TempDir(), "--dry-run", "--json"})
	command.SilenceUsage = true
	command.SilenceErrors = true

	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	var report userspaceInstallReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode install report: %v\n%s", err, output.String())
	}
	if len(report.Results) != 2 || report.Results[0].Component != userspacemanager.AudioComponent || report.Results[1].Component != userspacemanager.IPTSDComponent {
		t.Fatalf("results = %#v", report.Results)
	}
	if len(installer.calls) != 2 || !installer.calls[0].DryRun || !installer.calls[1].DryRun {
		t.Fatalf("dry-run calls = %#v", installer.calls)
	}
	if len(report.NextSteps) != 1 || !strings.Contains(report.NextSteps[0], "--yes") {
		t.Fatalf("next steps = %#v", report.NextSteps)
	}
}

// TestUserspaceInstallCameraForwardsRepositoryAuthority verifies that native
// camera provenance reaches the installer without affecting other selectors.
func TestUserspaceInstallCameraForwardsRepositoryAuthority(t *testing.T) {
	installer := &cliFakeInstaller{}
	app, _ := newUserspaceInstallTestApplication(installer)
	command := app.newUserspaceInstallCommand()
	authority := strings.Repeat("a", 64)
	command.SetArgs([]string{
		"camera", "--from", t.TempDir(), "--repository-root", "/fixture/oe",
		"--camera-authority-sha256", authority,
		"--dry-run",
	})
	command.SilenceUsage = true
	command.SilenceErrors = true

	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(installer.calls) != 1 || installer.calls[0].RepositoryRoot != "/fixture/oe" ||
		installer.calls[0].CameraAuthoritySHA256 != authority {
		t.Fatalf("installer calls = %#v", installer.calls)
	}
}

// TestUserspaceCameraBuildPrintsIndependentAuthority verifies human delivery
// exposes the digest which must accompany later release or installation work.
func TestUserspaceCameraBuildPrintsIndependentAuthority(t *testing.T) {
	authority := strings.Repeat("b", 64)
	var output bytes.Buffer
	app := &application{out: &output}
	result := userspacebuild.Result{
		Component: userspacebuild.ComponentCamera,
		Camera: &camerabuild.ExecutionReceipt{
			Published:       true,
			OutputDirectory: "/fixture/camera/build",
			AuthoritySHA256: authority,
			Bundle:          &camerabuild.BundleReceipt{PackageVersion: "0.7.0-fixture"},
		},
	}
	if err := app.writeUserspaceBuildResult(result); err != nil {
		t.Fatal(err)
	}
	if text := output.String(); !strings.Contains(text, "authority SHA-256: "+authority) || !strings.Contains(text, camerabuild.ReceiptName) {
		t.Fatalf("camera build output = %s", text)
	}
}

// TestUserspaceInstallHumanReportIncludesRebootAndDoctorGuidance verifies that
// interactive output explains changed files, reboot needs, and follow-up checks.
func TestUserspaceInstallHumanReportIncludesRebootAndDoctorGuidance(t *testing.T) {
	var output bytes.Buffer
	app := &application{out: &output}
	report := makeUserspaceInstallReport([]userspaceinstall.Result{{
		Component:      userspacemanager.AudioComponent,
		Root:           "/",
		Files:          []userspaceinstall.FileChange{{Source: "bundle", Target: "target", Action: "replace"}},
		RebootRequired: true,
	}}, false)
	if err := app.writeUserspaceInstallReport(report); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"installed audio-fullio-v19c", "file: replace target <- bundle", "Reboot", "doctor userspace"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("human report does not contain %q:\n%s", expected, output.String())
		}
	}
}

// TestUserspaceInstallJSONPreservesIncompleteActivation verifies that a
// non-zero command result still emits structured durable installed-file state.
func TestUserspaceInstallJSONPreservesIncompleteActivation(t *testing.T) {
	installer := &cliFakeInstaller{
		iptsdResult: userspaceinstall.Result{
			Component: userspacemanager.IPTSDComponent, Root: "/", FilesInstalled: true,
			ActivationRequired: true, ActivationError: "systemctl failed",
		},
		iptsdError: errors.New("activation incomplete"),
	}
	app, output := newUserspaceInstallTestApplication(installer)
	command := app.newUserspaceInstallCommand()
	command.SetArgs([]string{"iptsd", "--from", t.TempDir(), "--yes", "--json"})
	command.SilenceErrors = true
	command.SilenceUsage = true
	err := command.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected incomplete activation error")
	}
	var report userspaceInstallReport
	if decodeErr := json.Unmarshal(output.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode partial report: %v\n%s", decodeErr, output.String())
	}
	if report.Error == "" || len(report.Results) != 1 || !report.Results[0].FilesInstalled {
		t.Fatalf("partial report = %+v", report)
	}
}

// newUserspaceInstallTestApplication builds a command application around a
// supplied fake installer and returns its captured output buffer.
func newUserspaceInstallTestApplication(installer userspacemanager.Installer) (*application, *bytes.Buffer) {
	loader := userspacecatalog.NewLoader(lexr.UserspaceCatalogFS(), "supported-userspace.json")
	userspace := userspacemanager.New(loader, nil, nil)
	userspace.Installer = installer
	output := &bytes.Buffer{}
	return &application{out: output, userspace: userspace}, output
}
