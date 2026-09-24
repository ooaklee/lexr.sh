package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/platform"
	"github.com/ooaklee/lexr.sh/internal/profile"
)

// surfaceInstallationFixture recreates the reported shared-name fallback. Both
// models use a qcom-x1e ABI; only the selected same-ABI DTB establishes the model.
func surfaceInstallationFixture(t *testing.T, id string) (string, Request, string) {
	t.Helper()
	root, bundle := fixtureEnvironment(t)
	if err := os.Chmod(filepath.Join(root, "var"), 0o755); err != nil {
		t.Fatal(err)
	}
	selected, err := profile.Resolve(id)
	if err != nil {
		t.Fatal(err)
	}
	modelBytes := "oled dtb"
	if selected.Device == "x1p-lcd" {
		modelBytes = "lcd dtb"
	}
	for index, candidateID := range []string{"x1p64100-microsoft-denali", "x1e80100-microsoft-denali-oled"} {
		candidate, _ := profile.Resolve(candidateID)
		tree := &bundle.DeviceTrees[index]
		tree.Device = candidate.Platform
		tree.Basename = candidate.ID + ".dtb"
		tree.Path = "usr/lib/firmware/" + bundle.ABI + "/device-tree/qcom/" + tree.Basename
		tree.CompatibleStrings = []string{candidate.Compatible}
		tree.Selectors[0].Value = candidate.Compatible
	}
	if err := os.Remove(filepath.Join(root, "boot/dtb-"+fixtureFallbackABI)); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(root, "boot/sp11-denali.dtb"), "fallback "+modelBytes)
	grub := strings.ReplaceAll(fixtureGRUB(false), "/dtb-"+fixtureFallbackABI, "/sp11-denali.dtb")
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
	request := fixtureRequest(root, bundle, true)
	request.Profile = selected.ID
	return root, request, modelBytes
}

// TestSurfaceProfileInstall covers preflight, dry run and confirmed installation
// for both displays with the same misleading ABI suffix and retired hook paths.
func TestSurfaceProfileInstall(t *testing.T) {
	for _, id := range []string{"x1p64100-microsoft-denali", "x1e80100-microsoft-denali-oled"} {
		t.Run(id, func(t *testing.T) {
			root, request, modelBytes := surfaceInstallationFixture(t, id)
			selected, _ := profile.Resolve(id)
			hooks := writeRetiredBootHooks(t, root)
			runner := &fakeRunner{root: root}
			manager := fixtureManager(runner)
			manager.effectiveUID = func() int { return 0 }
			plan, err := manager.Preflight(context.Background(), request)
			if err != nil || plan.Profile != id || plan.FallbackBinding == nil || !plan.FallbackBinding.Create || plan.BootHookCleanup == nil || len(plan.BootHookCleanup.Findings) != 2 {
				t.Fatalf("preflight = %#v, %v", plan, err)
			}
			if _, err := manager.Install(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if len(runner.runs) != 0 {
				t.Fatal("read-only checks ran mutation commands")
			}
			if _, err := os.Stat(plan.FallbackBinding.Destination); !os.IsNotExist(err) {
				t.Fatal("dry run wrote the fallback binding")
			}
			for _, hook := range hooks {
				if _, err := os.Stat(hook); err != nil {
					t.Fatal("dry run changed a hook", err)
				}
			}
			refreshed := false
			runner.runHook = func(_ context.Context, command platform.Command) error {
				if !slices.Contains(command.Env, "LEXR_KERNEL_PLATFORM="+selected.Platform) {
					t.Fatal("package lifecycle lost the selected platform")
				}
				if slices.Contains(command.Args, "--install") {
					for _, hook := range hooks {
						if _, err := os.Stat(hook); !os.IsNotExist(err) {
							t.Fatal("legacy hook still active when packages were installed")
						}
					}
					data, err := os.ReadFile(plan.FallbackBinding.Destination)
					if err != nil || string(data) != "fallback "+modelBytes {
						t.Fatalf("fallback copy = %q, %v", data, err)
					}
					if err := installFixtureTarget(root); err != nil {
						return err
					}
					writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI), "target initramfs")
					for index, tree := range request.Bundle.DeviceTrees {
						payload := "lcd dtb"
						if index == 1 {
							payload = "oled dtb"
						}
						writeFixtureFile(t, filepath.Join(root, tree.Path), payload)
					}
				}
				if slices.Contains(command.Args, bootRefreshCommand) {
					if command.Args[len(command.Args)-1] != selected.Platform {
						t.Fatalf("refresh lost selected profile: %#v", command)
					}
					refreshed = true
					writeFixtureFile(t, filepath.Join(root, "boot/dtb-"+fixtureTargetABI), modelBytes)
					writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), fixtureGRUB(true))
				}
				return nil
			}
			request.DryRun = false
			receipt, err := manager.Install(context.Background(), request)
			if err != nil || !receipt.RebootRequired || !refreshed || !receipt.FallbackBindingCreated || receipt.BootHookCleanup == nil || receipt.BootHooksRestored {
				t.Fatalf("installed=%t refreshed=%t fallback copied=%t hooks restored=%t: %v", receipt.RebootRequired, refreshed, receipt.FallbackBindingCreated, receipt.BootHooksRestored, err)
			}
			if receipt.Installed.DeviceTreeBoot.SHA256 != digestText(modelBytes) {
				t.Fatal("installed boot bytes belong to the wrong display model")
			}
			if _, err := os.Stat(filepath.Join(receipt.BootHookCleanup.Backup, "receipt.json")); err != nil {
				t.Fatal("retired hooks lack a durable recovery receipt", err)
			}
		})
	}
}

// writeRetiredBootHooks supplies the exact paths and helper marker from the
// retired OpenEmbedded injector without executing any host package hook.
func writeRetiredBootHooks(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	for _, lifecycle := range []string{"postinst", "postrm"} {
		path := filepath.Join(root, "etc/kernel/"+lifecycle+".d/zzzz-surface-pro-11-dtb")
		writeFixtureFile(t, path, "#!/bin/sh\nexec /usr/local/sbin/sp11-grub-inject-dtb\n")
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

// TestProfilePreflightRejectsDifferentBootBytes proves a selected LCD profile
// cannot bless an OLED fallback even when both firmware DTBs are installed.
func TestProfilePreflightRejectsDifferentBootBytes(t *testing.T) {
	root, request, _ := surfaceInstallationFixture(t, "x1p64100-microsoft-denali")
	writeFixtureFile(t, filepath.Join(root, "boot/sp11-denali.dtb"), "fallback oled dtb")
	_, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "does not match selected profile") {
		t.Fatalf("wrong model fallback accepted: %v", err)
	}
}

// TestProfileDiagnosticRetainsBothEmbeddedModels prevents the selected LCD
// profile from discarding the valid OLED payload in a shared Stubble image.
func TestProfileDiagnosticRetainsBothEmbeddedModels(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	writeFixtureEmbeddedDTBImageSet(t, filepath.Join(root, "boot/vmlinuz-"+fixtureFallbackABI), [][]byte{
		[]byte("fallback lcd dtb"), []byte("fallback oled dtb"),
	})
	grub := strings.ReplaceAll(fixtureGRUB(false), " devicetree /dtb-"+fixtureFallbackABI+"\n", "")
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
	entries, err := InspectGRUB(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, device := range []string{"x1p-lcd", "x1e-oled"} {
		evidence, err := InspectDeviceTreeBootBinding(context.Background(), root, fixtureFallbackABI, device, entries)
		if err != nil || len(evidence.SHA256s) != 2 {
			t.Fatalf("%s embedded inventory = %#v, %v", device, evidence, err)
		}
	}
}

// TestFallbackCopyRejectsDriftAndRaces ensures publication never exposes
// partial bytes or replaces a destination introduced after preflight.
func TestFallbackCopyRejectsDriftAndRaces(t *testing.T) {
	for _, scenario := range []string{"source changed", "destination appeared"} {
		t.Run(scenario, func(t *testing.T) {
			root, request, _ := surfaceInstallationFixture(t, "x1p64100-microsoft-denali")
			plan, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			binding := *plan.FallbackBinding
			if scenario == "source changed" {
				writeFixtureFile(t, binding.Source.Path, "different dtb bytes")
			} else {
				writeFixtureFile(t, binding.Destination, "keep raced bytes")
			}
			if created, err := createFallbackBinding(context.Background(), root, binding); err == nil || created {
				t.Fatalf("unsafe copy succeeded: created=%t, %v", created, err)
			}
			data, err := os.ReadFile(binding.Destination)
			if scenario == "source changed" && !os.IsNotExist(err) {
				t.Fatalf("published partial copy: %q, %v", data, err)
			}
			if scenario == "destination appeared" && (err != nil || string(data) != "keep raced bytes") {
				t.Fatalf("replaced raced bytes: %q, %v", data, err)
			}
		})
	}
}

// TestFailedInstallRestoresRetiredHooks verifies a package failure restores
// original hook bytes and modes while retaining the proven fallback DTB copy.
func TestFailedInstallRestoresRetiredHooks(t *testing.T) {
	root, request, modelBytes := surfaceInstallationFixture(t, "x1p64100-microsoft-denali")
	hooks := writeRetiredBootHooks(t, root)
	runner := &fakeRunner{root: root, runHook: func(_ context.Context, command platform.Command) error {
		if slices.Contains(command.Args, "--install") {
			return errors.New("fixture package failure")
		}
		return nil
	}}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	request.DryRun = false
	receipt, err := manager.Install(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "fixture package failure") || !receipt.BootHooksRestored || receipt.Rollback == nil || receipt.Rollback.Error != "" {
		t.Fatalf("hooks restored=%t rollback=%#v: %v", receipt.BootHooksRestored, receipt.Rollback, err)
	}
	for _, hook := range hooks {
		data, err := os.ReadFile(hook)
		if err != nil || string(data) != "#!/bin/sh\nexec /usr/local/sbin/sp11-grub-inject-dtb\n" {
			t.Fatalf("restored hook = %q, %v", data, err)
		}
		info, _ := os.Stat(hook)
		if info.Mode().Perm() != 0o755 {
			t.Fatal("hook mode was not restored")
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "boot/dtb-"+fixtureFallbackABI))
	if err != nil || string(data) != "fallback "+modelBytes {
		t.Fatalf("verified fallback copy lost: %q, %v", data, err)
	}
}

// A package may recreate a retired hook. Refuse a successful installation and
// preserve changed local content if it conflicts with the recovery receipt.
func TestReintroducedBootHookTriggersRollbackAndReportsRecoveryConflict(t *testing.T) {
	root, request, _ := surfaceInstallationFixture(t, "x1p64100-microsoft-denali")
	hook := writeRetiredBootHooks(t, root)[0]
	const changed = "#!/bin/sh\n# recreated by the package\nexec /usr/local/sbin/sp11-grub-inject-dtb\n"
	runner := &fakeRunner{root: root, runHook: func(_ context.Context, command platform.Command) error {
		switch {
		case slices.Contains(command.Args, "--install"):
			if err := installFixtureTarget(root); err != nil {
				return err
			}
			writeFixtureFile(t, filepath.Join(root, "boot/initrd.img-"+fixtureTargetABI), "target initramfs")
			writeFixtureFile(t, hook, changed)
			return os.Chmod(hook, 0o755)
		case slices.Contains(command.Args, "--purge"):
			return removeFixtureTarget(root)
		case slices.Contains(command.Args, bootRefreshCommand):
			t.Fatal("refreshed boot despite a recreated competing hook")
		}
		return nil
	}}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	request.DryRun = false
	receipt, err := manager.Install(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "appeared after preflight") || !strings.Contains(err.Error(), "restore retired boot hooks") || receipt.RebootRequired || receipt.BootHooksRestored {
		t.Fatalf("recreated hook was not reported: receipt=%#v, %v", receipt, err)
	}
	if receipt.Rollback == nil || receipt.Rollback.Error != "" || receipt.BootHookCleanup == nil {
		t.Fatalf("missing rollback/recovery evidence: %#v", receipt)
	}
	data, readErr := os.ReadFile(hook)
	if readErr != nil || string(data) != changed {
		t.Fatalf("overwrote a changed hook during recovery: %q, %v", data, readErr)
	}
	if _, err := os.Stat(filepath.Join(receipt.BootHookCleanup.Backup, "receipt.json")); err != nil {
		t.Fatal("lost recovery receipt", err)
	}
}

// TestUnsafeBootPreparationBlocksBeforeMutation covers unrecognised hooks,
// conflicting exact-ABI DTBs and redirected boot destinations.
func TestUnsafeBootPreparationBlocksBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"unknown hook", "conflicting DTB", "symlink DTB"} {
		t.Run(scenario, func(t *testing.T) {
			root, request, _ := surfaceInstallationFixture(t, "x1p64100-microsoft-denali")
			switch scenario {
			case "unknown hook":
				hook := writeRetiredBootHooks(t, root)[0]
				writeFixtureFile(t, hook, "#!/bin/sh\ncustom unreviewed hook\n")
			case "conflicting DTB":
				writeFixtureFile(t, filepath.Join(root, "boot/dtb-"+fixtureFallbackABI), "other bytes")
			case "symlink DTB":
				if err := os.Symlink("sp11-denali.dtb", filepath.Join(root, "boot/dtb-"+fixtureFallbackABI)); err != nil {
					t.Fatal(err)
				}
			}
			runner := &fakeRunner{root: root}
			request.DryRun = false
			if _, err := fixtureManager(runner).Install(context.Background(), request); err == nil || len(runner.runs) != 0 {
				t.Fatalf("unsafe preparation was allowed: %v", err)
			}
		})
	}
}

// TestPreflightPermissionGuidance keeps errors.Is usable and explains why even
// a read-only check may need sudo on distributions with mode-0600 kernel images.
func TestPreflightPermissionGuidance(t *testing.T) {
	root, request, _ := surfaceInstallationFixture(t, "x1p64100-microsoft-denali")
	path := filepath.Join(root, "boot/vmlinuz-"+fixtureFallbackABI)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0o600)
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("current user can read mode-0000 files")
	}
	_, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), request)
	if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "sudo") || !strings.Contains(err.Error(), "--profile") {
		t.Fatalf("permission guidance missing: %v", err)
	}
}
