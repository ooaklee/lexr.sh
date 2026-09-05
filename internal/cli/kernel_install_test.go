package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	kernelinstall "github.com/ooaklee/lexr.sh/internal/kernel/install"
)

const (
	// kernelCLITargetABI is the coherent bundle ABI used by delivery tests.
	kernelCLITargetABI = "7.2.0-sp11v19-qcom-x1e"
	// kernelCLIFallbackABI is the distinct retained ABI used by delivery tests.
	kernelCLIFallbackABI = "6.18.0-sp11v18-qcom-x1e"
	// kernelCLIPackageVersion is the shared Debian version encoded in fixture names.
	kernelCLIPackageVersion = "7.2.0-sp11v19"
)

// stubKernelInstallationManager records delivery requests without running
// package-manager or bootloader commands.
type stubKernelInstallationManager struct {
	// preflightPlan is returned by Preflight.
	preflightPlan kernelinstall.Plan
	// preflightErr is returned by Preflight.
	preflightErr error
	// installReceipt is returned by Install.
	installReceipt kernelinstall.Receipt
	// installErr is returned by Install.
	installErr error
	// preflightRequests records every read-only request.
	preflightRequests []kernelinstall.Request
	// installRequests records every dry-run or mutating request.
	installRequests []kernelinstall.Request
}

// Preflight records request and returns the configured plan and error.
func (stub *stubKernelInstallationManager) Preflight(_ context.Context, request kernelinstall.Request) (kernelinstall.Plan, error) {
	stub.preflightRequests = append(stub.preflightRequests, request)
	return stub.preflightPlan, stub.preflightErr
}

// Install records request and returns the configured receipt and error.
func (stub *stubKernelInstallationManager) Install(_ context.Context, request kernelinstall.Request) (kernelinstall.Receipt, error) {
	stub.installRequests = append(stub.installRequests, request)
	return stub.installReceipt, stub.installErr
}

// TestKernelPreflightCommandPassesExplicitSafetyInputs verifies all delivery
// flags reach the native manager and its plan remains machine-readable.
func TestKernelPreflightCommandPassesExplicitSafetyInputs(t *testing.T) {
	bundleDirectory := kernelCLIBundleDirectory(t)
	root := t.TempDir()
	stub := &stubKernelInstallationManager{preflightPlan: kernelCLIPlan(root, true)}
	app, output := newKernelCLIApplication(stub)

	err := executeKernelCLICommand(t, app.newKernelCommand(),
		"preflight", bundleDirectory,
		"--root", root,
		"--fallback-abi", kernelCLIFallbackABI,
		"--running-abi", kernelCLIFallbackABI,
		"--allow-unverified",
		"--force",
		"--json",
	)
	if err != nil {
		t.Fatalf("kernel preflight error = %v", err)
	}
	if len(stub.preflightRequests) != 1 {
		t.Fatalf("preflight request count = %d, want 1", len(stub.preflightRequests))
	}
	request := stub.preflightRequests[0]
	if request.Bundle.ABI != kernelCLITargetABI || request.Root != root ||
		request.FallbackABI != kernelCLIFallbackABI || request.RunningABI != kernelCLIFallbackABI ||
		!request.DryRun || !request.AllowUnverified || !request.ForceFallbackMismatch || len(request.Bundle.Packages) != 4 {
		t.Fatalf("preflight request = %#v", request)
	}
	var plan kernelinstall.Plan
	if err := json.Unmarshal(output.Bytes(), &plan); err != nil {
		t.Fatalf("preflight JSON cannot be decoded: %v\n%s", err, output.String())
	}
	if plan.TargetABI != kernelCLITargetABI || !plan.DryRun || plan.Root != root {
		t.Fatalf("preflight JSON plan = %#v", plan)
	}
}

// TestKernelForceFallbackMismatchIsAuditable verifies both commands forward
// explicit authority, keep JSON parseable, and emit the warning on stderr.
func TestKernelForceFallbackMismatchIsAuditable(t *testing.T) {
	t.Parallel()
	runningABI := "7.2.0-jg-0sp11v17-qcom-x1e"
	warning := "warning: fallback ABI does not match the running ABI: running " + runningABI + ", fallback " + kernelCLIFallbackABI

	for _, subcommand := range []string{"preflight", "install"} {
		t.Run(subcommand, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			plan := kernelCLIPlan(root, true)
			plan.RunningABI = runningABI
			plan.FallbackMismatchForced = true
			plan.Warnings = []string{warning}
			stub := &stubKernelInstallationManager{
				preflightPlan:  plan,
				installReceipt: kernelinstall.Receipt{Plan: plan},
			}
			output := &bytes.Buffer{}
			errorOutput := &bytes.Buffer{}
			app := &application{out: output, errOut: errorOutput, kernelInstaller: stub}
			args := []string{
				subcommand, kernelCLIBundleDirectory(t),
				"--root", root,
				"--fallback-abi", kernelCLIFallbackABI,
				"--running-abi", runningABI,
				"--allow-unverified",
				"--force",
				"--json",
			}
			if subcommand == "install" {
				args = append(args, "--dry-run")
			}
			if err := executeKernelCLICommand(t, app.newKernelCommand(), args...); err != nil {
				t.Fatalf("kernel %s --force error = %v", subcommand, err)
			}
			var request kernelinstall.Request
			switch subcommand {
			case "preflight":
				if len(stub.preflightRequests) != 1 {
					t.Fatalf("preflight requests = %#v", stub.preflightRequests)
				}
				request = stub.preflightRequests[0]
				var decoded kernelinstall.Plan
				if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || !decoded.FallbackMismatchForced || len(decoded.Warnings) != 1 {
					t.Fatalf("preflight JSON = %s, error = %v", output.String(), err)
				}
			case "install":
				if len(stub.installRequests) != 1 {
					t.Fatalf("install requests = %#v", stub.installRequests)
				}
				request = stub.installRequests[0]
				var decoded kernelinstall.Receipt
				if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || !decoded.Plan.FallbackMismatchForced || len(decoded.Plan.Warnings) != 1 {
					t.Fatalf("install JSON = %s, error = %v", output.String(), err)
				}
			}
			if !request.ForceFallbackMismatch {
				t.Fatalf("kernel %s request did not retain --force: %#v", subcommand, request)
			}
			if errorOutput.String() != warning+"\n" {
				t.Fatalf("kernel %s warning = %q, want %q", subcommand, errorOutput.String(), warning+"\n")
			}
		})
	}
}

// TestKernelCommandsRequireExplicitRootAndFallback verifies Cobra rejects
// incomplete requests before bundle discovery or manager invocation.
func TestKernelCommandsRequireExplicitRootAndFallback(t *testing.T) {
	bundleDirectory := kernelCLIBundleDirectory(t)
	for _, subcommand := range []string{"preflight", "install"} {
		t.Run(subcommand, func(t *testing.T) {
			stub := &stubKernelInstallationManager{}
			app, _ := newKernelCLIApplication(stub)
			err := executeKernelCLICommand(t, app.newKernelCommand(), subcommand, bundleDirectory)
			if err == nil || !strings.Contains(err.Error(), "required flag") ||
				!strings.Contains(err.Error(), "fallback-abi") || !strings.Contains(err.Error(), "root") {
				t.Fatalf("missing required flags error = %v", err)
			}
			if len(stub.preflightRequests) != 0 || len(stub.installRequests) != 0 {
				t.Fatalf("manager was called for incomplete request: %#v / %#v", stub.preflightRequests, stub.installRequests)
			}
		})
	}
}

// TestKernelRunningABIOverrideIsAlternateRootOnly verifies live-root callers
// cannot replace trusted uname evidence with a flag value.
func TestKernelRunningABIOverrideIsAlternateRootOnly(t *testing.T) {
	stub := &stubKernelInstallationManager{}
	app, _ := newKernelCLIApplication(stub)
	err := executeKernelCLICommand(t, app.newKernelCommand(),
		"preflight", kernelCLIBundleDirectory(t),
		"--root", string(filepath.Separator),
		"--fallback-abi", kernelCLIFallbackABI,
		"--running-abi", kernelCLIFallbackABI,
	)
	if err == nil || !strings.Contains(err.Error(), "alternate target root") {
		t.Fatalf("live-root running ABI error = %v", err)
	}
	if len(stub.preflightRequests) != 0 {
		t.Fatalf("manager received rejected live-root override: %#v", stub.preflightRequests)
	}
}

// TestKernelInstallRequiresConfirmationBeforeManagerCall verifies a mutating
// request cannot cross the delivery boundary without --yes.
func TestKernelInstallRequiresConfirmationBeforeManagerCall(t *testing.T) {
	stub := &stubKernelInstallationManager{}
	app, _ := newKernelCLIApplication(stub)
	err := executeKernelCLICommand(t, app.newKernelCommand(),
		"install", kernelCLIBundleDirectory(t),
		"--root", t.TempDir(),
		"--fallback-abi", kernelCLIFallbackABI,
		"--running-abi", kernelCLIFallbackABI,
	)
	if err == nil || !strings.Contains(err.Error(), "requires --yes") {
		t.Fatalf("unconfirmed install error = %v", err)
	}
	if len(stub.installRequests) != 0 {
		t.Fatalf("manager received unconfirmed install: %#v", stub.installRequests)
	}
}

// TestKernelInstallDryRunNeedsNoConfirmation verifies --dry-run reaches the
// manager without --yes and returns an unmodified receipt as JSON.
func TestKernelInstallDryRunNeedsNoConfirmation(t *testing.T) {
	root := t.TempDir()
	receipt := kernelinstall.Receipt{Plan: kernelCLIPlan(root, true)}
	stub := &stubKernelInstallationManager{installReceipt: receipt}
	app, output := newKernelCLIApplication(stub)
	err := executeKernelCLICommand(t, app.newKernelCommand(),
		"install", kernelCLIBundleDirectory(t),
		"--root", root,
		"--fallback-abi", kernelCLIFallbackABI,
		"--running-abi", kernelCLIFallbackABI,
		"--allow-unverified",
		"--dry-run",
		"--json",
	)
	if err != nil {
		t.Fatalf("kernel install --dry-run error = %v", err)
	}
	if len(stub.installRequests) != 1 || !stub.installRequests[0].DryRun || !stub.installRequests[0].AllowUnverified {
		t.Fatalf("dry-run requests = %#v", stub.installRequests)
	}
	var decoded kernelinstall.Receipt
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("dry-run receipt JSON cannot be decoded: %v\n%s", err, output.String())
	}
	if !decoded.Plan.DryRun || decoded.RebootRequired || len(decoded.Executed) != 0 {
		t.Fatalf("dry-run receipt = %#v", decoded)
	}
}

// TestKernelInstallDryRunReportsFallbackDTBBinding keeps the human dry run in
// parity with kernel preflight and its own structured plan.
func TestKernelInstallDryRunReportsFallbackDTBBinding(t *testing.T) {
	root := t.TempDir()
	plan := kernelCLIPlan(root, true)
	plan.Fallback.DeviceTreeBoot.Mode = kernelinstall.DeviceTreeBootEmbedded
	plan.Fallback.DeviceTreeBoot.GRUBEntryCount = 2
	stub := &stubKernelInstallationManager{installReceipt: kernelinstall.Receipt{Plan: plan}}
	app, output := newKernelCLIApplication(stub)
	err := executeKernelCLICommand(t, app.newKernelCommand(),
		"install", kernelCLIBundleDirectory(t),
		"--root", root,
		"--fallback-abi", kernelCLIFallbackABI,
		"--running-abi", kernelCLIFallbackABI,
		"--allow-unverified",
		"--dry-run",
	)
	if err != nil {
		t.Fatalf("kernel install --dry-run error = %v", err)
	}
	for _, expected := range []string{
		"fallback boot device-tree mode: embedded",
		"fallback boot device-tree GRUB entries verified: 2",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("kernel install --dry-run output does not contain %q:\n%s", expected, output.String())
		}
	}
}

// TestKernelInstallJSONDistinguishesPackagedAndBootDTBEvidence keeps the
// machine-readable receipt explicit about installed DTB files versus the
// verified boot-time delivery path.
func TestKernelInstallJSONDistinguishesPackagedAndBootDTBEvidence(t *testing.T) {
	root := t.TempDir()
	plan := kernelCLIPlan(root, false)
	receipt := kernelinstall.Receipt{
		Plan:        plan,
		DeviceTrees: make([]kernelinstall.FileEvidence, 2),
		Installed: &kernelinstall.BootEvidence{
			ABI: kernelCLITargetABI,
			DeviceTreeBoot: kernelinstall.DeviceTreeBootEvidence{
				Mode:           kernelinstall.DeviceTreeBootExternal,
				SHA256:         strings.Repeat("a", 64),
				GRUBEntryCount: 2,
			},
		},
		RebootRequired: true,
	}
	stub := &stubKernelInstallationManager{installReceipt: receipt}
	app, output := newKernelCLIApplication(stub)
	err := executeKernelCLICommand(t, app.newKernelCommand(),
		"install", kernelCLIBundleDirectory(t),
		"--root", root,
		"--fallback-abi", kernelCLIFallbackABI,
		"--running-abi", kernelCLIFallbackABI,
		"--allow-unverified",
		"--yes",
		"--json",
	)
	if err != nil {
		t.Fatalf("kernel install --json error = %v", err)
	}
	var decoded kernelinstall.Receipt
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("kernel install receipt JSON cannot be decoded: %v\n%s", err, output.String())
	}
	if len(decoded.DeviceTrees) != 2 || decoded.Installed == nil ||
		decoded.Installed.DeviceTreeBoot.Mode != kernelinstall.DeviceTreeBootExternal ||
		decoded.Installed.DeviceTreeBoot.GRUBEntryCount != 2 ||
		decoded.Installed.DeviceTreeBoot.SHA256 != strings.Repeat("a", 64) {
		t.Fatalf("kernel install DTB receipt = %#v", decoded)
	}
}

// TestKernelInstallPackageSetSelection verifies complete local bundles are the
// default while callers can explicitly request the runtime pair only.
func TestKernelInstallPackageSetSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		packageSet   string
		wantPackages int
	}{
		{name: "all by default", wantPackages: 4},
		{name: "runtime only", packageSet: "runtime", wantPackages: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			stub := &stubKernelInstallationManager{installReceipt: kernelinstall.Receipt{Plan: kernelCLIPlan(root, true)}}
			app, _ := newKernelCLIApplication(stub)
			args := []string{
				"install", kernelCLIBundleDirectory(t),
				"--root", root,
				"--fallback-abi", kernelCLIFallbackABI,
				"--running-abi", kernelCLIFallbackABI,
				"--allow-unverified",
				"--dry-run",
			}
			if test.packageSet != "" {
				args = append(args, "--package-set", test.packageSet)
			}
			if err := executeKernelCLICommand(t, app.newKernelCommand(), args...); err != nil {
				t.Fatalf("kernel install --dry-run error = %v", err)
			}
			if len(stub.installRequests) != 1 || len(stub.installRequests[0].Bundle.Packages) != test.wantPackages {
				t.Fatalf("install requests = %#v, want %d packages", stub.installRequests, test.wantPackages)
			}
		})
	}
}

// TestKernelDryRunDisclosesConditionalInitramfsRepair verifies both human
// dry-run renderers disclose the bounded conditional repair command count.
func TestKernelDryRunDisclosesConditionalInitramfsRepair(t *testing.T) {
	t.Parallel()

	for _, subcommand := range []string{"preflight", "install"} {
		t.Run(subcommand, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			plan := kernelCLIPlan(root, true)
			plan.ConditionalCommands = []kernelinstall.Command{
				{Operation: kernelinstall.OperationEnsureInitramfs, Name: "/usr/sbin/update-initramfs", Args: []string{"-c", "-k", kernelCLITargetABI}},
			}
			stub := &stubKernelInstallationManager{
				preflightPlan:  plan,
				installReceipt: kernelinstall.Receipt{Plan: plan},
			}
			app, output := newKernelCLIApplication(stub)
			args := []string{
				subcommand, kernelCLIBundleDirectory(t),
				"--root", root,
				"--fallback-abi", kernelCLIFallbackABI,
				"--running-abi", kernelCLIFallbackABI,
				"--allow-unverified",
			}
			if subcommand == "install" {
				args = append(args, "--dry-run")
			}
			if err := executeKernelCLICommand(t, app.newKernelCommand(), args...); err != nil {
				t.Fatalf("kernel %s dry run error = %v", subcommand, err)
			}
			if !strings.Contains(output.String(), "conditional initramfs commands: 1") {
				t.Fatalf("conditional initramfs disclosure missing from output: %q", output.String())
			}
		})
	}
}

// TestKernelInstallRejectsUnknownPackageSet verifies selection remains a closed
// enum rather than a filename expression or arbitrary package filter.
func TestKernelInstallRejectsUnknownPackageSet(t *testing.T) {
	t.Parallel()

	stub := &stubKernelInstallationManager{}
	app, _ := newKernelCLIApplication(stub)
	err := executeKernelCLICommand(t, app.newKernelCommand(),
		"install", kernelCLIBundleDirectory(t),
		"--root", t.TempDir(),
		"--fallback-abi", kernelCLIFallbackABI,
		"--running-abi", kernelCLIFallbackABI,
		"--allow-unverified",
		"--dry-run",
		"--package-set", "linux-.*",
	)
	if err == nil || !strings.Contains(err.Error(), "package set") || !strings.Contains(err.Error(), "all") || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("unknown package-set error = %v", err)
	}
	if len(stub.installRequests) != 0 {
		t.Fatalf("manager received rejected package set: %#v", stub.installRequests)
	}
}

// TestKernelInspectPackageSetSelection verifies read-only bundle inspection
// uses the same complete-by-default role selection as installation.
func TestKernelInspectPackageSetSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		packageSet   string
		wantPackages int
	}{
		{name: "all by default", wantPackages: 4},
		{name: "runtime only", packageSet: "runtime", wantPackages: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			app, output := newKernelCLIApplication(&stubKernelInstallationManager{})
			args := []string{"inspect", kernelCLIBundleDirectory(t), "--json"}
			if test.packageSet != "" {
				args = append(args, "--package-set", test.packageSet)
			}
			if err := executeKernelCLICommand(t, app.newKernelCommand(), args...); err != nil {
				t.Fatalf("kernel inspect error = %v", err)
			}
			var bundle kernel.Bundle
			if err := json.Unmarshal(output.Bytes(), &bundle); err != nil {
				t.Fatalf("inspect JSON cannot be decoded: %v\n%s", err, output.String())
			}
			if len(bundle.Packages) != test.wantPackages {
				t.Fatalf("inspected packages = %d, want %d", len(bundle.Packages), test.wantPackages)
			}
		})
	}
}

// TestKernelPackageSetHumanOutput verifies inspection, preflight, and
// installation distinguish the requested selection policy from the effective
// package roles found in the validated local bundle.
func TestKernelPackageSetHumanOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		command           string
		packageSet        string
		includeHeaders    bool
		wantRequested     kernel.LocalPackageSet
		wantEffective     kernel.LocalPackageSet
		wantPackageCount  int
		mutatingOperation bool
	}{
		{
			name:             "inspect all without available headers",
			command:          "inspect",
			wantRequested:    kernel.LocalPackageSetAll,
			wantEffective:    kernel.LocalPackageSetRuntime,
			wantPackageCount: 2,
		},
		{
			name:             "preflight explicit runtime with available headers",
			command:          "preflight",
			packageSet:       string(kernel.LocalPackageSetRuntime),
			includeHeaders:   true,
			wantRequested:    kernel.LocalPackageSetRuntime,
			wantEffective:    kernel.LocalPackageSetRuntime,
			wantPackageCount: 2,
		},
		{
			name:              "install all with available headers",
			command:           "install",
			includeHeaders:    true,
			wantRequested:     kernel.LocalPackageSetAll,
			wantEffective:     kernel.LocalPackageSetAll,
			wantPackageCount:  4,
			mutatingOperation: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			plan := kernelCLIPlan(root, !test.mutatingOperation)
			if test.wantPackageCount == 4 {
				plan = kernelCLIPlanWithHeaders(root, !test.mutatingOperation)
			}
			stub := &stubKernelInstallationManager{
				preflightPlan: plan,
				installReceipt: kernelinstall.Receipt{
					Plan:    plan,
					Headers: make([]kernelinstall.HeaderEvidence, 2),
					Installed: &kernelinstall.BootEvidence{
						ABI: kernelCLITargetABI,
						DeviceTreeBoot: kernelinstall.DeviceTreeBootEvidence{
							Mode:           kernelinstall.DeviceTreeBootExternal,
							GRUBEntryCount: 1,
						},
					},
					RebootRequired: test.mutatingOperation,
				},
			}
			app, output := newKernelCLIApplication(stub)
			directory := kernelCLIRuntimeBundleDirectory(t)
			if test.includeHeaders {
				directory = kernelCLIBundleDirectory(t)
			}

			args := []string{test.command, directory}
			switch test.command {
			case "preflight":
				args = append(args,
					"--root", root,
					"--fallback-abi", kernelCLIFallbackABI,
					"--running-abi", kernelCLIFallbackABI,
					"--allow-unverified",
				)
			case "install":
				args = append(args,
					"--root", root,
					"--fallback-abi", kernelCLIFallbackABI,
					"--running-abi", kernelCLIFallbackABI,
					"--allow-unverified",
					"--yes",
				)
			}
			if test.packageSet != "" {
				args = append(args, "--package-set", test.packageSet)
			}
			if err := executeKernelCLICommand(t, app.newKernelCommand(), args...); err != nil {
				t.Fatalf("kernel %s error = %v", test.command, err)
			}

			text := output.String()
			for _, expected := range []string{
				"requested package set: " + string(test.wantRequested),
				"effective package set: " + string(test.wantEffective),
				fmt.Sprintf("packages: %d", test.wantPackageCount),
			} {
				if !strings.Contains(text, expected) {
					t.Errorf("kernel %s output does not contain %q:\n%s", test.command, expected, text)
				}
			}
			switch test.command {
			case "inspect":
				for _, expected := range []string{
					"kernel package bundle valid",
					"packaged device trees declared: 2",
					"installed boot binding: not inspected",
				} {
					if !strings.Contains(text, expected) {
						t.Errorf("kernel inspect output does not contain %q:\n%s", expected, text)
					}
				}
			case "preflight":
				if len(stub.preflightRequests) != 1 || len(stub.preflightRequests[0].Bundle.Packages) != test.wantPackageCount {
					t.Errorf("preflight requests = %#v, want %d packages", stub.preflightRequests, test.wantPackageCount)
				}
				if !strings.Contains(text, "fallback boot device-tree mode: grub-external") {
					t.Errorf("kernel preflight output does not report fallback boot DTB verification:\n%s", text)
				}
			case "install":
				if len(stub.installRequests) != 1 || len(stub.installRequests[0].Bundle.Packages) != test.wantPackageCount {
					t.Errorf("install requests = %#v, want %d packages", stub.installRequests, test.wantPackageCount)
				}
				if !strings.Contains(text, "header trees verified: 2") {
					t.Errorf("kernel install output does not report header verification:\n%s", text)
				}
				for _, expected := range []string{
					"packaged device trees verified: 0",
					"boot device-tree mode: grub-external",
					"boot device-tree GRUB entries verified: 1",
				} {
					if !strings.Contains(text, expected) {
						t.Errorf("kernel install output does not report %q:\n%s", expected, text)
					}
				}
			}
		})
	}
}

// TestKernelInstallConfirmationAndFailureReceipt verifies --yes permits the
// manager call while structured rollback evidence survives an operation error.
func TestKernelInstallConfirmationAndFailureReceipt(t *testing.T) {
	root := t.TempDir()
	operationErr := errors.New("package installation failed")
	receipt := kernelinstall.Receipt{
		Plan: kernelCLIPlan(root, false),
		Rollback: &kernelinstall.RollbackReceipt{
			Attempted:    true,
			GRUBRestored: true,
		},
	}
	stub := &stubKernelInstallationManager{installReceipt: receipt, installErr: operationErr}
	app, output := newKernelCLIApplication(stub)
	err := executeKernelCLICommand(t, app.newKernelCommand(),
		"install", kernelCLIBundleDirectory(t),
		"--root", root,
		"--fallback-abi", kernelCLIFallbackABI,
		"--running-abi", kernelCLIFallbackABI,
		"--allow-unverified",
		"--yes",
		"--json",
	)
	if !errors.Is(err, operationErr) {
		t.Fatalf("confirmed install error = %v, want %v", err, operationErr)
	}
	if len(stub.installRequests) != 1 || stub.installRequests[0].DryRun {
		t.Fatalf("confirmed install requests = %#v", stub.installRequests)
	}
	var decoded kernelinstall.Receipt
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("failure receipt JSON cannot be decoded: %v\n%s", err, output.String())
	}
	if decoded.Rollback == nil || !decoded.Rollback.Attempted || !decoded.Rollback.GRUBRestored {
		t.Fatalf("failure receipt lost rollback evidence: %#v", decoded)
	}
}

// TestKernelHelpIncludesNativeCommands verifies the completed native workflows
// are discoverable from the root command without adding implicit actions.
func TestKernelHelpIncludesNativeCommands(t *testing.T) {
	var output bytes.Buffer
	command := NewRootCommand(strings.NewReader(""), &output, &bytes.Buffer{})
	command.SetArgs([]string{"kernel", "--help"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("kernel --help error = %v", err)
	}
	for _, text := range []string{"preflight", "install", "--help"} {
		if !strings.Contains(output.String(), text) {
			t.Errorf("kernel help does not contain %q:\n%s", text, output.String())
		}
	}
}

// TestKernelPackageSetHelpExplainsSelections verifies every local bundle
// command describes the same closed package policy and complete-by-default
// behaviour.
func TestKernelPackageSetHelpExplainsSelections(t *testing.T) {
	t.Parallel()

	for _, subcommand := range []string{"inspect", "preflight", "install"} {
		t.Run(subcommand, func(t *testing.T) {
			t.Parallel()
			app, output := newKernelCLIApplication(&stubKernelInstallationManager{})
			command := app.newKernelCommand()
			command.SetOut(output)
			if err := executeKernelCLICommand(t, command, subcommand, "--help"); err != nil {
				t.Fatalf("kernel %s --help error = %v", subcommand, err)
			}
			for _, expected := range []string{
				"all exact-version packages",
				"matching headers when available",
				"runtime image/modules only",
				`(default "all")`,
			} {
				if !strings.Contains(output.String(), expected) {
					t.Errorf("kernel %s help does not contain %q:\n%s", subcommand, expected, output.String())
				}
			}
		})
	}
}

// TestKernelFallbackForceHelpExplainsSafetyScope keeps the override explicit
// and limited to an already verified bootable fallback ABI.
func TestKernelFallbackForceHelpExplainsSafetyScope(t *testing.T) {
	t.Parallel()
	for _, subcommand := range []string{"preflight", "install"} {
		t.Run(subcommand, func(t *testing.T) {
			t.Parallel()
			app, output := newKernelCLIApplication(&stubKernelInstallationManager{})
			command := app.newKernelCommand()
			command.SetOut(output)
			if err := executeKernelCLICommand(t, command, subcommand, "--help"); err != nil {
				t.Fatalf("kernel %s --help error = %v", subcommand, err)
			}
			for _, expected := range []string{"--force", "verified bootable fallback ABI", "differ from the running ABI"} {
				if !strings.Contains(output.String(), expected) {
					t.Errorf("kernel %s help does not contain %q:\n%s", subcommand, expected, output.String())
				}
			}
		})
	}
}

// kernelCLIBundleDirectory creates one complete coherent local package set used
// to exercise delivery-layer discovery without invoking dpkg.
func kernelCLIBundleDirectory(t *testing.T) string {
	t.Helper()
	return writeKernelCLIBundleDirectory(t, true)
}

// kernelCLIRuntimeBundleDirectory creates a local bundle containing only the
// mandatory runtime package pair.
func kernelCLIRuntimeBundleDirectory(t *testing.T) string {
	t.Helper()
	return writeKernelCLIBundleDirectory(t, false)
}

// writeKernelCLIBundleDirectory creates deterministic package fixtures with an
// optional coherent development-header pair.
func writeKernelCLIBundleDirectory(t *testing.T, includeHeaders bool) string {
	t.Helper()
	directory := t.TempDir()
	packages := map[string]string{
		"linux-image-" + kernelCLITargetABI + "_" + kernelCLIPackageVersion + "_arm64.deb":   "image fixture",
		"linux-modules-" + kernelCLITargetABI + "_" + kernelCLIPackageVersion + "_arm64.deb": "modules fixture",
	}
	if includeHeaders {
		packages["linux-headers-"+kernelCLITargetABI+"_"+kernelCLIPackageVersion+"_arm64.deb"] = "headers fixture"
		packages["linux-qcom-x1e-headers-"+strings.TrimSuffix(kernelCLITargetABI, "-qcom-x1e")+"_"+kernelCLIPackageVersion+"_all.deb"] = "common headers fixture"
	}
	for name, content := range packages {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write package fixture %s: %v", name, err)
		}
	}
	bundlePackages := make([]kernel.Package, 0, len(packages))
	for name, content := range packages {
		role, _, _, err := kernel.ParsePackageName(name)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(content))
		bundlePackages = append(bundlePackages, kernel.Package{
			Role: role, Name: name, Path: filepath.Join(directory, name), SHA256: fmt.Sprintf("%x", digest), Size: int64(len(content)),
		})
	}
	bundle, err := kernel.NewBundle(kernel.BundleOptions{
		Release: "fixture", RequestedBootImageMode: kernel.RequestedBootImageModeStubble,
		EffectiveDTBDelivery: kernel.DTBDeliveryEmbedded, EmbeddedDTBCount: 2,
		DTBSelectionProvenance: &kernel.DTBSelectionProvenance{
			Tool: "stubble", Version: "fixture-1", DatabaseSHA256: strings.Repeat("d", 64),
			StubSHA256: strings.Repeat("1", 64), HelperSHA256: strings.Repeat("2", 64), SBATSHA256: strings.Repeat("3", 64),
			UKifyTool: "ukify", UKifyPackage: "systemd-ukify", UKifyVersion: "258.1-1", UKifySHA256: strings.Repeat("4", 64),
			Selections: []kernel.DeviceTreeSelectionEvidence{
				{Device: "surface-pro-11-x1e-oled", Records: []kernel.DTBSelectionRecord{{Source: "hwids", Compatible: "microsoft,denali", HWIDs: []string{"11111111-1111-5111-8111-111111111111"}}}},
				{Device: "surface-pro-11-x1p-lcd", Records: []kernel.DTBSelectionRecord{{Source: "hwids", Compatible: "microsoft,denali-x1p", HWIDs: []string{"22222222-2222-5222-8222-222222222222"}}}},
			},
		},
		Packages: bundlePackages,
		DeviceTrees: []kernel.DeviceTree{
			{Device: "surface-pro-11-x1e-oled", Basename: "x1e80100-microsoft-denali-oled.dtb", Path: "usr/lib/firmware/" + kernelCLITargetABI + "/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb", CompatibleStrings: []string{"microsoft,denali"}, SHA256: strings.Repeat("e", 64), EmbeddedMatches: 1, Selectors: []kernel.DeviceTreeSelector{{Kind: kernel.DeviceTreeSelectorCompatible, Value: "microsoft,denali"}, {Kind: kernel.DeviceTreeSelectorHWID, Value: "11111111-1111-5111-8111-111111111111"}}, Required: true},
			{Device: "surface-pro-11-x1p-lcd", Basename: "x1p64100-microsoft-denali.dtb", Path: "usr/lib/firmware/" + kernelCLITargetABI + "/device-tree/qcom/x1p64100-microsoft-denali.dtb", CompatibleStrings: []string{"microsoft,denali-x1p"}, SHA256: strings.Repeat("f", 64), EmbeddedMatches: 1, Selectors: []kernel.DeviceTreeSelector{{Kind: kernel.DeviceTreeSelectorCompatible, Value: "microsoft,denali-x1p"}, {Kind: kernel.DeviceTreeSelectorHWID, Value: "22222222-2222-5222-8222-222222222222"}}, Required: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.Create(filepath.Join(directory, "lexr-kernel-bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.WriteJSON(manifest); err != nil {
		_ = manifest.Close()
		t.Fatal(err)
	}
	if err := manifest.Close(); err != nil {
		t.Fatal(err)
	}
	return directory
}

// kernelCLIPlan returns a stable plan suitable for JSON and human-output tests.
func kernelCLIPlan(root string, dryRun bool) kernelinstall.Plan {
	return kernelinstall.Plan{
		Root:               root,
		TargetABI:          kernelCLITargetABI,
		FallbackABI:        kernelCLIFallbackABI,
		RunningABI:         kernelCLIFallbackABI,
		Version:            kernelCLIPackageVersion,
		DryRun:             dryRun,
		UnverifiedAccepted: true,
		Packages: []kernelinstall.Package{
			{Name: "linux-image-" + kernelCLITargetABI + "_" + kernelCLIPackageVersion + "_arm64.deb"},
			{Name: "linux-modules-" + kernelCLITargetABI + "_" + kernelCLIPackageVersion + "_arm64.deb"},
		},
		DeviceTrees: []kernelinstall.DeviceTree{
			{Device: "surface-pro-11-x1e-oled"},
			{Device: "surface-pro-11-x1p-lcd"},
		},
		Fallback: kernelinstall.BootEvidence{
			ABI: kernelCLIFallbackABI,
			DeviceTreeBoot: kernelinstall.DeviceTreeBootEvidence{
				Mode:           kernelinstall.DeviceTreeBootExternal,
				GRUBEntryCount: 1,
			},
		},
		Commands: []kernelinstall.Command{{Operation: kernelinstall.OperationInstallPackages}},
	}
}

// kernelCLIPlanWithHeaders returns a stable plan containing the runtime and
// coherent development-header pairs.
func kernelCLIPlanWithHeaders(root string, dryRun bool) kernelinstall.Plan {
	plan := kernelCLIPlan(root, dryRun)
	plan.Packages = append(plan.Packages,
		kernelinstall.Package{Name: "linux-headers-" + kernelCLITargetABI + "_" + kernelCLIPackageVersion + "_arm64.deb"},
		kernelinstall.Package{Name: "linux-qcom-x1e-headers-" + strings.TrimSuffix(kernelCLITargetABI, "-qcom-x1e") + "_" + kernelCLIPackageVersion + "_all.deb"},
	)
	return plan
}

// newKernelCLIApplication constructs an isolated delivery application around
// one injected native manager and captured standard output.
func newKernelCLIApplication(installer kernelInstallationManager) (*application, *bytes.Buffer) {
	output := &bytes.Buffer{}
	return &application{out: output, errOut: &bytes.Buffer{}, kernelInstaller: installer}, output
}

// executeKernelCLICommand runs one isolated kernel command with deterministic
// context and without Cobra printing usage or diagnostics during assertions.
func executeKernelCLICommand(t *testing.T, command *cobra.Command, arguments ...string) error {
	t.Helper()
	command.SilenceUsage = true
	command.SilenceErrors = true
	command.SetArgs(arguments)
	return command.ExecuteContext(context.Background())
}

// TestKernelOverwriteFlagReachesDeliveryManager keeps the explicit overwrite
// consent plumbing on both delivery commands.
func TestKernelOverwriteFlagReachesDeliveryManager(t *testing.T) {
	t.Parallel()
	bundleDirectory := kernelCLIBundleDirectory(t)
	root := t.TempDir()
	stub := &stubKernelInstallationManager{preflightPlan: kernelCLIPlan(root, true)}
	app, _ := newKernelCLIApplication(stub)
	for _, subcommand := range []string{"preflight", "install"} {
		arguments := []string{subcommand, bundleDirectory, "--root", root, "--fallback-abi", kernelCLIFallbackABI, "--overwrite", "--json"}
		if subcommand == "install" {
			arguments = append(arguments, "--dry-run")
		}
		if err := executeKernelCLICommand(t, app.newKernelCommand(), arguments...); err != nil {
			t.Fatalf("kernel %s --overwrite error = %v", subcommand, err)
		}
	}
	var requests []kernelinstall.Request
	requests = append(requests, stub.preflightRequests...)
	requests = append(requests, stub.installRequests...)
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	for _, request := range requests {
		if !request.Overwrite {
			t.Errorf("request (dry run = %t) did not deliver --overwrite to the manager", request.DryRun)
		}
	}
}

// TestKernelOverwriteHelpExplainsRisk keeps the destructive override explicit
// about what an unsafe overwrite can break.
func TestKernelOverwriteHelpExplainsRisk(t *testing.T) {
	t.Parallel()
	for _, subcommand := range []string{"preflight", "install"} {
		t.Run(subcommand, func(t *testing.T) {
			t.Parallel()
			app, output := newKernelCLIApplication(&stubKernelInstallationManager{})
			command := app.newKernelCommand()
			command.SetOut(output)
			if err := executeKernelCLICommand(t, command, subcommand, "--help"); err != nil {
				t.Fatalf("kernel %s --help error = %v", subcommand, err)
			}
			for _, expected := range []string{"--overwrite", "unsafe overwrite could break the system or prevent returning to the desktop on reboot"} {
				if !strings.Contains(output.String(), expected) {
					t.Errorf("kernel %s help does not contain %q:\n%s", subcommand, expected, output.String())
				}
			}
		})
	}
}

// TestKernelPreflightRendersBlockedTargetDiagnostics proves that a blocked
// fresh-target gate still exposes stable human and JSON evidence.
func TestKernelPreflightRendersBlockedTargetDiagnostics(t *testing.T) {
	t.Parallel()
	bundleDirectory := kernelCLIBundleDirectory(t)
	root := t.TempDir()
	evidence := kernelinstall.TargetStateEvidence{
		ABI:             kernelCLITargetABI,
		Classification:  kernelinstall.TargetStatePartial,
		Reasons:         []string{"package linux-image-" + kernelCLITargetABI + " is half-configured"},
		Recommendations: []string{"repair the interrupted package transaction"},
	}
	stub := &stubKernelInstallationManager{
		preflightPlan: kernelinstall.Plan{Root: root, TargetABI: kernelCLITargetABI, TargetState: &evidence},
		preflightErr:  &kernelinstall.TargetStateError{Evidence: evidence},
	}
	app, output := newKernelCLIApplication(stub)
	err := executeKernelCLICommand(t, app.newKernelCommand(),
		"preflight", bundleDirectory,
		"--root", root,
		"--fallback-abi", kernelCLIFallbackABI,
		"--json",
	)
	if err == nil {
		t.Fatal("blocked preflight must fail")
	}
	var blocked *kernelinstall.TargetStateError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %v, want a TargetStateError", err)
	}
	stdout := output.String()
	for _, expected := range []string{
		`"blocked": true`,
		`"classification": "partial-or-inconsistent"`,
		`"target_state"`,
		"half-configured",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("JSON diagnostics missing %q:\n%s", expected, stdout)
		}
	}
}

// TestKernelPreflightRendersBlockedTargetDiagnosticsForHumans keeps the
// non-JSON blocker readable on stderr without corrupting stdout.
func TestKernelPreflightRendersBlockedTargetDiagnosticsForHumans(t *testing.T) {
	t.Parallel()
	bundleDirectory := kernelCLIBundleDirectory(t)
	root := t.TempDir()
	evidence := kernelinstall.TargetStateEvidence{
		ABI:             kernelCLITargetABI,
		Classification:  kernelinstall.TargetStatePartial,
		Reasons:         []string{"package linux-image-" + kernelCLITargetABI + " is half-configured"},
		Recommendations: []string{"repair the interrupted package transaction"},
	}
	stub := &stubKernelInstallationManager{
		preflightPlan: kernelinstall.Plan{Root: root, TargetABI: kernelCLITargetABI, TargetState: &evidence},
		preflightErr:  &kernelinstall.TargetStateError{Evidence: evidence},
	}
	app, output := newKernelCLIApplication(stub)
	err := executeKernelCLICommand(t, app.newKernelCommand(),
		"preflight", bundleDirectory,
		"--root", root,
		"--fallback-abi", kernelCLIFallbackABI,
	)
	if err == nil {
		t.Fatal("blocked preflight must fail")
	}
	if strings.Contains(output.String(), "half-configured") {
		t.Errorf("human diagnostics must stay on stderr:\n%s", output.String())
	}
	stderr := app.errOut.(*bytes.Buffer).String()
	for _, expected := range []string{"partial-or-inconsistent", "half-configured", "repair the interrupted package transaction"} {
		if !strings.Contains(stderr, expected) {
			t.Errorf("stderr diagnostics missing %q:\n%s", expected, stderr)
		}
	}
}
