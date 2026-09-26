package debianlive

import (
	"context"
	"errors"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// installRetainedMedia keeps the bundled Lexr, licence evidence and kernel
// packages available after Calamares removes its live-only packages. Recovery uses the native GRUB
// recovery entries and the retained USB.
func installRetainedMedia(ctx context.Context, docker *platform.Docker, image, workspace, volume string) error {
	const script = `root=/linux-work/rootfs
parent="$root/usr/share/lexr/debian-media"
test ! -e "$parent"
test ! -L "$parent"
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
retained="$root/usr/share/lexr/debian-media/sp11"
test -d "$retained"
test ! -L "$retained"
diff -qr /work/sp11 "$retained"
diff -qr /work/sp11/firmware "$root/usr/share/lexr/firmware"
cmp /work/sp11/firmware/qcom_gen70500_gmu.bin "$root/usr/lib/firmware/qcom/gen70500_gmu.bin"
cmp /work/sp11/firmware/qcom_gen70500_sqe.fw "$root/usr/lib/firmware/qcom/gen70500_sqe.fw"
for name in gen70500_gmu.bin gen70500_sqe.fw; do
    for suffix in .xz .zst; do
        test ! -e "$root/usr/lib/firmware/qcom/$name$suffix"
        test ! -L "$root/usr/lib/firmware/qcom/$name$suffix"
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
		return errors.New("Debian installed DTB selection is ambiguous")
	}
	return nil
}
