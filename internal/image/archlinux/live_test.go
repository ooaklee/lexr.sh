package archlinux

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// TestTerminalPackageLock rejects drift, ambiguous entries and desktop packages.
func TestTerminalPackageLock(t *testing.T) {
	entries, err := lockedPackages()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name, "gnome") || entry.Name == "gdm" {
			t.Fatalf("unexpected package in terminal lock: %s", entry.Name)
		}
	}
	original := append([]byte(nil), packageLockJSON...)
	defer func() { packageLockJSON = original }()
	for _, mutate := range []func([]lockedPackage) []lockedPackage{
		func(p []lockedPackage) []lockedPackage { return append(p, p[0]) },
		func(p []lockedPackage) []lockedPackage { p[0].SHA256 = "unverified"; return p },
		func(p []lockedPackage) []lockedPackage {
			p[0].URL = "http://ca.us.mirror.archlinuxarm.org/pkg"
			return p
		},
		func(p []lockedPackage) []lockedPackage { p[0].Signature = "not-base64"; return p },
		func(p []lockedPackage) []lockedPackage { return nil },
	} {
		candidate := append([]lockedPackage(nil), entries...)
		packageLockJSON, _ = json.Marshal(mutate(candidate))
		if _, err := lockedPackages(); err == nil {
			t.Fatal("accepted an invalid package lock")
		}
	}
}

// TestArchTerminalScriptsHaveValidSyntax checks every embedded executable input.
func TestArchTerminalScriptsHaveValidSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip(err)
	}
	for name, script := range map[string]string{"prepare": prepareRootScript, "live": liveSessionScript, "boot": buildBootScript, "setup": setupScript, "root-inspection": inspectRootScript, "kernel-inspection": inspectKernelPackageScript, "pack": packRootScript, "esp": createESPScript, "iso": createISOScript} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.CommandContext(context.Background(), bash, "-n")
			cmd.Stdin = strings.NewReader(script)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, output)
			}
		})
	}
}

// TestSetupNoninteractiveModes proves help and invalid invocation cannot start
// networking, package installation or disk operations without an operator choice.
func TestSetupNoninteractiveModes(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip(err)
	}
	for _, args := range [][]string{{"--help"}, {"--unknown"}, {}} {
		cmd := exec.Command(bash, append([]string{"-c", setupScript, "lexr-arch-setup"}, args...)...)
		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output
		err := cmd.Run()
		if len(args) > 0 && args[0] == "--help" {
			if err != nil || !strings.Contains(output.String(), "Usage:") {
				t.Fatalf("help failed: %v %s", err, output.String())
			}
		} else if err == nil {
			t.Fatal("noninteractive setup unexpectedly succeeded")
		}
	}
}

// TestArchBuildPlanRequiresPinnedRoot rejects incomplete source identities.
func TestArchBuildPlanRequiresPinnedRoot(t *testing.T) {
	request := Request{SourceRootfs: "/inputs/source.tar.gz", SourceSHA256: strings.Repeat("a", 64), OutputISO: "/outputs/live.iso", Bundle: kernel.Bundle{ABI: "7.2.0-jg-0sp11v23-qcom-x1e"}}
	if _, err := BuildPlan(request); err != nil {
		t.Fatal(err)
	}
	request.SourceSHA256 = ""
	if _, err := BuildPlan(request); err == nil {
		t.Fatal("accepted unpinned Arch rootfs")
	}
}
