package elementary

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/caspermedia"
)

// inspectedGRUBSHA256 identifies elementary 8.1's direct ARM64 GRUB. Its
// inspected embedded bootstrap searches /.disk/info and loads the ISO menu.
// A changed bootstrap requires source inspection before extending support.
const inspectedGRUBSHA256 = "38ed2ce9203397760e1f41ce029d20073cf31f8d061735eed5442de9a28e9ec9"

// extractSource preserves elementary's installer identity and fixed directory,
// rejecting an alternate GRUB route or a changed installer path contract.
func (r *Remasterer) extractSource(ctx context.Context, toolsImage, workspace, volume string) (sourceLayout, error) {
	if err := r.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace,
		"xorriso", "-osirrox", "on", "-indev", "/work/source.iso",
		"-extract", "/boot/grub", "/work/source-grub",
		"-extract", "/EFI/boot/grubaa64.efi", "/work/grubaa64.efi",
		"-extract", "/.disk/info", "/work/disk-info",
		"-extract", "/md5sum.txt", "/work/source-md5sum.txt"); err != nil {
		return sourceLayout{}, err
	}
	// ISO directories are read-only. Make only this private extracted tree
	// writable so both preparation and failure cleanup work as the host user.
	if err := writableDirectories(filepath.Join(workspace, "source-grub")); err != nil {
		return sourceLayout{}, err
	}
	grub, err := imagecontract.ReadBoundedExtractedFile(workspace, "source-grub/grub.cfg", maximumSourceConfigBytes)
	if err != nil {
		return sourceLayout{}, err
	}
	info, err := imagecontract.ReadBoundedExtractedFile(workspace, "disk-info", maximumSourceConfigBytes)
	if err != nil {
		return sourceLayout{}, err
	}
	layout, err := parseSourceLayout(grub, info)
	if err != nil {
		return layout, err
	}
	forwarding, err := imagecontract.ReadBoundedExtractedFile(workspace, "source-grub/arm64-efi/grub.cfg", maximumSourceConfigBytes)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(forwarding)) != inspectedForwardingSHA256 {
		return layout, errors.New("elementary alternate GRUB route has changed; inspect source before remastering")
	}
	if err := imagecontract.ValidateExtractedRegularFiles(workspace, []string{"grubaa64.efi", "source-grub/efi.img"}); err != nil {
		return layout, err
	}
	digest, err := artifact.HashFile(filepath.Join(workspace, "grubaa64.efi"))
	if err != nil {
		return layout, err
	}
	if digest != inspectedGRUBSHA256 {
		return layout, errors.New("elementary GRUB bootstrap has changed; inspect the new source before remastering")
	}
	arguments := []string{"xorriso", "-osirrox", "on", "-indev", "/work/source.iso"}
	for _, member := range []string{"filesystem.squashfs", "filesystem.manifest", "filesystem.manifest-remove"} {
		arguments = append(arguments, "-extract", "/"+layout.member(member), "/work/source-"+member)
	}
	if err := r.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace, arguments...); err != nil {
		return layout, err
	}
	if err := imagecontract.ValidateExtractedRegularFiles(workspace, []string{"source-filesystem.squashfs", "source-filesystem.manifest", "source-filesystem.manifest-remove"}); err != nil {
		return layout, err
	}
	removal, err := imagecontract.ReadBoundedExtractedFile(workspace, "source-filesystem.manifest-remove", maximumSourceConfigBytes)
	if err != nil {
		return layout, err
	}
	for _, name := range strings.Fields(string(removal)) {
		if strings.HasPrefix(name, "linux-") || strings.HasPrefix(name, "lexr") || strings.HasPrefix(name, "grub") || name == "initramfs-tools" {
			return layout, fmt.Errorf("elementary installer would remove required package %s", name)
		}
	}
	if err := r.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume,
		"unsquashfs", "-no-progress", "-xattrs-exclude", "^trusted\\.", "-d", "/linux-work/rootfs", "/work/source-filesystem.squashfs"); err != nil {
		return layout, err
	}
	const script = `root=/linux-work/rootfs
grep -qx 'ID=elementary' "$root/usr/lib/os-release"
test -x "$root/usr/bin/io.elementary.installer"
test -x "$root/usr/bin/distinst"
test ! -e "$root/usr/bin/kernelstub"
test -f "$root/usr/share/initramfs-tools/hooks/casper"
test ! -e "$root/usr/share/lexr/elementary-media"
sha256sum "$root/usr/sbin/update-grub" "$root/etc/grub.d/10_linux"
`
	if err := r.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "bash", "-ceu", script); err != nil {
		return layout, fmt.Errorf("unsupported elementary installer root: %w", err)
	}
	output, err := r.Docker.CaptureInWorkspaceVolume(ctx, toolsImage, workspace, volume, "sha256sum", "/linux-work/rootfs/usr/sbin/update-grub", "/linux-work/rootfs/etc/grub.d/10_linux")
	if err != nil || !strings.Contains(string(output), inspectedUpdateGRUBSHA256+" ") || !strings.Contains(string(output), inspectedLinuxGeneratorSHA256+" ") {
		return layout, errors.New("elementary installed GRUB contract changed")
	}
	return layout, nil
}

// writableDirectories restores owner write/search access in a private
// extracted tree without following symlinks or changing artefact file bytes.
func writableDirectories(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o755)
		}
		return nil
	})
}

// bindMedia uses the UUID actually emitted by elementary's Casper hook. The Linux
// extraction stays inside the case-sensitive volume; only the marker leaves.
func (r *Remasterer) bindMedia(ctx context.Context, toolsImage, workspace, volume string) error {
	const script = `unmkinitramfs /work/casper-initrd /linux-work/live-initrd
test -f /linux-work/live-initrd/main/conf/uuid.conf
test ! -L /linux-work/live-initrd/main/conf/uuid.conf
cat /linux-work/live-initrd/main/conf/uuid.conf
`
	output, err := r.Docker.CaptureInWorkspaceVolume(ctx, toolsImage, workspace, volume, "bash", "-ceu", script)
	if err != nil {
		return err
	}
	// unmkinitramfs may emit archive diagnostics. Read the bounded marker in a
	// separate command so no diagnostic line can be mistaken for the identity.
	output, err = r.Docker.CaptureInWorkspaceVolume(ctx, toolsImage, workspace, volume, "cat", "/linux-work/live-initrd/main/conf/uuid.conf")
	if err != nil {
		return err
	}
	uuid, err := caspermedia.ParseUUID(output)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(workspace, "casper-uuid-generic"), []byte(uuid+"\n"), 0o644)
}

// repackRoot keeps Linux ownership and supported xattrs and derives the
// installer package inventory from the actual post-transaction dpkg database.
func (r *Remasterer) repackRoot(ctx context.Context, toolsImage, workspace, volume string) error {
	const script = `set -o pipefail
root=/linux-work/rootfs
chroot "$root" dpkg-query -W -f='${Package} ${Version}\n' > /work/filesystem.manifest
du -sx --block-size=1 "$root" | cut -f1 > /work/filesystem.size
mksquashfs "$root" /linux-work/remastered.squashfs -noappend -no-progress -comp xz -b 1048576
cp /linux-work/remastered.squashfs /work/remastered.squashfs
chmod a+r /work/remastered.squashfs /work/filesystem.manifest /work/filesystem.size
`
	return r.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "bash", "-ceu", script)
}

// prepareEFI replaces shim with the inspected direct GRUB in the optical FAT
// image, which becomes the same appended partition used for USB boot.
func (r *Remasterer) prepareEFI(ctx context.Context, toolsImage, workspace string) error {
	if err := os.WriteFile(filepath.Join(workspace, "esp-grub.cfg"), []byte(espGRUBConfig), 0644); err != nil {
		return err
	}
	const script = `cp /work/source-grub/efi.img /work/esp.img
chmod u+w /work/esp.img
mdel -i /work/esp.img ::/EFI/boot/bootaa64.efi ::/EFI/boot/grubaa64.efi
mcopy -i /work/esp.img /work/grubaa64.efi ::/EFI/boot/bootaa64.efi
mcopy -o -i /work/esp.img /work/esp-grub.cfg ::/boot/grub/grub.cfg
mcopy -i /work/esp.img ::/EFI/boot/bootaa64.efi /work/esp-bootaa64.efi
cmp /work/grubaa64.efi /work/esp-bootaa64.efi
`
	if err := r.Docker.RunInWorkspaceAsHostUser(ctx, toolsImage, workspace, "bash", "-ceu", script); err != nil {
		return err
	}
	return imagecontract.ValidateExtractedRegularFiles(workspace, strings.Fields("esp.img esp-bootaa64.efi"))
}
