package archlinux

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/image/sp11"
	"github.com/ooaklee/lexr.sh/internal/plan"
)

// Create builds a terminal live filesystem from pinned inputs and publishes only
// after a separate validator has read the completed ISO as untrusted data.
func (r *Remasterer) Create(ctx context.Context, request Request) (result Result, resultErr error) {
	operation, err := BuildPlan(request)
	if err != nil {
		return result, err
	}
	output, err := filepath.Abs(request.OutputISO)
	if err != nil {
		return result, err
	}
	source, err := filepath.Abs(request.SourceRootfs)
	if err != nil {
		return result, err
	}
	if source == output {
		return result, errors.New("Arch source and output paths must differ")
	}
	if err = sp11.ValidateBundlePaths(request.Bundle); err != nil {
		return result, err
	}
	if err = os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return result, err
	}
	for _, suffix := range []string{"", ".manifest.json", ".journal.json"} {
		if err = imagecontract.RequireAbsentPublication(output+suffix, "Arch image output"); err != nil {
			return result, err
		}
	}
	if err = r.Docker.Check(ctx); err != nil {
		return result, err
	}
	parent := request.WorkspaceRoot
	if parent == "" {
		parent = filepath.Dir(output)
	}
	if err = os.MkdirAll(parent, 0755); err != nil {
		return result, err
	}
	workspace, err := os.MkdirTemp(parent, ".lexr-arch-")
	if err != nil {
		return result, err
	}
	if !request.KeepWorkspace {
		defer os.RemoveAll(workspace)
	}
	fmt.Fprintln(r.Out, "Arch build workspace:", workspace)
	journal := plan.NewJournal(operation.Operation)
	checkpoint := func(id string, digests map[string]string) error {
		journal.Complete(id, digests)
		return journal.Save(filepath.Join(workspace, "image-create.journal.json"))
	}
	progress := func(message string) { fmt.Fprintln(r.Out, message) }
	digest, size, err := imagecontract.SnapshotFile(ctx, source, filepath.Join(workspace, "source.tar.gz"), maximumImageBytes, nil)
	if err != nil {
		return result, err
	}
	if digest != request.SourceSHA256 {
		return result, errors.New("Arch source rootfs differs from its pinned SHA-256")
	}
	if err = checkpoint("verify-source", map[string]string{"source.tar.gz": digest}); err != nil {
		return result, err
	}
	if err = stageKernelBundle(ctx, request.Bundle, workspace); err != nil {
		return result, err
	}
	if err = checkpoint("verify-kernel", nil); err != nil {
		return result, err
	}
	support := companion.Absent(companion.OmissionReasonNotRequested)
	if request.Companion.SourceDirectory != "" {
		progress("Building the Linux ARM64 Lexr companion and matching source")
		companionRequest := request.Companion
		companionRequest.DestinationDirectory = workspace
		support, err = r.Companions.Build(ctx, companionRequest)
		if err != nil {
			return result, err
		}
	}
	if err = os.MkdirAll(filepath.Join(workspace, "sp11"), 0755); err != nil {
		return result, err
	}
	if err = checkpoint("stage-companion", nil); err != nil {
		return result, err
	}
	toolsImage, err := r.Docker.EnsureArchToolsImage(ctx)
	if err != nil {
		return result, err
	}
	volume, err := r.Docker.CreateWorkVolume(ctx)
	if err != nil {
		return result, err
	}
	defer func() {
		if request.KeepWorkspace {
			if resultErr != nil {
				resultErr = fmt.Errorf("%w (workspace %s; Docker volume %s)", resultErr, workspace, volume)
			}
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, r.Docker.RemoveWorkVolume(cleanup, volume))
	}()
	if err = checkpoint("prepare-tools", nil); err != nil {
		return result, err
	}
	if err = r.stagePackages(ctx, workspace, request.CacheDirectory); err != nil {
		return result, err
	}
	if err = checkpoint("acquire-packages", nil); err != nil {
		return result, err
	}
	run := func(script string, args ...string) error {
		return r.Docker.RunOfflineChrootWorkspace(ctx, toolsImage, workspace, volume, append([]string{"bash", "-ceu", script, "lexr-arch-build"}, args...)...)
	}
	progress("Extracting the signed rootfs and installing the locked terminal packages offline")
	if err = run(prepareRootScript); err != nil {
		return result, err
	}
	if err = checkpoint("prepare-root", nil); err != nil {
		return result, err
	}
	if _, err = sp11.PrepareWiFiBoard(ctx, r.Docker, toolsImage, workspace, volume, "rootfs"); err != nil {
		return result, err
	}
	var identity [16]byte
	if _, err = rand.Read(identity[:]); err != nil {
		return result, err
	}
	boot := LiveBoot{ABI: request.Bundle.ABI, ImageID: hex.EncodeToString(identity[:])}
	config, _ := boot.GRUBConfig()
	bootstrap, _ := boot.BootstrapConfig()
	for name, data := range map[string]string{"installed.conf": InstalledInitramfsConfig(), "live.conf": LiveInitramfsConfig(), "lexr_sp11": EarlySupportHook(), "grub.cfg": config, "bootstrap.cfg": bootstrap, "LEXR_GETTING_STARTED.txt": gettingStarted, "lexr-arch-setup": setupScript} {
		if err = os.WriteFile(filepath.Join(workspace, name), []byte(data), 0644); err != nil {
			return result, err
		}
	}
	progress("Registering v23-compatible kernel payloads with native pacman")
	const installKernelPrefix = `root=/linux-work/rootfs
mkdir -p "$root/etc/lexr" "$root/etc/initcpio/install"
cp /work/installed.conf "$root/etc/lexr/mkinitcpio-installed.conf"
cp /work/live.conf "$root/etc/lexr/mkinitcpio-live.conf"
cp /work/lexr_sp11 "$root/etc/initcpio/install/lexr_sp11"
mount --bind "$root" "$root"
mount -t proc proc "$root/proc"
mount --rbind /dev "$root/dev"
mount --make-rslave "$root/dev"
`
	if _, err = (KernelPackageIdentity{ABI: request.Bundle.ABI, Version: request.Bundle.Version}).PackageVersion(); err != nil {
		return result, err
	}
	if err = run(installKernelPrefix+KernelPackageBuildScript(), "/linux-work/rootfs", "/linux-work/kernel-payload", request.Bundle.ABI, request.Bundle.Version); err != nil {
		return result, err
	}
	if err = run(`cp /linux-work/rootfs/work/lexr-kernel-sp11.pkg.tar.gz /work/lexr-kernel-sp11.pkg.tar.gz
rm -rf /linux-work/rootfs/work
`); err != nil {
		return result, err
	}
	if err = checkpoint("install-kernel", nil); err != nil {
		return result, err
	}
	if err = os.Rename(filepath.Join(workspace, "kernel"), filepath.Join(workspace, "sp11/kernel")); err != nil {
		return result, err
	}
	if err = os.Rename(filepath.Join(workspace, "lexr-kernel-sp11.pkg.tar.gz"), filepath.Join(workspace, "sp11/lexr-kernel-sp11.pkg.tar.gz")); err != nil {
		return result, err
	}
	if err = stageInstallerScripts(workspace, request.Bundle.ABI); err != nil {
		return result, err
	}
	if err = run(installerExportScript, request.Bundle.ABI); err != nil {
		return result, err
	}
	if err = writeInstallerManifest(workspace, request.Bundle.ABI); err != nil {
		return result, err
	}
	if err = os.WriteFile(filepath.Join(workspace, "archinstall"), []byte(installerLauncher), 0644); err != nil {
		return result, err
	}
	progress("Configuring terminal login, NetworkManager and the setup guide")
	if err = run(liveSessionScript); err != nil {
		return result, err
	}
	if err = checkpoint("prepare-live-session", nil); err != nil {
		return result, err
	}
	progress("Building the live initramfs, installed initramfs and ARM64 GRUB")
	if err = run(buildBootScript, request.Bundle.ABI); err != nil {
		return result, err
	}
	if err = checkpoint("build-boot-assets", nil); err != nil {
		return result, err
	}
	progress("Compressing the terminal filesystem and calculating its live-boot checksum")
	if err = r.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "bash", "-ceu", packRootScript); err != nil {
		return result, err
	}
	if err = checkpoint("pack-filesystem", nil); err != nil {
		return result, err
	}
	if err = r.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace, "bash", "-ceu", createESPScript); err != nil {
		return result, err
	}
	manifest, err := buildManifest(request, boot, workspace, imagecontract.ArtifactRecord{Path: filepath.Base(source), SHA256: digest, Size: size}, support)
	if err != nil {
		return result, err
	}
	var encoded bytes.Buffer
	if err = manifest.WriteJSON(&encoded); err != nil {
		return result, err
	}
	if err = os.WriteFile(filepath.Join(workspace, "sp11/lexr-manifest.json"), encoded.Bytes(), 0644); err != nil {
		return result, err
	}
	label, _ := boot.Label()
	marker, _ := boot.Marker()
	progress("Creating the GPT/EFI image for optical and USB boot")
	if err = r.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "bash", "-ceu", createISOScript, "lexr-arch-iso", label, strings.TrimPrefix(marker, "/")); err != nil {
		return result, err
	}
	if err = checkpoint("create-hybrid-boot", nil); err != nil {
		return result, err
	}
	progress("Independently validating the finished Arch image")
	partial := filepath.Join(workspace, "output.partial.iso")
	validation, err := NewValidator(r.Docker).Validate(ctx, partial)
	if err != nil {
		return result, err
	}
	if !validation.Valid {
		return result, errors.New("Arch image validation did not pass")
	}
	manifestIdentity := imagecontract.IdentifyBytes(encoded.Bytes())
	if validation.ManifestSHA256 != manifestIdentity.SHA256 || validation.ManifestSize != manifestIdentity.Size {
		return result, errors.New("validated Arch manifest differs from publication sidecar")
	}
	journal.Output = &plan.OutputRecord{Path: output, SHA256: validation.SHA256, Size: validation.Size}
	if err = checkpoint("validate-output", map[string]string{"output.iso": validation.SHA256}); err != nil {
		return result, err
	}
	journal.Complete("publish-output", map[string]string{"output.iso": validation.SHA256})
	if err = journal.Save(filepath.Join(workspace, "image-create.complete.journal.json")); err != nil {
		return result, err
	}
	journalBytes, err := os.ReadFile(filepath.Join(workspace, "image-create.complete.journal.json"))
	if err != nil {
		return result, err
	}
	manifestPath, journalPath, err := imagecontract.PublishISOOutputs(partial, output, encoded.Bytes(), journalBytes, imagecontract.PublicationIdentity{SHA256: validation.SHA256, Size: validation.Size})
	if err != nil {
		return result, err
	}
	result = Result{OutputISO: output, SHA256: validation.SHA256, Size: validation.Size, ManifestPath: manifestPath, JournalPath: journalPath, CompanionBundle: support}
	if request.KeepWorkspace {
		result.WorkspacePath, result.WorkspaceVolume = workspace, volume
	}
	return result, nil
}

// packRootScript includes filesystem attributes and checksums the final squashfs.
const packRootScript = `set -euo pipefail
root=/linux-work/rootfs
mksquashfs "$root" /linux-work/iso/arch/aarch64/airootfs.sfs -noappend -no-progress -processors 4 -comp zstd -Xcompression-level 15 -wildcards -e 'proc/*' 'sys/*' 'dev/*' 'run/*' 'tmp/*'
cd /linux-work/iso/arch/aarch64
sha512sum airootfs.sfs > airootfs.sha512
cp airootfs.sfs airootfs.sha512 /work/
`

// createESPScript creates a direct ARM64 GRUB removable-media EFI partition.
const createESPScript = `set -euo pipefail
truncate -s 24M /work/esp.img
mkfs.vfat -n LEXR_EFI /work/esp.img
mmd -i /work/esp.img ::/EFI ::/EFI/BOOT ::/boot ::/boot/grub
mcopy -i /work/esp.img /work/BOOTAA64.EFI ::/EFI/BOOT/BOOTAA64.EFI
mcopy -i /work/esp.img /work/bootstrap.cfg ::/boot/grub/grub.cfg
`

// createISOScript points GPT and El Torito at exactly the same FAT bytes.
const createISOScript = `set -euo pipefail
label=$1
marker=$2
cp /work/sp11/lexr-manifest.json /linux-work/iso/sp11/
cp /work/esp.img /linux-work/iso/boot/grub/efi.img
mkdir -p /linux-work/iso/EFI/BOOT
cp /work/BOOTAA64.EFI /linux-work/iso/EFI/BOOT/BOOTAA64.EFI
: > "/linux-work/iso/$marker"
xorriso -outdev /work/output.partial.iso -volid "$label" -padding 0 -map /linux-work/iso / -boot_image any partition_offset=16 -append_partition 2 0xef /work/esp.img -boot_image any appended_part_as=gpt -boot_image any efi_path=--interval:appended_partition_2:all:: -boot_image any cat_path=/boot.catalog -commit -end
`
