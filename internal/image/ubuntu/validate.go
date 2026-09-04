package ubuntu

import (
	"bytes"
	"context"
	"crypto/sha256"
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

	"github.com/ooaklee/lexr.sh/internal/artifact"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/image/ubuntu/caspermedia"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	kernelinstall "github.com/ooaklee/lexr.sh/internal/kernel/install"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// maximumValidationImageBytes bounds one private ISO validation snapshot.
const maximumValidationImageBytes int64 = 64 << 30

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
func (v *Validator) Validate(ctx context.Context, isoPath string) (imagecontract.ValidationReport, error) {
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
	defer os.RemoveAll(workspace)
	digest, size, err := snapshotValidationImage(ctx, absolute, filepath.Join(workspace, "image.iso"))
	if err != nil {
		return imagecontract.ValidationReport{Path: absolute, Layout: "hybrid-iso", Adapter: AdapterID}, err
	}
	report := imagecontract.ValidationReport{
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

	extractErr := v.Docker.RunInWorkspace(ctx, toolsImage, workspace,
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
	markerBytes, markerErr := os.ReadFile(markerPath)
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
	unpackErr := v.Docker.RunInWorkspace(ctx, toolsImage, workspace,
		"unmkinitramfs", "/work/initrd", "/work/initrd-unpacked")
	initrdIdentityPath := filepath.Join(unpackedInitrd, "main", filepath.FromSlash(caspermedia.InitramfsIdentityPath))
	initrdIdentity, identityReadErr := os.ReadFile(initrdIdentityPath)
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

	defaultBoot, defaultBootErr := os.ReadFile(filepath.Join(unpackedInitrd, "main", "conf", "conf.d", "default-boot-to-casper.conf"))
	defaultLayer, defaultLayerErr := os.ReadFile(filepath.Join(unpackedInitrd, "main", "conf", "conf.d", "default-layer.conf"))
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
	moduleErr := v.Docker.RunInWorkspace(ctx, toolsImage, workspace,
		"unsquashfs", "-no-xattrs", "-no-progress", "-d", "/work/module-probe", "/work/minimal.squashfs", moduleProbe)
	_, statErr := os.Stat(filepath.Join(workspace, "module-probe", filepath.FromSlash(moduleProbe)))
	addCheck("live-root-kernel-modules", moduleErr == nil && statErr == nil, moduleProbe)
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

	grubBytes, err := os.ReadFile(filepath.Join(workspace, "grub.cfg"))
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
			copyErr := v.Docker.RunInWorkspace(ctx, toolsImage, workspace,
				"mcopy", "-o", "-i", spec, "::/EFI/BOOT/BOOTAA64.EFI", "/work/esp-bootaa64.efi")
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
		return report, errors.New("ISO validation failed")
	}
	return report, nil
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

	extractErr := v.Docker.RunInWorkspace(ctx, toolsImage, workspace,
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
	if err := v.Docker.RunInWorkspace(ctx, toolsImage, workspace, arguments...); err != nil {
		addCheck("installed-system-support-members", false, err.Error())
		return checks
	}
	addCheck("installed-system-support-members", true, "delivery-specific dpkg records, initramfs, GRUB configuration, and boot support are present")

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

	root := filepath.Join(workspace, "installed-root")
	statusBytes, statusErr := os.ReadFile(filepath.Join(root, "var/lib/dpkg/status"))
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
	initrdText := string(initrdListing)
	installedInitrdPassed := initrdErr == nil &&
		strings.Contains(initrdText, "usr/lib/modules/"+abi+"/") &&
		!strings.Contains(initrdText, "scripts/casper")
	initrdDetails := "non-Casper initramfs contains modules for " + abi
	if initrdErr != nil {
		initrdDetails = initrdErr.Error()
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
	if bundle.EffectiveDTBDelivery == kernel.DTBDeliveryExternalRequired {
		profile, tree, profileErr := requiredExternalProfile(bundle)
		dtbPassed = dtbPassed && profileErr == nil
		relative, ok := tree.FirmwareRelativePath(abi)
		dtbPassed = dtbPassed && ok
		if ok {
			for _, path := range []string{
				filepath.Join(root, "boot", "dtbs", abi, filepath.FromSlash(relative)),
				filepath.Join(root, "boot", "dtb-"+abi),
			} {
				actual, hashErr := artifact.HashFile(path)
				dtbPassed = dtbPassed && hashErr == nil && actual == tree.SHA256
			}
			for _, stateRoot := range []string{
				filepath.Join(root, "usr", "lib", "lexr", "kernel-platforms", profile),
				filepath.Join(root, "var", "lib", "lexr", "kernel-boot", abi, profile),
			} {
				pathBytes, pathErr := os.ReadFile(filepath.Join(stateRoot, "dtb-path"))
				digestBytes, digestErr := os.ReadFile(filepath.Join(stateRoot, "dtb-sha256"))
				dtbPassed = dtbPassed && pathErr == nil && digestErr == nil &&
					strings.TrimSpace(string(pathBytes)) == relative && strings.TrimSpace(string(digestBytes)) == tree.SHA256
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
	addCheck("installed-system-device-trees", dtbPassed, dtbDetails)

	grubDefaults, defaultsErr := os.ReadFile(filepath.Join(root, "etc/default/grub.d/99-surface-pro-11.cfg"))
	grubConfig, configErr := os.ReadFile(filepath.Join(root, "boot/grub/grub.cfg"))
	grubEntries, entriesErr := kernelinstall.InspectGRUB(ctx, root)
	installedGrubText := string(grubDefaults) + string(grubConfig)
	grubSupportPassed := defaultsErr == nil && configErr == nil && entriesErr == nil &&
		strings.Contains(string(grubConfig), "vmlinuz-"+abi) && strings.Contains(string(grubConfig), "initrd.img-"+abi)
	if entriesErr == nil {
		entriesErr = validateInstalledGRUBEntries(ctx, root, bundle, grubEntries)
		grubSupportPassed = grubSupportPassed && entriesErr == nil
	}
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
	grubDetails := "embedded delivery has exact-ABI kernel and initramfs entries without an external DTB binding"
	if bundle.EffectiveDTBDelivery == kernel.DTBDeliveryExternalRequired {
		grubSupportPassed = grubSupportPassed && strings.Contains(string(grubConfig), "dtb-"+abi)
		grubDetails = "every stock exact-ABI entry binds the selected /dtb-<abi> copy without the live USB blacklist"
	} else {
		grubSupportPassed = grubSupportPassed && !strings.Contains(string(grubConfig), "dtbs/"+abi+"/") &&
			!strings.Contains(string(grubConfig), "dtb-"+abi)
	}
	if entriesErr != nil {
		grubDetails = entriesErr.Error()
	}
	addCheck("installed-system-grub-support", grubSupportPassed, grubDetails)

	refreshPassed := listingErr == nil
	refreshDetails := "embedded delivery has no external boot-support package or lifecycle files"
	if bundle.EffectiveDTBDelivery == kernel.DTBDeliveryExternalRequired {
		refresh, refreshErr := os.ReadFile(filepath.Join(root, "usr/libexec/lexr/kernel-boot-refresh"))
		postInstall, postInstallErr := os.ReadFile(filepath.Join(root, "etc/kernel/postinst.d/05-lexr-kernel-boot"))
		postRemove, postRemoveErr := os.ReadFile(filepath.Join(root, "etc/kernel/postrm.d/05-lexr-kernel-boot"))
		lifecycle := string(refresh) + string(postInstall) + string(postRemove)
		refreshPassed = refreshPassed && refreshErr == nil && postInstallErr == nil && postRemoveErr == nil &&
			strings.Contains(string(refresh), `--defer-grub`) &&
			strings.Contains(string(refresh), `"$target_root/boot/dtbs/$abi/$dtb_path"`) &&
			strings.Contains(string(refresh), `compatibility="$target_root/boot/dtb-$abi"`) &&
			strings.Contains(string(refresh), `ensure_single_profile`) &&
			strings.Contains(string(postInstall), "/usr/libexec/lexr/kernel-boot-refresh refresh") &&
			strings.Contains(string(postRemove), "/usr/libexec/lexr/kernel-boot-refresh remove") &&
			!strings.Contains(strings.ToLower(lifecycle), "sp11") && !strings.Contains(strings.ToLower(lifecycle), "denali")
		refreshDetails = "the installed generic package owns exact-ABI selection, refresh, removal, and stock-GRUB compatibility state"
	} else {
		refreshPassed = refreshPassed && !installedPackagePresent(string(statusBytes), "lexr-kernel-boot-support")
		for _, path := range genericInstalledSupportPaths {
			refreshPassed = refreshPassed && !squashFSListingContainsPath(listing, path)
		}
	}
	addCheck("installed-system-kernel-refresh", refreshPassed, refreshDetails)
	return checks
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
