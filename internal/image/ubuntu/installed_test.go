package ubuntu

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
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

// TestInstallInstalledSystemSupportUsesOfflineRootContracts proves the selected
// profile and initramfs are materialised without probing an unmounted target.
func TestInstallInstalledSystemSupportUsesOfflineRootContracts(t *testing.T) {
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
		"/usr/bin/dracut", "dracutbasedir=/usr/lib/dracut",
		`module_directory="$root/usr/lib/modules/$abi"`, `--kmoddir "$module_directory"`,
		`firmware_directory="$root/usr/lib/firmware"`, `tool_firmware_directory=/usr/lib/firmware`,
		`install -d -m 0755 "$tool_firmware_directory"`,
		`cp -a -- "$firmware_directory/." "$tool_firmware_directory/"`, `--fwdir "$tool_firmware_directory"`,
		"--conf /dev/null", `--confdir "$configuration"`,
		"--no-hostonly", "--no-hostonly-cmdline", "--reproducible",
		`sh -n "$root/etc/grub.d/10_linux"`,
		`test ! -L "$module_directory/modules.dep"`,
		"lib/dracut/hooks/cmdline/00-parse-root.sh", `usr/lib/modules/$abi/modules.dep`,
		`lsinitramfs -l "$temporary"`, `$1 ~ /^-/`, `usr/lib/modules/$abi/kernel/`,
		`mv -f -- "$temporary" "$root/boot/initrd.img-$abi"`,
		`rm -f -- "$contents"`, `rmdir -- "$configuration"`,
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("installed-system command lacks %q", required)
		}
	}
	for _, forbidden := range []string{`chroot "$root" update-initramfs`, `chroot "$root" update-grub`, `--sysroot "$root"`, "grub-probe", "\nmount ", "\n\tmount ", "--privileged", "SYS_ADMIN"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("installed-system command contains offline-root operation %q", forbidden)
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

// TestValidateInstalledGRUBGenerator accepts only the stock exact-version DTB
// lookup and its one corresponding device-tree emission.
func TestValidateInstalledGRUBGenerator(t *testing.T) {
	valid := `#!/bin/sh
linux_entry ()
{
  version="$2"
  if test -n "${dtb}" ; then
    cat << EOF
	devicetree	${rel_dirname}/${dtb}
EOF
  fi
}
for linux in ${reverse_sorted_list}; do
  basename=` + "`" + `basename $linux` + "`" + `
  dirname=` + "`" + `dirname $linux` + "`" + `
  rel_dirname=` + "`" + `make_system_path_relative_to_its_root $dirname` + "`" + `
  version=` + "`" + `echo $basename | sed -e "s,^[^0-9]*-,,g"` + "`" + `
  alt_version=` + "`" + `echo $version | sed -e "s,\.old$,,g"` + "`" + `
  dtb=
  for i in "dtb-${version}" "dtb-${alt_version}" "dtb"; do
    if test -e "${dirname}/${i}" ; then
      dtb="$i"
      break
    fi
  done
  linux_entry "${OS}" "${version}" simple
done
`
	for _, test := range []struct {
		name    string
		content string
		mode    os.FileMode
		wantErr bool
	}{
		{name: "stock exact ABI", content: valid, mode: 0o755},
		{name: "shared only", content: strings.Replace(valid, `"dtb-${version}" "dtb-${alt_version}" `, "", 1), mode: 0o755, wantErr: true},
		{name: "missing emission", content: strings.Replace(valid, "\tdevicetree\t${rel_dirname}/${dtb}\n", "", 1), mode: 0o755, wantErr: true},
		{name: "conflicting emission", content: strings.Replace(valid, "${rel_dirname}/${dtb}", "/shared.dtb", 1), mode: 0o755, wantErr: true},
		{name: "missing function", content: strings.Replace(valid, "linux_entry ()\n", "", 1), mode: 0o755, wantErr: true},
		{name: "missing guard", content: strings.Replace(valid, `  if test -n "${dtb}" ; then`+"\n", "", 1), mode: 0o755, wantErr: true},
		{name: "missing call", content: strings.Replace(valid, `  linux_entry "${OS}" "${version}" simple`+"\n", "", 1), mode: 0o755, wantErr: true},
		{name: "missing existence test", content: strings.Replace(valid, `if test -e "${dirname}/${i}" ; then`, "if true; then", 1), mode: 0o755, wantErr: true},
		{name: "missing assignment", content: strings.Replace(valid, "      dtb=\"$i\"\n", "", 1), mode: 0o755, wantErr: true},
		{name: "missing break", content: strings.Replace(valid, "      break\n", "", 1), mode: 0o755, wantErr: true},
		{name: "late overwrite", content: valid + "dtb=shared.dtb\n", mode: 0o755, wantErr: true},
		{name: "missing version derivation", content: strings.Replace(valid, `  version=`+"`"+`echo $basename | sed -e "s,^[^0-9]*-,,g"`+"`"+"\n", "", 1), mode: 0o755, wantErr: true},
		{name: "wrong version derivation", content: strings.Replace(valid, `echo $basename | sed -e "s,^[^0-9]*-,,g"`, "printf foreign", 1), mode: 0o755, wantErr: true},
		{name: "late version overwrite", content: strings.Replace(valid, "  dtb=\n", "  version=foreign\n  dtb=\n", 1), mode: 0o755, wantErr: true},
		{name: "emission after lookup", content: strings.Replace(valid, "\tdevicetree\t${rel_dirname}/${dtb}\n", "", 1) + "devicetree ${rel_dirname}/${dtb}\n", mode: 0o755, wantErr: true},
		{name: "not executable", content: valid, mode: 0o644, wantErr: true},
		{name: "group writable", content: valid, mode: 0o775, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "10_linux")
			if err := os.WriteFile(path, []byte(test.content), test.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, test.mode); err != nil {
				t.Fatal(err)
			}
			err := validateInstalledGRUBGenerator(path)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateInstalledGRUBGenerator() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}

	t.Run("symlink", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(directory, "target")
		if err := os.WriteFile(target, []byte(valid), 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(directory, "10_linux")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if err := validateInstalledGRUBGenerator(link); err == nil || !strings.Contains(err.Error(), "non-symlink") {
			t.Fatalf("symlink error = %v", err)
		}
	})
}

// TestValidateInstalledGRUBGeneratorIntegration optionally checks the exact
// stock generator supplied by an Ubuntu image or installed target.
func TestValidateInstalledGRUBGeneratorIntegration(t *testing.T) {
	path := os.Getenv("LEXR_TEST_GRUB_GENERATOR")
	if path == "" {
		t.Skip("set LEXR_TEST_GRUB_GENERATOR to an extracted Ubuntu 10_linux")
	}
	if err := validateInstalledGRUBGenerator(path); err != nil {
		t.Fatal(err)
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
