package debianlive

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
	"strings"
	"time"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/image/sp11"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// Validator independently reads a private ISO snapshot with isolated Linux
// tools. Structural validation never asserts physical hardware qualification.
type Validator struct{ Docker *platform.Docker }

// NewValidator supplies the standard Docker runner when none is injected.
func NewValidator(docker *platform.Docker) *Validator {
	if docker == nil {
		docker = platform.NewDocker(nil)
	}
	return &Validator{Docker: docker}
}

// Validate binds all evidence to one complete ISO and checks both boot paths,
// live-boot discovery, installed-system payload and companion bytes.
func (v *Validator) Validate(ctx context.Context, isoPath string) (report imagecontract.ValidationReport, resultErr error) {
	absolute, err := filepath.Abs(isoPath)
	if err != nil {
		return report, err
	}
	report = imagecontract.ValidationReport{Path: absolute, Layout: "hybrid-iso", Adapter: AdapterID}
	workspace, err := os.MkdirTemp(filepath.Dir(absolute), ".lexr-debian-validate-")
	if err != nil {
		return report, err
	}
	defer func() {
		_ = writableDirectories(workspace)
		resultErr = errors.Join(resultErr, os.RemoveAll(workspace))
	}()
	report.SHA256, report.Size, err = imagecontract.SnapshotFile(ctx, absolute, filepath.Join(workspace, "image.iso"), maximumISOBytes, nil)
	if err != nil {
		return report, err
	}
	if err := v.Docker.Check(ctx); err != nil {
		return report, err
	}
	toolsImage, err := v.Docker.EnsureToolsImage(ctx)
	if err != nil {
		return report, err
	}
	check := func(name string, err error, detail string) error {
		if err != nil {
			detail = err.Error()
		}
		report.Checks = append(report.Checks, imagecontract.ValidationCheck{Name: name, Passed: err == nil, Details: detail})
		if err != nil {
			return fmt.Errorf("Debian validation %s: %w", name, err)
		}
		return nil
	}
	extract := func(arguments ...string) error {
		return v.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace,
			append([]string{"xorriso", "-osirrox", "on", "-indev", "/work/image.iso"}, arguments...)...)
	}
	if err := extract("-extract", "/sp11", "/work/sp11", "-extract", "/boot/grub", "/work/grub", "-extract", "/.disk", "/work/disk", "-extract", "/EFI/boot/bootaa64.efi", "/work/bootaa64.efi", "-extract", "/EFI/boot/grubaa64.efi", "/work/alternate-grubaa64.efi", "-extract", "/efi.img", "/work/alternate-esp.img", "-extract", "/md5sum.txt", "/work/md5sum.txt"); err != nil {
		return report, check("required-media-members", err, "")
	}
	encoded, err := imagecontract.ReadBoundedExtractedFile(workspace, "sp11/lexr-manifest.json", imagecontract.MaximumManifestSize)
	if err != nil {
		return report, check("embedded-manifest", err, "")
	}
	identity := imagecontract.IdentifyBytes(encoded)
	report.ManifestSHA256, report.ManifestSize = identity.SHA256, identity.Size
	manifest, err := imagecontract.DecodeManifest(bytes.NewReader(encoded))
	if err != nil {
		return report, check("embedded-manifest", err, "")
	}
	layout, contract, records, err := validateManifest(manifest)
	if err := check("embedded-manifest", err, "canonical Debian kernel, boot and media-discovery contract"); err != nil {
		return report, err
	}
	report.KernelABI = manifest.KernelBundle.ABI
	for _, tree := range manifest.KernelBundle.DeviceTrees {
		report.DeviceTrees = append(report.DeviceTrees, tree.Device)
	}
	if err := extract("-extract", "/"+layout.liveDirectory, "/work/live"); err != nil {
		return report, check("live-directory", err, "")
	}
	mapped := func(member string) string {
		switch {
		case strings.HasPrefix(member, layout.liveDirectory+"/"):
			return "live/" + strings.TrimPrefix(member, layout.liveDirectory+"/")
		case strings.HasPrefix(member, ".disk/"):
			return "disk/" + strings.TrimPrefix(member, ".disk/")
		case member == "boot/grub/efi.img":
			return "grub/efi.img"
		case member == "EFI/boot/bootaa64.efi":
			return "bootaa64.efi"
		default:
			return member
		}
	}
	for _, record := range records {
		if err := verifyRecord(workspace, mapped(record.Path), record); err != nil {
			return report, check("embedded-artifacts", err, "")
		}
	}
	for _, pair := range [][2]string{{"alternate-esp.img", "grub/efi.img"}, {"alternate-grubaa64.efi", "bootaa64.efi"}} {
		expected, err := recordFile(filepath.Join(workspace, pair[1]), pair[1])
		if err != nil {
			return report, err
		}
		if err := verifyRecord(workspace, pair[0], expected); err != nil {
			return report, check("alternate-boot-copies", err, "")
		}
	}
	check("embedded-artifacts", nil, "all kernel packages, boot artifacts and identity records match their complete SHA-256 and size")
	config, err := imagecontract.ReadBoundedExtractedFile(workspace, "grub/grub.cfg", maximumSourceConfigBytes)
	if err != nil {
		return report, check("live-grub", err, "")
	}
	if err := check("live-grub", validateGRUBConfig(config, layout, report.KernelABI), "every entry pairs the exact kernel, live directory and device tree"); err != nil {
		return report, err
	}
	for _, member := range []string{"grub/arm64-efi/grub.cfg", "grub/loopback.cfg"} {
		data, err := imagecontract.ReadBoundedExtractedFile(workspace, member, maximumSourceConfigBytes)
		if err != nil || string(data) != forwardingGRUBConfig {
			return report, check("grub-bootstrap", errors.New("alternate Debian GRUB route differs"), "")
		}
	}
	grubDigest, err := artifact.HashFile(filepath.Join(workspace, "bootaa64.efi"))
	if err != nil || grubDigest != inspectedGRUBSHA256 {
		return report, check("grub-bootstrap", errors.New("unrecognised embedded GRUB bootstrap"), "")
	}
	check("grub-bootstrap", nil, "inspected ARM64 bootstrap loads the validated ISO menu")
	info, err := imagecontract.ReadBoundedExtractedFile(workspace, "disk/info", maximumSourceConfigBytes)
	if err != nil {
		return report, check("installer-product", err, "")
	}
	if string(info) != inspectedDiskInfo {
		return report, check("installer-product", errors.New("Debian installer identity changed"), "")
	}
	check("installer-product", nil, "inspected Debian ARM64 GNOME live snapshot preserved")
	if err := check("optical-and-usb-efi", v.validateEFI(ctx, toolsImage, workspace, report.Size), "GPT ESP and El Torito select the same verified direct ARM64 GRUB"); err != nil {
		return report, err
	}
	if err := check("companion-bundle", companion.ValidateDirectory(manifest.CompanionBundle, filepath.Join(workspace, "sp11/companion")), "companion inventory, source and licences agree"); err != nil {
		return report, err
	}
	replacements := map[string]string{
		"live/filesystem.squashfs": "live/filesystem.squashfs", "live/filesystem.packages": "live/filesystem.packages", "live/filesystem.size": "live/filesystem.size",
		"live/vmlinuz": "live/vmlinuz", "live/initrd.img": "live/initrd.img", ".disk/live-uuid": "disk/live-uuid",
		"boot/grub/grub.cfg": "grub/grub.cfg", "boot/grub/arm64-efi/grub.cfg": "grub/arm64-efi/grub.cfg", "boot/grub/loopback.cfg": "grub/loopback.cfg",
		"boot/grub/efi.img": "grub/efi.img", "EFI/boot/bootaa64.efi": "bootaa64.efi", "efi.img": "alternate-esp.img",
	}
	if err := filepath.WalkDir(filepath.Join(workspace, "sp11"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(workspace, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		replacements[relative] = relative
		return nil
	}); err != nil {
		return report, err
	}
	if err := check("media-checksums", validateMediaChecksums(workspace, replacements), "every replaced member matches the on-media integrity inventory"); err != nil {
		return report, err
	}
	if err := check("supplemental-gpu-firmware", sp11.ValidateGPUFirmwareDirectory(workspace, "sp11/firmware"), "pinned GPU firmware and complete upstream licence evidence agree"); err != nil {
		return report, err
	}
	volume, err := v.Docker.CreateWorkVolume(ctx)
	if err != nil {
		return report, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := v.Docker.RemoveWorkVolume(cleanup, volume); err != nil {
			resultErr = errors.Join(resultErr, err)
			report.Valid = false
		}
	}()
	if err := check("kernel-and-installer-root", v.validateRoot(ctx, toolsImage, workspace, volume, manifest, contract), "live/installed kernel, initramfs, modules, DTBs, firmware and installer support agree"); err != nil {
		return report, err
	}
	if err := check("installed-companion-and-recovery", v.validateRetainedMedia(ctx, toolsImage, workspace, volume, manifest), "selected installed DTB, native GRUB recovery generator and offline companion are retained"); err != nil {
		return report, err
	}
	report.Valid = true
	return report, nil
}

// validateManifest interprets only bounded portable paths from canonical
// provenance; no manifest string is evaluated as a command or GRUB program.
func validateManifest(manifest imagecontract.Manifest) (sourceLayout, mediaContract, []imagecontract.ArtifactRecord, error) {
	var layout sourceLayout
	var empty mediaContract
	if manifest.SchemaVersion != imagecontract.ManifestSchemaVersion || manifest.Adapter != AdapterID || manifest.Layout != "hybrid-iso" || manifest.SecureBoot != secureBootPolicy || manifest.ToolVersion == "" || manifest.CreatedAt.IsZero() {
		return layout, empty, nil, errors.New("unsupported or incomplete Debian manifest identity")
	}
	if !kernelABIPattern.MatchString(manifest.KernelBundle.ABI) || manifest.KernelBundle.EffectiveDTBDelivery != kernel.DTBDeliveryExternalRequired {
		return layout, empty, nil, errors.New("Debian requires a portable exact ABI and external-required DTBs")
	}
	if err := sp11.ValidateManifestBundle(manifest.KernelBundle); err != nil {
		return layout, empty, nil, err
	}
	if err := imagecontract.ValidateArtifactRecord(manifest.SourceImage); err != nil {
		return layout, empty, nil, err
	}
	if manifest.SourceImage.SHA256 != inspectedSourceISOHash || manifest.SourceImage.Size != inspectedSourceISOSize {
		return layout, empty, nil, errors.New("unrecognised Debian source snapshot")
	}
	if err := companion.ValidateRecord(manifest.CompanionBundle); err != nil {
		return layout, empty, nil, err
	}
	media := manifest.MediaDiscovery
	liveBoot := media
	liveBoot.Evidence = nil
	seen := make(map[string]bool)
	records := []imagecontract.ArtifactRecord{manifest.BootArtifacts.Kernel, manifest.BootArtifacts.Initrd}
	for _, evidence := range media.Evidence {
		if seen[evidence.Role] {
			return layout, empty, nil, errors.New("duplicate Debian media-discovery role")
		}
		seen[evidence.Role] = true
		switch evidence.Role {
		case mediumIdentityRole, initramfsIdentityRole, bootArgumentsRole:
			liveBoot.Evidence = append(liveBoot.Evidence, evidence)
		case "live-directory":
			if evidence.Scope != "iso-filesystem" || evidence.Path != liveMediaPath || evidence.Value != "/"+evidence.Path || evidence.Artifact != nil {
				return layout, empty, nil, errors.New("invalid Debian live-directory evidence")
			}
			layout.liveDirectory = evidence.Path
		case "efi-bootloader", "efi-system-partition", "installer-product", "installed-grub-package":
			expected := map[string]string{"efi-bootloader": "EFI/boot/bootaa64.efi", "efi-system-partition": "boot/grub/efi.img", "installer-product": ".disk/info", "installed-grub-package": grubSupportDirectory + "/" + grubSupportDebName}[evidence.Role]
			if evidence.Scope != "iso-filesystem" || evidence.Path != expected || evidence.Value != "" || evidence.Artifact == nil || evidence.Artifact.Path != expected {
				return layout, empty, nil, errors.New("invalid Debian EFI or installer evidence")
			}
			records = append(records, *evidence.Artifact)
		default:
			return layout, empty, nil, errors.New("unrecognised Debian media-discovery role")
		}
	}
	if len(seen) != 8 || layout.liveDirectory == "" {
		return layout, empty, nil, errors.New("incomplete Debian media-discovery evidence")
	}
	contract, marker, err := fromDiscoveryRecord(liveBoot)
	if err != nil {
		return layout, empty, nil, err
	}
	records = append(records, marker)
	layout.kernel, layout.initrd = layout.member("vmlinuz"), layout.member("initrd.img")
	if manifest.BootArtifacts.Kernel.Path != layout.kernel || manifest.BootArtifacts.Initrd.Path != layout.initrd {
		return layout, empty, nil, errors.New("Debian boot artefacts do not use the declared live directory")
	}
	expectedArgs := strings.Fields(requiredBootArguments + " " + surfaceKernelArguments)
	if !reflect.DeepEqual(manifest.BootArguments, expectedArgs) {
		return layout, empty, nil, errors.New("Debian kernel arguments differ from the adapter contract")
	}
	if len(manifest.BootArtifacts.DTBs) != len(manifest.KernelBundle.DeviceTrees) {
		return layout, empty, nil, errors.New("Debian boot DTB inventory differs from the kernel bundle")
	}
	required := map[string]bool{"x1e80100-microsoft-denali-oled.dtb": false, "x1p64100-microsoft-denali.dtb": false}
	selectedProfiles := 0
	for index, tree := range manifest.KernelBundle.DeviceTrees {
		if tree.Required {
			selectedProfiles++
		}
		actual := manifest.BootArtifacts.DTBs[index]
		if actual.Path != "sp11/dtb/"+tree.Basename || actual.SHA256 != tree.SHA256 {
			return layout, empty, nil, errors.New("Debian boot DTB identity differs from the kernel bundle")
		}
		if _, ok := required[tree.Basename]; ok {
			required[tree.Basename] = true
		}
		records = append(records, actual)
	}
	if selectedProfiles != 1 {
		return layout, empty, nil, errors.New("Debian requires exactly one installed device profile")
	}
	for _, present := range required {
		if !present {
			return layout, empty, nil, errors.New("Debian SP11 DTB inventory is incomplete")
		}
	}
	for _, pkg := range manifest.KernelBundle.Packages {
		if pkg.Path != "" {
			records = append(records, imagecontract.ArtifactRecord{Path: pkg.Path, SHA256: pkg.SHA256, Size: pkg.Size})
		}
	}
	if err := imagecontract.ValidateArtifactRecords(records); err != nil {
		return layout, empty, nil, err
	}
	return layout, contract, records, nil
}

// verifyRecord compares a rooted regular file to the complete recorded bytes.
func verifyRecord(workspace, relative string, record imagecontract.ArtifactRecord) error {
	if err := imagecontract.ValidateExtractedRegularFiles(workspace, []string{relative}); err != nil {
		return err
	}
	actual, err := recordFile(filepath.Join(workspace, filepath.FromSlash(relative)), record.Path)
	if err != nil {
		return err
	}
	if actual != record {
		return fmt.Errorf("artifact %s differs from its recorded size or digest", record.Path)
	}
	return nil
}

// validateEFI compares the actual GPT/El Torito FAT extent with the ISO member,
// then extracts the default firmware executable from those exact bytes.
func (v *Validator) validateEFI(ctx context.Context, toolsImage, workspace string, size int64) error {
	output, err := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace, "xorriso", "-indev", "/work/image.iso", "-report_system_area", "plain", "-report_el_torito", "plain")
	if err != nil {
		return err
	}
	extent, err := parseBootLayout(string(output), size)
	if err != nil {
		return err
	}
	file, err := os.Open(filepath.Join(workspace, "image.iso"))
	if err != nil {
		return err
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.NewSectionReader(file, extent.offset, extent.size))
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	embedded, err := recordFile(filepath.Join(workspace, "grub/efi.img"), "boot/grub/efi.img")
	if err != nil {
		return err
	}
	if count != embedded.Size || fmt.Sprintf("%x", hash.Sum(nil)) != embedded.SHA256 {
		return errors.New("USB ESP differs from the ISO EFI image")
	}
	if err := v.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace, "mcopy", "-i", fmt.Sprintf("/work/image.iso@@%d", extent.offset), "::/EFI/boot/bootaa64.efi", "/work/esp-bootaa64.efi"); err != nil {
		return err
	}
	if err := imagecontract.ValidateExtractedRegularFiles(workspace, []string{"esp-bootaa64.efi"}); err != nil {
		return err
	}
	digest, err := artifact.HashFile(filepath.Join(workspace, "esp-bootaa64.efi"))
	if err != nil {
		return err
	}
	if digest != inspectedGRUBSHA256 {
		return errors.New("USB ESP does not contain the inspected direct ARM64 GRUB")
	}
	if err := v.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace, "mcopy", "-i", fmt.Sprintf("/work/image.iso@@%d", extent.offset), "::/boot/grub/grub.cfg", "/work/esp-grub.cfg"); err != nil {
		return err
	}
	config, err := imagecontract.ReadBoundedExtractedFile(workspace, "esp-grub.cfg", maximumSourceConfigBytes)
	if err != nil {
		return err
	}
	if string(config) != espGRUBConfig {
		return errors.New("ESP GRUB forwarding does not select the ISO menu")
	}
	inventory, err := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace, "mdir", "-b", "-s", "-i", fmt.Sprintf("/work/image.iso@@%d", extent.offset), "::/")
	if err != nil {
		return err
	}
	if err := validateESPInventory(string(inventory)); err != nil {
		return err
	}
	filesystem, err := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace, "blkid", "-p", "-O", "32768", "/work/image.iso")
	if err != nil {
		return err
	}
	if !strings.Contains(string(filesystem), `TYPE="iso9660"`) {
		return errors.New("GPT data partition lacks its ISO filesystem view")
	}
	return nil
}
