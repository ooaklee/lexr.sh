package ubuntu

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	kernelinstall "github.com/ooaklee/lexr.sh/internal/kernel/install"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// installedTestABI is the exact kernel identity shared by focused fixtures.
const installedTestABI = "7.2.2-jg-0sp11v10-qcom-x1e"

// installedCommandRunner records the one container command emitted by focused
// installed-system helpers without executing Docker.
type installedCommandRunner struct {
	commands []platform.Command
}

// Run records a non-capturing Docker command.
func (runner *installedCommandRunner) Run(_ context.Context, command platform.Command) error {
	runner.commands = append(runner.commands, command)
	return nil
}

// Capture records a capturing Docker command and returns empty output.
func (runner *installedCommandRunner) Capture(_ context.Context, command platform.Command) ([]byte, error) {
	runner.commands = append(runner.commands, command)
	return nil, nil
}

// installedTestBundle returns a two-profile fixture for either delivery mode.
func installedTestBundle(delivery kernel.DTBDelivery) kernel.Bundle {
	bundle := kernel.Bundle{
		ABI:                  installedTestABI,
		Version:              "7.2.2-jg-0sp11v10",
		EffectiveDTBDelivery: delivery,
		EmbeddedDTBCount:     0,
		Packages: []kernel.Package{
			{Role: kernel.RoleImage, Name: "linux-image-" + installedTestABI + "_7.2.2-jg-0sp11v10_arm64.deb"},
			{Role: kernel.RoleModules, Name: "linux-modules-" + installedTestABI + "_7.2.2-jg-0sp11v10_arm64.deb"},
		},
		DeviceTrees: []kernel.DeviceTree{
			{
				Device: "surface-pro-11-x1p-lcd", Basename: "x1p64100-microsoft-denali.dtb",
				Path:   "usr/lib/firmware/" + installedTestABI + "/device-tree/qcom/x1p64100-microsoft-denali.dtb",
				SHA256: strings.Repeat("b", 64), Required: true,
			},
			{
				Device: "surface-pro-11-x1e-oled", Basename: "x1e80100-microsoft-denali-oled.dtb",
				Path:   "usr/lib/firmware/" + installedTestABI + "/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb",
				SHA256: strings.Repeat("a", 64), Required: true,
			},
		},
	}
	if delivery == kernel.DTBDeliveryExternalRequired {
		bundle.DeviceTrees[0].Required = false
		bundle.Packages = append(bundle.Packages, kernel.Package{
			Role: kernel.RoleBootSupport, Name: "lexr-kernel-boot-support_7.2.2-jg-0sp11v10_all.deb",
		})
	} else {
		bundle.EmbeddedDTBCount = 2
		for index := range bundle.DeviceTrees {
			bundle.DeviceTrees[index].EmbeddedMatches = 1
		}
	}
	return bundle
}

// TestStageInstalledSupportFilesRetainsOnlyDeviceDefaults proves the adapter
// no longer generates a second DTB or GRUB lifecycle implementation.
func TestStageInstalledSupportFilesRetainsOnlyDeviceDefaults(t *testing.T) {
	workspace := t.TempDir()
	if err := stageInstalledSupportFiles(workspace); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(workspace, "installed-support")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != installedGrubDefaultsName {
		t.Fatalf("staged installed support = %v, want only %s", entries, installedGrubDefaultsName)
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("GRUB defaults mode = %04o, want 0644", info.Mode().Perm())
	}
}

// TestInstalledSupportSeparatesLiveAndInstalledArguments keeps the installed
// command line free from both live-media and retired audio compatibility flags.
func TestInstalledSupportSeparatesLiveAndInstalledArguments(t *testing.T) {
	defaults := installedGrubDefaults()
	for _, argument := range []string{
		"clk_ignore_unused", "pd_ignore_unused", "arm64.nopauth",
		"systemd.tpm2_wait=0",
	} {
		if !strings.Contains(defaults, argument) {
			t.Errorf("installed GRUB defaults do not contain %q", argument)
		}
	}
	if strings.Contains(defaults, "qcom_q6v5_pas") {
		t.Fatal("installed-system defaults contain the live USB qcom_q6v5_pas blacklist")
	}
	if strings.Contains(defaults, "sp11_feedback_active_offset2_zero") {
		t.Fatal("installed-system defaults contain the retired SoundWire compatibility parameter")
	}
}

// TestRequiredExternalProfileIsExplicitAndSingular proves an offline raw
// image never relies on machine detection or ambiguous multi-profile policy.
func TestRequiredExternalProfileIsExplicitAndSingular(t *testing.T) {
	profile, tree, err := requiredExternalProfile(installedTestBundle(kernel.DTBDeliveryExternalRequired))
	if err != nil {
		t.Fatal(err)
	}
	if profile != "surface-pro-11-x1e-oled" || tree.Device != profile {
		t.Fatalf("profile = %q, tree = %q", profile, tree.Device)
	}
	profile, tree, err = requiredExternalProfile(installedTestBundle(kernel.DTBDeliveryEmbedded))
	if err != nil || profile != "" || tree.Device != "" {
		t.Fatalf("embedded profile = %q, tree = %q, error %v; want none", profile, tree.Device, err)
	}
	bundle := installedTestBundle(kernel.DTBDeliveryExternalRequired)
	for index := range bundle.DeviceTrees {
		bundle.DeviceTrees[index].Required = false
	}
	if _, _, err := requiredExternalProfile(bundle); err == nil {
		t.Fatal("external delivery without a required profile succeeded")
	}
	bundle.DeviceTrees[0].Required = true
	bundle.DeviceTrees[1].Required = true
	if _, _, err := requiredExternalProfile(bundle); err == nil || !strings.Contains(err.Error(), "embedded delivery") {
		t.Fatalf("multi-profile external delivery error = %v", err)
	}
}

// TestInstallKernelPackagesSelectsSupportByEffectiveDelivery proves only a raw
// kernel installs the manifest-declared generic support archive.
func TestInstallKernelPackagesSelectsSupportByEffectiveDelivery(t *testing.T) {
	for _, test := range []struct {
		name        string
		delivery    kernel.DTBDelivery
		wantSupport bool
	}{
		{name: "external", delivery: kernel.DTBDeliveryExternalRequired, wantSupport: true},
		{name: "embedded", delivery: kernel.DTBDeliveryEmbedded, wantSupport: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &installedCommandRunner{}
			docker := platform.NewDocker(runner)
			if err := installKernelPackages(context.Background(), docker, "tools:test", t.TempDir(), "lexr-work-aaaaaaaaaaaaaaaaaaaaaaaa", installedTestBundle(test.delivery)); err != nil {
				t.Fatal(err)
			}
			if len(runner.commands) != 1 {
				t.Fatalf("commands = %d, want 1", len(runner.commands))
			}
			joined := strings.Join(runner.commands[0].Args, "\n")
			hasSupportArchive := strings.Contains(joined, "/work/kernel/lexr-kernel-boot-support_")
			if hasSupportArchive != test.wantSupport {
				t.Fatalf("support archive present = %t, want %t", hasSupportArchive, test.wantSupport)
			}
			for _, required := range []string{"restore_hook_directories", `dpkg --root="$root" --install "$support_archive"`} {
				if !strings.Contains(joined, required) {
					t.Errorf("install script lacks %q", required)
				}
			}
		})
	}
	bundle := installedTestBundle(kernel.DTBDeliveryExternalRequired)
	bundle.Packages = bundle.Packages[:2]
	if err := installKernelPackages(context.Background(), platform.NewDocker(&installedCommandRunner{}), "tools:test", t.TempDir(), "lexr-work-aaaaaaaaaaaaaaaaaaaaaaaa", bundle); err == nil {
		t.Fatal("external bundle without support package succeeded")
	}
}

// TestInstallInstalledSystemSupportUsesPackageHelperAndFinalGRUB proves all
// profiles are materialised before one final initramfs and GRUB generation.
func TestInstallInstalledSystemSupportUsesPackageHelperAndFinalGRUB(t *testing.T) {
	runner := &installedCommandRunner{}
	bundle := installedTestBundle(kernel.DTBDeliveryExternalRequired)
	if err := installInstalledSystemSupport(context.Background(), platform.NewDocker(runner), "tools:test", t.TempDir(), "lexr-work-aaaaaaaaaaaaaaaaaaaaaaaa", bundle); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(runner.commands))
	}
	joined := strings.Join(runner.commands[0].Args, "\n")
	for _, required := range []string{
		"/usr/libexec/lexr/kernel-boot-refresh refresh", "--defer-grub",
		"surface-pro-11-x1e-oled",
		"update-initramfs -c -k", "update-grub", "grub-script-check",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("installed-system command lacks %q", required)
		}
	}
	if strings.Contains(joined, "surface-pro-11-x1p-lcd") {
		t.Fatal("installed-system command materialises more than the selected external profile")
	}
	for _, retired := range []string{"09_lexr_sp11", "lexr-refresh-sp11-boot", "05-lexr-sp11-dtb"} {
		if !strings.Contains(joined, `rm -f --`) || !strings.Contains(joined, retired) {
			t.Errorf("installed-system command does not retire %q", retired)
		}
	}
}

// TestInstalledSupportPathsFollowDeliveryContract proves validation extracts
// the generic package state only when effective delivery requires it.
func TestInstalledSupportPathsFollowDeliveryContract(t *testing.T) {
	external := installedTestBundle(kernel.DTBDeliveryExternalRequired)
	paths, err := installedSupportPaths(external)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(paths, "\n")
	for _, required := range []string{
		"usr/libexec/lexr/kernel-boot-refresh",
		"boot/dtb-" + installedTestABI,
		"boot/dtbs/" + installedTestABI + "/qcom/x1e80100-microsoft-denali-oled.dtb",
		"var/lib/lexr/kernel-boot/" + installedTestABI + "/surface-pro-11-x1e-oled/dtb-sha256",
		"var/lib/dpkg/info/lexr-kernel-boot-support.list",
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("external support paths lack %q", required)
		}
	}
	for _, retired := range retiredInstalledSupportPaths {
		if slices.Contains(paths, retired) {
			t.Errorf("external support paths retain %q", retired)
		}
	}
	embeddedPaths, err := installedSupportPaths(installedTestBundle(kernel.DTBDeliveryEmbedded))
	if err != nil {
		t.Fatal(err)
	}
	embedded := strings.Join(embeddedPaths, "\n")
	if strings.Contains(embedded, "kernel-boot-refresh") || strings.Contains(embedded, "boot/dtbs/") || strings.Contains(embedded, "lexr-kernel-boot-support") {
		t.Fatalf("embedded support paths unexpectedly require external support:\n%s", embedded)
	}
}

// TestInstalledPackageStatusRequiresExactRecord rejects a wrong architecture,
// version or package configuration state.
func TestInstalledPackageStatusRequiresExactRecord(t *testing.T) {
	status := `Package: linux-modules-test
Status: install ok installed
Architecture: arm64
Version: 1.2.3

Package: lexr-kernel-boot-support
Status: install ok installed
Architecture: all
Version: 1.2.3

Package: linux-image-test
Status: install ok half-configured
Architecture: arm64
Version: 1.2.3
`
	if !installedPackageStatus(status, "linux-modules-test", "1.2.3", "arm64") ||
		!installedPackageStatus(status, "lexr-kernel-boot-support", "1.2.3", "all") {
		t.Fatal("installedPackageStatus rejected an exact installed package")
	}
	for _, test := range []struct{ name, version, architecture string }{
		{name: "linux-image-test", version: "1.2.3", architecture: "arm64"},
		{name: "linux-modules-test", version: "9.9.9", architecture: "arm64"},
		{name: "linux-modules-test", version: "1.2.3", architecture: "all"},
	} {
		if installedPackageStatus(status, test.name, test.version, test.architecture) {
			t.Errorf("installedPackageStatus(%q) accepted a non-installed record", test.name)
		}
	}
	if !installedPackagePresent(status, "lexr-kernel-boot-support") || installedPackagePresent(status, "missing") {
		t.Fatal("installedPackagePresent did not match exact package fields")
	}
}

// TestSquashFSListingContainsExactPath rejects similarly prefixed members when
// proving retired lifecycle files are absent.
func TestSquashFSListingContainsExactPath(t *testing.T) {
	listing := `drwxr-xr-x root/root 0 2026-09-04 00:00 squashfs-root/usr/libexec/lexr
-rwxr-xr-x root/root 1 2026-09-04 00:00 squashfs-root/usr/libexec/lexr/kernel-boot-refresh
-rwxr-xr-x root/root 1 2026-09-04 00:00 squashfs-root/usr/libexec/lexr/kernel-boot-refresh-old
`
	if !squashFSListingContainsPath(listing, "usr/libexec/lexr/kernel-boot-refresh") {
		t.Fatal("exact SquashFS path was not found")
	}
	if squashFSListingContainsPath(listing, "usr/libexec/lexr/kernel-boot") {
		t.Fatal("partial SquashFS path was accepted")
	}
}

// TestValidateInstalledGRUBEntriesRejectsUnboundStockEntry proves valid generic
// entries cannot mask a second 10_linux entry without a device-tree directive.
func TestValidateInstalledGRUBEntriesRejectsUnboundStockEntry(t *testing.T) {
	bundle := installedTestBundle(kernel.DTBDeliveryExternalRequired)
	entries := externalInstalledTestEntries(bundle)
	if err := validateInstalledGRUBEntriesForTest(t, bundle, entries); err != nil {
		t.Fatalf("complete external GRUB entries: %v", err)
	}
	entries = append(entries, kernelinstall.GRUBEntry{
		Title:  "Ubuntu " + bundle.ABI,
		Linux:  []kernelinstall.GRUBPathToken{{Command: "linux", Path: "/boot/vmlinuz-" + bundle.ABI}},
		Initrd: []kernelinstall.GRUBPathToken{{Command: "initrd", Path: "/boot/initrd.img-" + bundle.ABI}},
	})
	if err := validateInstalledGRUBEntriesForTest(t, bundle, entries); err == nil || !strings.Contains(err.Error(), "0 device-tree directives") {
		t.Fatalf("unbound stock entry error = %v", err)
	}
}

// TestValidateInstalledGRUBEntriesAcceptsStockSimpleEntry proves the generic
// stock-GRUB title remains valid alongside its one ABI-labelled advanced entry.
func TestValidateInstalledGRUBEntriesAcceptsStockSimpleEntry(t *testing.T) {
	bundle := installedTestBundle(kernel.DTBDeliveryExternalRequired)
	entries := externalInstalledTestEntries(bundle)
	simple := entries[0]
	simple.Title = "Ubuntu"
	entries = append(entries, simple)
	if err := validateInstalledGRUBEntriesForTest(t, bundle, entries); err != nil {
		t.Fatalf("stock simple, advanced, and recovery entries: %v", err)
	}
}

// TestValidateInstalledGRUBEntriesRejectsAdditionalBootArtefacts keeps image
// validation from accepting a target token alongside a foreign kernel or
// concatenated foreign initramfs.
func TestValidateInstalledGRUBEntriesRejectsAdditionalBootArtefacts(t *testing.T) {
	bundle := installedTestBundle(kernel.DTBDeliveryExternalRequired)
	for _, test := range []struct {
		name   string
		mutate func(*kernelinstall.GRUBEntry)
	}{
		{
			name: "foreign kernel",
			mutate: func(entry *kernelinstall.GRUBEntry) {
				entry.Linux = append(entry.Linux, kernelinstall.GRUBPathToken{Command: "linux", Path: "/boot/vmlinuz-foreign"})
			},
		},
		{
			name: "foreign initramfs",
			mutate: func(entry *kernelinstall.GRUBEntry) {
				entry.Initrd = append(entry.Initrd, kernelinstall.GRUBPathToken{Command: "initrd", Path: "/boot/initrd.img-foreign"})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			entries := externalInstalledTestEntries(bundle)
			test.mutate(&entries[0])
			if err := validateInstalledGRUBEntriesForTest(t, bundle, entries); err == nil || !strings.Contains(err.Error(), "exactly one exact-ABI") {
				t.Fatalf("additional boot artefact error = %v", err)
			}
		})
	}
}

// TestValidateInstalledGRUBEntriesBindsActualArtefactBytes rejects a
// same-basename path whose contents differ from the installed /boot artefact.
func TestValidateInstalledGRUBEntriesBindsActualArtefactBytes(t *testing.T) {
	bundle := installedTestBundle(kernel.DTBDeliveryExternalRequired)
	root := installedGRUBTestRoot(t, bundle)
	entries := externalInstalledTestEntries(bundle)
	foreign := "/attacker/vmlinuz-" + bundle.ABI
	writeInstalledGRUBTestFile(t, filepath.Join(root, strings.TrimPrefix(foreign, "/")), "foreign kernel bytes")
	entries[0].Linux[0].Path = foreign
	if err := validateInstalledGRUBEntries(context.Background(), root, bundle, entries); err == nil || !strings.Contains(err.Error(), "GRUB kernel token differs") {
		t.Fatalf("redirected installed kernel error = %v", err)
	}
}

// TestValidateInstalledGRUBEntriesRejectsDiscardedUnsafeBootToken ensures an
// unsafe parsed command fails even when it retained no candidate path token.
func TestValidateInstalledGRUBEntriesRejectsDiscardedUnsafeBootToken(t *testing.T) {
	bundle := installedTestBundle(kernel.DTBDeliveryExternalRequired)
	root := installedGRUBTestRoot(t, bundle)
	entries := externalInstalledTestEntries(bundle)
	entries = append(entries, kernelinstall.GRUBEntry{Title: "unsafe", UnsafeCommands: []string{"linux"}})
	if err := validateInstalledGRUBEntries(context.Background(), root, bundle, entries); err == nil || !strings.Contains(err.Error(), "unsafe kernel or initramfs path") {
		t.Fatalf("discarded unsafe boot token error = %v", err)
	}
}

// TestValidateInstalledGRUBEntriesKeepsEmbeddedAuthority proves embedded
// images accept normal and recovery entries only while both omit external DTBs.
func TestValidateInstalledGRUBEntriesKeepsEmbeddedAuthority(t *testing.T) {
	bundle := installedTestBundle(kernel.DTBDeliveryEmbedded)
	entries := []kernelinstall.GRUBEntry{
		{
			Title:  "Ubuntu " + bundle.ABI,
			Linux:  []kernelinstall.GRUBPathToken{{Command: "linux", Path: "/boot/vmlinuz-" + bundle.ABI}},
			Initrd: []kernelinstall.GRUBPathToken{{Command: "initrd", Path: "/boot/initrd.img-" + bundle.ABI}},
		},
		{
			Title: "Ubuntu " + bundle.ABI + " (recovery mode)", Recovery: true,
			Linux:  []kernelinstall.GRUBPathToken{{Command: "linux", Path: "/boot/vmlinuz-" + bundle.ABI}},
			Initrd: []kernelinstall.GRUBPathToken{{Command: "initrd", Path: "/boot/initrd.img-" + bundle.ABI}},
		},
	}
	if err := validateInstalledGRUBEntriesForTest(t, bundle, entries); err != nil {
		t.Fatalf("complete embedded GRUB entries: %v", err)
	}
	entries[0].DeviceTrees = []kernelinstall.GRUBPathToken{{Command: "devicetree", Path: "/boot/dtb-foreign"}}
	if err := validateInstalledGRUBEntriesForTest(t, bundle, entries); err == nil || !strings.Contains(err.Error(), "external device-tree") {
		t.Fatalf("embedded external-authority error = %v", err)
	}
}

// TestValidateInstalledGRUBEntriesRejectsSameBasenameElsewhere ensures a stock
// entry cannot redirect the selected DTB through a different GRUB directory.
func TestValidateInstalledGRUBEntriesRejectsSameBasenameElsewhere(t *testing.T) {
	bundle := installedTestBundle(kernel.DTBDeliveryExternalRequired)
	entries := externalInstalledTestEntries(bundle)
	entries[0].DeviceTrees[0].Path = "/foreign/dtb-" + bundle.ABI
	if err := validateInstalledGRUBEntriesForTest(t, bundle, entries); err == nil || !strings.Contains(err.Error(), "does not bind") {
		t.Fatalf("redirected selected-DTB error = %v", err)
	}
}

// externalInstalledTestEntries returns stock normal and recovery entries bound
// to the one selected ABI-stamped DTB.
func externalInstalledTestEntries(bundle kernel.Bundle) []kernelinstall.GRUBEntry {
	entries := make([]kernelinstall.GRUBEntry, 0, 2)
	for _, recovery := range []bool{false, true} {
		title := "Ubuntu " + bundle.ABI
		if recovery {
			title += " (recovery mode)"
		}
		entries = append(entries, kernelinstall.GRUBEntry{
			Title: title, Recovery: recovery,
			Linux:       []kernelinstall.GRUBPathToken{{Command: "linux", Path: "/boot/vmlinuz-" + bundle.ABI}},
			Initrd:      []kernelinstall.GRUBPathToken{{Command: "initrd", Path: "/boot/initrd.img-" + bundle.ABI}},
			DeviceTrees: []kernelinstall.GRUBPathToken{{Command: "devicetree", Path: "/dtb-" + bundle.ABI}},
		})
	}
	return entries
}

// validateInstalledGRUBEntriesForTest supplies the canonical files whose
// identities every synthetic GRUB token must prove.
func validateInstalledGRUBEntriesForTest(t *testing.T, bundle kernel.Bundle, entries []kernelinstall.GRUBEntry) error {
	t.Helper()
	return validateInstalledGRUBEntries(context.Background(), installedGRUBTestRoot(t, bundle), bundle, entries)
}

// installedGRUBTestRoot creates one canonical exact-ABI kernel/initramfs pair.
func installedGRUBTestRoot(t *testing.T, bundle kernel.Bundle) string {
	t.Helper()
	root := t.TempDir()
	writeInstalledGRUBTestFile(t, filepath.Join(root, "boot/vmlinuz-"+bundle.ABI), "canonical kernel bytes")
	writeInstalledGRUBTestFile(t, filepath.Join(root, "boot/initrd.img-"+bundle.ABI), "canonical initramfs bytes")
	return root
}

// writeInstalledGRUBTestFile creates one regular fixture artefact.
func writeInstalledGRUBTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
