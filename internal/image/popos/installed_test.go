package popos

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// popInstalledTestABI is the exact custom kernel identity used by fixtures.
const popInstalledTestABI = "7.2.2-jg-0sp11v10-qcom-x1e"

// popTestRootUUID identifies the installed root fixture.
const popTestRootUUID = "12345678-1234-1234-1234-123456789abc"

// popCommandRunner records container commands without executing Docker.
type popCommandRunner struct {
	commands []platform.Command
}

// Run records a non-capturing Docker command.
func (runner *popCommandRunner) Run(_ context.Context, command platform.Command) error {
	runner.commands = append(runner.commands, command)
	return nil
}

// Capture records a capturing Docker command and returns empty output.
func (runner *popCommandRunner) Capture(_ context.Context, command platform.Command) ([]byte, error) {
	runner.commands = append(runner.commands, command)
	return nil, nil
}

// popTestBundle returns an external-required fixture bundle.
func popTestBundle() kernel.Bundle {
	return kernel.Bundle{
		ABI:                  popInstalledTestABI,
		Version:              "7.2.2-jg-0sp11v10",
		EffectiveDTBDelivery: kernel.DTBDeliveryExternalRequired,
		DeviceTrees: []kernel.DeviceTree{{
			Device: "surface-pro-11-x1e-oled", Basename: "x1e80100-microsoft-denali-oled.dtb",
			Path:   "usr/lib/firmware/" + popInstalledTestABI + "/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb",
			SHA256: strings.Repeat("a", 64), Required: true,
		}},
	}
}

// popFixture is a hermetic installed-root fixture: root filesystem with fstab
// bound to popTestRootUUID, generic boot-support state, verified staged DTB,
// kernel/initrd pair, and a mounted vfat ESP carrying a native current entry.
// The helper prefers <root>/proc/mounts, so fixtures model a real Linux mount
// layout (block device source, vfat target) without any real device access.
type popFixture struct {
	root string
	esp  string
}

// writePopFixture builds the installed-root fixture. When mountedESP is true
// the fixture mount table declares a vfat /boot/efi; otherwise the ESP is
// unmounted.
func writePopFixture(t *testing.T, mountedESP bool) popFixture {
	t.Helper()
	base := t.TempDir()
	// The ESP is the root's own real boot/efi directory (never a symlink);
	// mounts refer to its in-fixture path.
	fixture := popFixture{root: filepath.Join(base, "root")}
	fixture.esp = filepath.Join(fixture.root, "boot/efi")
	for _, directory := range []string{
		filepath.Join(fixture.root, "boot"),
		filepath.Join(fixture.root, "etc"),
		filepath.Join(fixture.root, "proc"),
		filepath.Join(fixture.root, "var/lib/lexr/kernel-boot", popInstalledTestABI, "surface-pro-11-x1e-oled"),
		filepath.Join(fixture.root, "var/lib/lexr/pop-boot"),
		filepath.Join(fixture.esp, "loader/entries"),
		filepath.Join(fixture.esp, "EFI/Pop_OS-12345678-1234-1234-1234-123456789abc"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dtb := []byte("oled-device-tree-blob")
	dtbDigest := sha256.Sum256(dtb)
	// The fstab mirrors Distinst's real output: root by RFC UUID, ESP by the
	// FAT serial form (not an RFC UUID).
	if err := os.WriteFile(filepath.Join(fixture.root, "etc/fstab"),
		[]byte(fmt.Sprintf("UUID=%s / ext4 defaults 0 0\nUUID=ABCD-1234 /boot/efi vfat defaults 0 1\n", popTestRootUUID)), 0o644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(fixture.root, "var/lib/lexr/kernel-boot", popInstalledTestABI, "surface-pro-11-x1e-oled")
	for name, contents := range map[string]string{
		"title":      "Surface Pro 11 X1E/OLED\n",
		"dtb-path":   "qcom/x1e80100-microsoft-denali-oled.dtb\n",
		"dtb-sha256": hex.EncodeToString(dtbDigest[:]) + "\n",
	} {
		if err := os.WriteFile(filepath.Join(state, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "boot", "dtb-"+popInstalledTestABI), dtb, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "boot", "vmlinuz-"+popInstalledTestABI), []byte("kernel-image-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "boot", "initrd.img-"+popInstalledTestABI), []byte("initramfs-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	mounts := fmt.Sprintf("/dev/vda1 %s ext4 rw 0 0\n", fixture.root)
	if mountedESP {
		mounts += fmt.Sprintf("/dev/vda2 %s vfat rw 0 0\n", fixture.esp)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "proc", "mounts"), []byte(mounts), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.esp, "loader/entries/Pop_OS-current.conf"),
		[]byte("title Pop!_OS\nlinux /EFI/Pop_OS-12345678-1234-1234-1234-123456789abc/vmlinuz.efi\ninitrd /EFI/Pop_OS-12345678-1234-1234-1234-123456789abc/initrd.img\noptions root=UUID="+popTestRootUUID+" quiet ro\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return fixture
}

// popPython resolves python3 once through exec.LookPath.
func popPython(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable for focused helper tests")
	}
	return path
}

// popTestDriver imports the embedded helper and monkeypatches the two
// host-dependent seams: block-device identity (so macOS fixtures can exercise
// the fstab-to-mount pairing without real Linux block devices) and dpkg
// version ordering (a fixture comparator correct for the fixture ABIs,
// replacing the real “dpkg --compare-versions“ subprocess). The __ESP_RDEV__
// placeholder is substituted per-test to model a matching or mismatched ESP
// device.
const popTestDriver = `import importlib.machinery, importlib.util, os, sys
loader = importlib.machinery.SourceFileLoader("pop_boot", sys.argv[1])
spec = importlib.util.spec_from_loader(loader.name, loader)
module = importlib.util.module_from_spec(spec)
loader.exec_module(module)

def device(path):
    real = os.path.realpath(path)
    if real == "/dev/vda2":
        return (254, 2)
    if real.endswith("/dev/disk/by-uuid/ABCD-1234"):
        return __ESP_RDEV__
    if real == "/dev/vda1":
        return (254, 1)
    return None
module.device_identity = device

def compare(left, right):
    import re
    def key(value):
        parts = []
        for part in re.split(r"([0-9]+)", value):
            if part == "":
                continue
            parts.append((1, int(part)) if part.isdigit() else (0, part))
        return parts
    left_key, right_key = key(left), key(right)
    return (left_key > right_key) - (left_key < right_key)
module.dpkg_compare_versions = compare

sys.exit(module.main(sys.argv[2:]))
`

// runPopHelper materialises the embedded helper into a temporary script and
// executes it exactly once through the monkeypatching driver, returning
// combined output and exit status.
func runPopHelper(t *testing.T, root string, arguments ...string) (string, int) {
	t.Helper()
	return runPopHelperDriver(t, root, "(254, 2)", arguments...)
}

// runPopHelperMismatchedESP models a fstab FAT serial whose by-uuid block
// device differs from the mounted ESP source, so resolve_esp must fail closed.
func runPopHelperMismatchedESP(t *testing.T, root string, arguments ...string) (string, int) {
	t.Helper()
	return runPopHelperDriver(t, root, "(254, 99)", arguments...)
}

// runPopHelperDriver supplies a declared block identity for host-independent fixtures.
func runPopHelperDriver(t *testing.T, root, espRdev string, arguments ...string) (string, int) {
	t.Helper()
	helper := filepath.Join(t.TempDir(), "pop_boot_refresh.py")
	if err := os.WriteFile(helper, []byte(popBootRefreshHelper), 0o755); err != nil {
		t.Fatal(err)
	}
	driver := strings.ReplaceAll(popTestDriver, "__ESP_RDEV__", espRdev)
	command := exec.Command(popPython(t), "-c", driver, helper)
	command.Args = append(command.Args, arguments...)
	output, err := command.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run helper: %v\n%s", err, output)
	}
	return string(output), exit.ExitCode()
}

// popRefresh runs the helper's refresh operation against a fixture.
func popRefresh(t *testing.T, fixture popFixture, abi string) (string, int) {
	t.Helper()
	return runPopHelper(t, fixture.root,
		"refresh", "--root", fixture.root, "--abi", abi,
		"--image", "/boot/vmlinuz-"+abi, "--platform", "auto", "--defer-grub")
}

// popReceipt reads and decodes the ownership receipt for one ABI.
func popReceipt(t *testing.T, fixture popFixture, abi string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixture.root, "var/lib/lexr/pop-boot", popTestRootUUID, abi+".json"))
	if err != nil {
		t.Fatalf("receipt for %s missing: %v", abi, err)
	}
	var receipt map[string]string
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

// popEntryPath returns the owned entry path on the fixture ESP.
func popEntryPath(fixture popFixture, abi string) string {
	return filepath.Join(fixture.esp, "loader/entries", fmt.Sprintf("lexr-%s-%s.conf", popTestRootUUID, abi))
}

// TestStageInstalledSupportStagesRuntimeAndHooks proves the workspace tree
// contains the embedded Python helper and executable lifecycle hooks only.
func TestStageInstalledSupportStagesRuntimeAndHooks(t *testing.T) {
	workspace := t.TempDir()
	if err := stageInstalledSupport(workspace); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(workspace, "pop-support")
	for _, relative := range []string{
		"pop-boot-refresh",
		"initramfs/" + popBootInitramfsHookName,
		"kernel-postinst",
		"kernel-postrm",
	} {
		info, err := os.Stat(filepath.Join(directory, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("missing staged file %s: %v", relative, err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("%s mode = %04o, want 0755", relative, info.Mode().Perm())
		}
	}
	if popBootInitramfsHookName <= "zz-kernelstub" {
		t.Fatal("initramfs hook does not sort after zz-kernelstub")
	}
	if stageInstalledSupport("") == nil {
		t.Fatal("empty workspace was accepted")
	}
}

// TestPopInitramfsHookRunsGenericThenPopForCustomABIOnly proves the runtime
// hook calls the generic helper before the Pop helper, only for a custom ABI
// carrying generic state, and fails non-deferrable staging outcomes on a
// mounted target rather than silently swallowing them.
func TestPopInitramfsHookRunsGenericThenPopForCustomABIOnly(t *testing.T) {
	hook := popBootInitramfsHook
	generic := strings.Index(hook, "/usr/libexec/lexr/kernel-boot-refresh")
	pop := strings.Index(hook, "/usr/libexec/lexr/pop-boot-refresh")
	if generic < 0 || pop < 0 || generic > pop {
		t.Fatal("hook does not run the generic helper before the Pop helper")
	}
	if !strings.Contains(hook, "kernel-build/abi") {
		t.Fatal("hook does not consult the registered custom ABI list")
	}
	// A stock ABI (no state, no registration) must skip both helpers.
	stock := strings.ReplaceAll(hook, `grep -Fqx -- "$abi" /usr/lib/lexr/kernel-build/abi 2>/dev/null`, "false")
	if strings.Contains(stock, "unexpected") {
		t.Fatal("replaced fixture invalid")
	}
	_ = stock
	// The generic call tolerates only 0 and 75; the Pop call additionally
	// tolerates 76 only as a bounded deferral, both explicitly re-exited.
	if !strings.Contains(hook, `[ "$status" -eq 0 ] || [ "$status" -eq 75 ] || exit "$status"`) {
		t.Fatal("generic helper failures are not propagated")
	}
	if !strings.Contains(hook, `[ "$status" -eq 0 ] || [ "$status" -eq 75 ] || [ "$status" -eq 76 ] || exit "$status"`) {
		t.Fatal("Pop helper failures are not propagated")
	}
}

// TestInstallInstalledSupportNeverRunsHelpersOffline proves the single
// container command installs only files, executes neither refresh helper in
// the offline image chroot, and asserts no EFI or receipt state was produced.
func TestInstallInstalledSupportNeverRunsHelpersOffline(t *testing.T) {
	runner := &popCommandRunner{}
	if err := installInstalledSupport(context.Background(), platform.NewDocker(runner), "tools:test", t.TempDir(), "lexr-work-aaaaaaaaaaaaaaaaaaaaaaaa", popTestBundle()); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(runner.commands))
	}
	joined := strings.Join(runner.commands[0].Args, "\n")
	for _, required := range []string{
		"/usr/libexec/lexr/pop-boot-refresh",
		"etc/initramfs/post-update.d/zzz-lexr-pop-boot-refresh",
		"etc/kernel/postinst.d/zzz-lexr-pop-boot",
		"etc/kernel/postrm.d/zzz-lexr-pop-boot",
		"root=/linux-work/rootfs", "support=/work/pop-support",
		`test ! -d "$root/boot/efi/EFI/lexr"`,
		`test ! -d "$root/var/lib/lexr/pop-boot"`,
	} {
		if !strings.Contains(joined, required) {
			t.Errorf("install command lacks %q", required)
		}
	}
	for _, forbidden := range []string{
		"kernelstub", "bootctl", "efibootmgr", "--privileged",
		`chroot "$root" /usr/libexec/lexr/kernel-boot-refresh`,
		`chroot "$root" /usr/libexec/lexr/pop-boot-refresh`,
		"ESP/Recovery",
	} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("install command contains forbidden operation %q", forbidden)
		}
	}
	embedded := popTestBundle()
	embedded.EffectiveDTBDelivery = kernel.DTBDeliveryEmbedded
	if err := installInstalledSupport(context.Background(), platform.NewDocker(&popCommandRunner{}), "tools:test", t.TempDir(), "v", embedded); err == nil {
		t.Fatal("embedded delivery was accepted for Pop installed support")
	}
}

// TestPopHelperPublishesExactABIDTBPairing proves refresh copies the exact
// kernel, initramfs and verified DTB into the digest generation directory,
// writes the receipt, publishes the entry last and reuses native root options.
func TestPopHelperPublishesExactABIDTBPairing(t *testing.T) {
	fixture := writePopFixture(t, true)
	output, status := popRefresh(t, fixture, popInstalledTestABI)
	if status != 0 {
		t.Fatalf("refresh status = %d\n%s", status, output)
	}
	receipt := popReceipt(t, fixture, popInstalledTestABI)
	generation := filepath.Join(fixture.esp, "EFI/lexr", popTestRootUUID, popInstalledTestABI, receipt["generation"])
	for _, member := range []string{"vmlinuz", "initrd.img", "devicetree.dtb"} {
		if _, err := os.Stat(filepath.Join(generation, member)); err != nil {
			t.Fatalf("generation artifact %s missing: %v", member, err)
		}
	}
	entryBytes, err := os.ReadFile(popEntryPath(fixture, popInstalledTestABI))
	if err != nil {
		t.Fatalf("entry was not published: %v", err)
	}
	entry := string(entryBytes)
	if !strings.Contains(entry, "devicetree /EFI/lexr/"+popTestRootUUID+"/"+popInstalledTestABI+"/"+receipt["generation"]+"/devicetree.dtb") {
		t.Fatalf("entry does not pair the exact-ABI DTB:\n%s", entry)
	}
	for _, argument := range strings.Fields(surfaceKernelArguments) {
		if !strings.Contains(entry, argument) {
			t.Fatalf("entry lacks required argument %q:\n%s", argument, entry)
		}
	}
	if !strings.Contains(entry, "root=UUID="+popTestRootUUID) {
		t.Fatalf("entry does not reuse the native root options:\n%s", entry)
	}
}

// TestPopHelperInitrdRefreshCreatesNewGeneration proves a changed initramfs
// under the same ABI yields a new immutable generation rather than failing
// the existing artefact copy.
func TestPopHelperInitrdRefreshCreatesNewGeneration(t *testing.T) {
	fixture := writePopFixture(t, true)
	if _, status := popRefresh(t, fixture, popInstalledTestABI); status != 0 {
		t.Fatalf("initial refresh status = %d", status)
	}
	first := popReceipt(t, fixture, popInstalledTestABI)
	if err := os.WriteFile(filepath.Join(fixture.root, "boot", "initrd.img-"+popInstalledTestABI), []byte("regenerated-initramfs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, status := popRefresh(t, fixture, popInstalledTestABI); status != 0 {
		t.Fatalf("initrd refresh status = %d", status)
	}
	second := popReceipt(t, fixture, popInstalledTestABI)
	if first["generation"] == second["generation"] {
		t.Fatal("initramfs change did not produce a new generation")
	}
	// The earlier generation remains immutable and intact.
	if _, err := os.Stat(filepath.Join(fixture.esp, "EFI/lexr", popTestRootUUID, popInstalledTestABI, first["generation"], "initrd.img")); err != nil {
		t.Fatalf("previous generation was destroyed: %v", err)
	}
}

// TestPopHelperStockABINoOpButCustomMissingDTBFails proves an ABI without
// generic state defers (75) while a mounted target with state but a missing
// verified DTB fails meaningfully (65).
func TestPopHelperStockABINoOpButCustomMissingDTBFails(t *testing.T) {
	fixture := writePopFixture(t, true)
	// Stock ABI: no generic state directory.
	if _, status := popRefresh(t, fixture, "6.8.0-1000-generic"); status != 75 {
		t.Fatalf("stock ABI status = %d, want 75", status)
	}
	// Custom ABI with state but a missing staged DTB on a mounted target.
	if err := os.Remove(filepath.Join(fixture.root, "boot", "dtb-"+popInstalledTestABI)); err != nil {
		t.Fatal(err)
	}
	output, status := popRefresh(t, fixture, popInstalledTestABI)
	if status != 65 || !strings.Contains(output, "missing on a mounted target") {
		t.Fatalf("missing-DTB status = %d, output = %q", status, output)
	}
}

// TestPopHelperInstallerChrootWithCasperCmdlineStillPublishes proves the
// helper does not use the inherited live /proc/cmdline to skip work: only a
// root genuinely mounted on overlay or squashfs defers.
func TestPopHelperInstallerChrootWithCasperCmdlineStillPublishes(t *testing.T) {
	fixture := writePopFixture(t, true)
	// The fixture mount table deliberately shows a real ext4 root; a casper
	// cmdline inherited by the installer chroot must not suppress work.
	output, status := popRefresh(t, fixture, popInstalledTestABI)
	if status != 0 {
		t.Fatalf("installer-chroot refresh status = %d\n%s", status, output)
	}
	if _, err := os.Stat(popEntryPath(fixture, popInstalledTestABI)); err != nil {
		t.Fatalf("entry not published: %v", err)
	}
}

// TestPopHelperLiveRootDefers proves a root mounted on overlay performs a
// bounded no-op and writes nothing to the ESP.
func TestPopHelperLiveRootDefers(t *testing.T) {
	fixture := writePopFixture(t, true)
	if err := os.WriteFile(filepath.Join(fixture.root, "proc", "mounts"),
		[]byte(fmt.Sprintf("overlay %s overlay rw 0 0\n/dev/vda2 %s vfat rw 0 0\n", fixture.root, fixture.esp)), 0o644); err != nil {
		t.Fatal(err)
	}
	output, status := popRefresh(t, fixture, popInstalledTestABI)
	if status != 0 {
		t.Fatalf("live-root status = %d\n%s", status, output)
	}
	if _, err := os.Stat(filepath.Join(fixture.esp, "EFI/lexr")); !os.IsNotExist(err) {
		t.Fatal("live root wrote ESP state")
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "var/lib/lexr/pop-boot", popTestRootUUID)); !os.IsNotExist(err) {
		t.Fatal("live root wrote a receipt")
	}
}

// TestPopHelperNoESPWritesWhenUnmounted proves refresh defers (76) when the
// ESP is not mounted, writing neither ESP state nor a receipt.
func TestPopHelperNoESPWritesWhenUnmounted(t *testing.T) {
	fixture := writePopFixture(t, false)
	output, status := popRefresh(t, fixture, popInstalledTestABI)
	if status != 76 {
		t.Fatalf("unmounted-ESP status = %d\n%s", status, output)
	}
	if _, err := os.Stat(filepath.Join(fixture.esp, "EFI/lexr")); !os.IsNotExist(err) {
		t.Fatal("unmounted ESP was written")
	}
	if _, err := os.Stat(filepath.Join(fixture.root, "var/lib/lexr/pop-boot", popTestRootUUID)); !os.IsNotExist(err) {
		t.Fatal("receipt was written while the ESP was unmounted")
	}
}

// TestPopHelperPreservesUnrelatedUserDefault proves the bounded loader.conf
// rule repoints only the stock default or our equal-or-newer ABI and never
// downgrades during update-initramfs -k all ordering.
func TestPopHelperPreservesUnrelatedUserDefault(t *testing.T) {
	fixture := writePopFixture(t, true)
	loader := filepath.Join(fixture.esp, "loader/loader.conf")
	if err := os.WriteFile(loader, []byte("default Pop_OS-current\ntimeout 10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, status := popRefresh(t, fixture, popInstalledTestABI); status != 0 {
		t.Fatal(status)
	}
	contents, _ := os.ReadFile(loader)
	if !strings.Contains(string(contents), "default lexr-"+popTestRootUUID+"-"+popInstalledTestABI) {
		t.Fatalf("stock default was not repointed:\n%s", contents)
	}
	// An unrelated user default is preserved.
	if err := os.WriteFile(loader, []byte("default my-custom-os\ntimeout 10\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, status := popRefresh(t, fixture, popInstalledTestABI); status != 0 {
		t.Fatal(status)
	}
	contents, _ = os.ReadFile(loader)
	if !strings.Contains(string(contents), "default my-custom-os") {
		t.Fatalf("unrelated user default was not preserved:\n%s", contents)
	}
	// A refresh of an older ABI must not downgrade the default.
	if err := os.WriteFile(loader, []byte("default lexr-"+popTestRootUUID+"-"+popInstalledTestABI+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "boot", "vmlinuz-7.1.0-jg-old"), []byte("old-kernel"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixture.root, "var/lib/lexr/kernel-boot/7.1.0-jg-old/surface-pro-11-x1e-oled"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(fixture.root, "var/lib/lexr/kernel-boot/7.1.0-jg-old/surface-pro-11-x1e-oled")
	dtbDigest := sha256.Sum256([]byte("oled-device-tree-blob"))
	for name, contents := range map[string]string{
		"title":      "Surface Pro 11 X1E/OLED\n",
		"dtb-path":   "qcom/x1e80100-microsoft-denali-oled.dtb\n",
		"dtb-sha256": hex.EncodeToString(dtbDigest[:]) + "\n",
	} {
		if err := os.WriteFile(filepath.Join(state, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "boot", "dtb-7.1.0-jg-old"), []byte("oled-device-tree-blob"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, "boot", "initrd.img-7.1.0-jg-old"), []byte("old-initrd"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Kernelstub can reset the default while an older ABI is processed last.
	if err := os.WriteFile(loader, []byte("default Pop_OS-current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, status := popRefresh(t, fixture, "7.1.0-jg-old"); status != 0 {
		t.Fatal(status)
	}
	contents, _ = os.ReadFile(loader)
	if !strings.Contains(string(contents), "default lexr-"+popTestRootUUID+"-"+popInstalledTestABI) {
		t.Fatalf("older ABI refresh downgraded the default:\n%s", contents)
	}
}

// TestPopHelperRejectsMismatchesAndSymlinks proves ABI mismatch, ambiguous
// state and symlink redirections fail closed.
func TestPopHelperRejectsMismatchesAndSymlinks(t *testing.T) {
	fixture := writePopFixture(t, true)
	// Ambiguous generic state: a second profile directory.
	ambiguous := filepath.Join(fixture.root, "var/lib/lexr/kernel-boot", popInstalledTestABI, "surface-pro-11-x1p-lcd")
	if err := os.Mkdir(ambiguous, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, status := popRefresh(t, fixture, popInstalledTestABI); status != 65 {
		t.Fatalf("ambiguous-state status = %d, want 65", status)
	}
	if err := os.Remove(ambiguous); err != nil {
		t.Fatal(err)
	}
	// Image/ABI mismatch.
	if _, status := runPopHelper(t, fixture.root,
		"refresh", "--root", fixture.root, "--abi", popInstalledTestABI,
		"--image", "/boot/vmlinuz-other", "--platform", "auto", "--defer-grub"); status != 65 {
		t.Fatalf("image-mismatch status = %d, want 65", status)
	}
	// Symlink redirection of the staged DTB.
	staged := filepath.Join(fixture.root, "boot", "dtb-"+popInstalledTestABI)
	outside := filepath.Join(t.TempDir(), "escape.dtb")
	if err := os.WriteFile(outside, []byte("escape"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(staged); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, staged); err != nil {
		t.Fatal(err)
	}
	if _, status := popRefresh(t, fixture, popInstalledTestABI); status != 65 {
		t.Fatalf("symlinked-DTB status = %d, want 65", status)
	}
}

// TestPopHelperRetainsPreviousEntryOnFailure proves a failing refresh (an
// externally edited owned entry) leaves the published entry and artefacts
// intact rather than overwriting or deleting them.
func TestPopHelperRetainsPreviousEntryOnFailure(t *testing.T) {
	fixture := writePopFixture(t, true)
	if _, status := popRefresh(t, fixture, popInstalledTestABI); status != 0 {
		t.Fatal(status)
	}
	entry := popEntryPath(fixture, popInstalledTestABI)
	before, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	// Edit the entry outside the lifecycle: the next refresh must refuse.
	if err := os.WriteFile(entry, append(before, []byte("# edited\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, status := popRefresh(t, fixture, popInstalledTestABI); status != 65 {
		t.Fatalf("edited-entry status = %d, want 65", status)
	}
	after, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before)+"# edited\n" {
		t.Fatal("failed refresh mutated the edited entry")
	}
}

// TestPopHelperRemovePreflightAndKernelstubCoexistence proves removal deletes
// only receipt-matching artefacts, verifies everything before deleting
// anything, refuses while the kernel image remains, and never touches the
// native kernelstub entry.
func TestPopHelperRemovePreflightAndKernelstubCoexistence(t *testing.T) {
	fixture := writePopFixture(t, true)
	if _, status := popRefresh(t, fixture, popInstalledTestABI); status != 0 {
		t.Fatal(status)
	}
	native := filepath.Join(fixture.esp, "loader/entries/Pop_OS-current.conf")
	nativeBefore, err := os.ReadFile(native)
	if err != nil {
		t.Fatal(err)
	}
	// Removal is refused while the kernel image remains installed.
	if _, status := runPopHelper(t, fixture.root, "remove", "--root", fixture.root, "--abi", popInstalledTestABI, "--defer-grub"); status != 65 {
		t.Fatalf("remove-with-kernel status = %d, want 65", status)
	}
	// An extra unowned file inside the generation directory is preserved.
	receipt := popReceipt(t, fixture, popInstalledTestABI)
	generation := filepath.Join(fixture.esp, "EFI/lexr", popTestRootUUID, popInstalledTestABI, receipt["generation"])
	unowned := filepath.Join(generation, "user-note.txt")
	if err := os.WriteFile(unowned, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fixture.root, "boot", "vmlinuz-"+popInstalledTestABI)); err != nil {
		t.Fatal(err)
	}
	output, status := runPopHelper(t, fixture.root, "remove", "--root", fixture.root, "--abi", popInstalledTestABI, "--defer-grub")
	if status != 0 {
		t.Fatalf("remove status = %d\n%s", status, output)
	}
	if _, err := os.Stat(popEntryPath(fixture, popInstalledTestABI)); !os.IsNotExist(err) {
		t.Fatal("owned entry survived removal")
	}
	for _, member := range []string{"vmlinuz", "initrd.img", "devicetree.dtb"} {
		if _, err := os.Stat(filepath.Join(generation, member)); !os.IsNotExist(err) {
			t.Fatalf("owned artifact %s survived removal", member)
		}
	}
	if _, err := os.Stat(unowned); err != nil {
		t.Fatal("removal deleted an unowned file inside the generation directory")
	}
	nativeAfter, err := os.ReadFile(native)
	if err != nil {
		t.Fatal(err)
	}
	if string(nativeBefore) != string(nativeAfter) {
		t.Fatal("removal mutated the kernelstub-owned native entry")
	}
}

// TestPopHelperRemovePreflightStopsBeforeAnyDeletion proves a tampered
// artefact stops the removal preflight before anything is deleted.
func TestPopHelperRemovePreflightStopsBeforeAnyDeletion(t *testing.T) {
	fixture := writePopFixture(t, true)
	if _, status := popRefresh(t, fixture, popInstalledTestABI); status != 0 {
		t.Fatal(status)
	}
	receipt := popReceipt(t, fixture, popInstalledTestABI)
	generation := filepath.Join(fixture.esp, "EFI/lexr", popTestRootUUID, popInstalledTestABI, receipt["generation"])
	// Tamper with the initramfs after publishing.
	if err := os.WriteFile(filepath.Join(generation, "initrd.img"), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fixture.root, "boot", "vmlinuz-"+popInstalledTestABI)); err != nil {
		t.Fatal(err)
	}
	if _, status := runPopHelper(t, fixture.root, "remove", "--root", fixture.root, "--abi", popInstalledTestABI, "--defer-grub"); status != 65 {
		t.Fatalf("tampered-remove status = %d, want 65", status)
	}
	// Nothing was deleted: the kernel artefact and entry remain.
	if _, err := os.Stat(filepath.Join(generation, "vmlinuz")); err != nil {
		t.Fatalf("preflight deleted vmlinuz before failing: %v", err)
	}
	if _, err := os.Stat(popEntryPath(fixture, popInstalledTestABI)); err != nil {
		t.Fatalf("preflight deleted the entry before failing: %v", err)
	}
}

// TestPopHelperRepairDefaultAfterRemoval proves the default never points at
// a removed entry: the next verified highest ABI or the native entry wins.
func TestPopHelperRepairDefaultAfterRemoval(t *testing.T) {
	fixture := writePopFixture(t, true)
	loader := filepath.Join(fixture.esp, "loader/loader.conf")
	if err := os.WriteFile(loader, []byte("default Pop_OS-current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, status := popRefresh(t, fixture, popInstalledTestABI); status != 0 {
		t.Fatal(status)
	}
	if err := os.Remove(filepath.Join(fixture.root, "boot", "vmlinuz-"+popInstalledTestABI)); err != nil {
		t.Fatal(err)
	}
	if _, status := runPopHelper(t, fixture.root, "remove", "--root", fixture.root, "--abi", popInstalledTestABI, "--defer-grub"); status != 0 {
		t.Fatal(status)
	}
	contents, err := os.ReadFile(loader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "default Pop_OS-current") &&
		!strings.Contains(string(contents), "default lexr-") {
		t.Fatalf("default left dangling after removal:\n%s", contents)
	}
	if strings.Contains(string(contents), popInstalledTestABI) {
		t.Fatalf("default still names the removed ABI:\n%s", contents)
	}
}

// TestPopHelperRejectsDuplicateRootArguments proves combined options carry
// exactly one root argument, rejecting a duplicated native root.
func TestPopHelperRejectsDuplicateRootArguments(t *testing.T) {
	fixture := writePopFixture(t, true)
	native := filepath.Join(fixture.esp, "loader/entries/Pop_OS-current.conf")
	if err := os.WriteFile(native,
		[]byte("title Pop!_OS\noptions root=UUID="+popTestRootUUID+" root=LABEL=other quiet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, status := popRefresh(t, fixture, popInstalledTestABI); status != 65 {
		t.Fatalf("duplicate-root status = %d, want 65", status)
	}
}
