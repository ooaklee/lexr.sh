package fedora

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// maximumValidationImageBytes bounds the immutable ISO snapshot accepted for inspection.
const maximumValidationImageBytes int64 = 64 << 30

// Validator independently inspects a completed Fedora image and returns
// digest-bound structural evidence for both live and installed paths.
type Validator struct {
	Docker *platform.Docker
}

// NewValidator creates a Fedora structural validator.
func NewValidator(docker *platform.Docker) *Validator {
	if docker == nil {
		docker = platform.NewDocker(nil)
	}
	return &Validator{Docker: docker}
}

// Validate checks the hybrid boot layout, ESP indirection, manifest, EROFS,
// RPM ownership, Anaconda hand-off, dracut modules, Stubble PE data, and
// live-versus-installed boot policy.
func (v *Validator) Validate(ctx context.Context, isoPath string) (report imagecontract.ValidationReport, returnErr error) {
	absolute, err := filepath.Abs(isoPath)
	if err != nil {
		return report, err
	}
	report = imagecontract.ValidationReport{Path: absolute, Layout: "hybrid-iso", Adapter: AdapterID}
	addCheck := func(name string, passed bool, details string) {
		report.Checks = append(report.Checks, imagecontract.ValidationCheck{Name: name, Passed: passed, Details: details})
	}
	if err := v.Docker.Check(ctx); err != nil {
		return report, err
	}
	toolsImage, err := v.Docker.EnsureFedoraToolsImage(ctx)
	if err != nil {
		return report, err
	}
	workspace, err := os.MkdirTemp(filepath.Dir(absolute), ".lexr-fedora-validate-")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(workspace)
	report.SHA256, report.Size, err = snapshotValidationImage(ctx, absolute, filepath.Join(workspace, "image.iso"))
	if err != nil {
		return report, err
	}

	systemArea, systemErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
		"xorriso", "-indev", "/work/image.iso", "-report_system_area", "plain")
	if systemErr != nil {
		addCheck("hybrid-system-area", false, systemErr.Error())
	} else {
		_, offsetErr := appendedESPOffset(string(systemArea))
		passed := strings.Contains(string(systemArea), "System area summary: MBR protective-msdos-label cyl-align-off GPT") && offsetErr == nil
		details := "protective MBR, GPT, and appended ESP are present"
		if offsetErr != nil {
			details = offsetErr.Error()
		}
		addCheck("hybrid-system-area", passed, details)
	}
	elTorito, elToritoErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
		"xorriso", "-indev", "/work/image.iso", "-report_el_torito", "plain")
	addCheck("arm64-efi-boot-catalog", elToritoErr == nil &&
		strings.Contains(string(elTorito), "El Torito boot img :   1  UEFI  y"), strings.TrimSpace(string(elTorito)))
	pvd, pvdErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
		"xorriso", "-indev", "/work/image.iso", "-pvd_info")
	volumePassed := pvdErr == nil && strings.Contains(string(pvd), "Volume Id    : "+SourceVolumeID)
	addCheck("dracut-live-volume-label", volumePassed, "expected ISO volume label "+SourceVolumeID)

	extractErr := v.Docker.RunInWorkspace(ctx, toolsImage, workspace,
		"xorriso", "-osirrox", "on", "-indev", "/work/image.iso",
		"-extract", "/sp11/lexr-manifest.json", "/work/manifest.json",
		"-extract", "/sp11/fedora/boot-policy.json", "/work/boot-policy.json",
		"-extract", "/sp11/fedora/lexr-kernel-sp11.aarch64.rpm", "/work/kernel.rpm",
		"-extract", "/sp11/dtb/x1e80100-microsoft-denali-oled.dtb", "/work/x1e.dtb",
		"-extract", "/sp11/dtb/x1p64100-microsoft-denali.dtb", "/work/x1p.dtb",
		"-extract", "/boot/aarch64/loader/linux", "/work/vmlinuz",
		"-extract", "/boot/aarch64/loader/initrd", "/work/initrd",
		"-extract", "/boot/aarch64/loader/linux-fedora", "/work/vmlinuz-fedora",
		"-extract", "/boot/aarch64/loader/initrd-fedora", "/work/initrd-fedora",
		"-extract", "/boot/grub2/grub.cfg", "/work/grub.cfg",
		"-extract", "/boot/0x503d6c7e", "/work/media-marker",
		"-extract", "/EFI/BOOT/grub.cfg", "/work/iso-esp-grub.cfg",
		"-extract", "/EFI/BOOT/BOOTAA64.EFI", "/work/iso-bootaa64.efi",
		"-extract", "/EFI/BOOT/grubaa64.efi", "/work/iso-grubaa64.efi",
		"-extract", "/LiveOS/squashfs.img", "/work/live.erofs")
	if extractErr != nil {
		addCheck("required-iso-members", false, extractErr.Error())
		return report, errors.New("Fedora ISO validation failed: required members cannot be extracted")
	}
	markerInfo, markerErr := os.Lstat(filepath.Join(workspace, "media-marker"))
	if markerErr != nil || markerInfo.Mode()&os.ModeSymlink != 0 || !markerInfo.Mode().IsRegular() {
		addCheck("required-iso-members", false, "GRUB search marker is not a regular outer-ISO member")
		return report, errors.Join(errors.New("Fedora ISO validation failed: invalid GRUB search marker"), markerErr)
	}
	addCheck("required-iso-members", true, "custom and fallback boot sets, EROFS root, RPM, DTBs, GRUB, and manifest are present")

	manifestBytes, err := readBoundedRegularFile(filepath.Join(workspace, "manifest.json"), imagecontract.MaximumManifestSize)
	if err != nil {
		addCheck("embedded-manifest", false, err.Error())
		return report, fmt.Errorf("inspect embedded manifest: %w", err)
	}
	manifestDigest := sha256.Sum256(manifestBytes)
	report.ManifestSHA256 = hex.EncodeToString(manifestDigest[:])
	report.ManifestSize = int64(len(manifestBytes))
	manifest, err := imagecontract.DecodeManifest(bytes.NewReader(manifestBytes))
	if err != nil {
		addCheck("embedded-manifest", false, err.Error())
		return report, err
	}
	report.KernelABI = manifest.KernelBundle.ABI
	for _, dtb := range manifest.KernelBundle.DeviceTrees {
		report.DeviceTrees = append(report.DeviceTrees, dtb.Device)
	}
	manifestErr := validateFedoraManifest(manifest)
	manifestOK := manifestErr == nil
	manifestDetails := fmt.Sprintf("schema=%d adapter=%s abi=%s", manifest.SchemaVersion, manifest.Adapter, manifest.KernelBundle.ABI)
	if manifestErr != nil {
		manifestDetails = manifestErr.Error()
	}
	addCheck("embedded-manifest", manifestOK, manifestDetails)
	if !manifestOK || !safeKernelABIExpression.MatchString(manifest.KernelBundle.ABI) {
		return report, errors.New("Fedora ISO validation failed: invalid embedded manifest contract")
	}
	stockKernelRecord, _ := findEvidenceArtifact(manifest.MediaDiscovery, "stock-fallback-kernel")
	stockInitrdRecord, _ := findEvidenceArtifact(manifest.MediaDiscovery, "stock-fallback-initramfs")

	for _, expected := range []struct {
		name, file string
		record     imagecontract.ArtifactRecord
	}{
		{"live-kernel-digest", "vmlinuz", manifest.BootArtifacts.Kernel},
		{"live-initramfs-digest", "initrd", manifest.BootArtifacts.Initrd},
		{"stock-fallback-kernel-digest", "vmlinuz-fedora", stockKernelRecord},
		{"stock-fallback-initramfs-digest", "initrd-fedora", stockInitrdRecord},
		{"x1e-dtb-digest", "x1e.dtb", findArtifact(manifest.BootArtifacts.DTBs, "x1e80100-microsoft-denali-oled.dtb")},
		{"x1p-dtb-digest", "x1p.dtb", findArtifact(manifest.BootArtifacts.DTBs, "x1p64100-microsoft-denali.dtb")},
	} {
		actual, hashErr := artifact.HashFile(filepath.Join(workspace, expected.file))
		info, statErr := os.Stat(filepath.Join(workspace, expected.file))
		recordErr := imagecontract.ValidateArtifactRecord(expected.record)
		passed := hashErr == nil && statErr == nil && recordErr == nil && actual == expected.record.SHA256 && info.Size() == expected.record.Size
		addCheck(expected.name, passed, fmt.Sprintf("expected_sha256=%s actual_sha256=%s", expected.record.SHA256, actual))
	}

	rpmRecord, rpmRecordFound := findEvidenceArtifact(manifest.MediaDiscovery, "installed-kernel-rpm")
	rpmDigest, rpmHashErr := artifact.HashFile(filepath.Join(workspace, "kernel.rpm"))
	rpmInfo, rpmStatErr := os.Stat(filepath.Join(workspace, "kernel.rpm"))
	rpmRecordErr := imagecontract.ValidateArtifactRecord(rpmRecord)
	rpmIdentityPassed := rpmRecordFound && rpmRecord.Path == "sp11/fedora/lexr-kernel-sp11.aarch64.rpm" &&
		rpmHashErr == nil && rpmStatErr == nil && rpmRecordErr == nil &&
		rpmDigest == rpmRecord.SHA256 && rpmInfo.Size() == rpmRecord.Size
	rpmOutput, rpmQueryErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
		"bash", "-ceu", `abi=$1
rpm -K --nosignature /work/kernel.rpm
[ "$(rpm -qp --qf '%{NAME}:%{ARCH}' /work/kernel.rpm)" = 'lexr-kernel-sp11:aarch64' ]
rpm -qp --provides /work/kernel.rpm | grep -Fx kernel-uname-r
rpm -qpl /work/kernel.rpm | grep -Fx "/boot/vmlinuz-$abi"
rpm -qpl /work/kernel.rpm | grep -Fx "/usr/lib/modules/$abi/vmlinuz-dtbloader.efi"`,
		"validate-native-rpm", manifest.KernelBundle.ABI)
	rpmPassed := rpmIdentityPassed && rpmQueryErr == nil
	addCheck("fedora-native-kernel-rpm", rpmPassed,
		fmt.Sprintf("sha256=%s metadata=%s", rpmDigest, strings.TrimSpace(string(rpmOutput))))

	report.Checks = append(report.Checks, v.validateKernelPackages(ctx, toolsImage, workspace, manifest)...)
	report.Checks = append(report.Checks, v.validateCompanion(ctx, toolsImage, workspace, manifest.CompanionBundle)...)
	report.Checks = append(report.Checks, v.validateIPTSDRPM(ctx, toolsImage, workspace, manifest)...)

	sections, sectionErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace, "objdump", "-h", "/work/vmlinuz")
	sectionText := string(sections)
	sectionsPassed := sectionErr == nil
	for _, section := range []string{".linux", ".hwids", ".dtbauto", ".uname", ".sbat"} {
		sectionsPassed = sectionsPassed && strings.Contains(sectionText, section)
	}
	addCheck("stubble-pe-sections", sectionsPassed, "expected .linux, .hwids, .dtbauto, .uname, and .sbat")
	identities, identityErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace, "strings", "/work/vmlinuz")
	identityText := string(identities)
	identityPassed := identityErr == nil && strings.Contains(identityText, "Microsoft Surface Pro 11th Edition (OLED)") &&
		strings.Contains(identityText, "microsoft,denali-oled")
	addCheck("stubble-x1e-auto-dtb", identityPassed, "X1E/OLED SMBIOS model and microsoft,denali-oled are embedded; X1P remains unclaimed")
	unameMatches, unameSections, unameMatchErr := matchingPESectionValues(
		filepath.Join(workspace, "vmlinuz"), ".uname", []byte(manifest.KernelBundle.ABI))
	addCheck("stubble-exact-uname", unameMatchErr == nil && unameMatches == 1 && unameSections == 1,
		fmt.Sprintf("exact_matches=%d uname_sections=%d", unameMatches, unameSections))
	x1eMatches, dtbautoSections, x1eMatchErr := matchingPESectionPayloads(
		filepath.Join(workspace, "vmlinuz"), ".dtbauto", filepath.Join(workspace, "x1e.dtb"))
	addCheck("stubble-x1e-exact-dtb", x1eMatchErr == nil && x1eMatches == 1,
		fmt.Sprintf("exact_matches=%d dtbauto_sections=%d", x1eMatches, dtbautoSections))

	stockHeaders, stockHeaderErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
		"bash", "-ceu", `objdump -f /work/vmlinuz-fedora
objdump -h /work/vmlinuz-fedora`, "validate-stock-fallback-pe")
	stockHeaderText := string(stockHeaders)
	stockPEPassed := stockHeaderErr == nil && strings.Contains(stockHeaderText, "file format pei-aarch64-little") &&
		strings.Contains(stockHeaderText, "architecture: aarch64") && strings.Contains(stockHeaderText, ".linux") &&
		strings.Contains(stockHeaderText, ".uname") && strings.Contains(stockHeaderText, ".sbat")
	stockABIErr := v.Docker.RunInWorkspace(ctx, toolsImage, workspace,
		"objcopy", "--dump-section", ".uname=/work/stock-uname", "/work/vmlinuz-fedora", "/work/stock-inspected.efi")
	stockABIBytes, stockABIReadErr := readBoundedRegularFile(filepath.Join(workspace, "stock-uname"), 4<<10)
	stockABI := strings.TrimRight(string(stockABIBytes), "\x00\r\n")
	stockABIPassed := stockABIErr == nil && stockABIReadErr == nil && safeKernelABIExpression.MatchString(stockABI) &&
		stockABI != manifest.KernelBundle.ABI
	addCheck("stock-fallback-pe-identity", stockPEPassed && stockABIPassed,
		fmt.Sprintf("format=PE/COFF-AArch64 abi=%s", stockABI))
	if !stockPEPassed || !stockABIPassed {
		return report, errors.Join(errors.New("Fedora ISO validation failed: invalid stock fallback kernel identity"), stockHeaderErr, stockABIErr, stockABIReadErr)
	}

	grubBytes, err := os.ReadFile(filepath.Join(workspace, "grub.cfg"))
	if err != nil {
		return report, err
	}
	grubCheckErr := v.Docker.RunInWorkspace(ctx, toolsImage, workspace, "grub2-script-check", "/work/grub.cfg")
	grubPassed := grubCheckErr == nil && bytes.Equal(grubBytes, []byte(grubConfig(manifest.KernelBundle.ABI)))
	addCheck("fedora-live-grub-policy", grubPassed, "GRUB exactly matches the custom normal/basic and stock fallback entries with required live arguments")
	fallbackDTBsPassed := grubPassed &&
		bytes.Contains(grubBytes, []byte("devicetree ($root)/sp11/dtb/x1e80100-microsoft-denali-oled.dtb")) &&
		bytes.Contains(grubBytes, []byte("devicetree ($root)/sp11/dtb/x1p64100-microsoft-denali.dtb"))
	addCheck("stock-fallback-external-dtbs", fallbackDTBsPassed, "stock X1E and live-only X1P entries load their manifest-bound loose Surface DTBs")

	policyBytes, policyErr := os.ReadFile(filepath.Join(workspace, "boot-policy.json"))
	policy, policyDecodeErr := decodeBootPolicy(policyBytes)
	expectedPolicy := expectedBootPolicy(manifest.KernelBundle.ABI)
	policyPassed := policyErr == nil && policyDecodeErr == nil && policy.SchemaVersion == expectedPolicy.SchemaVersion &&
		policy.KernelABI == expectedPolicy.KernelABI && slices.Equal(policy.Installed, expectedPolicy.Installed) &&
		slices.Equal(policy.LiveOnly, expectedPolicy.LiveOnly) && policy.X1PStatus == expectedPolicy.X1PStatus
	addCheck("live-installed-policy-split", policyPassed, "qcom_q6v5_pas is declared live-only")

	espBytes, espErr := os.ReadFile(filepath.Join(workspace, "iso-esp-grub.cfg"))
	espPassed := espErr == nil && bytes.Contains(espBytes, []byte("search --file --set=root /boot/0x503d6c7e")) &&
		bytes.Contains(espBytes, []byte("configfile ($root)/boot/grub2/grub.cfg"))
	loaderPassed := false
	grubLoaderPassed := false
	if systemErr == nil {
		offset, offsetErr := appendedESPOffset(string(systemArea))
		if offsetErr == nil {
			copyErr := v.Docker.RunInWorkspace(ctx, toolsImage, workspace,
				"bash", "-ceu", `image=$1
mcopy -o -i "$image" ::/EFI/BOOT/grub.cfg /work/appended-esp-grub.cfg
mcopy -o -i "$image" ::/EFI/BOOT/BOOTAA64.EFI /work/appended-bootaa64.efi
mcopy -o -i "$image" ::/EFI/BOOT/grubaa64.efi /work/appended-grubaa64.efi`,
				"validate-appended-esp", fmt.Sprintf("/work/image.iso@@%d", offset))
			same, sameErr := sameDigest(filepath.Join(workspace, "iso-esp-grub.cfg"), filepath.Join(workspace, "appended-esp-grub.cfg"))
			espPassed = espPassed && copyErr == nil && sameErr == nil && same
			loaderSame, loaderSameErr := sameDigest(filepath.Join(workspace, "iso-bootaa64.efi"), filepath.Join(workspace, "appended-bootaa64.efi"))
			loaderErr := v.Docker.RunInWorkspace(ctx, toolsImage, workspace,
				"bash", "-ceu", `objdump -f /work/appended-bootaa64.efi | grep -F 'file format pei-aarch64-little'
objdump -f /work/appended-bootaa64.efi | grep -F 'architecture: aarch64'`)
			loaderPassed = copyErr == nil && loaderSameErr == nil && loaderSame && loaderErr == nil
			grubSame, grubSameErr := sameDigest(filepath.Join(workspace, "iso-grubaa64.efi"), filepath.Join(workspace, "appended-grubaa64.efi"))
			grubLoaderErr := v.Docker.RunInWorkspace(ctx, toolsImage, workspace,
				"bash", "-ceu", `objdump -f /work/appended-grubaa64.efi | grep -F 'file format pei-aarch64-little'
objdump -f /work/appended-grubaa64.efi | grep -F 'architecture: aarch64'
strings /work/appended-grubaa64.efi | grep -Fx devicetree
strings /work/appended-grubaa64.efi | grep -F grub_fdt_load`, "validate-appended-grub")
			grubLoaderPassed = copyErr == nil && grubSameErr == nil && grubSame && grubLoaderErr == nil
		} else {
			espPassed = false
		}
	}
	addCheck("appended-esp-outer-config", espPassed, "appended ESP and ISO stubs select /boot/grub2/grub.cfg")
	addCheck("appended-esp-arm64-loader", loaderPassed, "appended ESP contains the same PE/COFF AArch64 BOOTAA64.EFI shim as the ISO tree")
	addCheck("appended-esp-arm64-grub", grubLoaderPassed, "appended ESP contains the same PE/COFF AArch64 GRUB with devicetree/FDT support as the ISO tree")

	workVolume, err := v.Docker.CreateWorkVolume(ctx)
	if err != nil {
		return report, err
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if cleanupErr := v.Docker.RemoveWorkVolume(cleanupContext, workVolume); cleanupErr != nil {
			returnErr = errors.Join(returnErr, cleanupErr)
		}
	}()
	filesystem, filesystemErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace, "blkid", "-p", "-s", "TYPE", "-o", "value", "/work/live.erofs")
	erofsInfo, erofsInfoErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace, "dump.erofs", "-s", "/work/live.erofs")
	erofsPassed := filesystemErr == nil && strings.TrimSpace(string(filesystem)) == "erofs" && erofsInfoErr == nil && strings.Contains(string(erofsInfo), "lzma")
	addCheck("fedora-erofs-live-root", erofsPassed, "LiveOS/squashfs.img is LZMA-compressed EROFS")
	if err := v.Docker.RunInWorkspaceVolumePreservingXattrs(ctx, toolsImage, workspace, workVolume,
		erofsExtractionArguments("/work/live.erofs", "/linux-work/rootfs")...); err != nil {
		addCheck("extract-remastered-erofs", false, err.Error())
		report.Valid = false
		return report, errors.New("Fedora ISO validation failed: remastered EROFS cannot be extracted")
	}
	addCheck("extract-remastered-erofs", true, "fsck.erofs extracted the complete root into a Linux-native volume")

	rootChecks := v.validateLiveRoot(ctx, toolsImage, workspace, workVolume, manifest, stockABI)
	report.Checks = append(report.Checks, rootChecks...)
	report.Valid = !slices.ContainsFunc(report.Checks, func(check imagecontract.ValidationCheck) bool { return !check.Passed })
	if !report.Valid {
		return report, errors.New("Fedora ISO validation failed")
	}
	return report, nil
}

// validateKernelPackages checks embedded package bytes against the manifest inventory.
func (v *Validator) validateKernelPackages(ctx context.Context, image, workspace string, manifest imagecontract.Manifest) []imagecontract.ValidationCheck {
	var checks []imagecontract.ValidationCheck
	add := func(name string, passed bool, details string) {
		checks = append(checks, imagecontract.ValidationCheck{Name: name, Passed: passed, Details: details})
	}
	for index, pkg := range manifest.KernelBundle.Packages {
		if pkg.Role != kernel.RoleImage && pkg.Role != kernel.RoleModules && pkg.Role != kernel.RoleBootSupport {
			continue
		}
		if filepath.Base(pkg.Path) != pkg.Name || !strings.HasPrefix(pkg.Path, "sp11/kernel/") {
			add("kernel-package-"+string(pkg.Role), false, "manifest package path is not portable")
			continue
		}
		name := fmt.Sprintf("kernel-package-%d", index)
		extractErr := v.Docker.RunInWorkspace(ctx, image, workspace, "xorriso", "-osirrox", "on", "-indev", "/work/image.iso", "-extract", "/"+pkg.Path, "/work/"+name)
		digest, hashErr := artifact.HashFile(filepath.Join(workspace, name))
		info, statErr := os.Stat(filepath.Join(workspace, name))
		passed := extractErr == nil && hashErr == nil && statErr == nil && digest == pkg.SHA256 && (pkg.Size == 0 || info.Size() == pkg.Size)
		add("kernel-package-"+string(pkg.Role), passed, fmt.Sprintf("sha256=%s size=%d", digest, func() int64 {
			if info != nil {
				return info.Size()
			}
			return 0
		}()))
	}
	return checks
}

// validateCompanion checks optional offline companion records and embedded bytes.
func (v *Validator) validateCompanion(ctx context.Context, image, workspace string, record imagecontract.CompanionBundleRecord) []imagecontract.ValidationCheck {
	if err := companion.ValidateRecord(record); err != nil {
		return []imagecontract.ValidationCheck{{Name: "companion-contract", Passed: false, Details: err.Error()}}
	}
	if !record.Included {
		return []imagecontract.ValidationCheck{{Name: "companion-contract", Passed: true, Details: "companion omitted: " + record.Reason}}
	}
	passed := true
	details := "all companion artifacts match the embedded manifest"
	for index, expected := range companion.FlattenArtifacts(record) {
		name := fmt.Sprintf("companion-%d", index)
		if err := v.Docker.RunInWorkspace(ctx, image, workspace, "xorriso", "-osirrox", "on", "-indev", "/work/image.iso", "-extract", "/"+expected.Path, "/work/"+name); err != nil {
			passed, details = false, err.Error()
			break
		}
		digest, err := artifact.HashFile(filepath.Join(workspace, name))
		info, statErr := os.Stat(filepath.Join(workspace, name))
		if err != nil || statErr != nil || digest != expected.SHA256 || info.Size() != expected.Size {
			passed, details = false, "companion artifact identity mismatch: "+expected.Path
			break
		}
	}
	return []imagecontract.ValidationCheck{{Name: "companion-contract", Passed: passed, Details: details}}
}

// validateLiveRoot inspects RPM, Anaconda handoff, dracut, policy, and SELinux state.
func (v *Validator) validateLiveRoot(ctx context.Context, image, workspace, volume string, manifest imagecontract.Manifest, stockABI string) []imagecontract.ValidationCheck {
	var checks []imagecontract.ValidationCheck
	add := func(name string, passed bool, details string) {
		checks = append(checks, imagecontract.ValidationCheck{Name: name, Passed: passed, Details: details})
	}
	abi := manifest.KernelBundle.ABI
	rpmOutput, rpmErr := v.Docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", `root=/linux-work/rootfs; abi=$1
rpm --root "$root" -q --qf '%{NAME}\t%{ARCH}\n' lexr-kernel-sp11
rpm --root "$root" -q --qf '%{NAME}\n' --whatprovides kernel-uname-r
rpm --root "$root" -q --qf '%{NAME}\n' --file "/boot/vmlinuz-$abi"
rpm --root "$root" -q --qf '%{NAME}\n' --file "/usr/lib/modules/$abi/vmlinuz-dtbloader.efi"`, "validate-rpm", abi)
	rpmPassed := rpmErr == nil && strings.Count(string(rpmOutput), "lexr-kernel-sp11") >= 4 && strings.Contains(string(rpmOutput), "aarch64")
	add("fedora-rpm-ownership", rpmPassed, strings.TrimSpace(string(rpmOutput)))

	payloadErr := v.Docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", `root=/linux-work/rootfs; abi=$1; stock_abi=$2
test -s "$root/boot/vmlinuz-$abi"
test -s "$root/usr/lib/modules/$abi/vmlinuz-dtbloader.efi"
test -L "$root/usr/lib/modules/$abi/vmlinuz"
test -s "$root/usr/lib/modules/$abi/modules.dep"
test -s "$root/usr/lib/modules/$abi/config"
test -s "$root/usr/lib/modules/$abi/System.map"
test -x "$root/boot/vmlinuz-$abi"
test -x "$root/usr/lib/modules/$abi/vmlinuz-dtbloader.efi"
[ "$(find "$root/boot" -maxdepth 1 -type f -name 'vmlinuz-*' -printf '%f\n')" = "vmlinuz-$abi" ]
cmp "$root/boot/vmlinuz-$abi" "$root/usr/lib/modules/$abi/vmlinuz-dtbloader.efi"
cmp "$root/boot/vmlinuz-$abi" /work/vmlinuz
cmp "$root/boot/initramfs-$abi.img" /work/initrd
for name in x1e80100-microsoft-denali-oled.dtb x1p64100-microsoft-denali.dtb; do
	test -s "$root/usr/lib/modules/$abi/dtb/qcom/$name"
	test -s "$root/usr/lib/firmware/$abi/device-tree/qcom/$name"
done
stock_image="$root/usr/lib/modules/$stock_abi/vmlinuz-dtbloader.efi"
test -s "$stock_image"
test -x "$stock_image"
rpm --root "$root" -q --whatprovides "/boot/vmlinuz-$stock_abi" >/dev/null
tmp=$(mktemp -d /linux-work/stock-sections.XXXXXX)
trap 'rm -rf -- "$tmp"' EXIT
objcopy --dump-section ".linux=$tmp/outer-linux" --dump-section ".uname=$tmp/outer-uname" \
	/work/vmlinuz-fedora "$tmp/outer-inspected.efi"
objcopy --dump-section ".linux=$tmp/module-linux" --dump-section ".uname=$tmp/module-uname" \
	"$stock_image" "$tmp/module-inspected.efi"
cmp "$tmp/outer-linux" "$tmp/module-linux"
cmp "$tmp/outer-uname" "$tmp/module-uname"`, "validate-payload", abi, stockABI)
	add("installed-kernel-handoff", payloadErr == nil, "Anaconda-visible custom kernel and exact-ABI stock fallback have matching RPM-owned module trees")
	add("stock-fallback-installed-kernel", payloadErr == nil, "outer stock fallback .linux and .uname sections match its RPM-owned installed PE")

	dracutOutput, dracutErr := v.Docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", `root=/linux-work/rootfs; abi=$1; chroot "$root" /usr/bin/lsinitrd -m "/boot/initramfs-$abi.img"; chroot "$root" /usr/bin/lsinitrd "/boot/initramfs-$abi.img" | grep -F "usr/lib/modules/$abi/" >/dev/null`, "validate-dracut", abi)
	dracutPassed := dracutErr == nil && strings.Contains(string(dracutOutput), "dmsquash-live")
	add("dracut-live-initramfs", dracutPassed, "exact-ABI initramfs contains dmsquash-live and custom modules")

	stockInitrdOutput, stockInitrdErr := v.Docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", `root=/linux-work/rootfs; stock_abi=$1
stock_copy="$root/tmp/lexr-stock-fallback-initrd.img"
trap 'rm -f -- "$stock_copy"' EXIT
install -m 0600 /work/initrd-fedora "$stock_copy"
chroot "$root" /usr/bin/lsinitrd -m /tmp/lexr-stock-fallback-initrd.img
chroot "$root" /usr/bin/lsinitrd /tmp/lexr-stock-fallback-initrd.img | grep -F "usr/lib/modules/$stock_abi/" >/dev/null`,
		"validate-stock-initrd", stockABI)
	stockInitrdPassed := stockInitrdErr == nil && strings.Contains(string(stockInitrdOutput), "dmsquash-live")
	add("stock-fallback-live-initramfs", stockInitrdPassed, "manifest-bound fallback initramfs contains dmsquash-live and its exact stock ABI modules")

	policyOutput, policyErr := v.Docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", `root=/linux-work/rootfs; abi=$1
grep -F 'clk_ignore_unused pd_ignore_unused systemd.tpm2_wait=0 soundwire_qcom.sp11_feedback_active_offset2_zero=1' "$root/etc/default/grub"
grep -Fx 'layout=other' "$root/etc/kernel/install.conf"
! grep -R -E '(^|[[:space:]])(modprobe|rd.driver).blacklist=qcom_q6v5_pas' "$root/etc/default/grub" "$root/usr/lib/lexr/sp11/grub-defaults"
test -x "$root/usr/lib/lexr/sp11/finalize-installed"
test -L "$root/usr/lib/systemd/system/multi-user.target.wants/lexr-sp11-installed-finalize.service"
grep -F 'ConditionKernelCommandLine=!rd.live.image' "$root/usr/lib/systemd/system/lexr-sp11-installed-finalize.service"
grep -F 'rm -f -- /etc/modprobe.d/anaconda-denylist.conf' "$root/usr/lib/lexr/sp11/finalize-installed"
grep -F 'rpm -q --whatprovides "$boot_image"' "$root/usr/lib/lexr/sp11/finalize-installed"
grep -F '/usr/sbin/depmod -a "$stock_abi"' "$root/usr/lib/lexr/sp11/finalize-installed"
grep -F '/usr/bin/dracut --force "/boot/initramfs-$stock_abi.img" "$stock_abi"' "$root/usr/lib/lexr/sp11/finalize-installed"
grep -F 'fallback_dtb=qcom/x1e80100-microsoft-denali-oled.dtb' "$root/usr/lib/lexr/sp11/finalize-installed"
grep -F 'GRUB_DEVICETREE="$fallback_dtb" KERNEL_INSTALL_LAYOUT=other' "$root/usr/lib/lexr/sp11/finalize-installed"
grep -F 'cmp "$fallback_dtb_source" "$stock_dtb"' "$root/usr/lib/lexr/sp11/finalize-installed"
grep -F 'grep -Fx "devicetree /dtb-$stock_abi/$fallback_dtb" "$bls_entry"' "$root/usr/lib/lexr/sp11/finalize-installed"
grep -F 'kernel-install add "$stock_abi" "$stock_image"' "$root/usr/lib/lexr/sp11/finalize-installed"
grep -F 'grubby --set-default="/boot/vmlinuz-$abi"' "$root/usr/lib/lexr/sp11/finalize-installed"`, "validate-policy", abi)
	add("installed-boot-policy", policyErr == nil, strings.TrimSpace(string(policyOutput)))

	xattrOutput, xattrErr := v.Docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume,
		"getfattr", "--only-values", "-n", "security.selinux", "/linux-work/rootfs/usr/bin/bash")
	xattrPassed := xattrErr == nil && strings.Contains(string(xattrOutput), ":shell_exec_t:")
	add("selinux-file-contexts", xattrPassed, strings.TrimSpace(string(xattrOutput)))
	checks = append(checks, v.validateIPTSDLiveRoot(ctx, image, workspace, volume, manifest)...)
	return checks
}

// snapshotValidationImage copies one stable regular file while hashing bounded bytes.
func snapshotValidationImage(ctx context.Context, sourcePath, destinationPath string) (digest string, size int64, returnErr error) {
	listed, err := os.Lstat(sourcePath)
	if err != nil {
		return "", 0, err
	}
	if listed.Mode()&os.ModeSymlink != 0 || !listed.Mode().IsRegular() || listed.Size() <= 0 || listed.Size() > maximumValidationImageBytes {
		return "", 0, errors.New("ISO is not a bounded non-symbolic-link regular file")
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", 0, err
	}
	defer func() { returnErr = errors.Join(returnErr, source.Close()) }()
	opened, err := source.Stat()
	if err != nil || !os.SameFile(listed, opened) {
		return "", 0, errors.Join(errors.New("ISO identity changed while opening validation snapshot"), err)
	}
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, err
	}
	keep := false
	defer func() {
		returnErr = errors.Join(returnErr, destination.Close())
		if !keep {
			returnErr = errors.Join(returnErr, os.Remove(destinationPath))
		}
	}()
	hasher := sha256.New()
	buffer := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", size, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			size += int64(count)
			if size > listed.Size() || size > maximumValidationImageBytes {
				return "", size, errors.New("ISO grew while creating validation snapshot")
			}
			if _, err := destination.Write(buffer[:count]); err != nil {
				return "", size, err
			}
			_, _ = hasher.Write(buffer[:count])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", size, readErr
		}
	}
	if size != listed.Size() {
		return "", size, errors.New("ISO size changed while creating validation snapshot")
	}
	if err := destination.Sync(); err != nil {
		return "", size, err
	}
	keep = true
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

// readBoundedRegularFile reads a non-link regular file below the supplied size limit.
func readBoundedRegularFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maximum {
		return nil, errors.New("file is not a bounded non-symbolic-link regular file")
	}
	return os.ReadFile(path)
}

// validFedoraMediaDiscovery enforces Fedora's exact dracut volume-label contract.
func validFedoraMediaDiscovery(record imagecontract.MediaDiscoveryRecord) bool {
	if record.Strategy != "direct-hybrid-iso" || record.Protocol != "dracut-live" {
		return false
	}
	var label, liveRoot bool
	for _, evidence := range record.Evidence {
		label = label || evidence.Role == "iso-volume-label" && evidence.Scope == "iso9660-pvd" && evidence.Value == SourceVolumeID
		liveRoot = liveRoot || evidence.Role == "live-root" && evidence.Scope == "grub" && evidence.Value == "root=live:CDLABEL="+SourceVolumeID+" rd.live.image"
	}
	return label && liveRoot
}

// decodeBootPolicy accepts exactly one object and rejects silent policy-field drift.
func decodeBootPolicy(data []byte) (bootPolicy, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var policy bootPolicy
	if err := decoder.Decode(&policy); err != nil {
		return bootPolicy{}, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return bootPolicy{}, errors.New("boot policy contains multiple JSON values")
		}
		return bootPolicy{}, err
	}
	return policy, nil
}

// validateFedoraManifest proves provenance and policy are complete before any
// extracted payload is allowed to satisfy the lower-level structural checks.
func validateFedoraManifest(manifest imagecontract.Manifest) error {
	if manifest.SchemaVersion != imagecontract.ManifestSchemaVersion || manifest.Adapter != AdapterID || manifest.Layout != "hybrid-iso" {
		return errors.New("manifest schema, adapter, or layout does not identify Fedora Live")
	}
	if manifest.CreatedAt.IsZero() || strings.TrimSpace(manifest.ToolVersion) == "" {
		return errors.New("manifest creation time and tool version are required")
	}
	if err := imagecontract.ValidateArtifactRecord(manifest.SourceImage); err != nil || manifest.SourceImage.Path != "source.iso" {
		return errors.Join(errors.New("manifest source-image record is invalid"), err)
	}
	bundle := manifest.KernelBundle
	if bundle.SchemaVersion != kernel.BundleSchemaVersion || bundle.Architecture != "arm64" ||
		strings.TrimSpace(bundle.Release) == "" || strings.TrimSpace(bundle.Version) == "" ||
		requireSupportedKernel(bundle.ABI) != nil {
		return errors.New("manifest kernel bundle identity is incomplete or unsupported")
	}
	expectedPackages := 2
	if bundle.EffectiveDTBDelivery == kernel.DTBDeliveryExternalRequired {
		expectedPackages = 3
	}
	if len(bundle.Packages) != expectedPackages {
		return fmt.Errorf("manifest kernel bundle contains %d packages; expected %d delivery packages", len(bundle.Packages), expectedPackages)
	}
	seenRoles := make(map[kernel.PackageRole]bool, expectedPackages)
	for _, pkg := range bundle.Packages {
		if pkg.Role != kernel.RoleImage && pkg.Role != kernel.RoleModules && pkg.Role != kernel.RoleBootSupport || seenRoles[pkg.Role] || !pkg.Verified || pkg.Size <= 0 {
			return fmt.Errorf("manifest kernel package %q has invalid role, verification, or size", pkg.Name)
		}
		seenRoles[pkg.Role] = true
		role, abi, version, err := kernel.ParsePackageName(pkg.Name)
		identityMatches := role == kernel.RoleBootSupport && abi == "" && version == bundle.Version || abi == bundle.ABI && version == bundle.Version
		if err != nil || role != pkg.Role || !identityMatches ||
			pkg.Path != "sp11/kernel/"+pkg.Name {
			return errors.Join(fmt.Errorf("manifest kernel package %q does not match bundle identity", pkg.Name), err)
		}
		if err := imagecontract.ValidateArtifactRecord(imagecontract.ArtifactRecord{Path: pkg.Path, SHA256: pkg.SHA256, Size: pkg.Size}); err != nil {
			return fmt.Errorf("manifest kernel package %q: %w", pkg.Name, err)
		}
	}
	if !seenRoles[kernel.RoleImage] || !seenRoles[kernel.RoleModules] {
		return errors.New("manifest kernel bundle lacks the exact image/modules pair")
	}
	if bundle.EffectiveDTBDelivery == kernel.DTBDeliveryExternalRequired && !seenRoles[kernel.RoleBootSupport] {
		return errors.New("manifest external-required kernel bundle lacks boot support")
	}
	canonical, err := kernel.NewBundle(kernel.BundleOptions{
		Release: bundle.Release, Repository: bundle.Repository,
		RequestedBootImageMode: bundle.RequestedBootImageMode, EffectiveDTBDelivery: bundle.EffectiveDTBDelivery,
		EmbeddedDTBCount: bundle.EmbeddedDTBCount, DTBSelectionProvenance: bundle.DTBSelectionProvenance,
		Packages: bundle.Packages, DeviceTrees: bundle.DeviceTrees,
	})
	if err != nil || !reflect.DeepEqual(bundle, canonical) {
		return errors.Join(errors.New("manifest kernel bundle delivery contract is invalid or non-canonical"), err)
	}
	if err := imagecontract.ValidateArtifactRecord(manifest.BootArtifacts.Kernel); err != nil ||
		manifest.BootArtifacts.Kernel.Path != "boot/aarch64/loader/linux" {
		return errors.Join(errors.New("manifest live-kernel record is invalid"), err)
	}
	if err := imagecontract.ValidateArtifactRecord(manifest.BootArtifacts.Initrd); err != nil ||
		manifest.BootArtifacts.Initrd.Path != "boot/aarch64/loader/initrd" {
		return errors.Join(errors.New("manifest live-initramfs record is invalid"), err)
	}
	expectedDTBPaths := []string{
		"sp11/dtb/x1e80100-microsoft-denali-oled.dtb",
		"sp11/dtb/x1p64100-microsoft-denali.dtb",
	}
	if len(manifest.BootArtifacts.DTBs) != len(expectedDTBPaths) {
		return errors.New("manifest boot-artifact DTB set is incomplete")
	}
	for index, record := range manifest.BootArtifacts.DTBs {
		if err := imagecontract.ValidateArtifactRecord(record); err != nil || record.Path != expectedDTBPaths[index] {
			return errors.Join(fmt.Errorf("manifest DTB record %d is invalid", index), err)
		}
	}
	expectedArguments := append([]string(nil), installedBootArguments...)
	expectedArguments = append(expectedArguments, liveOnlyBootArguments...)
	if !slices.Equal(manifest.BootArguments, expectedArguments) || manifest.SecureBoot != secureBootPolicy {
		return errors.New("manifest boot arguments or Secure Boot policy differ from the Fedora adapter contract")
	}
	if err := companion.ValidateRecord(manifest.CompanionBundle); err != nil {
		return fmt.Errorf("manifest companion record: %w", err)
	}
	_, iptsdIncluded, err := fedoraIPTSDUserspace(manifest.CompanionBundle)
	if err != nil {
		return fmt.Errorf("manifest IPTSD companion record: %w", err)
	}
	if !validFedoraMediaDiscovery(manifest.MediaDiscovery) {
		return errors.New("manifest dracut-live media-discovery record is invalid")
	}
	rpmRecord, ok := findEvidenceArtifact(manifest.MediaDiscovery, "installed-kernel-rpm")
	if !ok || rpmRecord.Path != "sp11/fedora/lexr-kernel-sp11.aarch64.rpm" {
		return errors.New("manifest lacks the native installed-kernel RPM record")
	}
	if err := imagecontract.ValidateArtifactRecord(rpmRecord); err != nil {
		return fmt.Errorf("manifest native installed-kernel RPM record: %w", err)
	}
	for _, expected := range []struct {
		role string
		path string
	}{
		{role: "stock-fallback-kernel", path: "boot/aarch64/loader/linux-fedora"},
		{role: "stock-fallback-initramfs", path: "boot/aarch64/loader/initrd-fedora"},
	} {
		record, found := findEvidenceArtifact(manifest.MediaDiscovery, expected.role)
		if !found || record.Path != expected.path {
			return fmt.Errorf("manifest lacks the exact %s record", expected.role)
		}
		if err := imagecontract.ValidateArtifactRecord(record); err != nil {
			return fmt.Errorf("manifest %s record: %w", expected.role, err)
		}
	}
	iptsdRPMRecord, hasIPTSDRPM := findEvidenceArtifact(manifest.MediaDiscovery, "installed-iptsd-rpm")
	iptsdSRPMRecord, hasIPTSDSRPM := findEvidenceArtifact(manifest.MediaDiscovery, "iptsd-source-rpm")
	if iptsdIncluded {
		if !hasIPTSDRPM || iptsdRPMRecord.Path != fedoraIPTSDRPMPath {
			return errors.New("manifest IPTSD companion lacks its native installed-RPM record")
		}
		if err := imagecontract.ValidateArtifactRecord(iptsdRPMRecord); err != nil {
			return fmt.Errorf("manifest native installed-IPTSD RPM record: %w", err)
		}
		if !hasIPTSDSRPM || iptsdSRPMRecord.Path != fedoraIPTSDSRPMPath {
			return errors.New("manifest IPTSD companion lacks its native source-RPM record")
		}
		if err := imagecontract.ValidateArtifactRecord(iptsdSRPMRecord); err != nil {
			return fmt.Errorf("manifest native IPTSD source-RPM record: %w", err)
		}
	} else if hasIPTSDRPM || hasIPTSDSRPM {
		return errors.New("manifest claims native IPTSD RPMs without the source-bearing companion release")
	}
	expectedEvidence := 5
	if iptsdIncluded {
		expectedEvidence += 2
	}
	if len(manifest.MediaDiscovery.Evidence) != expectedEvidence {
		return fmt.Errorf("manifest contains %d media-discovery evidence records; expected %d", len(manifest.MediaDiscovery.Evidence), expectedEvidence)
	}
	return nil
}

// appendedESPOffset parses xorriso's validated GPT report into an mtools byte offset.
func appendedESPOffset(report string) (int64, error) {
	matches := regexp.MustCompile(`GPT start and size\s*:\s*2\s+(\d+)\s+(\d+)`).FindStringSubmatch(report)
	if matches == nil {
		return 0, errors.New("no appended ESP partition found")
	}
	start, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, err
	}
	size, err := strconv.ParseInt(matches[2], 10, 64)
	if err != nil || size <= 0 || start > (1<<63-1)/512 {
		return 0, errors.New("invalid appended ESP extent")
	}
	return start * 512, nil
}

// sameDigest compares the SHA-256 identities of two extracted artefacts.
func sameDigest(first, second string) (bool, error) {
	firstDigest, err := artifact.HashFile(first)
	if err != nil {
		return false, err
	}
	secondDigest, err := artifact.HashFile(second)
	if err != nil {
		return false, err
	}
	return firstDigest == secondDigest, nil
}

// findArtifact locates one exact portable suffix in a manifest artefact set.
func findArtifact(records []imagecontract.ArtifactRecord, suffix string) imagecontract.ArtifactRecord {
	for _, record := range records {
		if strings.HasSuffix(record.Path, suffix) {
			return record
		}
	}
	return imagecontract.ArtifactRecord{}
}

// findEvidenceArtifact returns one manifest-bound adapter artefact with an
// internally consistent evidence path.
func findEvidenceArtifact(record imagecontract.MediaDiscoveryRecord, role string) (imagecontract.ArtifactRecord, bool) {
	for _, evidence := range record.Evidence {
		if evidence.Role == role && evidence.Scope == "iso9660" && evidence.Artifact != nil && evidence.Path == evidence.Artifact.Path {
			return *evidence.Artifact, true
		}
	}
	return imagecontract.ArtifactRecord{}, false
}
