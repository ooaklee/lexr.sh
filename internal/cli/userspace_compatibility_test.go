package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/userspace/assessment"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	userspaceinstall "github.com/ooaklee/lexr.sh/internal/userspace/install"
)

// TestConfirmationDoesNotImplyCompatibilityOverride keeps the operator's
// consent to write distinct from accepting unverified compatibility evidence.
func TestConfirmationDoesNotImplyCompatibilityOverride(t *testing.T) {
	for _, allow := range []bool{false, true} {
		installer := &cliFakeInstaller{}
		app, _ := newUserspaceInstallTestApplication(installer)
		command := app.newUserspaceInstallCommand()
		args := []string{"audio", "--from", t.TempDir(), "--yes", "--json"}
		if allow {
			args = append(args, "--allow-unverified-compatibility")
		}
		command.SetArgs(args)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(installer.calls) != 1 || installer.calls[0].AllowUnverifiedCompatibility != allow {
			t.Fatalf("confirmation changed override: %+v", installer.calls)
		}
	}
}

// TestBlockedCompatibilityRetainsStructuredDecision delivers the failed
// preflight evidence without claiming a completed installation or next write.
func TestBlockedCompatibilityRetainsStructuredDecision(t *testing.T) {
	installer := &cliFakeInstaller{iptsdError: errors.New("hard incompatibility"), iptsdResult: userspaceinstall.Result{Component: "iptsd-v1", Root: "/", Compatibility: &assessment.Record{SchemaVersion: 1, Decision: compatibility.Decision{Status: compatibility.Incompatible, TargetIndex: -1, Reasons: []string{"wrong patch line"}}}}}
	app, output := newUserspaceInstallTestApplication(installer)
	command := app.newUserspaceInstallCommand()
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetArgs([]string{"iptsd", "--from", t.TempDir(), "--yes", "--allow-unverified-compatibility", "--json"})
	if err := command.ExecuteContext(context.Background()); err == nil {
		t.Fatal("hard incompatibility became success")
	}
	var report userspaceInstallReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Error == "" || len(report.Results) != 1 || report.Results[0].Compatibility.Decision.Status != compatibility.Incompatible || report.Results[0].FilesInstalled || len(report.NextSteps) != 0 {
		t.Fatalf("incorrect failure report: %+v", report)
	}
}
