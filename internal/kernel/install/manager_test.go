package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

const (
	// fixtureTargetABI is the current integration release used by installation tests.
	fixtureTargetABI = "7.2.0-jg-0sp11v19-qcom-x1e"
	// fixtureFallbackABI is a distinct earlier Surface ABI kept bootable in fixtures.
	fixtureFallbackABI = "7.2.0-jg-0sp11v18-qcom-x1e"

	// altTargetABI is a third distinct ABI for overwrite-guard tests so the
	// renamed target never collides with the fixture fallback ABI.
	altTargetABI = "7.2.0-jg-0sp11v17-qcom-x1e"
	// fixtureVersion is the Debian package version shared by the target pair.
	fixtureVersion = "7.2.0-jg-0sp11v19"
)

// fakeRunner records direct commands and supplies deterministic Debian metadata.
type fakeRunner struct {
	mu             sync.Mutex
	runs           []platform.Command
	captures       []platform.Command
	root           string
	mutateAfter    string
	mutatePath     string
	runHook        func(context.Context, platform.Command) error
	metadataHook   func(string, string) (string, bool)
	captureFailure error
}

// Run records one mutating command and delegates fixture changes to runHook.
func (runner *fakeRunner) Run(ctx context.Context, command platform.Command) error {
	if command.Name == unameCommand || command.Name == dpkgDebCommand {
		runner.mu.Lock()
		runner.captures = append(runner.captures, clonePlatformCommand(command))
		runner.mu.Unlock()
		output, err := runner.fixtureCapture(command)
		if err != nil {
			return err
		}
		if command.Stdout == nil {
			return errors.New("bounded capture did not provide stdout")
		}
		_, err = command.Stdout.Write(output)
		return err
	}
	runner.mu.Lock()
	runner.runs = append(runner.runs, clonePlatformCommand(command))
	runner.mu.Unlock()
	if runner.runHook != nil {
		return runner.runHook(ctx, command)
	}
	return nil
}

// Capture returns uname or dpkg-deb fixture output without executing a process.
func (runner *fakeRunner) Capture(_ context.Context, command platform.Command) ([]byte, error) {
	runner.mu.Lock()
	runner.captures = append(runner.captures, clonePlatformCommand(command))
	runner.mu.Unlock()
	return runner.fixtureCapture(command)
}

// fixtureCapture supplies the bytes for one recorded read-only test command.
func (runner *fakeRunner) fixtureCapture(command platform.Command) ([]byte, error) {
	if runner.captureFailure != nil {
		return nil, runner.captureFailure
	}
	if command.Name == unameCommand {
		return []byte(fixtureFallbackABI + "\n"), nil
	}
	if command.Name != dpkgDebCommand || len(command.Args) != 3 {
		return nil, fmt.Errorf("unexpected capture: %s %v", command.Name, command.Args)
	}
	name := filepath.Base(command.Args[1])
	field := command.Args[2]
	if runner.metadataHook != nil {
		if value, handled := runner.metadataHook(name, field); handled {
			return []byte(value + "\n"), nil
		}
	}
	value, err := fixtureMetadata(name, field)
	if err != nil {
		return nil, err
	}
	if runner.mutateAfter == field && runner.mutatePath != "" {
		if err := os.WriteFile(runner.mutatePath, []byte("changed after metadata inspection"), 0o600); err != nil {
			return nil, err
		}
		runner.mutateAfter = ""
	}
	return []byte(value + "\n"), nil
}

// clonePlatformCommand copies argument slices before a test can mutate them.
func clonePlatformCommand(command platform.Command) platform.Command {
	command.Args = append([]string(nil), command.Args...)
	return command
}

// fixtureMetadata returns coherent control fields for fixture package names.
func fixtureMetadata(name, field string) (string, error) {
	role, _, version, err := kernel.ParsePackageName(name)
	if err != nil {
		return "", err
	}
	packageName := strings.TrimSuffix(name, "_"+version+"_arm64.deb")
	if role == kernel.RoleCommonHeaders {
		packageName = strings.TrimSuffix(name, "_"+version+"_all.deb")
	}
	switch field {
	case "Package":
		return packageName, nil
	case "Version":
		return version, nil
	case "Architecture":
		if role == kernel.RoleCommonHeaders {
			return "all", nil
		}
		return "arm64", nil
	case "Depends":
		abi := fixtureTargetABI
		if strings.Contains(name, altTargetABI) {
			abi = altTargetABI
		}
		switch role {
		case kernel.RoleImage:
			return "kmod, linux-modules-" + abi, nil
		case kernel.RoleHeaders:
			return "linux-qcom-x1e-headers-" + strings.TrimSuffix(abi, "-qcom-x1e") + " (= " + version + ")", nil
		default:
			return "", nil
		}
	default:
		return "", fmt.Errorf("unexpected metadata field %s", field)
	}
}

// fixtureEnvironment creates a bootable fallback root and coherent package bundle.
func fixtureEnvironment(t *testing.T) (string, kernel.Bundle) {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "boot/vmlinuz-"+fixtureFallbackABI), "fallback kernel")
	writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureFallbackABI), "fallback initramfs")
	writeFixtureFile(t, filepath.Join(root, "boot/System.map-"+fixtureFallbackABI), "fallback symbols")
	writeFixtureFile(t, filepath.Join(root, "boot/config-"+fixtureFallbackABI), "fallback config")
	writeFixtureFile(t, filepath.Join(root, "usr/lib/modules", fixtureFallbackABI, "modules.dep"), "kernel/fallback.ko.zst:\n")
	writeFixtureFile(t, filepath.Join(root, "usr/lib/modules", fixtureFallbackABI, "kernel/fallback.ko.zst"), "fallback module")
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), fixtureGRUB(false))
	if err := os.MkdirAll(filepath.Join(root, "var/tmp"), 0o1777); err != nil {
		t.Fatal(err)
	}

	packageDirectory := t.TempDir()
	packages := []kernel.Package{
		fixturePackage(t, packageDirectory, kernel.RoleImage, "image package"),
		fixturePackage(t, packageDirectory, kernel.RoleModules, "modules package"),
	}
	bundle, err := kernel.NewBundle("sp11-v19", "", packages)
	if err != nil {
		t.Fatal(err)
	}
	return root, bundle
}

// fixtureBundleWithHeaders adds the coherent development-header pair emitted by
// complete native kernel builds.
func fixtureBundleWithHeaders(t *testing.T, bundle kernel.Bundle) kernel.Bundle {
	t.Helper()
	directory := filepath.Dir(bundle.Packages[0].Path)
	bundle.Packages = append(bundle.Packages,
		fixturePackage(t, directory, kernel.RoleHeaders, "flavour headers"),
		fixturePackage(t, directory, kernel.RoleCommonHeaders, "common headers"),
	)
	return bundle
}

// fixturePackage writes one package-shaped immutable fixture and records its digest.
func fixturePackage(t *testing.T, directory string, role kernel.PackageRole, content string) kernel.Package {
	t.Helper()
	var name string
	switch role {
	case kernel.RoleImage:
		name = "linux-image-" + fixtureTargetABI + "_" + fixtureVersion + "_arm64.deb"
	case kernel.RoleModules:
		name = "linux-modules-" + fixtureTargetABI + "_" + fixtureVersion + "_arm64.deb"
	case kernel.RoleHeaders:
		name = "linux-headers-" + fixtureTargetABI + "_" + fixtureVersion + "_arm64.deb"
	case kernel.RoleCommonHeaders:
		name = "linux-qcom-x1e-headers-" + strings.TrimSuffix(fixtureTargetABI, "-qcom-x1e") + "_" + fixtureVersion + "_all.deb"
	default:
		t.Fatalf("unsupported fixture role %s", role)
	}
	path := filepath.Join(directory, name)
	writeFixtureFile(t, path, content)
	digest := sha256.Sum256([]byte(content))
	return kernel.Package{
		Role:     role,
		Name:     name,
		Path:     path,
		SHA256:   hex.EncodeToString(digest[:]),
		Size:     int64(len(content)),
		Verified: true,
	}
}

// writeFixtureFile creates all parents and writes one regular test file.
func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixtureGRUB renders one fallback entry and, optionally, one target entry.
func fixtureGRUB(includeTarget bool) string {
	text := "menuentry 'Ubuntu " + fixtureFallbackABI + "' {\n" +
		" linux /boot/vmlinuz-" + fixtureFallbackABI + " root=fixture\n" +
		" initrd /boot/initrd.img-" + fixtureFallbackABI + "\n}\n"
	if includeTarget {
		text += "menuentry 'Ubuntu " + fixtureTargetABI + "' {\n" +
			" linux /boot/vmlinuz-" + fixtureTargetABI + " root=fixture\n" +
			" initrd /boot/initrd.img-" + fixtureTargetABI + "\n}\n"
	}
	return text
}

// fixtureRequest returns the explicit alternate-root request used by most tests.
func fixtureRequest(root string, bundle kernel.Bundle, dryRun bool) Request {
	return Request{
		Bundle:      bundle,
		Root:        root,
		FallbackABI: fixtureFallbackABI,
		RunningABI:  fixtureFallbackABI,
		DryRun:      dryRun,
	}
}

// fixtureManager constructs a non-root test manager with deterministic time.
func fixtureManager(runner *fakeRunner) *Manager {
	manager := New(runner)
	manager.effectiveUID = func() int { return 501 }
	manager.now = func() time.Time { return time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC) }
	return manager
}

// installFixtureTarget simulates Debian maintainer scripts for the target ABI.
func installFixtureTarget(root string) error {
	files := map[string]string{
		filepath.Join(root, "boot/vmlinuz-"+fixtureTargetABI):                                                            "target kernel",
		filepath.Join(root, "boot/System.map-"+fixtureTargetABI):                                                         "target symbols",
		filepath.Join(root, "boot/config-"+fixtureTargetABI):                                                             "target config",
		filepath.Join(root, "usr/lib/modules", fixtureTargetABI, "modules.dep"):                                          "kernel/target.ko.zst:\n",
		filepath.Join(root, "usr/lib/modules", fixtureTargetABI, "kernel/target.ko.zst"):                                 "target module",
		filepath.Join(root, "usr/lib/firmware", fixtureTargetABI, "device-tree/qcom/x1e80100-microsoft-denali-oled.dtb"): "oled dtb",
		filepath.Join(root, "usr/lib/firmware", fixtureTargetABI, "device-tree/qcom/x1p64100-microsoft-denali.dtb"):      "lcd dtb",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// installFixtureHeaders simulates the exact non-symlink source trees and
// top-level marker files installed by the two development-header packages.
func installFixtureHeaders(root string) error {
	base := strings.TrimSuffix(fixtureTargetABI, "-qcom-x1e")
	files := map[string]string{
		filepath.Join(root, "usr/src/linux-headers-"+fixtureTargetABI, "Makefile"): "flavour header makefile",
		filepath.Join(root, "usr/src/linux-qcom-x1e-headers-"+base, "Makefile"):    "common header makefile",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// removeFixtureTarget simulates purging only the failed target ABI packages.
func removeFixtureTarget(root string) error {
	base := strings.TrimSuffix(fixtureTargetABI, "-qcom-x1e")
	paths := []string{
		filepath.Join(root, "boot/vmlinuz-"+fixtureTargetABI),
		filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI),
		filepath.Join(root, "boot/System.map-"+fixtureTargetABI),
		filepath.Join(root, "boot/config-"+fixtureTargetABI),
		filepath.Join(root, "usr/lib/modules", fixtureTargetABI),
		filepath.Join(root, "usr/lib/firmware", fixtureTargetABI),
		filepath.Join(root, "usr/src/linux-headers-"+fixtureTargetABI),
		filepath.Join(root, "usr/src/linux-qcom-x1e-headers-"+base),
	}
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

// TestPreflightProducesExactDryRunPlan verifies current v19 runtime-only policy.
func TestPreflightProducesExactDryRunPlan(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	runner := &fakeRunner{root: root}
	manager := fixtureManager(runner)
	plan, err := manager.Preflight(context.Background(), fixtureRequest(root, bundle, true))
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetABI != fixtureTargetABI || plan.FallbackABI != fixtureFallbackABI || !plan.DryRun {
		t.Fatalf("unexpected plan identity: %+v", plan)
	}
	if len(plan.Packages) != 2 || plan.Packages[0].Role != kernel.RoleModules || plan.Packages[1].Role != kernel.RoleImage {
		t.Fatalf("unexpected package order: %+v", plan.Packages)
	}
	if len(plan.DeviceTrees) != 2 || len(plan.Commands) != 2 {
		t.Fatalf("unexpected plan coverage: %+v", plan)
	}
	if filepath.Base(plan.Commands[0].Name) != "chroot" || filepath.Base(plan.Commands[1].Name) != "chroot" {
		t.Fatalf("alternate-root commands = %+v", plan.Commands)
	}
	if len(plan.Commands[0].Args) < 3 || plan.Commands[0].Args[1] != "/usr/bin/dpkg" || plan.Commands[0].Args[2] != "--install" {
		t.Fatalf("alternate-root package command is not chroot-isolated: %+v", plan.Commands[0])
	}
	serialised := fmt.Sprintf("%+v", plan.Commands)
	for _, retired := range []string{"mshw0485_touch", "spi-geni-qcom.ko", "install-sp11-support", "bash", "sh -c"} {
		if strings.Contains(serialised, retired) {
			t.Fatalf("plan contains retired workaround %q: %s", retired, serialised)
		}
	}
}

// TestDryRunNeedsNoPrivilegeAndPerformsNoMutation verifies the privilege boundary.
func TestDryRunNeedsNoPrivilegeAndPerformsNoMutation(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	runner := &fakeRunner{root: root}
	manager := fixtureManager(runner)
	receipt, err := manager.Install(context.Background(), fixtureRequest(root, bundle, true))
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Executed) != 0 || receipt.RebootRequired {
		t.Fatalf("dry-run receipt executed mutations: %+v", receipt)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("dry run invoked runner.Run: %+v", runner.runs)
	}
	if _, err := os.Lstat(filepath.Join(root, "boot/vmlinuz-"+fixtureTargetABI)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created target kernel: %v", err)
	}
}

// TestInstallRequiresRootAfterPreflight verifies no mutating command runs unprivileged.
func TestInstallRequiresRootAfterPreflight(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	runner := &fakeRunner{root: root}
	manager := fixtureManager(runner)
	_, err := manager.Install(context.Background(), fixtureRequest(root, bundle, false))
	if err == nil || !strings.Contains(err.Error(), "effective UID 0") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("unprivileged install invoked mutation: %+v", runner.runs)
	}
}

// TestInstallStagesPackagesAndVerifiesBootEvidence exercises the complete happy path.
func TestInstallStagesPackagesAndVerifiesBootEvidence(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	runner := &fakeRunner{root: root}
	runner.runHook = func(_ context.Context, command platform.Command) error {
		switch {
		case slicesContain(command.Args, "--install"):
			for _, argument := range command.Args {
				if strings.HasSuffix(argument, ".deb") && !strings.Contains(argument, stagingPrefix) {
					return fmt.Errorf("package was not staged: %s", argument)
				}
			}
			if err := installFixtureTarget(root); err != nil {
				return err
			}
			writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), fixtureGRUB(true))
		case command.Name == chrootCommand && slicesContain(command.Args, updateInitramfsCommand):
			writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI), "target initramfs")
		}
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(context.Background(), fixtureRequest(root, bundle, false))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Installed == nil || receipt.Installed.ABI != fixtureTargetABI || len(receipt.DeviceTrees) != 2 || !receipt.RebootRequired {
		t.Fatalf("unexpected install receipt: %+v", receipt)
	}
	if len(receipt.Executed) != 2 || receipt.Rollback != nil {
		t.Fatalf("unexpected command receipt: %+v", receipt)
	}
}

// TestInstallFourPackageSetVerifiesHeaderTrees exercises immutable staging,
// dependency-friendly ordering, and post-install evidence for both headers.
func TestInstallFourPackageSetVerifiesHeaderTrees(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	bundle = fixtureBundleWithHeaders(t, bundle)
	runner := &fakeRunner{root: root}
	runner.runHook = func(_ context.Context, command platform.Command) error {
		switch {
		case slicesContain(command.Args, "--install"):
			var packages []string
			for _, argument := range command.Args {
				if strings.HasSuffix(argument, ".deb") {
					if !strings.Contains(argument, stagingPrefix) {
						return fmt.Errorf("package was not staged: %s", argument)
					}
					packages = append(packages, filepath.Base(argument))
				}
			}
			expected := expectedPackageNames(fixtureTargetABI)
			want := []string{
				expected[kernel.RoleModules] + "_" + fixtureVersion + "_arm64.deb",
				expected[kernel.RoleImage] + "_" + fixtureVersion + "_arm64.deb",
				expected[kernel.RoleCommonHeaders] + "_" + fixtureVersion + "_all.deb",
				expected[kernel.RoleHeaders] + "_" + fixtureVersion + "_arm64.deb",
			}
			if strings.Join(packages, "\n") != strings.Join(want, "\n") {
				return fmt.Errorf("package order = %v, want %v", packages, want)
			}
			if err := installFixtureTarget(root); err != nil {
				return err
			}
			if err := installFixtureHeaders(root); err != nil {
				return err
			}
			writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), fixtureGRUB(true))
		case command.Name == chrootCommand && slicesContain(command.Args, updateInitramfsCommand):
			writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI), "target initramfs")
		}
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(context.Background(), fixtureRequest(root, bundle, false))
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Headers) != 2 || receipt.Headers[0].Role != kernel.RoleCommonHeaders || receipt.Headers[1].Role != kernel.RoleHeaders {
		t.Fatalf("unexpected header evidence: %+v", receipt.Headers)
	}
	for _, header := range receipt.Headers {
		if header.DebianPackage == "" || header.TreePath == "" || header.Marker.Size <= 0 || header.Marker.SHA256 == "" {
			t.Errorf("incomplete header evidence: %+v", header)
		}
	}
}

// TestMissingSelectedHeadersRollsBackFourPackages proves an apparently
// successful package command cannot leave an unverified development bundle.
func TestMissingSelectedHeadersRollsBackFourPackages(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	bundle = fixtureBundleWithHeaders(t, bundle)
	originalGRUB, err := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{root: root}
	runner.runHook = func(_ context.Context, command platform.Command) error {
		switch {
		case slicesContain(command.Args, "--install"):
			if err := installFixtureTarget(root); err != nil {
				return err
			}
			writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), fixtureGRUB(true))
		case command.Name == chrootCommand && slicesContain(command.Args, updateInitramfsCommand):
			writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI), "target initramfs")
		case slicesContain(command.Args, "--purge"):
			return removeFixtureTarget(root)
		}
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(context.Background(), fixtureRequest(root, bundle, false))
	if err == nil || !strings.Contains(err.Error(), "linux-qcom-x1e-headers-") {
		t.Fatalf("missing-header error = %v", err)
	}
	if receipt.Rollback == nil || !receipt.Rollback.GRUBRestored || receipt.Rollback.Error != "" || len(receipt.Rollback.Commands) != 1 {
		t.Fatalf("unexpected rollback receipt: %+v", receipt.Rollback)
	}
	purge := receipt.Rollback.Commands[0]
	for _, item := range receipt.Plan.Packages {
		if !slicesContain(purge.Args, item.DebianPackage) {
			t.Errorf("rollback omitted %s: %+v", item.DebianPackage, purge)
		}
	}
	restored, readErr := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
	if readErr != nil || string(restored) != string(originalGRUB) {
		t.Fatalf("GRUB was not restored: %q, %v", restored, readErr)
	}
}

// TestPreflightRejectsStaleHeaderTrees verifies freshness uses both exact
// package-installed /usr/src names, including the qcom common-header prefix.
func TestPreflightRejectsStaleHeaderTrees(t *testing.T) {
	base := strings.TrimSuffix(fixtureTargetABI, "-qcom-x1e")
	tests := []string{
		"usr/src/linux-headers-" + fixtureTargetABI,
		"usr/src/linux-qcom-x1e-headers-" + base,
	}
	for _, relative := range tests {
		t.Run(filepath.Base(relative), func(t *testing.T) {
			root, bundle := fixtureEnvironment(t)
			writeFixtureFile(t, filepath.Join(root, relative, "Makefile"), "stale header tree")
			runner := &fakeRunner{root: root}
			_, err := fixtureManager(runner).Preflight(context.Background(), fixtureRequest(root, bundle, true))
			if err == nil || !strings.Contains(err.Error(), filepath.Join(root, relative)) {
				t.Fatalf("stale-header error = %v", err)
			}
			if len(runner.runs) != 0 {
				t.Fatalf("stale-header preflight mutated target: %+v", runner.runs)
			}
		})
	}
}

// TestInstallPreservesPackagePostinstDeviceTree verifies Lexr does not run a
// redundant final update-grub that would erase a package hook's DTB injection.
func TestInstallPreservesPackagePostinstDeviceTree(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	injected := " devicetree /boot/sp11-denali.dtb\n"
	postinstGRUB := strings.Replace(
		fixtureGRUB(true),
		" initrd /boot/initrd.img-"+fixtureTargetABI+"\n",
		injected+" initrd /boot/initrd.img-"+fixtureTargetABI+"\n",
		1,
	)
	runner := &fakeRunner{root: root}
	runner.runHook = func(_ context.Context, command platform.Command) error {
		switch {
		case slicesContain(command.Args, "--install"):
			if err := installFixtureTarget(root); err != nil {
				return err
			}
			writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), postinstGRUB)
		case command.Name == chrootCommand && slicesContain(command.Args, updateInitramfsCommand):
			writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI), "target initramfs")
		}
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(context.Background(), fixtureRequest(root, bundle, false))
	if err != nil {
		t.Fatal(err)
	}
	grub, err := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(grub), injected) {
		t.Fatalf("package postinst DTB injection was not preserved: %q", grub)
	}
	if len(receipt.Executed) != 2 {
		t.Fatalf("install executed redundant commands: %+v", receipt.Executed)
	}
}

// TestInstallEnsuresInitramfsWhenMaintainerScriptsSkipIt proves the manager
// repairs a staged install that completed without the target ABI initramfs.
func TestInstallEnsuresInitramfsWhenMaintainerScriptsSkipIt(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	runner := &fakeRunner{root: root}
	runner.runHook = func(_ context.Context, command platform.Command) error {
		switch {
		case slicesContain(command.Args, "--install"):
			if err := installFixtureTarget(root); err != nil {
				return err
			}
			writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), fixtureGRUB(true))
		case command.Name == chrootCommand && slicesContain(command.Args, updateInitramfsCommand):
			if slicesContain(command.Args, "-c") {
				writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI), "repaired initramfs")
			}
		}
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(context.Background(), fixtureRequest(root, bundle, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI)); statErr != nil {
		t.Fatalf("initramfs repair did not produce the ABI image: %v", statErr)
	}
	operations := make([]Operation, 0, len(receipt.Executed))
	for _, command := range receipt.Executed {
		operations = append(operations, command.Operation)
	}
	if !slices.Contains(operations, OperationEnsureInitramfs) {
		t.Fatalf("receipt omitted the ensure-initramfs repair: %+v", receipt.Executed)
	}
	if !receipt.RebootRequired {
		t.Fatal("repaired installation did not request a reboot")
	}
}

// TestPlanDisclosesConditionalInitramfsRepair verifies the dry run discloses
// the bounded create command that a missing-image install may later execute.
func TestPlanDisclosesConditionalInitramfsRepair(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	runner := &fakeRunner{root: root}
	plan, err := fixtureManager(runner).Preflight(context.Background(), fixtureRequest(root, bundle, true))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ConditionalCommands) != 1 {
		t.Fatalf("unexpected conditional commands: %+v", plan.ConditionalCommands)
	}
	command := plan.ConditionalCommands[0]
	if command.Operation != OperationEnsureInitramfs || !slices.Contains(command.Args, "-c") ||
		!slices.Contains(command.Args, fixtureTargetABI) {
		t.Fatalf("conditional repair command is not an exact-ABI create: %+v", command)
	}
}

// TestInstallInitramfsRepairFailureRollsBack proves a failing create command
// cannot reach the reboot hand-off and triggers the bounded rollback path.
func TestInstallInitramfsRepairFailureRollsBack(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	originalGRUB, err := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{root: root}
	runner.runHook = func(_ context.Context, command platform.Command) error {
		switch {
		case slicesContain(command.Args, "--install"):
			if err := installFixtureTarget(root); err != nil {
				return err
			}
			writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), fixtureGRUB(true))
		case command.Name == chrootCommand && slicesContain(command.Args, updateInitramfsCommand):
			if slicesContain(command.Args, "-c") {
				writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI), "partially written initramfs")
				return errors.New("fixture ensure-initramfs failure")
			}
		case slicesContain(command.Args, "--purge"):
			return removeFixtureTarget(root)
		}
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(context.Background(), fixtureRequest(root, bundle, false))
	if err == nil || !strings.Contains(err.Error(), "fixture ensure-initramfs failure") {
		t.Fatalf("ensure-initramfs error = %v", err)
	}
	if receipt.Rollback == nil || !receipt.Rollback.Attempted || !receipt.Rollback.GRUBRestored || receipt.Rollback.Error != "" {
		t.Fatalf("unexpected rollback receipt: %+v", receipt.Rollback)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partially written initramfs was not removed: %v", statErr)
	}
	restored, readErr := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
	if readErr != nil || string(restored) != string(originalGRUB) {
		t.Fatalf("GRUB was not restored: %q, %v", restored, readErr)
	}
}

// TestInstallWithoutInitramfsFailsVerificationAndRollsBack proves the manager
// never passes verification when the create command succeeds but still leaves
// no initramfs image, keeping the postcondition evidence fail-closed.
func TestInstallWithoutInitramfsFailsVerificationAndRollsBack(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	originalGRUB, err := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{root: root}
	runner.runHook = func(_ context.Context, command platform.Command) error {
		switch {
		case slicesContain(command.Args, "--install"):
			if err := installFixtureTarget(root); err != nil {
				return err
			}
			writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), fixtureGRUB(true))
		case slicesContain(command.Args, "--purge"):
			return removeFixtureTarget(root)
		}
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(context.Background(), fixtureRequest(root, bundle, false))
	if err == nil || !strings.Contains(err.Error(), "initrd.img-"+fixtureTargetABI) {
		t.Fatalf("missing-initramfs error = %v", err)
	}
	if receipt.Rollback == nil || !receipt.Rollback.GRUBRestored {
		t.Fatalf("unexpected rollback receipt: %+v", receipt.Rollback)
	}
	restored, readErr := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
	if readErr != nil || string(restored) != string(originalGRUB) {
		t.Fatalf("GRUB was not restored: %q, %v", restored, readErr)
	}
}

// TestFailurePurgesOnlyTargetAndRestoresGRUB verifies bounded best-effort rollback.
func TestFailurePurgesOnlyTargetAndRestoresGRUB(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	originalGRUB, err := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{root: root}
	runner.runHook = func(ctx context.Context, command platform.Command) error {
		switch {
		case slicesContain(command.Args, "--install"):
			if err := installFixtureTarget(root); err != nil {
				return err
			}
			writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), "damaged by failed transaction")
		case command.Name == chrootCommand && slicesContain(command.Args, updateInitramfsCommand):
			return errors.New("fixture initramfs failure")
		case slicesContain(command.Args, "--purge"):
			if ctx.Err() != nil {
				return fmt.Errorf("rollback inherited cancellation: %w", ctx.Err())
			}
			return removeFixtureTarget(root)
		}
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(context.Background(), fixtureRequest(root, bundle, false))
	if err == nil || !strings.Contains(err.Error(), "fixture initramfs failure") {
		t.Fatalf("error = %v", err)
	}
	if receipt.Rollback == nil || !receipt.Rollback.Attempted || !receipt.Rollback.GRUBRestored || receipt.Rollback.Error != "" {
		t.Fatalf("unexpected rollback receipt: %+v", receipt.Rollback)
	}
	restored, readErr := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
	if readErr != nil || string(restored) != string(originalGRUB) {
		t.Fatalf("GRUB was not restored: %q, %v", restored, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "boot/vmlinuz-"+fixtureFallbackABI)); statErr != nil {
		t.Fatalf("fallback kernel was removed: %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "boot/vmlinuz-"+fixtureTargetABI)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target kernel remained after rollback: %v", statErr)
	}
}

// TestCancelledInstallUsesIndependentRollbackContext verifies cancellation recovery.
func TestCancelledInstallUsesIndependentRollbackContext(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	runner := &fakeRunner{root: root}
	runner.runHook = func(ctx context.Context, command platform.Command) error {
		if slicesContain(command.Args, "--install") {
			if err := installFixtureTarget(root); err != nil {
				return err
			}
			return context.Canceled
		}
		if slicesContain(command.Args, "--purge") {
			if ctx.Err() != nil {
				return errors.New("rollback context was already cancelled")
			}
			return removeFixtureTarget(root)
		}
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(context.Background(), fixtureRequest(root, bundle, false))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if receipt.Rollback == nil || !receipt.Rollback.GRUBRestored || receipt.Rollback.Error != "" {
		t.Fatalf("unexpected cancellation rollback: %+v", receipt.Rollback)
	}
}

// TestSourceMutationAfterPreflightIsRejectedBeforeMutation verifies TOCTOU defence.
func TestSourceMutationAfterPreflightIsRejectedBeforeMutation(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	image, ok := bundle.Package(kernel.RoleImage)
	if !ok {
		t.Fatal("fixture image package missing")
	}
	runner := &fakeRunner{root: root, mutateAfter: "Depends", mutatePath: image.Path}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	_, err := manager.Install(context.Background(), fixtureRequest(root, bundle, false))
	if err == nil || !strings.Contains(err.Error(), "changed after preflight") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("mutated input crossed privilege boundary: %+v", runner.runs)
	}
}

// TestPackageSymlinkAndTargetArtefactAreRejected exercises hostile path inputs.
func TestPackageSymlinkAndTargetArtefactAreRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic link creation is not reliably available on Windows")
	}
	t.Run("package symlink", func(t *testing.T) {
		root, bundle := fixtureEnvironment(t)
		image, _ := bundle.Package(kernel.RoleImage)
		realPath := image.Path + ".real"
		if err := os.Rename(image.Path, realPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realPath, image.Path); err != nil {
			t.Fatal(err)
		}
		_, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), fixtureRequest(root, bundle, true))
		if err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("pre-existing target symlink", func(t *testing.T) {
		root, bundle := fixtureEnvironment(t)
		outside := filepath.Join(t.TempDir(), "outside")
		writeFixtureFile(t, outside, "outside")
		if err := os.Symlink(outside, filepath.Join(root, "boot/vmlinuz-"+fixtureTargetABI)); err != nil {
			t.Fatal(err)
		}
		_, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), fixtureRequest(root, bundle, true))
		if err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("target parent symlink escape", func(t *testing.T) {
		root, bundle := fixtureEnvironment(t)
		outside := t.TempDir()
		if err := os.Rename(filepath.Join(root, "boot"), filepath.Join(outside, "boot")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(outside, "boot"), filepath.Join(root, "boot")); err != nil {
			t.Fatal(err)
		}
		_, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), fixtureRequest(root, bundle, true))
		if err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("error = %v", err)
		}
	})
}

// TestPackagePolicyRejectsUnexpectedSetsAndMetadata exercises the closed allow-list.
func TestPackagePolicyRejectsUnexpectedSetsAndMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *kernel.Bundle, *fakeRunner)
		want   string
	}{
		{
			name: "headers without common headers",
			mutate: func(t *testing.T, bundle *kernel.Bundle, _ *fakeRunner) {
				bundle.Packages = append(bundle.Packages, fixturePackage(t, filepath.Dir(bundle.Packages[0].Path), kernel.RoleHeaders, "headers"))
			},
			want: "exactly the runtime pair or runtime and header pairs",
		},
		{
			name: "wrong control package",
			mutate: func(_ *testing.T, _ *kernel.Bundle, runner *fakeRunner) {
				runner.metadataHook = func(name, field string) (string, bool) {
					if strings.HasPrefix(name, "linux-image-") && field == "Package" {
						return "linux-image-hostile-qcom-x1e", true
					}
					return "", false
				}
			},
			want: "control name",
		},
		{
			name: "cross ABI dependency",
			mutate: func(_ *testing.T, _ *kernel.Bundle, runner *fakeRunner) {
				runner.metadataHook = func(name, field string) (string, bool) {
					if strings.HasPrefix(name, "linux-image-") && field == "Depends" {
						return "linux-modules-7.3.0-hostile-qcom-x1e", true
					}
					return "", false
				}
			},
			want: "outside the selected ABI set",
		},
		{
			name: "control character",
			mutate: func(_ *testing.T, _ *kernel.Bundle, runner *fakeRunner) {
				runner.metadataHook = func(name, field string) (string, bool) {
					if strings.HasPrefix(name, "linux-image-") && field == "Depends" {
						return "kmod\x00hostile", true
					}
					return "", false
				}
			},
			want: "control characters",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root, bundle := fixtureEnvironment(t)
			runner := &fakeRunner{root: root}
			test.mutate(t, &bundle, runner)
			_, err := fixtureManager(runner).Preflight(context.Background(), fixtureRequest(root, bundle, true))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

// TestCoherentOptionalHeaderPairIsAccepted verifies exact four-package support.
func TestCoherentOptionalHeaderPairIsAccepted(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	directory := filepath.Dir(bundle.Packages[0].Path)
	bundle.Packages = append(bundle.Packages,
		fixturePackage(t, directory, kernel.RoleHeaders, "flavour headers"),
		fixturePackage(t, directory, kernel.RoleCommonHeaders, "common headers"),
	)
	plan, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), fixtureRequest(root, bundle, true))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Packages) != 4 || plan.Packages[2].Role != kernel.RoleCommonHeaders || plan.Packages[3].Role != kernel.RoleHeaders {
		t.Fatalf("unexpected header package order: %+v", plan.Packages)
	}
}

// TestFallbackMustBeRunningAndBootable verifies the recovery-kernel invariant.
func TestFallbackMustBeRunningAndBootable(t *testing.T) {
	t.Run("not running", func(t *testing.T) {
		root, bundle := fixtureEnvironment(t)
		request := fixtureRequest(root, bundle, true)
		request.RunningABI = "7.2.0-jg-0sp11v17-qcom-x1e"
		_, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), request)
		if err == nil || !strings.Contains(err.Error(), "exactly match") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("forced mismatch retains warning and boot evidence", func(t *testing.T) {
		root, bundle := fixtureEnvironment(t)
		request := fixtureRequest(root, bundle, true)
		request.RunningABI = "7.2.0-jg-0sp11v17-qcom-x1e"
		request.ForceFallbackMismatch = true
		plan, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		expected := "warning: fallback ABI does not match the running ABI: running 7.2.0-jg-0sp11v17-qcom-x1e, fallback " + fixtureFallbackABI
		if !plan.FallbackMismatchForced || plan.RunningABI != request.RunningABI || plan.Fallback.ABI != fixtureFallbackABI ||
			len(plan.Warnings) != 1 || plan.Warnings[0] != expected {
			t.Fatalf("forced mismatch plan = %+v", plan)
		}
	})
	t.Run("force without mismatch has no warning", func(t *testing.T) {
		root, bundle := fixtureEnvironment(t)
		request := fixtureRequest(root, bundle, true)
		request.ForceFallbackMismatch = true
		plan, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if plan.FallbackMismatchForced || len(plan.Warnings) != 0 {
			t.Fatalf("matching forced plan = %+v", plan)
		}
	})
	forcedArtifacts := []struct {
		name     string
		relative string
		want     string
	}{
		{name: "kernel image", relative: "boot/vmlinuz-" + fixtureFallbackABI, want: "vmlinuz-"},
		{name: "initramfs", relative: "boot/initrd.img-" + fixtureFallbackABI, want: "initrd.img-"},
		{name: "system map", relative: "boot/System.map-" + fixtureFallbackABI, want: "System.map-"},
		{name: "kernel config", relative: "boot/config-" + fixtureFallbackABI, want: "config-"},
		{name: "module dependency index", relative: "usr/lib/modules/" + fixtureFallbackABI + "/modules.dep", want: "modules.dep"},
		{name: "populated module tree", relative: "usr/lib/modules/" + fixtureFallbackABI + "/kernel/fallback.ko.zst", want: "no non-empty kernel module"},
	}
	for _, artifact := range forcedArtifacts {
		t.Run("forced mismatch still requires "+artifact.name, func(t *testing.T) {
			root, bundle := fixtureEnvironment(t)
			if err := os.Remove(filepath.Join(root, artifact.relative)); err != nil {
				t.Fatal(err)
			}
			request := fixtureRequest(root, bundle, true)
			request.RunningABI = "7.2.0-jg-0sp11v17-qcom-x1e"
			request.ForceFallbackMismatch = true
			_, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), artifact.want) {
				t.Fatalf("forced incomplete fallback error = %v, want %q", err, artifact.want)
			}
		})
	}
	t.Run("forced mismatch still requires one non-recovery entry", func(t *testing.T) {
		root, bundle := fixtureEnvironment(t)
		grub := "menuentry 'Ubuntu recovery " + fixtureFallbackABI + "' {\n linux /boot/vmlinuz-" + fixtureFallbackABI + "\n initrd /boot/initrd.img-" + fixtureFallbackABI + "\n}\n"
		writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
		request := fixtureRequest(root, bundle, true)
		request.RunningABI = "7.2.0-jg-0sp11v17-qcom-x1e"
		request.ForceFallbackMismatch = true
		_, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), request)
		if err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("forced recovery-only fallback error = %v", err)
		}
	})
	t.Run("missing initramfs", func(t *testing.T) {
		root, bundle := fixtureEnvironment(t)
		if err := os.Remove(filepath.Join(root, "boot/initrd.img-"+fixtureFallbackABI)); err != nil {
			t.Fatal(err)
		}
		_, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), fixtureRequest(root, bundle, true))
		if err == nil || !strings.Contains(err.Error(), "initrd.img-") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("recovery only", func(t *testing.T) {
		root, bundle := fixtureEnvironment(t)
		grub := "menuentry 'Ubuntu recovery " + fixtureFallbackABI + "' {\n linux /boot/vmlinuz-" + fixtureFallbackABI + "\n initrd /boot/initrd.img-" + fixtureFallbackABI + "\n}\n"
		writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
		_, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), fixtureRequest(root, bundle, true))
		if err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("error = %v", err)
		}
	})
}

// TestRequestAndCancellationValidationRejectsBeforeInspection exercises early checks.
func TestRequestAndCancellationValidationRejectsBeforeInspection(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	runner := &fakeRunner{root: root}
	manager := fixtureManager(runner)
	tests := []struct {
		name    string
		request Request
		context context.Context
		want    string
	}{
		{name: "missing root", request: fixtureRequest(root, bundle, true), context: context.Background(), want: "root is required"},
		{name: "relative root", request: fixtureRequest(root, bundle, true), context: context.Background(), want: "canonical and absolute"},
		{name: "same fallback", request: fixtureRequest(root, bundle, true), context: context.Background(), want: "must differ"},
	}
	tests[0].request.Root = ""
	tests[1].request.Root = "relative"
	tests[2].request.FallbackABI = fixtureTargetABI
	tests[2].request.ForceFallbackMismatch = true
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := manager.Preflight(test.context, test.request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := manager.Preflight(cancelled, fixtureRequest(root, bundle, true))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
}

// slicesContain reports whether a command argument list contains an exact value.
func slicesContain(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

// targetDPkgStatusPath returns the package database path inside a test root.
func targetDPkgStatusPath(root string) string {
	return filepath.Join(root, "var/lib/dpkg/status")
}

// writeTargetDPkgStatus writes a minimal but structurally valid package database.
func writeTargetDPkgStatus(t *testing.T, root string, stanzas []string) {
	t.Helper()
	writeFixtureFile(t, targetDPkgStatusPath(root), strings.Join(stanzas, "\n\n")+"\n")
}

// targetPackageStanza renders one dpkg status stanza for a package name.
func targetPackageStanza(name, status string) string {
	return "Package: " + name + "\nStatus: " + status + "\nVersion: " + fixtureVersion
}

// snapshotTree hashes every path beneath root so tests can prove that target
// classification performs no filesystem mutation.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		checksum := "dir"
		if !entry.IsDir() {
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			digest := sha256.Sum256(content)
			checksum = hex.EncodeToString(digest[:])
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}
		snapshot[relative] = checksum
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

// expectBlockedTargetState asserts the typed fresh-target blocker.
func expectBlockedTargetState(t *testing.T, err error) TargetStateEvidence {
	t.Helper()
	if err == nil {
		t.Fatal("expected the fresh-target gate to block the request")
	}
	var blocked *TargetStateError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %v, want a TargetStateError", err)
	}
	return blocked.Evidence
}

// assertNoTargetStateMutation proves that classification ran no commands and
// wrote nothing to the target root.
func assertNoTargetStateMutation(t *testing.T, runner *fakeRunner, root string, before map[string]string) {
	t.Helper()
	if len(runner.runs) != 0 {
		t.Fatalf("classification executed commands: %+v", runner.runs)
	}
	after := snapshotTree(t, root)
	if len(before) != len(after) {
		t.Fatalf("classification changed the target tree: %d paths before, %d after", len(before), len(after))
	}
	for path, checksum := range before {
		if after[path] != checksum {
			t.Fatalf("classification modified %s", path)
		}
	}
}

// TestPreflightExplainsHalfConfiguredImage preserves the reported real-world
// case: modules and headers installed, image half-configured, no GRUB entry.
func TestPreflightExplainsHalfConfiguredImage(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	if err := installFixtureTarget(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "boot/vmlinuz-"+fixtureTargetABI)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "boot/grub/grub.cfg")); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), fixtureGRUB(false))
	base := strings.TrimSuffix(fixtureTargetABI, "-qcom-x1e")
	writeTargetDPkgStatus(t, root, []string{
		targetPackageStanza("linux-modules-"+fixtureTargetABI, "install ok installed"),
		targetPackageStanza("linux-headers-"+fixtureTargetABI, "install ok installed"),
		targetPackageStanza("linux-qcom-x1e-headers-"+base, "install ok installed"),
		targetPackageStanza("linux-image-"+fixtureTargetABI, "install ok half-configured"),
	})
	runner := &fakeRunner{root: root}
	before := snapshotTree(t, root)
	_, err := fixtureManager(runner).Preflight(context.Background(), fixtureRequest(root, bundle, true))
	evidence := expectBlockedTargetState(t, err)
	assertNoTargetStateMutation(t, runner, root, before)
	if evidence.Classification != TargetStatePartial {
		t.Fatalf("classification = %s, want partial-or-inconsistent", evidence.Classification)
	}
	joined := strings.Join(evidence.Reasons, "\n")
	for _, expected := range []string{"half-configured", "incomplete boot baseline", "incomplete"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("reasons missing %q:\n%s", expected, joined)
		}
	}
	recommendations := strings.Join(evidence.Recommendations, "\n")
	if !strings.Contains(recommendations, "linux-image-"+fixtureTargetABI) ||
		!strings.Contains(recommendations, "dpkg --configure") {
		t.Errorf("recommendations must propose repairing the exact half-configured package:\n%s", recommendations)
	}
	if strings.Contains(recommendations, fixtureFallbackABI) {
		t.Errorf("recommendations must never propose removing the fallback ABI:\n%s", recommendations)
	}
}

// TestPreflightExplainsBootArtefactsWithoutGRUBEntry classifies orphan boot
// artefacts as partial even when every package record is cleanly installed.
func TestPreflightExplainsBootArtefactsWithoutGRUBEntry(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	if err := installFixtureTarget(root); err != nil {
		t.Fatal(err)
	}
	if err := installFixtureHeaders(root); err != nil {
		t.Fatal(err)
	}
	// Keep the fallback-only GRUB configuration from fixtureEnvironment.
	writeTargetDPkgStatus(t, root, []string{
		targetPackageStanza("linux-image-"+fixtureTargetABI, "install ok installed"),
		targetPackageStanza("linux-modules-"+fixtureTargetABI, "install ok installed"),
	})
	runner := &fakeRunner{root: root}
	before := snapshotTree(t, root)
	_, err := fixtureManager(runner).Preflight(context.Background(), fixtureRequest(root, bundle, true))
	evidence := expectBlockedTargetState(t, err)
	assertNoTargetStateMutation(t, runner, root, before)
	if evidence.Classification != TargetStatePartial {
		t.Fatalf("classification = %s, want partial-or-inconsistent", evidence.Classification)
	}
	joined := strings.Join(evidence.Reasons, "\n")
	if !strings.Contains(joined, "incomplete boot baseline") {
		t.Errorf("reasons missing the boot baseline gap:\n%s", joined)
	}
	if !strings.Contains(joined, "leftover boot-file") {
		t.Errorf("reasons must name the leftover boot artefacts:\n%s", joined)
	}
}

// TestPreflightExplainsRecordsWithoutFiles classifies a recorded package set
// whose expected boot files are missing as partial-or-inconsistent.
func TestPreflightExplainsRecordsWithoutFiles(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	writeTargetDPkgStatus(t, root, []string{
		targetPackageStanza("linux-image-"+fixtureTargetABI, "install ok installed"),
		targetPackageStanza("linux-modules-"+fixtureTargetABI, "install ok installed"),
	})
	runner := &fakeRunner{root: root}
	before := snapshotTree(t, root)
	_, err := fixtureManager(runner).Preflight(context.Background(), fixtureRequest(root, bundle, true))
	evidence := expectBlockedTargetState(t, err)
	assertNoTargetStateMutation(t, runner, root, before)
	if evidence.Classification != TargetStatePartial {
		t.Fatalf("classification = %s, want partial-or-inconsistent", evidence.Classification)
	}
	if len(evidence.Packages) != 2 {
		t.Fatalf("package findings = %d, want 2", len(evidence.Packages))
	}
	for _, item := range evidence.Packages {
		if !item.Installed {
			t.Errorf("package %s should be recorded as installed", item.Package)
		}
	}
	joined := strings.Join(evidence.Recommendations, "\n")
	if !strings.Contains(joined, "purge only the exact target-ABI packages") {
		t.Errorf("recommendations must propose a scoped purge:\n%s", joined)
	}
	if strings.Contains(joined, fixtureFallbackABI) {
		t.Errorf("recommendations must never propose removing the fallback ABI:\n%s", joined)
	}
}

// TestPreflightExplainsOrphanArtefactsWithoutRecords classifies leftover
// artefacts that have no package database records at all.
func TestPreflightExplainsOrphanArtefactsWithoutRecords(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	if err := os.MkdirAll(filepath.Join(root, "boot"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "boot/vmlinuz-"+fixtureTargetABI), "orphan kernel image")
	runner := &fakeRunner{root: root}
	before := snapshotTree(t, root)
	_, err := fixtureManager(runner).Preflight(context.Background(), fixtureRequest(root, bundle, true))
	evidence := expectBlockedTargetState(t, err)
	assertNoTargetStateMutation(t, runner, root, before)
	if evidence.Classification != TargetStatePartial {
		t.Fatalf("classification = %s, want partial-or-inconsistent", evidence.Classification)
	}
	if evidence.PackageDatabasePresent {
		t.Error("package database should be reported as absent")
	}
	joined := strings.Join(evidence.Reasons, "\n")
	if !strings.Contains(joined, "incomplete boot baseline") || !strings.Contains(joined, "leftover boot-file") {
		t.Errorf("reasons missing the orphan explanations:\n%s", joined)
	}
	if len(evidence.Packages) != 0 {
		t.Errorf("package findings = %+v, want none", evidence.Packages)
	}
}

// TestPreflightClassifiesCompleteInstalledTarget proves a fully installed
// target is blocked as complete and proceeds only with --overwrite plus a
// risk warning.
func TestPreflightClassifiesCompleteInstalledTarget(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	if err := installFixtureTarget(root); err != nil {
		t.Fatal(err)
	}
	if err := installFixtureHeaders(root); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI), "target initramfs")
	writeTargetDPkgStatus(t, root, []string{
		targetPackageStanza("linux-image-"+fixtureTargetABI, "install ok installed"),
		targetPackageStanza("linux-modules-"+fixtureTargetABI, "install ok installed"),
		targetPackageStanza("linux-headers-"+fixtureTargetABI, "install ok installed"),
		targetPackageStanza("linux-qcom-x1e-headers-"+strings.TrimSuffix(fixtureTargetABI, "-qcom-x1e"), "install ok installed"),
	})
	// Simulate the maintainer-script GRUB entry for the complete target.
	targetEntry := "menuentry 'Ubuntu " + fixtureTargetABI + "' {\n" +
		" linux /boot/vmlinuz-" + fixtureTargetABI + "\n" +
		" initrd /boot/initrd.img-" + fixtureTargetABI + "\n}\n"
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), fixtureGRUB(false)+targetEntry)

	t.Run("blocked without overwrite", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{root: root}
		before := snapshotTree(t, root)
		_, err := fixtureManager(runner).Preflight(context.Background(), fixtureRequest(root, bundle, true))
		evidence := expectBlockedTargetState(t, err)
		assertNoTargetStateMutation(t, runner, root, before)
		if evidence.Classification != TargetStateComplete {
			t.Fatalf("classification = %s, want already-installed-complete", evidence.Classification)
		}
	})

	t.Run("allowed with overwrite and warning", func(t *testing.T) {
		t.Parallel()
		runner := &fakeRunner{root: root}
		request := fixtureRequest(root, bundle, true)
		request.Overwrite = true
		plan, err := fixtureManager(runner).Preflight(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if !plan.Overwrite {
			t.Error("plan must record the overwrite request")
		}
		if plan.TargetState == nil || plan.TargetState.Classification != TargetStateComplete {
			t.Fatalf("plan target state = %+v, want already-installed-complete", plan.TargetState)
		}
		joined := strings.Join(plan.Warnings, "\n")
		for _, expected := range []string{"--overwrite", "could break the system or prevent returning to the desktop on reboot"} {
			if !strings.Contains(joined, expected) {
				t.Errorf("warnings missing %q:\n%s", expected, joined)
			}
		}
	})
}

// TestTargetStateEvidenceJSONIsStable keeps the machine-readable evidence
// contract deterministic for receipt consumers.
func TestTargetStateEvidenceJSONIsStable(t *testing.T) {
	t.Parallel()
	evidence := TargetStateEvidence{
		ABI:             fixtureTargetABI,
		Classification:  TargetStatePartial,
		GRUBEntries:     0,
		Reasons:         []string{"target ABI " + fixtureTargetABI + " has an incomplete boot baseline"},
		Recommendations: []string{"never remove or overwrite the running or fallback ABI"},
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"classification": "partial-or-inconsistent"`,
		`"boot_files"`,
		`"grub_entries": 0`,
		`"package_database_present": false`,
		`"reasons"`,
		`"recommendations"`,
	} {
		if !strings.Contains(string(encoded), expected) {
			t.Errorf("stable JSON missing %q:\n%s", expected, encoded)
		}
	}
}

// TestOverwriteRejectsRunningTargetABI proves that --overwrite never targets
// the running kernel, which the fallback verification protects.
func TestOverwriteRejectsRunningTargetABI(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	request := fixtureRequest(root, bundle, true)
	request.Bundle = renameBundleABI(bundle, altTargetABI)
	request.FallbackABI = fixtureTargetABI
	request.RunningABI = altTargetABI
	request.ForceFallbackMismatch = true
	request.Overwrite = true
	for index := range request.Bundle.Packages {
		source := bundle.Packages[index].Path
		renamedPath := filepath.Join(filepath.Dir(source), filepath.Base(request.Bundle.Packages[index].Name))
		if err := os.Rename(source, renamedPath); err != nil {
			t.Fatal(err)
		}
		request.Bundle.Packages[index].Path = renamedPath
	}
	// The verified fallback ABI is the fixture target ABI in this scenario;
	// populate its boot evidence alongside the renamed target's artefacts.
	for _, relative := range []string{
		"boot/vmlinuz-" + request.FallbackABI,
		"boot/initrd.img-" + request.FallbackABI,
		"boot/System.map-" + request.FallbackABI,
		"boot/config-" + request.FallbackABI,
		filepath.Join("usr/lib/modules", request.FallbackABI, "modules.dep"),
		filepath.Join("usr/lib/modules", request.FallbackABI, "kernel/fallback.ko.zst"),
	} {
		source := filepath.Join(root, "boot/vmlinuz-"+fixtureFallbackABI)
		if strings.Contains(relative, "initrd") {
			source = filepath.Join(root, "boot/initrd.img-"+fixtureFallbackABI)
		} else if strings.Contains(relative, "System.map") {
			source = filepath.Join(root, "boot/System.map-"+fixtureFallbackABI)
		} else if strings.Contains(relative, "config-") {
			source = filepath.Join(root, "boot/config-"+fixtureFallbackABI)
		} else if strings.Contains(relative, "modules.dep") {
			source = filepath.Join(root, "usr/lib/modules", fixtureFallbackABI, "modules.dep")
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, relative)), 0o755); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, filepath.Join(root, relative), string(content))
	}
	// GRUB must label a non-recovery entry for the verified fallback ABI.
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"),
		fixtureGRUB(false)+
			"menuentry 'Ubuntu "+request.FallbackABI+"' {\n"+
			" linux /boot/vmlinuz-"+request.FallbackABI+" root=fixture\n"+
			" initrd /boot/initrd.img-"+request.FallbackABI+"\n}\n")
	_, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "must never replace the running ABI") {
		t.Fatalf("error = %v, want the running-ABI overwrite guard", err)
	}
}

// renameBundleABI rewrites every package name in a fixture bundle so tests
// can exercise an alternate target ABI without crafting new archives.
func renameBundleABI(bundle kernel.Bundle, abi string) kernel.Bundle {
	renamed := bundle
	renamed.ABI = abi
	renamed.Packages = slices.Clone(bundle.Packages)
	for index := range renamed.Packages {
		renamed.Packages[index].Name = strings.Replace(
			renamed.Packages[index].Name, bundle.ABI, abi, 1)
	}
	return renamed
}
