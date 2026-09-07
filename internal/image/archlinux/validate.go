package archlinux

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// Validator inspects a private ISO snapshot without running image executables.
type Validator struct{ Docker *platform.Docker }

// NewValidator supplies the standard trusted Linux-tool boundary.
func NewValidator(docker *platform.Docker) *Validator {
	if docker == nil {
		docker = platform.NewDocker(nil)
	}
	return &Validator{Docker: docker}
}

// Validate verifies discovery, boot payload, terminal root and companion bytes.
// Structural validation is distinct from boot qualification on a real Surface.
func (v *Validator) Validate(ctx context.Context, isoPath string) (report imagecontract.ValidationReport, resultErr error) {
	absolute, err := filepath.Abs(isoPath)
	if err != nil {
		return report, err
	}
	report = imagecontract.ValidationReport{Path: absolute, Layout: "hybrid-iso", Adapter: AdapterID}
	workspace, err := os.MkdirTemp(filepath.Dir(absolute), ".lexr-arch-validate-")
	if err != nil {
		return report, err
	}
	defer func() {
		_ = filepath.WalkDir(workspace, func(p string, e os.DirEntry, err error) error {
			if err == nil && e.IsDir() {
				return os.Chmod(p, 0755)
			}
			return err
		})
		resultErr = errors.Join(resultErr, os.RemoveAll(workspace))
	}()
	report.SHA256, report.Size, err = imagecontract.SnapshotFile(ctx, absolute, filepath.Join(workspace, "image.iso"), maximumImageBytes, nil)
	if err != nil {
		return report, err
	}
	if err = v.Docker.Check(ctx); err != nil {
		return report, err
	}
	toolsImage, err := v.Docker.EnsureArchToolsImage(ctx)
	if err != nil {
		return report, err
	}
	check := func(name string, err error, detail string) error {
		if err != nil {
			detail = err.Error()
		}
		report.Checks = append(report.Checks, imagecontract.ValidationCheck{Name: name, Passed: err == nil, Details: detail})
		if err != nil {
			return fmt.Errorf("Arch validation %s: %w", name, err)
		}
		return nil
	}
	extract := func(args ...string) error {
		return v.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace, append([]string{"xorriso", "-osirrox", "on", "-indev", "/work/image.iso"}, args...)...)
	}
	if err = extract("-extract", "/sp11", "/work/sp11"); err != nil {
		return report, check("manifest-members", err, "")
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
	boot, records, err := validateManifest(manifest)
	if err = check("embedded-manifest", err, "canonical terminal live-image discovery and kernel contract"); err != nil {
		return report, err
	}
	report.KernelABI = boot.ABI
	for _, tree := range manifest.KernelBundle.DeviceTrees {
		report.DeviceTrees = append(report.DeviceTrees, tree.Device)
	}
	if err = extract("-extract", "/arch", "/work/arch", "-extract", "/boot", "/work/boot", "-extract", "/EFI", "/work/EFI", "-extract", "/LEXR_GETTING_STARTED.txt", "/work/LEXR_GETTING_STARTED.txt"); err != nil {
		return report, check("required-media-members", err, "")
	}
	for _, record := range records {
		if err = verifyRecord(workspace, record.Path, record); err != nil {
			return report, check("embedded-artifacts", err, "")
		}
	}
	check("embedded-artifacts", nil, "complete kernel, rootfs, package and boot payload digests match the manifest")
	if err = check("live-filesystem-checksum", validateLiveChecksum(workspace), "archiso checksum names only the complete live filesystem"); err != nil {
		return report, err
	}
	config, _ := boot.GRUBConfig()
	data, err := imagecontract.ReadBoundedExtractedFile(workspace, "boot/grub/grub.cfg", 64<<10)
	if err != nil || string(data) != config {
		return report, check("grub-menu", errors.New("GRUB menu differs from the complete Arch boot contract"), "")
	}
	marker, _ := boot.Marker()
	marker = strings.TrimPrefix(marker, "/")
	data, err = imagecontract.ReadBoundedExtractedFile(workspace, marker, 1)
	if err != nil || len(data) != 0 {
		return report, check("media-discovery", errors.New("Arch discovery marker is absent or altered"), "")
	}
	lock, err := imagecontract.ReadBoundedExtractedFile(workspace, "sp11/packages.lock.json", 1<<20)
	if err != nil || !bytes.Equal(lock, packageLockJSON) {
		return report, check("package-lock", errors.New("unrecognised Arch package snapshot"), "")
	}
	guide, err := imagecontract.ReadBoundedExtractedFile(workspace, "LEXR_GETTING_STARTED.txt", 64<<10)
	if err != nil || string(guide) != gettingStarted {
		return report, check("getting-started", errors.New("terminal setup guide differs"), "")
	}
	if err = check("optical-and-usb-efi", v.validateEFI(ctx, toolsImage, workspace, report.Size, boot), "GPT and optical boot select one ARM64 GRUB and the exact image marker"); err != nil {
		return report, err
	}
	if err = check("companion", companion.ValidateDirectory(manifest.CompanionBundle, filepath.Join(workspace, "sp11/companion")), "Lexr, matching source, catalogues and notices agree"); err != nil {
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
	if err = check("terminal-root-and-initramfs", v.validateRoot(ctx, toolsImage, workspace, volume, manifest), "native kernel, initramfs, firmware, terminal setup and retained companion agree"); err != nil {
		return report, err
	}
	report.Valid = true
	return report, nil
}

// verifyRecord checks a regular extracted member without following symlinks.
func verifyRecord(workspace, relative string, expected imagecontract.ArtifactRecord) error {
	if err := imagecontract.ValidateExtractedRegularFiles(workspace, []string{relative}); err != nil {
		return err
	}
	actual, err := recordFile(filepath.Join(workspace, relative), expected.Path)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("Arch artefact %s differs from its recorded bytes", expected.Path)
	}
	return nil
}

// validateEFI reads the real GPT extent, not merely an ISO-visible copy of it.
func (v *Validator) validateEFI(ctx context.Context, toolsImage, workspace string, size int64, boot LiveBoot) error {
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
	expected, err := recordFile(filepath.Join(workspace, "boot/grub/efi.img"), "boot/grub/efi.img")
	if err != nil {
		return err
	}
	if count != expected.Size || fmt.Sprintf("%x", hash.Sum(nil)) != expected.SHA256 {
		return errors.New("GPT EFI partition differs from the ISO member")
	}
	spec := fmt.Sprintf("/work/image.iso@@%d", extent.offset)
	for _, pair := range [][2]string{{"::/EFI/BOOT/BOOTAA64.EFI", "esp-BOOTAA64.EFI"}, {"::/boot/grub/grub.cfg", "esp-grub.cfg"}} {
		if err = v.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace, "mcopy", "-i", spec, pair[0], "/work/"+pair[1]); err != nil {
			return err
		}
	}
	efi, err := recordFile(filepath.Join(workspace, "EFI/BOOT/BOOTAA64.EFI"), "EFI/BOOT/BOOTAA64.EFI")
	if err != nil {
		return err
	}
	if err = verifyRecord(workspace, "esp-BOOTAA64.EFI", efi); err != nil {
		return err
	}
	bootstrap, _ := boot.BootstrapConfig()
	config, err := imagecontract.ReadBoundedExtractedFile(workspace, "esp-grub.cfg", 64<<10)
	if err != nil || string(config) != bootstrap {
		return errors.New("ESP bootstrap differs from the Arch marker")
	}
	if err = validateStandaloneGRUB(filepath.Join(workspace, "esp-BOOTAA64.EFI"), bootstrap); err != nil {
		return err
	}
	listing, err := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace, "mdir", "-b", "-s", "-i", spec, "::/")
	if err != nil {
		return err
	}
	if err = validateESPInventory(string(listing)); err != nil {
		return err
	}
	filesystem, err := v.Docker.CaptureInWorkspace(ctx, toolsImage, workspace, "blkid", "-p", "-O", "32768", "/work/image.iso")
	if err != nil {
		return err
	}
	label, _ := boot.Label()
	if !strings.Contains(string(filesystem), `TYPE="iso9660"`) || !strings.Contains(string(filesystem), `LABEL="`+label+`"`) {
		return errors.New("partition-relative ISO9660 view has the wrong discovery label")
	}
	return nil
}

// validateLiveChecksum confines archiso's checksum file to one exact member.
func validateLiveChecksum(workspace string) error {
	checksum, err := imagecontract.ReadBoundedExtractedFile(workspace, "arch/aarch64/airootfs.sha512", 256)
	if err != nil {
		return err
	}
	file, err := os.Open(filepath.Join(workspace, "arch/aarch64/airootfs.sfs"))
	if err != nil {
		return err
	}
	hash := sha512.New()
	_, err = io.Copy(hash, file)
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if string(checksum) != fmt.Sprintf("%x  airootfs.sfs\n", hash.Sum(nil)) {
		return errors.New("archiso checksum does not bind only airootfs.sfs")
	}
	return nil
}
