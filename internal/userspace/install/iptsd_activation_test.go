package install

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// legacyPenProbeRunner models cleanup failures separately from systemd's
// machine-readable state, including blocked commands and later real failures.
type legacyPenProbeRunner struct {
	commands     []platform.Command
	state        string
	disableErr   error
	probeErr     error
	laterErr     error
	blockProbe   bool
	blockDisable bool
}

// Run supplies controlled service state without managing any host services.
func (runner *legacyPenProbeRunner) Run(ctx context.Context, command platform.Command) error {
	runner.commands = append(runner.commands, command)
	if command.Name == "/usr/bin/systemctl" {
		switch command.Args[0] {
		case "disable":
			if runner.blockDisable {
				<-ctx.Done()
				return ctx.Err()
			}
			return runner.disableErr
		case "show":
			if runner.blockProbe {
				<-ctx.Done()
				return ctx.Err()
			}
			_, _ = io.WriteString(command.Stdout, runner.state)
			return runner.probeErr
		case "daemon-reload":
			return runner.laterErr
		}
	}
	return nil
}

// Capture rejects unbounded output collection in the activation policy.
func (*legacyPenProbeRunner) Capture(context.Context, platform.Command) ([]byte, error) {
	return nil, errors.New("unexpected capture")
}

// TestIPTSDMissingLegacyPenCleanup distinguishes a harmless absent unit from
// existing conflicts, uncertain inspection, timeouts and unrelated failures.
func TestIPTSDMissingLegacyPenCleanup(t *testing.T) {
	absent := "LoadState=not-found\nActiveState=inactive\nUnitFileState=\n"
	for _, test := range []struct {
		name         string
		state        string
		probeErr     error
		laterErr     error
		blockProbe   bool
		blockDisable bool
		wantSuccess  bool
	}{
		{name: "absent", state: absent, wantSuccess: true},
		{name: "property order", state: "UnitFileState=\nActiveState=inactive\nLoadState=not-found\n", wantSuccess: true},
		{name: "existing disabled unit", state: "LoadState=loaded\nActiveState=inactive\nUnitFileState=disabled\n"},
		{name: "active unit with missing file", state: "LoadState=not-found\nActiveState=active\nUnitFileState=\n"},
		{name: "enabled unit", state: "LoadState=not-found\nActiveState=inactive\nUnitFileState=enabled\n"},
		{name: "masked unit", state: "LoadState=masked\nActiveState=inactive\nUnitFileState=masked\n"},
		{name: "empty response"},
		{name: "missing property", state: "LoadState=not-found\nActiveState=inactive\n"},
		{name: "duplicate property", state: absent + "LoadState=not-found\n"},
		{name: "unexpected property", state: absent + "Other=value\n"},
		{name: "truncated response", state: absent + strings.Repeat("\n", maximumActivationOutput)},
		{name: "bus failure", state: absent, probeErr: errors.New("bus unavailable")},
		{name: "probe timeout", state: absent, blockProbe: true},
		{name: "cleanup timeout", state: absent, blockDisable: true},
		{name: "later failure", state: absent, laterErr: errors.New("daemon reload failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &legacyPenProbeRunner{
				state: test.state, disableErr: errors.New("translated cleanup failure"),
				probeErr: test.probeErr, laterErr: test.laterErr,
				blockProbe: test.blockProbe, blockDisable: test.blockDisable,
			}
			installer := New(runner)
			installer.activationTimeout = 10 * time.Millisecond
			err := installer.runActivationCommands(context.Background(), cloneInstallCommands(iptsdActivationCommands))
			if (err == nil) != test.wantSuccess {
				t.Fatalf("activation error = %v, want success %v", err, test.wantSuccess)
			}
			if test.blockDisable && len(runner.commands) != len(iptsdActivationCommands) {
				t.Fatal("a timed-out cleanup must not be accepted through an absence probe")
			}
			if test.laterErr != nil && !strings.Contains(err.Error(), "daemon reload failed") {
				t.Fatalf("later failure was suppressed: %v", err)
			}
		})
	}
}

// TestIPTSDSuccessfulLegacyCleanupDoesNotProbe preserves the existing success
// path without extra commands when systemd already completed the cleanup.
func TestIPTSDSuccessfulLegacyCleanupDoesNotProbe(t *testing.T) {
	runner := &legacyPenProbeRunner{}
	if err := New(runner).runActivationCommands(context.Background(), cloneInstallCommands(iptsdActivationCommands)); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != len(iptsdActivationCommands) {
		t.Fatalf("unexpected inspection after successful cleanup: %+v", runner.commands)
	}
}

// TestIPTSDAbsentLegacyServiceReceipt covers the reported fresh-live failure
// through the complete transaction, including its no-command dry run and receipt.
func TestIPTSDAbsentLegacyServiceReceipt(t *testing.T) {
	bundle := makeTestIPTSDBundle(t)
	runner := &legacyPenProbeRunner{
		state:      "LoadState=not-found\nActiveState=inactive\nUnitFileState=\n",
		disableErr: errors.New("Unit g6-pen.service does not exist"),
	}
	installer, _ := configureTestIPTSDInstaller(t, runner)
	installer.isLiveRoot = func(string) bool { return true }
	installer.euid = func() int { return 501 }
	options := Options{BundleDir: bundle, Root: t.TempDir(), DryRun: true}
	if _, err := installer.IPTSD(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 0 {
		t.Fatal("dry run executed service commands")
	}
	options.DryRun = false
	installer.euid = func() int { return 0 }
	result, err := installer.IPTSD(context.Background(), options)
	if err != nil || !result.FilesInstalled || !result.ActivationComplete || result.ActivationError != "" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	data, err := os.ReadFile(result.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	var receipt iptsdReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.FilesInstalled || !receipt.ActivationRequired || !receipt.ActivationComplete || receipt.ActivationError != "" {
		t.Fatalf("incorrect receipt after absent-service cleanup: %+v", receipt)
	}
}
