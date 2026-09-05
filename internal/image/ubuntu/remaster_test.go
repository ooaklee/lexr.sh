package ubuntu

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// retainedVolumeRunner records Docker commands and deliberately fails the
// extraction container so tests can inspect diagnostic-volume retention.
type retainedVolumeRunner struct {
	commands []platform.Command
	volume   string
}

// ubuntuDetailedSelectionProvenance returns complete embedded fixture evidence.
func ubuntuDetailedSelectionProvenance() *kernel.DTBSelectionProvenance {
	return &kernel.DTBSelectionProvenance{
		Tool: "stubble", Version: "fixture-1", DatabaseSHA256: strings.Repeat("d", 64), StubSHA256: strings.Repeat("1", 64), HelperSHA256: strings.Repeat("2", 64), SBATSHA256: strings.Repeat("3", 64),
		UKifyTool: "ukify", UKifyPackage: "systemd-ukify", UKifyVersion: "258.1-1", UKifySHA256: strings.Repeat("4", 64),
		Selections: []kernel.DeviceTreeSelectionEvidence{
			{Device: "surface-pro-11-x1e-oled", Records: []kernel.DTBSelectionRecord{{Source: "hwids", Compatible: "microsoft,denali", HWIDs: []string{"11111111-1111-5111-8111-111111111111"}}}},
			{Device: "surface-pro-11-x1p-lcd", Records: []kernel.DTBSelectionRecord{{Source: "hwids", Compatible: "microsoft,denali-x1p", HWIDs: []string{"22222222-2222-5222-8222-222222222222"}}}},
		},
	}
}

// Capture simulates the Docker information and volume-identity responses used
// before the remaster operation reaches its intentional extraction failure.
func (r *retainedVolumeRunner) Capture(_ context.Context, command platform.Command) ([]byte, error) {
	r.commands = append(r.commands, command)
	if len(command.Args) == 0 {
		return nil, errors.New("unexpected empty Docker command")
	}
	switch command.Args[0] {
	case "info", "image":
		return []byte("ok\n"), nil
	case "volume":
		name := command.Args[len(command.Args)-1]
		switch command.Args[1] {
		case "create":
			r.volume = name
			return []byte(name + "\n"), nil
		case "inspect":
			return []byte(name + " local true\n"), nil
		}
	}
	return nil, errors.New("unexpected Docker capture")
}

// Run records non-capturing Docker calls and fails the container invocation to
// leave the test volume available for diagnostics.
func (r *retainedVolumeRunner) Run(_ context.Context, command platform.Command) error {
	r.commands = append(r.commands, command)
	if len(command.Args) > 0 && command.Args[0] == "run" {
		return errors.New("forced extraction failure")
	}
	return nil
}

// TestUpdatePackageManifestPreservesLayeredDiffHeaders verifies kernel package
// additions retain the special headers required by Ubuntu's layered manifest.
func TestUpdatePackageManifestPreservesLayeredDiffHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "minimal.manifest")
	input := "--- full.manifest\n+++ minimal.manifest\n+base-package\t1.0\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle := kernel.Bundle{
		Version: "7.2.0-jg-0sp11v19",
		Packages: []kernel.Package{
			{Role: kernel.RoleImage, Name: "linux-image-custom_7.2.0-jg-0sp11v19_arm64.deb"},
			{Role: kernel.RoleModules, Name: "linux-modules-custom_7.2.0-jg-0sp11v19_arm64.deb"},
		},
	}

	if err := updatePackageManifest(path, bundle); err != nil {
		t.Fatalf("updatePackageManifest() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 || lines[0] != "--- full.manifest" || lines[1] != "+++ minimal.manifest" {
		t.Fatalf("manifest headers were not preserved: %q", string(data))
	}
	for _, want := range []string{
		"+linux-image-custom\t7.2.0-jg-0sp11v19",
		"+linux-modules-custom\t7.2.0-jg-0sp11v19",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("manifest does not contain %q:\n%s", want, data)
		}
	}
}

// TestValidateSourceLayout verifies the supported three-layer Casper layout is
// accepted when its metadata and every required SquashFS image are present.
func TestValidateSourceLayout(t *testing.T) {
	workspace := t.TempDir()
	configuration := `kernel:
  default: linux-qcom-x1e
sources:
- path: minimal.squashfs
  type: fsimage-layered
- path: minimal.standard.squashfs
  type: fsimage-layered
`
	if err := os.WriteFile(filepath.Join(workspace, "install-sources.yaml"), []byte(configuration), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"minimal.squashfs",
		"minimal.standard.squashfs",
		"minimal.standard.live.squashfs",
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("squashfs"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "source-casper-uuid-generic"),
		[]byte("7bda4398-0498-4564-acfe-e90dcc1c75f2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := validateSourceLayout(workspace); err != nil {
		t.Fatalf("validateSourceLayout() error = %v", err)
	}
}

// TestValidateSourceLayoutRejectsNonLayeredSource verifies remastering stops
// before mutation when the Ubuntu source does not declare layered filesystem images.
func TestValidateSourceLayoutRejectsNonLayeredSource(t *testing.T) {
	workspace := t.TempDir()
	configuration := "kernel:\n  default: linux-qcom-x1e\nsources:\n- path: minimal.squashfs\n  type: fsimage\n- path: minimal.standard.squashfs\n  type: fsimage\n"
	if err := os.WriteFile(filepath.Join(workspace, "install-sources.yaml"), []byte(configuration), 0o644); err != nil {
		t.Fatal(err)
	}

	err := validateSourceLayout(workspace)
	if err == nil || !strings.Contains(err.Error(), "fsimage-layered") {
		t.Fatalf("validateSourceLayout() error = %v, want layered-layout error", err)
	}
}

// TestValidateSourceLayoutRejectsMalformedCasperIdentity verifies the adapter
// will not remaster source media whose live-initramfs binding is not canonical.
func TestValidateSourceLayoutRejectsMalformedCasperIdentity(t *testing.T) {
	workspace := t.TempDir()
	configuration := `kernel:
  default: linux-qcom-x1e
sources:
- path: minimal.squashfs
  type: fsimage-layered
- path: minimal.standard.squashfs
  type: fsimage-layered
`
	if err := os.WriteFile(filepath.Join(workspace, "install-sources.yaml"), []byte(configuration), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"minimal.squashfs", "minimal.standard.squashfs", "minimal.standard.live.squashfs"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte("squashfs"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "source-casper-uuid-generic"), []byte("not-a-uuid\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := validateSourceLayout(workspace)
	if err == nil || !strings.Contains(err.Error(), "Casper UUID") {
		t.Fatalf("validateSourceLayout() error = %v, want Casper UUID error", err)
	}
}

// TestUpdateMD5ManifestTracksCasperIdentity verifies Ubuntu's compatibility
// checksum list records the newly generated medium UUID rather than the source value.
func TestUpdateMD5ManifestTracksCasperIdentity(t *testing.T) {
	workspace := t.TempDir()
	files := map[string]string{
		"remastered.squashfs": "root",
		"minimal.manifest":    "manifest",
		"minimal.size":        "4\n",
		"casper-vmlinuz":      "kernel",
		"casper-initrd":       "initrd",
		"casper-uuid-generic": "c5ef1897-ed0f-490e-b6d8-961dc41124b2\n",
		"grub.cfg":            "grub",
		"grubaa64.efi":        "efi",
		"disk-info":           "info",
		"sp11/README.txt":     "support",
	}
	for name, contents := range files {
		path := filepath.Join(workspace, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "md5sum.txt"),
		[]byte(strings.Repeat("0", 32)+"  ./.disk/casper-uuid-generic\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := updateMD5Manifest(workspace); err != nil {
		t.Fatalf("updateMD5Manifest() error = %v", err)
	}
	digest, err := md5File(filepath.Join(workspace, "casper-uuid-generic"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(workspace, "md5sum.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := digest + "  ./.disk/casper-uuid-generic"
	if !strings.Contains(string(manifest), want) {
		t.Fatalf("md5sum.txt does not contain %q:\n%s", want, manifest)
	}
}

// TestStageFileDereferencesSourceSymlink verifies staging copies the selected
// artefact bytes rather than preserving a link to a removable source target.
func TestStageFileDereferencesSourceSymlink(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.iso")
	link := filepath.Join(directory, "latest.iso")
	destination := filepath.Join(directory, "workspace", "source.iso")
	if err := os.WriteFile(source, []byte("image bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if err := stageFile(link, destination); err != nil {
		t.Fatalf("stageFile() error = %v", err)
	}
	if info, err := os.Lstat(destination); err != nil {
		t.Fatal(err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("stageFile() preserved a source symlink instead of copying its target")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("staged file no longer works after removing symlink target: %v", err)
	}
	if string(data) != "image bytes" {
		t.Fatalf("staged contents = %q", data)
	}
}

// TestAppendedESPOffset verifies an xorriso GPT report is converted from sector
// units into the byte offset of the appended EFI System Partition.
func TestAppendedESPOffset(t *testing.T) {
	report := "GPT start and size :   2  7817736  12288\n"
	offset, err := appendedESPOffset(report)
	if err != nil {
		t.Fatalf("appendedESPOffset() error = %v", err)
	}
	if want := int64(7817736 * 512); offset != want {
		t.Fatalf("appendedESPOffset() = %d, want %d", offset, want)
	}
}

// TestAppendedESPOffsetRejectsInvalidPartition verifies missing, empty, and
// overflowing EFI partition reports are rejected instead of producing offsets.
func TestAppendedESPOffsetRejectsInvalidPartition(t *testing.T) {
	for _, report := range []string{
		"System area summary: GPT\n",
		"GPT start and size : 2 100 0\n",
		"GPT start and size : 2 18014398509481984 1\n",
	} {
		if _, err := appendedESPOffset(report); err == nil {
			t.Fatalf("appendedESPOffset(%q) succeeded, want error", report)
		}
	}
}

// TestGrubConfigPairsDeviceTreesWithBootEntries verifies generated GRUB menus
// include both device variants, their matching DTBs, and self-locating boot logic.
func TestGrubConfigPairsDeviceTreesWithBootEntries(t *testing.T) {
	config := grubConfig("test-abi")
	for _, want := range []string{
		"Ubuntu for Surface Pro 11 X1E/OLED (test-abi)",
		"Ubuntu for Surface Pro 11 X1P/LCD (test-abi, hardware qualification pending)",
		"devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb",
		"devicetree /sp11/dtb/x1p64100-microsoft-denali.dtb",
		"modprobe.blacklist=qcom_q6v5_pas",
		"insmod part_gpt",
		"insmod iso9660",
		"insmod search_fs_file",
		"insmod fdt",
		"search --no-floppy --file --set=iso_root /casper/vmlinuz",
		"set root=$iso_root",
	} {
		if !strings.Contains(config, want) {
			t.Errorf("grubConfig() does not contain %q", want)
		}
	}
	if strings.Contains(config, "sp11_feedback_active_offset2_zero") {
		t.Fatal("grubConfig() retains the removed SoundWire compatibility parameter")
	}
	if strings.Contains(config, "allow aDSP") {
		t.Fatal("grubConfig() exposes the unsafe live-USB aDSP entry")
	}
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "linux /casper/vmlinuz") && !strings.Contains(line, "modprobe.blacklist=qcom_q6v5_pas") {
			t.Errorf("live kernel line does not protect USB media from aDSP reset: %q", line)
		}
	}
}

// TestSafeKernelABI verifies only a non-empty, path-safe kernel ABI can be used
// to construct remaster workspace and boot paths.
func TestSafeKernelABI(t *testing.T) {
	for _, test := range []struct {
		abi  string
		want bool
	}{
		{abi: "7.2.0-jg-0sp11v19-qcom-x1e", want: true},
		{abi: "../escape", want: false},
		{abi: "contains/slash", want: false},
		{abi: "contains space", want: false},
		{abi: "", want: false},
	} {
		if got := safeKernelABI(test.abi); got != test.want {
			t.Errorf("safeKernelABI(%q) = %v, want %v", test.abi, got, test.want)
		}
	}
}

// TestCreateErrorReportsRetainedDiagnosticVolume verifies a failed remaster with
// workspace retention reports the exact Docker volume and does not remove it.
func TestCreateErrorReportsRetainedDiagnosticVolume(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "source.iso")
	imagePackage := filepath.Join(workspace, "linux-image-test.deb")
	modulesPackage := filepath.Join(workspace, "linux-modules-test.deb")
	for path, contents := range map[string]string{
		source:         "source",
		imagePackage:   "image package",
		modulesPackage: "modules package",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	imageDigest, err := artifact.HashFile(imagePackage)
	if err != nil {
		t.Fatal(err)
	}
	modulesDigest, err := artifact.HashFile(modulesPackage)
	if err != nil {
		t.Fatal(err)
	}
	const abi = "7.2.0-test-qcom-x1e"
	const version = "7.2.0-test"
	bundle, err := kernel.NewBundle(kernel.BundleOptions{
		Release: "test", RequestedBootImageMode: kernel.RequestedBootImageModeStubble,
		EffectiveDTBDelivery: kernel.DTBDeliveryEmbedded, EmbeddedDTBCount: 2,
		DTBSelectionProvenance: ubuntuDetailedSelectionProvenance(),
		Packages: []kernel.Package{
			{Role: kernel.RoleImage, Name: "linux-image-" + abi + "_" + version + "_arm64.deb", Path: imagePackage, SHA256: imageDigest, Size: int64(len("image package")), Verified: true},
			{Role: kernel.RoleModules, Name: "linux-modules-" + abi + "_" + version + "_arm64.deb", Path: modulesPackage, SHA256: modulesDigest, Size: int64(len("modules package")), Verified: true},
		},
		DeviceTrees: []kernel.DeviceTree{
			{Device: "surface-pro-11-x1e-oled", Basename: "x1e80100-microsoft-denali-oled.dtb", Path: "usr/lib/firmware/" + abi + "/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb", CompatibleStrings: []string{"microsoft,denali"}, SHA256: strings.Repeat("e", 64), EmbeddedMatches: 1, Selectors: []kernel.DeviceTreeSelector{{Kind: kernel.DeviceTreeSelectorCompatible, Value: "microsoft,denali"}, {Kind: kernel.DeviceTreeSelectorHWID, Value: "11111111-1111-5111-8111-111111111111"}}, Required: true},
			{Device: "surface-pro-11-x1p-lcd", Basename: "x1p64100-microsoft-denali.dtb", Path: "usr/lib/firmware/" + abi + "/device-tree/qcom/x1p64100-microsoft-denali.dtb", CompatibleStrings: []string{"microsoft,denali-x1p"}, SHA256: strings.Repeat("f", 64), EmbeddedMatches: 1, Selectors: []kernel.DeviceTreeSelector{{Kind: kernel.DeviceTreeSelectorCompatible, Value: "microsoft,denali-x1p"}, {Kind: kernel.DeviceTreeSelectorHWID, Value: "22222222-2222-5222-8222-222222222222"}}, Required: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &retainedVolumeRunner{}
	remasterer := NewRemasterer(platform.NewDocker(runner), nil)

	_, err = remasterer.Create(context.Background(), Request{
		SourceISO:     source,
		OutputISO:     filepath.Join(workspace, "output.iso"),
		Bundle:        bundle,
		WorkspaceRoot: workspace,
		KeepWorkspace: true,
	})
	if err == nil || runner.volume == "" || !strings.Contains(err.Error(), "diagnostic Docker volume retained: "+runner.volume) {
		t.Fatalf("Create() error = %v, volume = %q; want exact retained-volume diagnostic", err, runner.volume)
	}
	if slices.ContainsFunc(runner.commands, func(command platform.Command) bool {
		return len(command.Args) >= 2 && command.Args[0] == "volume" && command.Args[1] == "rm"
	}) {
		t.Fatalf("KeepWorkspace=true removed diagnostic volume: %#v", runner.commands)
	}
}

// TestEmbeddedManifestContainsOnlyPortableKernelPaths verifies published image
// metadata references ISO-relative packages without leaking host workspace paths.
func TestEmbeddedManifestContainsOnlyPortableKernelPaths(t *testing.T) {
	workspace := t.TempDir()
	for path, content := range map[string]string{
		"source.iso":          "source",
		"casper-vmlinuz":      "kernel",
		"casper-initrd":       "initrd",
		"casper-uuid-generic": "c5ef1897-ed0f-490e-b6d8-961dc41124b2\n",
		"sp11/dtb/x1e80100-microsoft-denali-oled.dtb": "x1e",
		"sp11/dtb/x1p64100-microsoft-denali.dtb":      "x1p",
	} {
		absolute := filepath.Join(workspace, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const abi = "7.2.0-test-qcom-x1e"
	bundle, err := kernel.NewBundle(kernel.BundleOptions{
		Release: "test", RequestedBootImageMode: kernel.RequestedBootImageModeStubble,
		EffectiveDTBDelivery: kernel.DTBDeliveryEmbedded, EmbeddedDTBCount: 2,
		DTBSelectionProvenance: ubuntuDetailedSelectionProvenance(),
		Packages: []kernel.Package{
			{
				Name: "linux-image-" + abi + "_7.2.0-test_arm64.deb",
				Path: filepath.Join(workspace, "private", "image.deb"), SHA256: strings.Repeat("a", 64), Size: 1, Verified: true,
			},
			{
				Name: "linux-modules-" + abi + "_7.2.0-test_arm64.deb",
				Path: filepath.Join(workspace, "private", "modules.deb"), SHA256: strings.Repeat("b", 64), Size: 1, Verified: true,
			},
		},
		DeviceTrees: []kernel.DeviceTree{
			{Device: "surface-pro-11-x1e-oled", Basename: "x1e80100-microsoft-denali-oled.dtb", Path: "usr/lib/firmware/" + abi + "/device-tree/qcom/x1e80100-microsoft-denali-oled.dtb", CompatibleStrings: []string{"microsoft,denali"}, SHA256: strings.Repeat("e", 64), EmbeddedMatches: 1, Selectors: []kernel.DeviceTreeSelector{{Kind: kernel.DeviceTreeSelectorCompatible, Value: "microsoft,denali"}, {Kind: kernel.DeviceTreeSelectorHWID, Value: "11111111-1111-5111-8111-111111111111"}}, Required: true},
			{Device: "surface-pro-11-x1p-lcd", Basename: "x1p64100-microsoft-denali.dtb", Path: "usr/lib/firmware/" + abi + "/device-tree/qcom/x1p64100-microsoft-denali.dtb", CompatibleStrings: []string{"microsoft,denali-x1p"}, SHA256: strings.Repeat("f", 64), EmbeddedMatches: 1, Selectors: []kernel.DeviceTreeSelector{{Kind: kernel.DeviceTreeSelectorCompatible, Value: "microsoft,denali-x1p"}, {Kind: kernel.DeviceTreeSelectorHWID, Value: "22222222-2222-5222-8222-222222222222"}}, Required: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := buildEmbeddedManifest(
		Request{Bundle: bundle}, workspace, strings.Repeat("c", 64), companion.Absent(companion.OmissionReasonNotRequested))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateManifestKernelBundle(manifest.KernelBundle); err != nil {
		t.Fatalf("portable manifest bundle is invalid: %v", err)
	}
	mutated := manifest.KernelBundle
	mutated.EffectiveDTBDelivery = kernel.DTBDeliveryExternalRequired
	if err := validateManifestKernelBundle(mutated); err == nil {
		t.Fatal("manifest validation accepted delivery semantics without boot support")
	}
	for _, pkg := range manifest.KernelBundle.Packages {
		want := "sp11/kernel/" + pkg.Name
		if pkg.Path != want {
			t.Fatalf("package path = %q, want %q", pkg.Path, want)
		}
	}
	if manifest.MediaDiscovery.Strategy != "direct-hybrid" ||
		manifest.MediaDiscovery.Protocol != "casper" ||
		len(manifest.MediaDiscovery.Evidence) != 2 ||
		manifest.MediaDiscovery.Evidence[0].Value != "c5ef1897-ed0f-490e-b6d8-961dc41124b2" ||
		manifest.MediaDiscovery.Evidence[0].Artifact == nil ||
		manifest.MediaDiscovery.Evidence[0].Artifact.Path != ".disk/casper-uuid-generic" {
		t.Fatalf("media discovery = %#v", manifest.MediaDiscovery)
	}
	if manifest.CompanionBundle.Included || manifest.CompanionBundle.Root != companion.ISOFilesystemRoot ||
		manifest.CompanionBundle.Reason != companion.OmissionReasonNotRequested || manifest.CompanionBundle.Userspace == nil {
		t.Fatalf("companion bundle = %#v", manifest.CompanionBundle)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), workspace) {
		t.Fatalf("embedded manifest leaked host workspace: %s", encoded)
	}
}

// TestPortableKernelBundlePreservesExternalDelivery proves Ubuntu media keeps
// boot-support and delivery provenance while owning nested inventory slices.
func TestPortableKernelBundlePreservesExternalDelivery(t *testing.T) {
	bundle := kernel.Bundle{
		RequestedBootImageMode: kernel.RequestedBootImageModeSource,
		EffectiveDTBDelivery:   kernel.DTBDeliveryExternalRequired,
		Packages: []kernel.Package{
			{Role: kernel.RoleImage, Name: "linux-image.deb", Path: "/host/linux-image.deb"},
			{Role: kernel.RoleModules, Name: "linux-modules.deb", Path: "/host/linux-modules.deb"},
			{Role: kernel.RoleBootSupport, Name: "lexr-kernel-boot-support.deb", Path: "/host/lexr-kernel-boot-support.deb"},
			{Role: kernel.RoleHeaders, Name: "linux-headers.deb", Path: "/host/linux-headers.deb"},
		},
		DeviceTrees: []kernel.DeviceTree{{Device: "x1e", Path: "firmware/x1e.dtb", CompatibleStrings: []string{"qcom,x1e"}}},
	}
	portable := portableKernelBundle(bundle)
	for _, role := range []kernel.PackageRole{kernel.RoleImage, kernel.RoleModules, kernel.RoleBootSupport} {
		pkg, ok := portable.Package(role)
		if !ok || pkg.Path != "sp11/kernel/"+pkg.Name {
			t.Fatalf("portable %s package = %#v, present=%t", role, pkg, ok)
		}
	}
	headers, _ := portable.Package(kernel.RoleHeaders)
	if headers.Path != "" || portable.RequestedBootImageMode != bundle.RequestedBootImageMode || portable.EffectiveDTBDelivery != bundle.EffectiveDTBDelivery {
		t.Fatalf("portable delivery contract = %#v", portable)
	}
	portable.DeviceTrees[0].CompatibleStrings[0] = "mutated"
	if bundle.DeviceTrees[0].CompatibleStrings[0] != "qcom,x1e" {
		t.Fatal("portableKernelBundle() aliases nested device-tree evidence")
	}
}
