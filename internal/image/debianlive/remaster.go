package debianlive

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/image/debian"
	"github.com/ooaklee/lexr.sh/internal/image/sp11"
	"github.com/ooaklee/lexr.sh/internal/plan"
)

// Create prepares Debian's single installer filesystem, creates a USB-visible
// EFI partition, and publishes only after independent image validation.
func (r *Remasterer) Create(ctx context.Context, request Request) (result Result, resultErr error) {
	operation, err := BuildPlan(request)
	if err != nil {
		return result, err
	}
	output, err := filepath.Abs(request.OutputISO)
	if err != nil {
		return result, err
	}
	source, err := filepath.Abs(request.SourceISO)
	if err != nil {
		return result, err
	}
	if source == output {
		return result, errors.New("source and output ISO paths must differ")
	}
	if err := sp11.ValidateBundlePaths(request.Bundle); err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return result, err
	}
	for _, suffix := range []string{"", ".manifest.json", ".journal.json"} {
		if err := imagecontract.RequireAbsentPublication(output+suffix, "Debian image output"); err != nil {
			return result, err
		}
	}
	if err := r.Docker.Check(ctx); err != nil {
		return result, err
	}
	parent := request.WorkspaceRoot
	if parent == "" {
		parent = filepath.Dir(output)
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return result, err
	}
	workspace, err := os.MkdirTemp(parent, ".lexr-debian-")
	if err != nil {
		return result, err
	}
	if !request.KeepWorkspace {
		defer os.RemoveAll(workspace)
	}
	journal := plan.NewJournal(operation.Operation)
	checkpoint := func(id string, digests map[string]string) error {
		journal.Complete(id, digests)
		return journal.Save(filepath.Join(workspace, "image-create.journal.json"))
	}
	progress := func(message string) { fmt.Fprintln(r.Out, message) }
	progress("Verifying private copies of the Debian source and kernel packages")
	digest, size, err := imagecontract.SnapshotFile(ctx, source, filepath.Join(workspace, "source.iso"), maximumISOBytes, nil)
	if err != nil {
		return result, err
	}
	if digest != inspectedSourceISOHash {
		return result, errors.New("source ISO differs from the inspected Debian live snapshot")
	}
	if request.SourceSHA256 != "" && !strings.EqualFold(request.SourceSHA256, digest) {
		return result, fmt.Errorf("source ISO SHA-256 mismatch: expected %s, got %s", request.SourceSHA256, digest)
	}
	if err := checkpoint("verify-source", map[string]string{"source.iso": digest}); err != nil {
		return result, err
	}
	if err := stageKernelBundle(ctx, request.Bundle, workspace); err != nil {
		return result, err
	}
	if err := checkpoint("verify-kernel", nil); err != nil {
		return result, err
	}
	support := companion.Absent(companion.OmissionReasonNotRequested)
	if request.Companion.SourceDirectory != "" {
		if r.Companions == nil {
			return result, errors.New("companion builder is unavailable")
		}
		progress("Building the Linux ARM64 Lexr companion with corresponding source")
		companionRequest := request.Companion
		companionRequest.DestinationDirectory = workspace
		support, err = r.Companions.Build(ctx, companionRequest)
		if err != nil {
			return result, err
		}
	}
	if err := checkpoint("stage-companion", nil); err != nil {
		return result, err
	}
	toolsImage, err := r.Docker.EnsureToolsImage(ctx)
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
		if err := r.Docker.RemoveWorkVolume(cleanup, volume); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove Debian workspace volume %s: %w", volume, err))
		}
	}()
	if err := checkpoint("prepare-tools", nil); err != nil {
		return result, err
	}
	progress("Inspecting Debian's pinned live-boot directory and installer filesystem")
	layout, err := r.extractSource(ctx, toolsImage, workspace, volume)
	if err != nil {
		return result, err
	}
	if err := checkpoint("extract-live-root", nil); err != nil {
		return result, err
	}
	progress("Adding checksum-pinned public X1E GPU firmware and notices")
	if err := sp11.PrepareGPUFirmware(ctx, r.Docker, toolsImage, workspace, volume); err != nil {
		return result, err
	}
	progress("Preparing SP11 Wi-Fi from the distribution's firmware")
	wifiDigest, err := sp11.PrepareWiFiBoard(ctx, r.Docker, toolsImage, workspace, volume, "rootfs")
	if err != nil {
		return result, err
	}
	if err := checkpoint("prepare-wifi", map[string]string{sp11.WiFiBoard: wifiDigest}); err != nil {
		return result, err
	}
	progress("Installing the custom kernel and Debian installed-system support")
	if err := prepareInstalledPackages(ctx, r.Docker, toolsImage, workspace, volume); err != nil {
		return result, err
	}
	if err := checkpoint("prepare-installed-grub", nil); err != nil {
		return result, err
	}
	if err := debian.InstallKernelPackages(ctx, r.Docker, toolsImage, workspace, volume, request.Bundle); err != nil {
		return result, err
	}
	if err := r.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "depmod", "-a", "-b", "/linux-work/rootfs", request.Bundle.ABI); err != nil {
		return result, err
	}
	if err := installInstalledSupport(ctx, r.Docker, toolsImage, workspace, volume, request.Bundle); err != nil {
		return result, err
	}
	if err := installGettingStarted(ctx, r.Docker, toolsImage, workspace, volume, support.Included); err != nil {
		return result, err
	}
	if err := checkpoint("install-kernel", nil); err != nil {
		return result, err
	}
	progress("Generating separate live and installed-system initramfs images")
	if err := buildInitramfs(ctx, r.Docker, toolsImage, workspace, volume, request.Bundle.ABI); err != nil {
		return result, err
	}
	if err := checkpoint("build-initramfs", nil); err != nil {
		return result, err
	}
	if err := r.bindMedia(ctx, toolsImage, workspace, volume); err != nil {
		return result, err
	}
	if err := checkpoint("bind-live-media", nil); err != nil {
		return result, err
	}
	if err := os.MkdirAll(filepath.Join(workspace, "sp11", "dtb"), 0o755); err != nil {
		return result, err
	}
	for _, tree := range request.Bundle.DeviceTrees {
		if err := r.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "cp", "/linux-work/rootfs/"+tree.Path, "/work/sp11/dtb/"+tree.Basename); err != nil {
			return result, err
		}
	}
	if err := checkpoint("pair-device-trees", nil); err != nil {
		return result, err
	}
	if err := r.prepareEFI(ctx, toolsImage, workspace); err != nil {
		return result, err
	}
	config, err := grubConfig(layout, request.Bundle.ABI)
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(workspace, "grub.cfg"), []byte(config), 0o644); err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(workspace, "forwarding-grub.cfg"), []byte(forwardingGRUBConfig), 0644); err != nil {
		return result, err
	}
	manifest, err := buildManifest(request, layout, workspace, imagecontract.ArtifactRecord{Path: filepath.Base(source), SHA256: digest, Size: size}, support)
	if err != nil {
		return result, err
	}
	encoded, err := manifestBytes(manifest)
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(workspace, "sp11", "lexr-manifest.json"), encoded, 0o644); err != nil {
		return result, err
	}
	if err := os.Rename(filepath.Join(workspace, "kernel"), filepath.Join(workspace, "sp11", "kernel")); err != nil {
		return result, err
	}
	if err := installRetainedMedia(ctx, r.Docker, toolsImage, workspace, volume); err != nil {
		return result, err
	}
	progress("Repacking Debian's installer filesystem, recovery support and package inventory")
	if err := r.repackRoot(ctx, toolsImage, workspace, volume); err != nil {
		return result, err
	}
	if err := checkpoint("repack-live-root", nil); err != nil {
		return result, err
	}
	progress("Creating matching optical and USB EFI boot paths")
	mappings := map[string]string{
		layout.member("filesystem.squashfs"): filepath.Join(workspace, "remastered.squashfs"),
		layout.member("filesystem.packages"): filepath.Join(workspace, "filesystem.packages"),
		layout.member("filesystem.size"):     filepath.Join(workspace, "filesystem.size"),
		layout.kernel:                        filepath.Join(workspace, "live-vmlinuz"),
		layout.initrd:                        filepath.Join(workspace, "live-initrd"),
		mediumIdentityPath:                   filepath.Join(workspace, "live-uuid"),
		"boot/grub/arm64-efi/grub.cfg":       filepath.Join(workspace, "forwarding-grub.cfg"),
		"boot/grub/loopback.cfg":             filepath.Join(workspace, "forwarding-grub.cfg"),
		"efi.img":                            filepath.Join(workspace, "esp.img"),
		"boot/grub/grub.cfg":                 filepath.Join(workspace, "grub.cfg"),
		"boot/grub/efi.img":                  filepath.Join(workspace, "esp.img"),
		"EFI/boot/bootaa64.efi":              filepath.Join(workspace, "grubaa64.efi"),
	}
	if err := filepath.WalkDir(filepath.Join(workspace, "sp11"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular support member %s", entry.Name())
		}
		relative, err := filepath.Rel(workspace, path)
		if err != nil {
			return err
		}
		mappings[filepath.ToSlash(relative)] = path
		return nil
	}); err != nil {
		return result, err
	}
	if err := updateMediaChecksums(workspace, mappings); err != nil {
		return result, err
	}
	mappings["md5sum.txt"] = filepath.Join(workspace, "md5sum.txt")
	arguments := hybridBootArguments()
	keys := make([]string, 0, len(mappings))
	for key := range mappings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, member := range keys {
		relative, err := filepath.Rel(workspace, mappings[member])
		if err != nil {
			return result, err
		}
		arguments = append(arguments, "-map", "/work/"+filepath.ToSlash(relative), "/"+member)
	}
	arguments = append(arguments, "-commit", "-end")
	if err := r.Docker.RunInWorkspace(ctx, toolsImage, workspace, arguments...); err != nil {
		return result, err
	}
	if err := checkpoint("create-hybrid-boot", nil); err != nil {
		return result, err
	}
	progress("Independently validating the complete Debian image")
	partial := filepath.Join(workspace, "output.partial.iso")
	validation, err := NewValidator(r.Docker).Validate(ctx, partial)
	if err != nil {
		return result, err
	}
	if !validation.Valid {
		return result, errors.New("Debian image validation did not pass")
	}
	manifestIdentity := imagecontract.IdentifyBytes(encoded)
	if validation.ManifestSHA256 != manifestIdentity.SHA256 || validation.ManifestSize != manifestIdentity.Size {
		return result, errors.New("validated manifest differs from the publication sidecar")
	}
	journal.Output = &plan.OutputRecord{Path: output, SHA256: validation.SHA256, Size: validation.Size}
	if err := checkpoint("validate-output", map[string]string{"output.iso": validation.SHA256}); err != nil {
		return result, err
	}
	completed := *journal
	completed.Records = append([]plan.StepRecord(nil), journal.Records...)
	completed.Complete("publish-output", map[string]string{"output.iso": validation.SHA256})
	if err := completed.Save(filepath.Join(workspace, "image-create.complete.journal.json")); err != nil {
		return result, err
	}
	journalBytes, err := os.ReadFile(filepath.Join(workspace, "image-create.complete.journal.json"))
	if err != nil {
		return result, err
	}
	manifestPath, journalPath, err := imagecontract.PublishISOOutputs(partial, output, encoded, journalBytes,
		imagecontract.PublicationIdentity{SHA256: validation.SHA256, Size: validation.Size})
	if err != nil {
		return result, err
	}
	result = Result{OutputISO: output, SHA256: validation.SHA256, Size: validation.Size, ManifestPath: manifestPath, JournalPath: journalPath, CompanionBundle: support}
	if request.KeepWorkspace {
		result.WorkspacePath, result.WorkspaceVolume = workspace, volume
	}
	return result, nil
}
