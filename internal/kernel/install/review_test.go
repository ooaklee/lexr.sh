package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestKnownHeaderSymlinksAreIgnoredWithoutFollowing verifies developer headers
// do not make an otherwise bootable fallback fail preflight.
func TestKnownHeaderSymlinksAreIgnoredWithoutFollowing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic link creation is not reliably available on Windows")
	}
	root, bundle := fixtureEnvironment(t)
	moduleTree := filepath.Join(root, "usr/lib/modules", fixtureFallbackABI)
	if err := os.Symlink("/usr/src/linux-headers-"+fixtureFallbackABI, filepath.Join(moduleTree, "build")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("build", filepath.Join(moduleTree, "source")); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), fixtureRequest(root, bundle, true)); err != nil {
		t.Fatal(err)
	}
}

// TestUnverifiedLocalBundleRequiresExplicitAcceptance verifies the trust boundary.
func TestUnverifiedLocalBundleRequiresExplicitAcceptance(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	for index := range bundle.Packages {
		bundle.Packages[index].Verified = false
	}
	manager := fixtureManager(&fakeRunner{root: root})
	request := fixtureRequest(root, bundle, true)
	_, err := manager.Preflight(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "explicitly allow") {
		t.Fatalf("error = %v", err)
	}
	request.AllowUnverified = true
	plan, err := manager.Preflight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.UnverifiedAccepted {
		t.Fatalf("unverified acceptance was not recorded: %+v", plan)
	}
}

// TestLiveRootCommandsUseAptWithoutACommandShell covers the live-system branch.
func TestLiveRootCommandsUseAptWithoutACommandShell(t *testing.T) {
	t.Parallel()
	packages := []string{"/tmp/linux-modules-test.deb", "/tmp/linux-image-test.deb"}
	commands, err := installationCommands(string(filepath.Separator), fixtureTargetABI, packages)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 2 || filepath.Base(commands[0].Name) != "apt-get" || filepath.Base(commands[1].Name) != "update-initramfs" {
		t.Fatalf("unexpected live-root commands: %+v", commands)
	}
	for _, command := range commands {
		if filepath.Base(command.Name) == "update-grub" {
			t.Fatalf("redundant GRUB regeneration would discard package postinst changes: %+v", commands)
		}
	}
	for _, command := range commands {
		if filepath.Base(command.Name) == "sh" || filepath.Base(command.Name) == "bash" || slicesContain(command.Args, "-c") {
			t.Fatalf("command uses a shell: %+v", command)
		}
	}
}

// TestStaleTargetRecoveryEntryFailsPreflight preserves the legacy safety parity.
func TestStaleTargetRecoveryEntryFailsPreflight(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	grub := fixtureGRUB(false) + "menuentry 'Ubuntu recovery " + fixtureTargetABI + "' {\n" +
		" linux /boot/vmlinuz-" + fixtureTargetABI + "\n" +
		" initrd /boot/initrd.img-" + fixtureTargetABI + "\n}\n"
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
	_, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), fixtureRequest(root, bundle, true))
	if err == nil || !strings.Contains(err.Error(), "already has 1 GRUB entries") {
		t.Fatalf("error = %v", err)
	}
}

// TestGRUBTitleParsingIgnoresRecoveryInFlags verifies recovery classification
// uses the title rather than unrelated menuentry options.
func TestGRUBTitleParsingIgnoresRecoveryInFlags(t *testing.T) {
	t.Parallel()
	root, _ := fixtureEnvironment(t)
	grub := "menuentry 'Ubuntu " + fixtureFallbackABI + "' --class recovery-tools {\n" +
		" linux /boot/vmlinuz-" + fixtureFallbackABI + "\n" +
		" initrd /boot/initrd.img-" + fixtureFallbackABI + "\n}\n"
	path := filepath.Join(root, "boot/grub/grub.cfg")
	writeFixtureFile(t, path, grub)
	count, err := countGRUBEntries(context.Background(), path, fixtureFallbackABI, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("entry count = %d, want 1", count)
	}
}

// TestVerifyFallbackRejectsWrongPatchLineDeviceTree verifies the retained ABI
// cannot pass preflight with a legacy shared DTB containing different bytes.
func TestVerifyFallbackRejectsWrongPatchLineDeviceTree(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/x1e80100-microsoft-denali-oled.dtb"), "fallback oled dtb")
	writeFixtureFile(t, filepath.Join(root, "boot/sp11-denali.dtb"), "another ABI's oled dtb")
	grub := strings.Replace(
		fixtureGRUB(false),
		" initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		" devicetree /boot/sp11-denali.dtb\n initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		1,
	)
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
	_, err := verifyFallback(context.Background(), root, fixtureFallbackABI)
	if err == nil || !strings.Contains(err.Error(), "does not match installed ABI "+fixtureFallbackABI) {
		t.Fatalf("fallback DTB mismatch error = %v", err)
	}
}

// TestVerifyFallbackAcceptsABIStampedDeviceTree verifies a generated
// /dtb-<abi> token is bound to the current device's same-ABI firmware bytes.
func TestVerifyFallbackAcceptsABIStampedDeviceTree(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	deviceTree := "fallback oled dtb"
	token := "/dtb-7.2.0-sp11beta18"
	writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/x1e80100-microsoft-denali-oled.dtb"), deviceTree)
	writeFixtureFile(t, filepath.Join(root, "boot", strings.TrimPrefix(token, "/")), deviceTree)
	grub := strings.Replace(
		fixtureGRUB(false),
		" initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		" devicetree "+token+"\n initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		1,
	)
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
	if _, err := verifyFallback(context.Background(), root, fixtureFallbackABI); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyFallbackAcceptsABIStampedX1PDeviceTree proves a generic title whose
// ABI ends in x1e cannot cause an X1P installation to select the X1E tree.
func TestVerifyFallbackAcceptsABIStampedX1PDeviceTree(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	deviceTree := "fallback lcd dtb"
	token := "/dtb-7.2.0-sp11beta18"
	writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/x1p64100-microsoft-denali.dtb"), deviceTree)
	writeFixtureFile(t, filepath.Join(root, "boot", strings.TrimPrefix(token, "/")), deviceTree)
	grub := strings.Replace(
		fixtureGRUB(false),
		" initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		" devicetree "+token+"\n initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		1,
	)
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
	if _, err := verifyFallback(context.Background(), root, fixtureFallbackABI); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyFallbackRejectsAmbiguousABIStampedDeviceTrees requires differing
// installed variant candidates to fail closed even when one matches boot bytes.
func TestVerifyFallbackRejectsAmbiguousABIStampedDeviceTrees(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	token := "/dtb-7.2.0-sp11beta18"
	writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/x1e80100-microsoft-denali-oled.dtb"), "fallback oled dtb")
	writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/x1p64100-microsoft-denali.dtb"), "fallback lcd dtb")
	writeFixtureFile(t, filepath.Join(root, "boot", strings.TrimPrefix(token, "/")), "fallback lcd dtb")
	grub := strings.Replace(
		fixtureGRUB(false),
		" initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		" devicetree "+token+"\n initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		1,
	)
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
	_, err := verifyFallback(context.Background(), root, fixtureFallbackABI)
	if err == nil || !strings.Contains(err.Error(), "ambiguous installed variant candidates") {
		t.Fatalf("stamped DTB ambiguity error = %v", err)
	}
}

// TestVerifyFallbackAcceptsIdenticalABIStampedDeviceTrees permits duplicate
// variant paths only when their bytes and the boot-side bytes all agree.
func TestVerifyFallbackAcceptsIdenticalABIStampedDeviceTrees(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	deviceTree := "shared fallback dtb"
	token := "/dtb-7.2.0-sp11beta18"
	for _, tree := range requiredDeviceTrees {
		writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree", tree.Path), deviceTree)
	}
	writeFixtureFile(t, filepath.Join(root, "boot", strings.TrimPrefix(token, "/")), deviceTree)
	grub := strings.Replace(
		fixtureGRUB(false),
		" initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		" devicetree "+token+"\n initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		1,
	)
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
	if _, err := verifyFallback(context.Background(), root, fixtureFallbackABI); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyFallbackRejectsABIStampedDeviceTreeMismatch verifies stamped names
// do not bypass the existing same-ABI digest proof.
func TestVerifyFallbackRejectsABIStampedDeviceTreeMismatch(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	token := "/dtb-7.2.0-sp11beta18"
	writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/x1e80100-microsoft-denali-oled.dtb"), "fallback oled dtb")
	writeFixtureFile(t, filepath.Join(root, "boot", strings.TrimPrefix(token, "/")), "wrong device tree")
	grub := strings.Replace(
		fixtureGRUB(false),
		" initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		" devicetree "+token+"\n initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		1,
	)
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
	_, err := verifyFallback(context.Background(), root, fixtureFallbackABI)
	if err == nil || !strings.Contains(err.Error(), "does not match installed ABI "+fixtureFallbackABI) {
		t.Fatalf("stamped DTB mismatch error = %v", err)
	}
}

// TestVerifyFallbackAcceptsCanonicalDeviceTree preserves direct canonical-name
// verification alongside ABI-stamped and legacy shared tokens.
func TestVerifyFallbackAcceptsCanonicalDeviceTree(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	deviceTree := "fallback oled dtb"
	basename := "x1e80100-microsoft-denali-oled.dtb"
	writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom", basename), deviceTree)
	writeFixtureFile(t, filepath.Join(root, "boot", basename), deviceTree)
	grub := strings.Replace(
		fixtureGRUB(false),
		" initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		" devicetree /boot/"+basename+"\n initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		1,
	)
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
	if _, err := verifyFallback(context.Background(), root, fixtureFallbackABI); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyFallbackRejectsTraversingDeviceTreePath verifies a recognised
// directive cannot disguise traversal as an absent optional DTB directive.
func TestVerifyFallbackRejectsTraversingDeviceTreePath(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	grub := strings.Replace(
		fixtureGRUB(false),
		" initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		" devicetree /boot/../private.dtb\n initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		1,
	)
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
	_, err := verifyFallback(context.Background(), root, fixtureFallbackABI)
	if err == nil || !strings.Contains(err.Error(), "unsafe device-tree path") {
		t.Fatalf("traversing DTB error = %v", err)
	}
}

// TestVerifyFallbackRejectsMixedSafeAndUnsafeDeviceTrees verifies a retained
// valid directive cannot conceal a later traversal directive in the stanza.
func TestVerifyFallbackRejectsMixedSafeAndUnsafeDeviceTrees(t *testing.T) {
	root, _ := fixtureEnvironment(t)
	deviceTree := "fallback oled dtb"
	writeFixtureFile(t, filepath.Join(root, "usr/lib/firmware", fixtureFallbackABI, "device-tree/qcom/x1e80100-microsoft-denali-oled.dtb"), deviceTree)
	writeFixtureFile(t, filepath.Join(root, "boot/sp11-denali.dtb"), deviceTree)
	grub := strings.Replace(
		fixtureGRUB(false),
		" initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		" devicetree /boot/sp11-denali.dtb\n devicetree /boot/../private.dtb\n initrd /boot/initrd.img-"+fixtureFallbackABI+"\n",
		1,
	)
	writeFixtureFile(t, filepath.Join(root, "boot/grub/grub.cfg"), grub)
	_, err := verifyFallback(context.Background(), root, fixtureFallbackABI)
	if err == nil || !strings.Contains(err.Error(), "unsafe device-tree path") {
		t.Fatalf("mixed safe and unsafe DTB error = %v", err)
	}
}

// TestHashGRUBPathRejectsAmbiguousRootAndBootFiles verifies a root-relative
// token cannot silently prefer one of two distinct regular-file candidates.
func TestHashGRUBPathRejectsAmbiguousRootAndBootFiles(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "boot/sp11-denali.dtb"), "boot-directory bytes")
	writeFixtureFile(t, filepath.Join(root, "sp11-denali.dtb"), "root-directory bytes")
	_, err := HashGRUBPath(context.Background(), root, "/sp11-denali.dtb")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous GRUB path error = %v", err)
	}
}

// TestHashGRUBPathRejectsRelativeToken verifies GRUB file evidence always
// starts from an absolute target-system path after prefix normalisation.
func TestHashGRUBPathRejectsRelativeToken(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "boot/sp11-denali.dtb"), "boot-directory bytes")
	_, err := HashGRUBPath(context.Background(), root, "sp11-denali.dtb")
	if err == nil || !strings.Contains(err.Error(), "safe absolute path") {
		t.Fatalf("relative GRUB path error = %v", err)
	}
}

// TestInspectGRUBPathClassifiesPermissionDenied verifies wrapping retains
// os.ErrPermission for doctor-style tri-state inspection.
func TestInspectGRUBPathClassifiesPermissionDenied(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "boot/vmlinuz-test")
	writeFixtureFile(t, path, "kernel")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(path, 0o600) }()
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("current user can read chmod 0000 files")
	}
	_, state, err := InspectGRUBPath(context.Background(), root, "/boot/vmlinuz-test")
	if state != GRUBPathInaccessible || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("permission availability=%q error=%v", state, err)
	}
}

// TestRequiredTreeForEntryDoesNotMixVariants verifies a title for one device
// cannot silently validate a direct DTB path belonging to the other device.
func TestRequiredTreeForEntryDoesNotMixVariants(t *testing.T) {
	entry := GRUBEntry{
		Title: "Surface Pro 11 X1P/LCD",
		DeviceTrees: []GRUBPathToken{{
			Command: "devicetree",
			Path:    "/boot/x1e80100-microsoft-denali-oled.dtb",
		}},
	}
	if device, relative, valid := requiredTreeForEntry(entry, fixtureFallbackABI); valid || device != "" || relative != "" {
		t.Fatalf("mixed device-tree variant = %q, %q, %t", device, relative, valid)
	}
	entry.DeviceTrees[0].Path = "/boot/sp11-denali.dtb"
	device, relative, valid := requiredTreeForEntry(entry, fixtureFallbackABI)
	if !valid || device != requiredDeviceTrees[1].Device || relative != requiredDeviceTrees[1].Path {
		t.Fatalf("shared X1P device-tree variant = %q, %q, %t", device, relative, valid)
	}
	entry.DeviceTrees[0].Path = "/dtb-7.2.0-sp11beta18"
	entry.Title += " " + fixtureFallbackABI
	device, relative, valid = requiredTreeForEntry(entry, fixtureFallbackABI)
	if !valid || device != abiStampedDeviceTreeMarker || relative != "" {
		t.Fatalf("ABI-stamped X1P device-tree variant = %q, %q, %t", device, relative, valid)
	}
	entry.DeviceTrees[0].Path = "/dtb-7.2.1-sp11beta18"
	if device, relative, valid := requiredTreeForEntry(entry, fixtureFallbackABI); valid || device != "" || relative != "" {
		t.Fatalf("conflicting ABI-stamped device tree = %q, %q, %t", device, relative, valid)
	}
}

// TestLegacyModuleTreeIsAccepted verifies non-usr-merged target roots retain parity.
func TestLegacyModuleTreeIsAccepted(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	usrTree := filepath.Join(root, "usr/lib/modules", fixtureFallbackABI)
	legacyTree := filepath.Join(root, "lib/modules", fixtureFallbackABI)
	if err := os.MkdirAll(filepath.Dir(legacyTree), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(usrTree, legacyTree); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureManager(&fakeRunner{root: root}).Preflight(context.Background(), fixtureRequest(root, bundle, true)); err != nil {
		t.Fatal(err)
	}
}

// TestRollbackReportsFallbackDamage verifies recovery never claims success when
// a failed maintainer script has damaged the known-good kernel.
func TestRollbackReportsFallbackDamage(t *testing.T) {
	root, bundle := fixtureEnvironment(t)
	runner := &fakeRunner{root: root}
	runner.runHook = func(_ context.Context, command platform.Command) error {
		if slicesContain(command.Args, "--install") {
			writeFixtureFile(t, filepath.Join(root, "boot/vmlinuz-"+fixtureFallbackABI), "damaged fallback")
			return errors.New("fixture package failure")
		}
		return nil
	}
	manager := fixtureManager(runner)
	manager.effectiveUID = func() int { return 0 }
	receipt, err := manager.Install(context.Background(), fixtureRequest(root, bundle, false))
	if err == nil || !strings.Contains(err.Error(), "rollback incomplete") {
		t.Fatalf("error = %v", err)
	}
	if receipt.Rollback == nil || !strings.Contains(receipt.Rollback.Error, "fallback") {
		t.Fatalf("rollback did not report fallback damage: %+v", receipt.Rollback)
	}
	if len(receipt.Rollback.Commands) != 1 || receipt.Rollback.Commands[0].Operation != OperationRollbackPackages {
		t.Fatalf("rollback included redundant commands: %+v", receipt.Rollback.Commands)
	}
}

// TestBoundedCaptureRejectsOversizedMetadata verifies output memory stays bounded.
func TestBoundedCaptureRejectsOversizedMetadata(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	runner := &fakeRunner{root: root}
	runner.metadataHook = func(name, field string) (string, bool) {
		if strings.HasPrefix(name, "linux-image-") && field == "Depends" {
			return strings.Repeat("a", maximumDependencyBytes+20), true
		}
		return "", false
	}
	_, err := fixtureManager(runner).Preflight(context.Background(), fixtureRequest(root, bundle, true))
	if !errors.Is(err, errCommandOutputLimit) {
		t.Fatalf("error = %v", err)
	}
}

// TestBoundedErrorPreservesUTF8 verifies receipt diagnostics stay valid JSON text.
func TestBoundedErrorPreservesUTF8(t *testing.T) {
	t.Parallel()
	message := strings.Repeat("€", maximumReceiptErrorBytes)
	bounded := boundedError(errors.New(message))
	if len(bounded) > maximumReceiptErrorBytes || !utf8.ValidString(bounded) {
		t.Fatalf("bounded diagnostic is oversized or invalid UTF-8")
	}
}

// TestHeaderPairDependencyRemainsExact verifies constrained local dependencies.
func TestHeaderPairDependencyRemainsExact(t *testing.T) {
	t.Parallel()
	root, bundle := fixtureEnvironment(t)
	directory := filepath.Dir(bundle.Packages[0].Path)
	bundle.Packages = append(bundle.Packages,
		fixturePackage(t, directory, kernel.RoleHeaders, "flavour headers"),
		fixturePackage(t, directory, kernel.RoleCommonHeaders, "common headers"),
	)
	runner := &fakeRunner{root: root}
	runner.metadataHook = func(name, field string) (string, bool) {
		if strings.HasPrefix(name, "linux-headers-") && field == "Depends" {
			return "linux-qcom-x1e-headers-" + strings.TrimSuffix(fixtureTargetABI, "-qcom-x1e") + " (>= " + fixtureVersion + ")", true
		}
		return "", false
	}
	_, err := fixtureManager(runner).Preflight(context.Background(), fixtureRequest(root, bundle, true))
	if err == nil || !strings.Contains(err.Error(), "non-exact or mismatched") {
		t.Fatalf("error = %v", err)
	}
}
