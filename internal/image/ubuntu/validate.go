package ubuntu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/image/ubuntu/caspermedia"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// maximumValidationImageBytes bounds one private ISO validation snapshot.
const maximumValidationImageBytes int64 = 64 << 30

// maximumValidationTextBytes bounds small, extracted configuration records
// before any of their untrusted contents are retained in validation evidence.
const maximumValidationTextBytes int64 = 64 << 10

// maximumGRUBConfigurationBytes bounds the extracted live-media menu.
const maximumGRUBConfigurationBytes int64 = 1 << 20

// maximumPackageStatusBytes accommodates a normal Debian installed-package
// database while preventing an extracted record from consuming unbounded RAM.
const maximumPackageStatusBytes int64 = 32 << 20

// Validator inspects a completed Ubuntu image with isolated tooling and produces
// a digest-bound report covering its boot layout and embedded kernel payload.
type Validator struct {
	// Docker provides xorriso, SquashFS, initramfs, and FAT inspection tools.
	Docker *platform.Docker
}

// NewValidator creates a structural image validator and supplies the standard
// Docker runner when none is injected.
func NewValidator(docker *platform.Docker) *Validator {
	if docker == nil {
		docker = platform.NewDocker(nil)
	}
	return &Validator{Docker: docker}
}

// Validate checks that isoPath is a regular hybrid ARM64 image whose manifest,
// live and installed kernels, initramfs images, device trees, and GRUB paths
// agree. It returns accumulated evidence alongside an error on any failure.
func (v *Validator) Validate(ctx context.Context, isoPath string) (report imagecontract.ValidationReport, resultErr error) {
	absolute, err := filepath.Abs(isoPath)
	if err != nil {
		return imagecontract.ValidationReport{}, err
	}
	if err := v.Docker.Check(ctx); err != nil {
		return imagecontract.ValidationReport{Path: absolute, Layout: "hybrid-iso", Adapter: AdapterID}, err
	}
	toolsImage, err := v.Docker.EnsureToolsImage(ctx)
	if err != nil {
		return imagecontract.ValidationReport{Path: absolute, Layout: "hybrid-iso", Adapter: AdapterID}, err
	}
	workspace, err := os.MkdirTemp(filepath.Dir(absolute), ".lexr-validate-")
	if err != nil {
		return imagecontract.ValidationReport{Path: absolute, Layout: "hybrid-iso", Adapter: AdapterID}, err
	}
	defer func() {
		if err := os.RemoveAll(workspace); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove private validation workspace: %w", err))
		}
	}()
	digest, size, err := snapshotValidationImage(ctx, absolute, filepath.Join(workspace, "image.iso"))
	if err != nil {
		return imagecontract.ValidationReport{Path: absolute, Layout: "hybrid-iso", Adapter: AdapterID}, err
	}
	report = imagecontract.ValidationReport{
		Path: absolute, SHA256: digest, Size: size, Layout: "hybrid-iso", Adapter: AdapterID,
	}
	addCheck := func(name string, passed bool, details string) {
		report.Checks = append(report.Checks, imagecontract.ValidationCheck{Name: name, Passed: passed, Details: details})
	}

	systemArea, systemErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
		"xorriso", "-indev", "/work/image.iso", "-report_system_area", "plain")
	if systemErr != nil {
		addCheck("hybrid-system-area", false, systemErr.Error())
	} else {
		details := string(systemArea)
		_, offsetErr := appendedESPOffset(details)
		passed := strings.Contains(details, "GPT") && offsetErr == nil
		checkDetails := firstMatchingLine(details, "System area summary")
		if offsetErr != nil {
			checkDetails = offsetErr.Error()
		}
		addCheck("hybrid-system-area", passed, checkDetails)
	}
	elTorito, elToritoErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
		"xorriso", "-indev", "/work/image.iso", "-report_el_torito", "plain")
	if elToritoErr != nil {
		addCheck("arm64-efi-boot-catalog", false, elToritoErr.Error())
	} else {
		details := string(elTorito)
		passed := strings.Contains(strings.ToLower(details), "uefi") || strings.Contains(strings.ToLower(details), "efi")
		addCheck("arm64-efi-boot-catalog", passed, strings.TrimSpace(details))
	}

	extractErr := v.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace,
		"xorriso", "-osirrox", "on", "-indev", "/work/image.iso",
		"-extract", "/sp11/lexr-manifest.json", "/work/manifest.json",
		"-extract", "/casper/vmlinuz", "/work/vmlinuz",
		"-extract", "/casper/initrd", "/work/initrd",
		"-extract", "/.disk/casper-uuid-generic", "/work/casper-uuid-generic",
		"-extract", "/casper/minimal.squashfs", "/work/minimal.squashfs",
		"-extract", "/sp11/dtb/x1e80100-microsoft-denali-oled.dtb", "/work/x1e.dtb",
		"-extract", "/sp11/dtb/x1p64100-microsoft-denali.dtb", "/work/x1p.dtb",
		"-extract", "/EFI/boot/bootaa64.efi", "/work/iso-bootaa64.efi",
		"-extract", "/EFI/boot/grubaa64.efi", "/work/iso-grubaa64.efi",
		"-extract", "/boot/grub/grub.cfg", "/work/grub.cfg")
	if extractErr != nil {
		addCheck("required-iso-members", false, extractErr.Error())
		report.Valid = false
		return report, fmt.Errorf("ISO validation failed: required members cannot be extracted")
	}
	initialMembers := []string{
		"manifest.json", "vmlinuz", "initrd", "casper-uuid-generic", "minimal.squashfs",
		"x1e.dtb", "x1p.dtb", "iso-bootaa64.efi", "iso-grubaa64.efi", "grub.cfg",
	}
	if err := validateExtractedRegularFiles(workspace, initialMembers); err != nil {
		addCheck("required-iso-members", false, err.Error())
		report.Valid = false
		return report, fmt.Errorf("ISO validation failed: required members are unsafe: %w", err)
	}
	addCheck("required-iso-members", true, "kernel, initramfs, Casper identity, live root, paired DTBs, manifest, and GRUB files are present")

	manifestPath := filepath.Join(workspace, "manifest.json")
	manifestBytes, err := readValidationManifest(manifestPath)
	if err != nil {
		addCheck("embedded-manifest", false, "embedded manifest is missing, non-regular, or outside its size limit")
		return report, fmt.Errorf("inspect embedded manifest: %w", err)
	}
	manifestDigest := sha256.Sum256(manifestBytes)
	report.ManifestSHA256 = fmt.Sprintf("%x", manifestDigest)
	report.ManifestSize = int64(len(manifestBytes))
	manifest, err := imagecontract.DecodeManifest(bytes.NewReader(manifestBytes))
	if err != nil {
		addCheck("embedded-manifest", false, err.Error())
		return report, fmt.Errorf("decode embedded manifest: %w", err)
	}
	report.KernelABI = manifest.KernelBundle.ABI
	for _, dtb := range manifest.KernelBundle.DeviceTrees {
		report.DeviceTrees = append(report.DeviceTrees, dtb.Device)
	}
	abiSafe := safeKernelABI(manifest.KernelBundle.ABI)
	manifestMediaContract, markerRecord, manifestMediaErr := caspermedia.FromDiscoveryRecord(manifest.MediaDiscovery)
	companionRecordErr := companion.ValidateRecord(manifest.CompanionBundle)
	manifestBundleErr := validateManifestKernelBundle(manifest.KernelBundle)
	manifestOK := manifest.SchemaVersion == imagecontract.ManifestSchemaVersion &&
		manifest.Adapter == AdapterID &&
		manifestBundleErr == nil &&
		manifestMediaErr == nil &&
		companionRecordErr == nil &&
		abiSafe
	manifestDetails := fmt.Sprintf("schema=%d adapter=%s abi=%s", manifest.SchemaVersion, manifest.Adapter, manifest.KernelBundle.ABI)
	if manifestBundleErr != nil {
		manifestDetails = manifestBundleErr.Error()
	}
	addCheck("embedded-manifest", manifestOK, manifestDetails)
	report.Checks = append(report.Checks, v.validateCompanionBundle(ctx, toolsImage, workspace, manifest.CompanionBundle, companionRecordErr)...)
	if !abiSafe {
		report.Valid = false
		return report, errors.New("ISO validation failed: embedded kernel ABI is not a safe path component")
	}

	for _, expected := range []struct {
		name   string
		path   string
		record imagecontract.ArtifactRecord
	}{
		{"live-kernel-digest", "vmlinuz", manifest.BootArtifacts.Kernel},
		{"live-initramfs-digest", "initrd", manifest.BootArtifacts.Initrd},
		{"x1e-dtb-digest", "x1e.dtb", findArtifact(manifest.BootArtifacts.DTBs, "x1e80100-microsoft-denali-oled.dtb")},
		{"x1p-dtb-digest", "x1p.dtb", findArtifact(manifest.BootArtifacts.DTBs, "x1p64100-microsoft-denali.dtb")},
	} {
		artifactPath := filepath.Join(workspace, expected.path)
		actual, hashErr := artifact.HashFile(artifactPath)
		artifactInfo, statErr := os.Stat(artifactPath)
		recordErr := imagecontract.ValidateArtifactRecord(expected.record)
		passed := hashErr == nil && statErr == nil && recordErr == nil &&
			actual == expected.record.SHA256 && artifactInfo.Size() == expected.record.Size
		details := fmt.Sprintf("expected_sha256=%s actual_sha256=%s expected_size=%d",
			expected.record.SHA256, actual, expected.record.Size)
		if hashErr != nil {
			details = hashErr.Error()
		} else if statErr != nil {
			details = statErr.Error()
		} else if recordErr != nil {
			details = recordErr.Error()
		} else {
			details += fmt.Sprintf(" actual_size=%d", artifactInfo.Size())
		}
		addCheck(expected.name, passed, details)
	}

	initrdListing, initrdErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace, "lsinitramfs", "/work/initrd")
	if initrdErr != nil {
		addCheck("matching-initramfs-modules", false, initrdErr.Error())
		addCheck("casper-live-initramfs", false, initrdErr.Error())
	} else {
		listing := string(initrdListing)
		needle := "usr/lib/modules/" + manifest.KernelBundle.ABI + "/"
		passed := strings.Contains(listing, needle)
		addCheck("matching-initramfs-modules", passed, "expected "+needle)
		casperPassed := strings.Contains(listing, "scripts/casper") && strings.Contains(listing, "scripts/casper-bottom/")
		addCheck("casper-live-initramfs", casperPassed, "expected Casper boot and live-session scripts")
	}

	markerPath := filepath.Join(workspace, "casper-uuid-generic")
	markerBytes, markerErr := readBoundedExtractedFile(workspace, "casper-uuid-generic", maximumValidationTextBytes)
	markerDigest, markerHashErr := artifact.HashFile(markerPath)
	markerInfo, markerStatErr := os.Stat(markerPath)
	markerPassed := markerErr == nil && markerHashErr == nil && markerStatErr == nil && markerRecord.SHA256 != "" &&
		markerDigest == markerRecord.SHA256 && markerInfo.Size() == markerRecord.Size
	markerDetails := fmt.Sprintf("expected=%s actual=%s", markerRecord.SHA256, markerDigest)
	if markerErr != nil {
		markerDetails = markerErr.Error()
	} else if markerHashErr != nil {
		markerDetails = markerHashErr.Error()
	} else if markerStatErr != nil {
		markerDetails = markerStatErr.Error()
	}
	addCheck("casper-media-identity-digest", markerPassed, markerDetails)

	unpackedInitrd := filepath.Join(workspace, "initrd-unpacked")
	unpackErr := v.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace,
		"unmkinitramfs", "/work/initrd", "/work/initrd-unpacked")
	firmwareErr := unpackErr
	if firmwareErr == nil {
		firmwareErr = validateLiveGPUFirmware(unpackedInitrd, manifest.KernelBundle.ABI)
	}
	firmwareDetails := "X1E GPU GMU and SQE firmware are present before the live root is mounted"
	if firmwareErr != nil {
		firmwareDetails = firmwareErr.Error()
	}
	addCheck("live-gpu-firmware", firmwareErr == nil, firmwareDetails)
	initrdIdentityRelative := path.Join("main", caspermedia.InitramfsIdentityPath)
	initrdIdentity, identityReadErr := readBoundedExtractedFile(unpackedInitrd, initrdIdentityRelative, maximumValidationTextBytes)
	mediaContract, identityErr := caspermedia.Matches(markerBytes, initrdIdentity)
	identityPassed := unpackErr == nil && markerErr == nil && identityReadErr == nil && identityErr == nil &&
		mediaContract.UUID == manifestMediaContract.UUID
	identityDetails := fmt.Sprintf("medium=%q initramfs=%q manifest=%q",
		strings.TrimSpace(string(markerBytes)), strings.TrimSpace(string(initrdIdentity)), manifestMediaContract.UUID)
	if unpackErr != nil {
		identityDetails = unpackErr.Error()
	} else if identityReadErr != nil {
		identityDetails = identityReadErr.Error()
	} else if identityErr != nil {
		identityDetails = identityErr.Error()
	}
	addCheck("casper-media-uuid", identityPassed, identityDetails)

	defaultBoot, defaultBootErr := readBoundedExtractedFile(unpackedInitrd, "main/conf/conf.d/default-boot-to-casper.conf", maximumValidationTextBytes)
	defaultLayer, defaultLayerErr := readBoundedExtractedFile(unpackedInitrd, "main/conf/conf.d/default-layer.conf", maximumValidationTextBytes)
	bootDefaultPassed := unpackErr == nil && defaultBootErr == nil && strings.Contains(string(defaultBoot), "export BOOT=casper")
	addCheck("casper-default-boot", bootDefaultPassed, "initramfs defaults an otherwise unset BOOT value to casper")
	layerName := strings.TrimPrefix(strings.TrimSpace(string(defaultLayer)), "LAYERFS_PATH=")
	layerOutput, layerMemberErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
		"xorriso", "-indev", "/work/image.iso", "-ls", "/casper/"+layerName)
	layerPassed := unpackErr == nil && defaultLayerErr == nil && layerName == "minimal.standard.live.squashfs" &&
		layerMemberErr == nil && strings.Contains(string(layerOutput), "/casper/"+layerName)
	layerDetails := fmt.Sprintf("LAYERFS_PATH=%s", layerName)
	if defaultLayerErr != nil {
		layerDetails = defaultLayerErr.Error()
	} else if layerMemberErr != nil {
		layerDetails = layerMemberErr.Error()
	}
	addCheck("casper-default-layer", layerPassed, layerDetails)

	moduleProbe := "usr/lib/modules/" + manifest.KernelBundle.ABI + "/modules.dep"
	moduleErr := v.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace,
		"unsquashfs", "-no-xattrs", "-no-progress", "-d", "/work/module-probe", "/work/minimal.squashfs", moduleProbe)
	moduleValidationErr := validateExtractedRegularFiles(filepath.Join(workspace, "module-probe"), []string{moduleProbe})
	modulePassed := moduleErr == nil && moduleValidationErr == nil
	moduleDetails := moduleProbe
	if moduleErr != nil {
		moduleDetails = moduleErr.Error()
	} else if moduleValidationErr != nil {
		moduleDetails = moduleValidationErr.Error()
	}
	addCheck("live-root-kernel-modules", modulePassed, moduleDetails)
	report.Checks = append(report.Checks, v.validateInstalledSystemSupport(ctx, toolsImage, workspace, manifest)...)

	for _, dtb := range []string{"x1e.dtb", "x1p.dtb"} {
		fileOutput, fileErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace, "file", "/work/"+dtb)
		passed := fileErr == nil && strings.Contains(string(fileOutput), "Device Tree Blob")
		details := strings.TrimSpace(string(fileOutput))
		if fileErr != nil {
			details = fileErr.Error()
		}
		addCheck("device-tree-format-"+strings.TrimSuffix(dtb, ".dtb"), passed, details)
	}

	grubBytes, err := readBoundedExtractedFile(workspace, "grub.cfg", maximumGRUBConfigurationBytes)
	if err != nil {
		return report, err
	}
	grubText := string(grubBytes)
	grubPassed := strings.Contains(grubText, "devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb") &&
		strings.Contains(grubText, "devicetree /sp11/dtb/x1p64100-microsoft-denali.dtb") &&
		strings.Contains(grubText, "modprobe.blacklist=qcom_q6v5_pas") &&
		!strings.Contains(grubText, "sp11_feedback_active_offset2_zero")
	for _, line := range strings.Split(grubText, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "linux /casper/vmlinuz") && !strings.Contains(line, "modprobe.blacklist=qcom_q6v5_pas") {
			grubPassed = false
		}
	}
	grubPassed = grubPassed && !strings.Contains(grubText, "allow aDSP")
	addCheck("surface-grub-menu", grubPassed, "X1E/X1P DTBs and aDSP-safe live entries without the retired SoundWire parameter")
	argumentsErr := validateLiveKernelArguments(grubText)
	argumentsDetails := "every live kernel command explicitly selects Casper and preserves Surface clocks, power domains, and USB safety"
	if argumentsErr != nil {
		argumentsDetails = argumentsErr.Error()
	}
	addCheck("surface-live-kernel-arguments", argumentsErr == nil, argumentsDetails)
	selfLocationPassed := strings.Contains(grubText, "insmod part_gpt") &&
		strings.Contains(grubText, "insmod iso9660") &&
		strings.Contains(grubText, "insmod search") &&
		strings.Contains(grubText, "insmod search_fs_file") &&
		strings.Contains(grubText, "search --no-floppy --file --set=iso_root /casper/vmlinuz") &&
		strings.Contains(grubText, "set root=$iso_root")
	addCheck("grub-iso-self-location", selfLocationPassed, "partition, ISO 9660, and file-search modules select the ISO containing /casper/vmlinuz")
	directDiscoveryPassed := !strings.Contains(grubText, "iso-scan/filename=") && !strings.Contains(grubText, "ignore_uuid")
	addCheck("grub-direct-media-discovery", directDiscoveryPassed, "direct hybrid media relies on paired Casper UUIDs without nested-ISO or identity-bypass arguments")

	directISO, directErr := sameDigest(filepath.Join(workspace, "iso-bootaa64.efi"), filepath.Join(workspace, "iso-grubaa64.efi"))
	addCheck("direct-grub-iso-path", directErr == nil && directISO, "EFI/boot/bootaa64.efi matches grubaa64.efi")
	if systemErr == nil {
		offset, offsetErr := appendedESPOffset(string(systemArea))
		if offsetErr != nil {
			addCheck("direct-grub-appended-esp", false, offsetErr.Error())
		} else {
			spec := fmt.Sprintf("/work/image.iso@@%d", offset)
			copyErr := v.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace,
				"mcopy", "-o", "-i", spec, "::/EFI/BOOT/BOOTAA64.EFI", "/work/esp-bootaa64.efi")
			if copyErr == nil {
				copyErr = validateExtractedRegularFiles(workspace, []string{"esp-bootaa64.efi"})
			}
			same, compareErr := sameDigest(filepath.Join(workspace, "esp-bootaa64.efi"), filepath.Join(workspace, "iso-grubaa64.efi"))
			passed := copyErr == nil && compareErr == nil && same
			details := "appended ESP BOOTAA64.EFI matches direct GRUB"
			if copyErr != nil {
				details = copyErr.Error()
			} else if compareErr != nil {
				details = compareErr.Error()
			}
			addCheck("direct-grub-appended-esp", passed, details)
			listing, listingErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
				"mdir", "-b", "-i", spec, "::/EFI/BOOT")
			noRedundantGRUB := listingErr == nil && !strings.Contains(strings.ToUpper(string(listing)), "GRUBAA64.EFI")
			listingDetails := "redundant GRUBAA64.EFI was removed before installing direct GRUB"
			if listingErr != nil {
				listingDetails = listingErr.Error()
			}
			addCheck("appended-esp-space-reclaimed", noRedundantGRUB, listingDetails)
		}
	}

	report.Valid = !slices.ContainsFunc(report.Checks, func(check imagecontract.ValidationCheck) bool { return !check.Passed })
	if !report.Valid {
		return report, fmt.Errorf("ISO validation failed: %s", failedValidationSummary(report.Checks))
	}
	return report, nil
}

// validateInstalledInitramfsListing proves that Dracut emitted the minimum
// exact boot capabilities rather than merely copying a module-directory name.
func validateInstalledInitramfsListing(listing, longListing, abi string) error {
	members := make(map[string]struct{})
	for _, line := range strings.Split(listing, "\n") {
		member := strings.TrimSuffix(line, "\r")
		if member != "" {
			members[member] = struct{}{}
		}
	}
	require := func(label string, alternatives ...string) error {
		for _, member := range alternatives {
			if _, ok := members[member]; ok {
				return nil
			}
		}
		return fmt.Errorf("installed initramfs is missing required capability %s", label)
	}
	for _, requirement := range []struct {
		label        string
		alternatives []string
	}{
		{label: "init", alternatives: []string{"init"}},
		{label: "shell", alternatives: []string{"usr/bin/sh", "bin/sh", "usr/bin/bash", "bin/bash"}},
		{label: "mount", alternatives: []string{"usr/bin/mount", "bin/mount"}},
		{label: "modprobe", alternatives: []string{"usr/sbin/modprobe", "sbin/modprobe"}},
		{label: "Dracut library", alternatives: []string{"usr/lib/dracut-lib.sh", "lib/dracut-lib.sh"}},
		{label: "Dracut root parser", alternatives: []string{"usr/lib/dracut/hooks/cmdline/00-parse-root.sh", "lib/dracut/hooks/cmdline/00-parse-root.sh"}},
		{label: "exact modules.dep", alternatives: []string{"usr/lib/modules/" + abi + "/modules.dep"}},
	} {
		if err := require(requirement.label, requirement.alternatives...); err != nil {
			return err
		}
	}
	modulePrefix := "usr/lib/modules/" + abi + "/kernel/"
	hasModule := false
	for _, line := range strings.Split(longListing, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "-") {
			continue
		}
		member := fields[len(fields)-1]
		if strings.HasPrefix(member, modulePrefix) && (strings.HasSuffix(member, ".ko") ||
			strings.HasSuffix(member, ".ko.gz") || strings.HasSuffix(member, ".ko.xz") ||
			strings.HasSuffix(member, ".ko.zst")) {
			hasModule = true
			break
		}
	}
	for member := range members {
		if strings.Contains(member, "scripts/casper") {
			return errors.New("installed initramfs unexpectedly contains Casper support")
		}
	}
	if !hasModule {
		return fmt.Errorf("installed initramfs contains no kernel modules for %s", abi)
	}
	return nil
}

// failedValidationSummary retains bounded actionable evidence when image
// creation validates a private partial ISO which cannot be published for a
// later standalone validation pass.
func failedValidationSummary(checks []imagecontract.ValidationCheck) string {
	const maximumSummaryBytes = 2048
	var summary strings.Builder
	for _, check := range checks {
		if check.Passed {
			continue
		}
		name := sanitizedValidationText(check.Name, 96)
		detail := sanitizedValidationText(check.Details, 256)
		item := name
		if detail != "" {
			item += ": " + detail
		}
		separator := ""
		if summary.Len() > 0 {
			separator = "; "
		}
		if summary.Len()+len(separator)+len(item) > maximumSummaryBytes {
			marker := separator + "additional failures omitted"
			if summary.Len()+len(marker) <= maximumSummaryBytes {
				summary.WriteString(marker)
			}
			break
		}
		summary.WriteString(separator)
		summary.WriteString(item)
	}
	if summary.Len() == 0 {
		return "validator returned no failed check details"
	}
	return summary.String()
}

// sanitizedValidationText collapses whitespace, replaces terminal control
// characters and truncates on rune boundaries within one byte budget.
func sanitizedValidationText(value string, maximumBytes int) string {
	compact := strings.Join(strings.Fields(value), " ")
	if maximumBytes <= 0 {
		return ""
	}
	var cleaned strings.Builder
	truncated := false
	contentLimit := maximumBytes
	if len(compact) > maximumBytes && maximumBytes > len("...") {
		contentLimit -= len("...")
		truncated = true
	}
	for _, valueRune := range compact {
		if !unicode.IsPrint(valueRune) {
			valueRune = '?'
		}
		runeBytes := utf8.RuneLen(valueRune)
		if runeBytes < 0 || cleaned.Len()+runeBytes > contentLimit {
			truncated = true
			break
		}
		cleaned.WriteRune(valueRune)
	}
	if truncated && cleaned.Len()+len("...") <= maximumBytes {
		cleaned.WriteString("...")
	}
	return cleaned.String()
}

// validateManifestKernelBundle proves that on-media delivery provenance is
// canonical and every package path matches whether the package is embedded.
func validateManifestKernelBundle(bundle kernel.Bundle) error {
	canonical, err := kernel.NewBundle(kernel.BundleOptions{
		Release: bundle.Release, Repository: bundle.Repository,
		RequestedBootImageMode: bundle.RequestedBootImageMode, EffectiveDTBDelivery: bundle.EffectiveDTBDelivery,
		EmbeddedDTBCount: bundle.EmbeddedDTBCount, DTBSelectionProvenance: bundle.DTBSelectionProvenance,
		Packages: bundle.Packages, DeviceTrees: bundle.DeviceTrees,
	})
	if err != nil || !reflect.DeepEqual(bundle, canonical) {
		return errors.Join(errors.New("manifest kernel bundle delivery contract is invalid or non-canonical"), err)
	}
	for _, pkg := range bundle.Packages {
		expectedPath := ""
		if pkg.Role == kernel.RoleImage || pkg.Role == kernel.RoleModules || pkg.Role == kernel.RoleBootSupport {
			expectedPath = "sp11/kernel/" + pkg.Name
		}
		if pkg.Path != expectedPath {
			return fmt.Errorf("manifest kernel package %s has unexpected media path %q", pkg.Name, pkg.Path)
		}
	}
	return nil
}

// snapshotValidationImage copies and hashes one descriptor-pinned ISO into the
// private validation workspace so every later check observes those same bytes.
func snapshotValidationImage(ctx context.Context, sourcePath, destinationPath string) (digest string, size int64, resultErr error) {
	return snapshotValidationImageAfterInspection(ctx, sourcePath, destinationPath, nil)
}

// snapshotValidationImageAfterInspection exposes one test-only scheduling seam
// while retaining the production descriptor and identity checks.
func snapshotValidationImageAfterInspection(ctx context.Context, sourcePath, destinationPath string, afterInspection func() error) (digest string, size int64, resultErr error) {
	listed, err := os.Lstat(sourcePath)
	if err != nil {
		return "", 0, fmt.Errorf("inspect ISO: %w", err)
	}
	if listed.Mode()&os.ModeSymlink != 0 || !listed.Mode().IsRegular() || listed.Size() <= 0 || listed.Size() > maximumValidationImageBytes {
		return "", 0, fmt.Errorf("ISO path %q is not a bounded non-symbolic-link regular file", sourcePath)
	}
	if afterInspection != nil {
		if err := afterInspection(); err != nil {
			return "", 0, err
		}
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", 0, fmt.Errorf("open ISO snapshot source: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, source.Close()) }()
	opened, err := source.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(listed, opened) ||
		opened.Size() != listed.Size() || opened.Size() <= 0 || opened.Size() > maximumValidationImageBytes {
		return "", 0, errors.Join(errors.New("ISO identity changed while opening its validation snapshot"), err)
	}
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("create private ISO validation snapshot: %w", err)
	}
	keepDestination := false
	defer func() {
		if !keepDestination {
			resultErr = errors.Join(resultErr, os.Remove(destinationPath))
		}
	}()
	hasher := sha256.New()
	buffer := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			_ = destination.Close()
			return "", size, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			size += int64(count)
			if size > opened.Size() || size > maximumValidationImageBytes {
				_ = destination.Close()
				return "", size, errors.New("ISO grew while its validation snapshot was copied")
			}
			written, writeErr := io.MultiWriter(destination, hasher).Write(buffer[:count])
			if writeErr != nil {
				_ = destination.Close()
				return "", size, writeErr
			}
			if written != count {
				_ = destination.Close()
				return "", size, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = destination.Close()
			return "", size, readErr
		}
	}
	afterRead, statErr := source.Stat()
	syncErr := destination.Sync()
	closeErr := destination.Close()
	if err := errors.Join(statErr, syncErr, closeErr); err != nil {
		return "", size, err
	}
	if size != opened.Size() || !afterRead.Mode().IsRegular() || !os.SameFile(opened, afterRead) || afterRead.Size() != opened.Size() {
		return "", size, errors.New("ISO changed while its validation snapshot was copied")
	}
	keepDestination = true
	return fmt.Sprintf("%x", hasher.Sum(nil)), size, nil
}

// readValidationManifest reads one bounded regular manifest from a single
// descriptor and rejects identity or size drift around that read.
func readValidationManifest(path string) (data []byte, resultErr error) {
	return readValidationManifestAfterInspection(path, nil)
}

// readValidationManifestAfterInspection exposes one test-only scheduling seam
// while retaining the production bounded descriptor read.
func readValidationManifestAfterInspection(path string, afterInspection func() error) (data []byte, resultErr error) {
	listed, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if listed.Mode()&os.ModeSymlink != 0 || !listed.Mode().IsRegular() || listed.Size() < 0 || listed.Size() > imagecontract.MaximumManifestSize {
		return nil, errors.New("embedded manifest is not a bounded non-symbolic-link regular file")
	}
	if afterInspection != nil {
		if err := afterInspection(); err != nil {
			return nil, err
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(listed, opened) ||
		opened.Size() != listed.Size() || opened.Size() < 0 || opened.Size() > imagecontract.MaximumManifestSize {
		return nil, errors.Join(errors.New("embedded manifest identity changed while opening"), err)
	}
	data, err = io.ReadAll(io.LimitReader(file, imagecontract.MaximumManifestSize+1))
	if err != nil {
		return nil, err
	}
	afterRead, err := file.Stat()
	if err != nil {
		return nil, err
	}
	current, currentErr := os.Lstat(path)
	if currentErr != nil || len(data) > imagecontract.MaximumManifestSize || int64(len(data)) != opened.Size() ||
		!afterRead.Mode().IsRegular() || !os.SameFile(opened, afterRead) || afterRead.Size() != opened.Size() ||
		current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) || current.Size() != opened.Size() {
		return nil, errors.Join(errors.New("embedded manifest changed while reading"), currentErr)
	}
	return data, nil
}

// validateCompanionBundle checks the single manifest's companion record,
// proves that its reserved ISO directory agrees with the inclusion flag, and
// verifies every extracted artefact when the optional payload is present.
func (v *Validator) validateCompanionBundle(
	ctx context.Context,
	toolsImage string,
	workspace string,
	record imagecontract.CompanionBundleRecord,
	recordErr error,
) []imagecontract.ValidationCheck {
	checks := make([]imagecontract.ValidationCheck, 0, 3)
	addCheck := func(name string, passed bool, details string) {
		checks = append(checks, imagecontract.ValidationCheck{Name: name, Passed: passed, Details: details})
	}
	recordDetails := "single image manifest declares a valid companion bundle"
	if recordErr != nil {
		recordDetails = recordErr.Error()
	}
	addCheck("companion-bundle-record", recordErr == nil, recordDetails)

	listing, presenceErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
		"xorriso", "-indev", "/work/image.iso", "-ls", "/sp11")
	companionPresent := presenceErr == nil && isoDirectoryListingContains(listing, "companion")
	if !record.Included {
		passed := presenceErr == nil && !companionPresent
		details := "reserved companion directory is absent as declared"
		if presenceErr != nil {
			details = fmt.Sprintf("inspect /sp11 directory: %v", presenceErr)
		} else if companionPresent {
			details = "manifest marks the companion absent, but the ISO contains " + companion.ISOFilesystemRoot
		}
		addCheck("companion-bundle-presence", passed, details)
		return checks
	}

	presencePassed := presenceErr == nil && companionPresent
	presenceDetails := "reserved companion directory is present as declared"
	if presenceErr != nil {
		presenceDetails = fmt.Sprintf("inspect /sp11 directory: %v", presenceErr)
	} else if !companionPresent {
		presenceDetails = "manifest includes the companion, but the ISO has no " + companion.ISOFilesystemRoot
	}
	addCheck("companion-bundle-presence", presencePassed, presenceDetails)
	if !presencePassed || recordErr != nil {
		return checks
	}

	extractErr := v.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace,
		"xorriso", "-osirrox", "on", "-indev", "/work/image.iso",
		"-extract", "/"+companion.ISOFilesystemRoot, "/work/companion")
	if extractErr != nil {
		addCheck("companion-bundle-contents", false, fmt.Sprintf("extract companion directory: %v", extractErr))
		return checks
	}
	directoryErr := companion.ValidateDirectory(record, filepath.Join(workspace, "companion"))
	details := fmt.Sprintf("%d declared companion artefacts form an exact, verified directory", len(companion.FlattenArtifacts(record)))
	if directoryErr != nil {
		details = directoryErr.Error()
	}
	addCheck("companion-bundle-contents", directoryErr == nil, details)
	return checks
}

// isoDirectoryListingContains reports whether xorriso's one-entry-per-line
// directory output contains the exact quoted or unquoted child name.
func isoDirectoryListingContains(listing []byte, name string) bool {
	quotedName := "'" + name + "'"
	for _, line := range strings.Split(string(listing), "\n") {
		line = strings.TrimSpace(line)
		if line == name || line == quotedName {
			return true
		}
	}
	return false
}

// validateInstalledGRUBGeneratorSyntax asks the trusted tools image to parse
// the already bounded extracted generator without executing it.
func validateInstalledGRUBGeneratorSyntax(ctx context.Context, docker *platform.Docker, toolsImage, workspace string) error {
	if err := docker.RunInWorkspace(ctx, toolsImage, workspace,
		"sh", "-n", "/work/installed-root/etc/grub.d/10_linux"); err != nil {
		return fmt.Errorf("parse installed GRUB generator with trusted shell: %w", err)
	}
	return nil
}

// validateExtractedRegularFiles rejects any extracted member whose path is
// non-canonical, escapes the private root, traverses a symbolic link, or ends
// in anything other than a regular file.
func validateExtractedRegularFiles(root string, paths []string) error {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect extracted root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return errors.New("extracted root is not a non-symbolic-link directory")
	}
	for _, relative := range paths {
		if relative == "" || path.IsAbs(relative) || path.Clean(relative) != relative || relative == "." || strings.HasPrefix(relative, "../") {
			return fmt.Errorf("extracted path %q is not canonical and relative", relative)
		}
		current := root
		components := strings.Split(relative, "/")
		for index, component := range components {
			if component == "" || component == "." || component == ".." {
				return fmt.Errorf("extracted path %q contains an invalid component", relative)
			}
			current = filepath.Join(current, filepath.FromSlash(component))
			info, err := os.Lstat(current)
			if err != nil {
				return fmt.Errorf("inspect extracted path %q: %w", relative, err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("extracted path %q traverses a symbolic link", relative)
			}
			if index < len(components)-1 {
				if !info.IsDir() {
					return fmt.Errorf("extracted path %q traverses a non-directory", relative)
				}
				continue
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("extracted path %q is not a regular file", relative)
			}
		}
	}
	return nil
}

// readBoundedExtractedFile reads one regular extracted file through an
// os.Root descriptor after rejecting symbolic-link traversal. The descriptor
// identity and size are rechecked so untrusted contents cannot escape their
// private extraction root or produce unbounded validation evidence.
func readBoundedExtractedFile(rootPath, relative string, maximumBytes int64) (data []byte, resultErr error) {
	if maximumBytes < 0 {
		return nil, errors.New("extracted file size bound must not be negative")
	}
	if err := validateExtractedRegularFiles(rootPath, []string{relative}); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open extracted root: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	listed, err := root.Lstat(relative)
	if err != nil || listed.Mode()&os.ModeSymlink != 0 || !listed.Mode().IsRegular() || listed.Size() < 0 || listed.Size() > maximumBytes {
		return nil, errors.Join(fmt.Errorf("extracted path %q is not a bounded regular file", relative), err)
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, fmt.Errorf("open extracted path %q: %w", relative, err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(listed, opened) || opened.Size() != listed.Size() {
		return nil, errors.Join(fmt.Errorf("extracted path %q changed while opening", relative), err)
	}
	data, err = io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read extracted path %q: %w", relative, err)
	}
	afterRead, statErr := file.Stat()
	current, lstatErr := root.Lstat(relative)
	if statErr != nil || lstatErr != nil || int64(len(data)) != opened.Size() || int64(len(data)) > maximumBytes ||
		!afterRead.Mode().IsRegular() || !os.SameFile(opened, afterRead) || afterRead.Size() != opened.Size() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) || current.Size() != opened.Size() {
		return nil, errors.Join(fmt.Errorf("extracted path %q changed while reading", relative), statErr, lstatErr)
	}
	return data, nil
}

// validateInstalledSystemSupport extracts and checks the minimal root assets
// that Ubuntu's installer is expected to copy into the target filesystem.
func (v *Validator) validateInstalledSystemSupport(ctx context.Context, toolsImage, workspace string, manifest imagecontract.Manifest) []imagecontract.ValidationCheck {
	checks := make([]imagecontract.ValidationCheck, 0, 9)
	addCheck := func(name string, passed bool, details string) {
		checks = append(checks, imagecontract.ValidationCheck{Name: name, Passed: passed, Details: details})
	}
	bundle := manifest.KernelBundle
	abi := bundle.ABI
	paths, pathsErr := installedSupportPaths(bundle)
	if pathsErr != nil {
		addCheck("installed-system-support-members", false, pathsErr.Error())
		return checks
	}
	arguments := []string{
		"unsquashfs", "-no-xattrs", "-no-progress", "-d", "/work/installed-root", "/work/minimal.squashfs",
	}
	arguments = append(arguments, paths...)
	if err := v.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace, arguments...); err != nil {
		addCheck("installed-system-support-members", false, err.Error())
		return checks
	}
	root := filepath.Join(workspace, "installed-root")
	if err := validateExtractedRegularFiles(root, paths); err != nil {
		addCheck("installed-system-support-members", false, err.Error())
		return checks
	}
	addCheck("installed-system-support-members", true, "delivery-specific dpkg records, initramfs, GRUB generation support, and boot support are present")

	listingBytes, listingErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
		"unsquashfs", "-ll", "/work/minimal.squashfs")
	listing := string(listingBytes)
	retiredAbsent := listingErr == nil
	for _, retired := range retiredInstalledSupportPaths {
		retiredAbsent = retiredAbsent && !squashFSListingContainsPath(listing, retired)
	}
	retiredDetails := "retired SP11-specific refresh helper, hooks, seed DTBs, and GRUB generator are absent"
	if listingErr != nil {
		retiredDetails = listingErr.Error()
	}
	addCheck("installed-system-retired-support-absent", retiredAbsent, retiredDetails)
	transientAbsent := listingErr == nil && !strings.Contains(listing, "/boot/.initrd.img-")
	transientDetails := "no Lexr initramfs staging or inspection files remain in the deployable root"
	if listingErr != nil {
		transientDetails = listingErr.Error()
	}
	addCheck("installed-system-transient-state-absent", transientAbsent, transientDetails)

	statusBytes, statusErr := readBoundedExtractedFile(root, "var/lib/dpkg/status", maximumPackageStatusBytes)
	packagesInstalled := statusErr == nil
	for _, expected := range installedPackageExpectations(bundle) {
		packagesInstalled = packagesInstalled && installedPackageStatus(
			string(statusBytes), expected.name, expected.version, expected.architecture)
		if info, err := os.Stat(filepath.Join(root, "var/lib/dpkg/info", expected.name+".list")); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			packagesInstalled = false
		}
	}
	if bundle.EffectiveDTBDelivery == kernel.DTBDeliveryEmbedded {
		packagesInstalled = packagesInstalled && !installedPackagePresent(string(statusBytes), "lexr-kernel-boot-support")
	}
	packageDetails := "the exact delivery-specific package set is registered as installed"
	if statusErr != nil {
		packageDetails = statusErr.Error()
	}
	addCheck("installed-system-kernel-packages", packagesInstalled, packageDetails)
	installedKernelDigest, installedKernelErr := artifact.HashFile(filepath.Join(root, "boot", "vmlinuz-"+abi))
	installedKernelPassed := installedKernelErr == nil &&
		manifest.BootArtifacts.Kernel.SHA256 != "" && installedKernelDigest == manifest.BootArtifacts.Kernel.SHA256
	installedKernelDetails := fmt.Sprintf("expected=%s actual=%s", manifest.BootArtifacts.Kernel.SHA256, installedKernelDigest)
	if installedKernelErr != nil {
		installedKernelDetails = installedKernelErr.Error()
	}
	addCheck("installed-system-kernel-digest", installedKernelPassed, installedKernelDetails)

	installedInitrd := filepath.Join(root, "boot", "initrd.img-"+abi)
	initrdListing, initrdErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
		"lsinitramfs", "/work/installed-root/boot/initrd.img-"+abi)
	initrdLongListing, initrdLongErr := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace,
		"lsinitramfs", "-l", "/work/installed-root/boot/initrd.img-"+abi)
	initrdText := string(initrdListing)
	initrdContractErr := validateInstalledInitramfsListing(initrdText, string(initrdLongListing), abi)
	installedInitrdPassed := initrdErr == nil && initrdLongErr == nil && initrdContractErr == nil
	initrdDetails := "complete non-Casper Dracut initramfs contains modules for " + abi
	if initrdErr != nil {
		initrdDetails = initrdErr.Error()
	} else if initrdLongErr != nil {
		initrdDetails = initrdLongErr.Error()
	} else if initrdContractErr != nil {
		initrdDetails = initrdContractErr.Error()
	} else if info, err := os.Stat(installedInitrd); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		installedInitrdPassed = false
		if err != nil {
			initrdDetails = err.Error()
		} else {
			initrdDetails = "installed initramfs is empty"
		}
	}
	addCheck("installed-system-initramfs", installedInitrdPassed, initrdDetails)

	dtbPassed := listingErr == nil
	dtbDetails := "the installed kernel retains its manifest-bound embedded DTB inventory without external support"
	var dtbValidationErr error
	if listingErr != nil {
		dtbValidationErr = fmt.Errorf("inspect installed filesystem inventory: %w", listingErr)
	}
	if bundle.EffectiveDTBDelivery == kernel.DTBDeliveryExternalRequired {
		profile, tree, profileErr := requiredExternalProfile(bundle)
		dtbPassed = dtbPassed && profileErr == nil
		if profileErr != nil && dtbValidationErr == nil {
			dtbValidationErr = profileErr
		}
		relative, ok := tree.FirmwareRelativePath(abi)
		dtbPassed = dtbPassed && ok
		if !ok && dtbValidationErr == nil {
			dtbValidationErr = fmt.Errorf("device tree %s is outside the exact-ABI firmware directory", tree.Device)
		}
		if ok {
			for _, candidatePath := range []string{
				filepath.Join(root, "boot", "dtbs", abi, filepath.FromSlash(relative)),
				filepath.Join(root, "boot", "dtb-"+abi),
			} {
				actual, hashErr := artifact.HashFile(candidatePath)
				dtbPassed = dtbPassed && hashErr == nil && actual == tree.SHA256
				if hashErr != nil && dtbValidationErr == nil {
					dtbValidationErr = fmt.Errorf("hash installed device tree %s: %w", candidatePath, hashErr)
				} else if hashErr == nil && actual != tree.SHA256 && dtbValidationErr == nil {
					dtbValidationErr = fmt.Errorf("installed device-tree %s digest %s does not match %s", candidatePath, actual, tree.SHA256)
				}
			}
			for _, stateRoot := range []string{
				path.Join("usr/lib/lexr/kernel-platforms", profile),
				path.Join("var/lib/lexr/kernel-boot", abi, profile),
			} {
				pathState := path.Join(stateRoot, "dtb-path")
				digestState := path.Join(stateRoot, "dtb-sha256")
				pathBytes, pathErr := readBoundedExtractedFile(root, pathState, maximumValidationTextBytes)
				digestBytes, digestErr := readBoundedExtractedFile(root, digestState, maximumValidationTextBytes)
				dtbPassed = dtbPassed && pathErr == nil && digestErr == nil &&
					strings.TrimSpace(string(pathBytes)) == relative && strings.TrimSpace(string(digestBytes)) == tree.SHA256
				if pathErr != nil && dtbValidationErr == nil {
					dtbValidationErr = fmt.Errorf("read installed device-tree path state %s: %w", pathState, pathErr)
				} else if digestErr != nil && dtbValidationErr == nil {
					dtbValidationErr = fmt.Errorf("read installed device-tree digest state %s: %w", digestState, digestErr)
				} else if pathErr == nil && digestErr == nil &&
					(strings.TrimSpace(string(pathBytes)) != relative || strings.TrimSpace(string(digestBytes)) != tree.SHA256) && dtbValidationErr == nil {
					dtbValidationErr = errors.New("installed device-tree package state does not match the selected manifest profile")
				}
			}
		}
		dtbDetails = "the selected profile has digest-bound canonical and stock-GRUB exact-ABI DTBs with package-owned state"
	} else {
		dtbPassed = dtbPassed && bundle.EffectiveDTBDelivery == kernel.DTBDeliveryEmbedded && bundle.EmbeddedDTBCount > 0 &&
			!squashFSListingContainsPath(listing, "boot/dtbs/"+abi) &&
			!squashFSListingContainsPath(listing, "boot/dtb-"+abi)
		for _, tree := range bundle.DeviceTrees {
			if tree.Required {
				dtbPassed = dtbPassed && tree.EmbeddedMatches == 1
			}
		}
	}
	if dtbValidationErr != nil {
		dtbDetails = dtbValidationErr.Error()
	}
	addCheck("installed-system-device-trees", dtbPassed, dtbDetails)

	grubDefaults, defaultsErr := readBoundedExtractedFile(root, "etc/default/grub.d/99-surface-pro-11.cfg", maximumValidationTextBytes)
	generatorErr := validateInstalledGRUBGenerator(filepath.Join(root, "etc/grub.d/10_linux"))
	if generatorErr == nil {
		generatorErr = validateInstalledGRUBGeneratorSyntax(ctx, v.Docker, toolsImage, workspace)
	}
	installedGrubText := string(grubDefaults)
	grubSupportPassed := defaultsErr == nil && generatorErr == nil
	for _, required := range []string{
		"clk_ignore_unused",
		"pd_ignore_unused",
		"arm64.nopauth",
		"systemd.tpm2_wait=0",
	} {
		grubSupportPassed = grubSupportPassed && strings.Contains(installedGrubText, required)
	}
	grubSupportPassed = grubSupportPassed && !strings.Contains(installedGrubText, "qcom_q6v5_pas")
	grubSupportPassed = grubSupportPassed && !strings.Contains(installedGrubText, "sp11_feedback_active_offset2_zero")
	grubDetails := "the installed root is ready for target-side GRUB generation after Ubuntu mounts the destination disk"
	if bundle.EffectiveDTBDelivery == kernel.DTBDeliveryExternalRequired {
		grubDetails = "stock 10_linux prefers /dtb-<abi>; the installer must generate and verify concrete entries on the mounted target"
	}
	if defaultsErr != nil {
		grubDetails = defaultsErr.Error()
	} else if generatorErr != nil {
		grubDetails = generatorErr.Error()
	}
	addCheck("installed-system-grub-support", grubSupportPassed, grubDetails)

	refreshPassed := listingErr == nil
	refreshDetails := "embedded delivery has no external boot-support package or lifecycle files"
	var refreshValidationErr error
	if listingErr != nil {
		refreshValidationErr = fmt.Errorf("inspect installed filesystem inventory: %w", listingErr)
	}
	if bundle.EffectiveDTBDelivery == kernel.DTBDeliveryExternalRequired {
		refreshPath := "usr/libexec/lexr/kernel-boot-refresh"
		postInstallPath := "etc/kernel/postinst.d/05-lexr-kernel-boot"
		postRemovePath := "etc/kernel/postrm.d/05-lexr-kernel-boot"
		refresh, refreshErr := readBoundedExtractedFile(root, refreshPath, maximumGRUBConfigurationBytes)
		postInstall, postInstallErr := readBoundedExtractedFile(root, postInstallPath, maximumValidationTextBytes)
		postRemove, postRemoveErr := readBoundedExtractedFile(root, postRemovePath, maximumValidationTextBytes)
		refreshPassed = refreshPassed && refreshErr == nil && postInstallErr == nil && postRemoveErr == nil &&
			strings.Contains(string(refresh), `--defer-grub`) &&
			strings.Contains(string(refresh), `"$target_root/boot/dtbs/$abi/$dtb_path"`) &&
			strings.Contains(string(refresh), `compatibility="$target_root/boot/dtb-$abi"`) &&
			strings.Contains(string(refresh), `ensure_single_profile`) &&
			strings.Contains(string(postInstall), "/usr/libexec/lexr/kernel-boot-refresh refresh") &&
			strings.Contains(string(postRemove), "/usr/libexec/lexr/kernel-boot-refresh remove") &&
			lifecycleAvoidsDevicePolicy(string(refresh), abi, string(postInstall), string(postRemove))
		refreshDetails = "the installed generic package owns exact-ABI selection, refresh, removal, and stock-GRUB compatibility state"
		for _, candidate := range []struct {
			path string
			err  error
		}{
			{path: refreshPath, err: refreshErr},
			{path: postInstallPath, err: postInstallErr},
			{path: postRemovePath, err: postRemoveErr},
		} {
			if candidate.err != nil && refreshValidationErr == nil {
				refreshValidationErr = fmt.Errorf("read installed lifecycle file %s: %w", candidate.path, candidate.err)
			}
		}
		if !refreshPassed && refreshValidationErr == nil {
			refreshValidationErr = errors.New("installed kernel boot-support lifecycle does not match the generic package contract")
		}
	} else {
		refreshPassed = refreshPassed && !installedPackagePresent(string(statusBytes), "lexr-kernel-boot-support")
		for _, path := range genericInstalledSupportPaths {
			refreshPassed = refreshPassed && !squashFSListingContainsPath(listing, path)
		}
		if !refreshPassed && refreshValidationErr == nil {
			refreshValidationErr = errors.New("embedded delivery retains external boot-support package state")
		}
	}
	if refreshValidationErr != nil {
		refreshDetails = refreshValidationErr.Error()
	}
	addCheck("installed-system-kernel-refresh", refreshPassed, refreshDetails)
	return checks
}

// lifecycleAvoidsDevicePolicy permits only ABI-bound hooks to name their exact
// device-scoped ABI. The generic refresh helper and every other hook token must
// remain free of independent SP11 or Denali policy.
func lifecycleAvoidsDevicePolicy(refresh, abi string, abiBoundHooks ...string) bool {
	if abi == "" {
		return false
	}
	containsDevicePolicy := func(value string) bool {
		lower := strings.ToLower(value)
		return strings.Contains(lower, "sp11") || strings.Contains(lower, "denali")
	}
	if containsDevicePolicy(refresh) {
		return false
	}
	lowerABI := strings.ToLower(abi)
	for _, hook := range abiBoundHooks {
		normalized := strings.ReplaceAll(strings.ToLower(hook), lowerABI, "<exact-abi>")
		if containsDevicePolicy(normalized) {
			return false
		}
	}
	return true
}

// appendedESPOffset parses xorriso's GPT report and returns the byte offset of
// the appended EFI system partition, rejecting absent, empty, or overflowing data.
func appendedESPOffset(report string) (int64, error) {
	pattern := regexp.MustCompile(`GPT start and size\s*:\s*2\s+(\d+)\s+(\d+)`)
	matches := pattern.FindStringSubmatch(report)
	if matches == nil {
		return 0, errors.New("no appended ESP partition found")
	}
	start, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse appended ESP start sector: %w", err)
	}
	size, err := strconv.ParseInt(matches[2], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse appended ESP size: %w", err)
	}
	if size <= 0 {
		return 0, errors.New("appended ESP has no sectors")
	}
	if start > (1<<63-1)/512 {
		return 0, errors.New("appended ESP byte offset overflows int64")
	}
	return start * 512, nil
}

// sameDigest reports whether two regular artefacts have identical SHA-256
// content without relying on their filenames or metadata.
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

// findArtifact returns the first manifest record whose portable path ends with
// suffix, or a zero record when the required artefact is absent.
func findArtifact(records []imagecontract.ArtifactRecord, suffix string) imagecontract.ArtifactRecord {
	for _, record := range records {
		if strings.HasSuffix(record.Path, suffix) {
			return record
		}
	}
	return imagecontract.ArtifactRecord{}
}

// firstMatchingLine extracts a concise diagnostic line containing prefix and
// falls back to the trimmed full value when the report uses an unknown format.
func firstMatchingLine(value, prefix string) string {
	for _, line := range strings.Split(value, "\n") {
		if strings.Contains(line, prefix) {
			return strings.TrimSpace(line)
		}
	}
	return strings.TrimSpace(value)
}
