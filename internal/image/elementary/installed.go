package elementary

import (
	"context"
	"errors"
	"fmt"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
	"os"
	"path/filepath"
)

// inspectedUpdateGRUBSHA256 identifies elementary's updater of both its normal
// configuration and the active /boot/efi/EFI/ubuntu/grub/grub.cfg used by Distinst.
const inspectedUpdateGRUBSHA256 = "134b4f4ee57c2f4e29c5d9353c83ae25c1b675df6e0075a5cc497d74c35a6606"

// inspectedLinuxGeneratorSHA256 identifies the source generator with exact-ABI
// dtb lookup for normal and recovery entries.
const inspectedLinuxGeneratorSHA256 = "f7ee0f15c8a81fd375c2ed74d40fcb162a89750f9151bb72984e14ba1dd78561"

// inspectedForwardingSHA256 binds the source's alternate ARM64 configuration
// before replacing it with a single canonical forwarding command.
const inspectedForwardingSHA256 = "28e0691d2fe8dfc5bcee30ede680541b67d18109f144b578d1c93ab29a2f9507"

// installedGrubDefaults retains the stock generator and its recovery entries.
const installedGrubDefaults = `# Surface Pro 11 arguments for installed elementary OS kernels.
GRUB_CMDLINE_LINUX_DEFAULT="${GRUB_CMDLINE_LINUX_DEFAULT} clk_ignore_unused pd_ignore_unused arm64.nopauth systemd.tpm2_wait=0"
GRUB_TIMEOUT_STYLE=menu
GRUB_TIMEOUT=15
`

// installInstalledSupport uses the package-owned exact-ABI DTB staging helper.
// Distinst's kernel_copy resolves /vmlinuz; initialise both root and /boot links
// explicitly so the installer cannot overwrite a stock kernel with custom bytes.
func installInstalledSupport(ctx context.Context, docker *platform.Docker, image, workspace, volume string, bundle kernel.Bundle) error {
	if !kernelABIPattern.MatchString(bundle.ABI) || bundle.EffectiveDTBDelivery != kernel.DTBDeliveryExternalRequired {
		return errors.New("elementary installed support requires a safe exact ABI and external DTBs")
	}
	profile := ""
	for _, tree := range bundle.DeviceTrees {
		if tree.Required {
			if profile != "" {
				return errors.New("elementary requires one installed device profile")
			}
			profile = tree.Device
		}
	}
	if profile == "" {
		return errors.New("elementary requires an installed device profile")
	}
	if err := os.WriteFile(filepath.Join(workspace, "installed-grub-defaults"), []byte(installedGrubDefaults), 0644); err != nil {
		return err
	}
	const script = `set -o pipefail
root=/linux-work/rootfs
abi=$1
profile=$2
install -D -m 0644 /work/installed-grub-defaults "$root/etc/default/grub.d/99-surface-pro-11.cfg"
chroot "$root" /usr/libexec/lexr/kernel-boot-refresh refresh --root / --abi "$abi" --image "/boot/vmlinuz-$abi" --platform "$profile" --defer-grub
for member in vmlinuz initrd.img; do
    for prefix in "" boot/; do
        target="$root/$prefix$member"
        # Only change conventional symlinks or an absent link, never a real file.
        [ ! -e "$target" ] || [ -L "$target" ]
        if [ "$member" = vmlinuz ]; then name="vmlinuz-$abi"; else name="initrd.img-$abi"; fi
        if [ -z "$prefix" ]; then name="boot/$name"; fi
        ln -sfn "$name" "$target"
    done
done
# No live medium or installer creates EFI state during image preparation.
test ! -e "$root/boot/efi/EFI/ubuntu/grub/grub.cfg"
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", script, "lexr-elementary-installed", bundle.ABI, profile); err != nil {
		return fmt.Errorf("prepare elementary installed kernel and device tree: %w", err)
	}
	return nil
}

// installRetainedMedia keeps the bundled Lexr, licence evidence and kernel
// packages available after Distinst removes its live-only packages. Elementary's
// automatic partitioning has no Pop recovery volume; recovery uses stock GRUB
// recovery entries and the retained USB, not a generated BLS entry.
func installRetainedMedia(ctx context.Context, docker *platform.Docker, image, workspace, volume string) error {
	const script = `root=/linux-work/rootfs
parent="$root/usr/share/lexr/elementary-media"
test ! -e "$parent" && test ! -L "$parent"
install -d -m 0755 "$parent"
cp -a /work/sp11 "$parent/"
chmod -R a+rX "$parent"
`
	return docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", script)
}

// validateRetainedMedia compares installed support to the complete on-media
// companion and pins the exact DTB selected for the installer's GRUB entries.
func (v *Validator) validateRetainedMedia(ctx context.Context, toolsImage, workspace, volume string, manifest imagecontract.Manifest) error {
	const script = `set -o pipefail
root=/linux-work/rootfs
retained="$root/usr/share/lexr/elementary-media/sp11"
test -d "$retained" && test ! -L "$retained"
diff -qr /work/sp11 "$retained"
diff -qr /work/sp11/firmware "$root/usr/share/lexr/firmware"
cmp /work/sp11/firmware/qcom_gen70500_gmu.bin "$root/usr/lib/firmware/qcom/gen70500_gmu.bin"
cmp /work/sp11/firmware/qcom_gen70500_sqe.fw "$root/usr/lib/firmware/qcom/gen70500_sqe.fw"
for name in gen70500_gmu.bin gen70500_sqe.fw; do
    for suffix in .xz .zst; do
        test ! -e "$root/usr/lib/firmware/qcom/$name$suffix" && test ! -L "$root/usr/lib/firmware/qcom/$name$suffix"
    done
done
test ! -e "$root/recovery"
`
	if err := v.Docker.RunWithReadOnlyVolumeAsHostUser(ctx, toolsImage, workspace, volume, "bash", "-ceu", script); err != nil {
		return err
	}
	required := 0
	for _, tree := range manifest.KernelBundle.DeviceTrees {
		if tree.Required {
			required++
			if err := v.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "cmp", "/linux-work/rootfs/boot/dtb-"+manifest.KernelBundle.ABI, "/work/sp11/dtb/"+tree.Basename); err != nil {
				return err
			}
		}
	}
	if required != 1 {
		return errors.New("elementary installed DTB selection is ambiguous")
	}
	return nil
}
