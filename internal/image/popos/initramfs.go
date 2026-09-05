package popos

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ooaklee/lexr.sh/internal/image/sp11"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// buildInitramfs creates an installed-system image without Casper, restores
// the source's live hooks, then generates a separately named live image with
// an explicit media UUID. Only the installed image remains under /boot.
func buildInitramfs(ctx context.Context, docker *platform.Docker, image, workspace, volume, abi string) error {
	if !kernelABIPattern.MatchString(abi) {
		return fmt.Errorf("invalid Pop initramfs kernel ABI")
	}
	if err := os.WriteFile(filepath.Join(workspace, "sp11-firmware-hook"), []byte(sp11.LiveFirmwareHook()), 0o644); err != nil {
		return err
	}
	const script = `set -o pipefail
root=/linux-work/rootfs
abi=$1
backup=/linux-work/lexr-casper-backup
test ! -e "$backup"
test ! -L "$backup"
test -f "$root/boot/vmlinuz-$abi"
test -d "$root/usr/lib/modules/$abi"
test -x "$root/usr/sbin/mkinitramfs"
test -f "$root/usr/share/initramfs-tools/hooks/casper"
test -f "$root/usr/share/initramfs-tools/scripts/casper"
install -D -m 0755 /work/sp11-firmware-hook "$root/etc/initramfs-tools/hooks/lexr-sp11-firmware"
mkdir -p "$backup/hooks" "$backup/scripts"

restore_casper() {
    for kind in hooks scripts; do
        for member in "$backup/$kind"/*; do
            [ -e "$member" ] || [ -L "$member" ] || continue
            mv "$member" "$root/usr/share/initramfs-tools/$kind/"
        done
    done
    rmdir "$backup/hooks" "$backup/scripts" "$backup"
}
cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    restore_casper
    exit "$status"
}
trap cleanup EXIT HUP INT TERM

# The single Pop filesystem is also the installer payload. Temporarily hide
# only Casper's initramfs inputs; retain the package database and restore the
# exact files even if generation fails. Distinst later removes the live
# packages and regenerates the installed initramfs on its real target.
for kind in hooks scripts; do
    for member in "$root/usr/share/initramfs-tools/$kind"/casper*; do
        [ -e "$member" ] || [ -L "$member" ] || continue
        mv "$member" "$backup/$kind/"
    done
done
chroot "$root" env -u CASPER_GENERATE_UUID mkinitramfs -o "/boot/initrd.img-$abi" "$abi"
restore_casper
trap - EXIT HUP INT TERM

# Pop's stock hook generates conf/uuid.conf only when this variable is set.
# Do not persist it in /etc: installed kernel updates must never default to
# Casper or regenerate a requirement for installation media.
chroot "$root" env CASPER_GENERATE_UUID=1 mkinitramfs -o /boot/lexr-live-initrd "$abi"
cp "$root/boot/vmlinuz-$abi" /work/casper-vmlinuz
cp "$root/boot/initrd.img-$abi" /work/installed-initrd
mv "$root/boot/lexr-live-initrd" /work/casper-initrd
chmod a+r /work/casper-vmlinuz /work/installed-initrd /work/casper-initrd
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", script, "lexr-pop-initramfs", abi); err != nil {
		return fmt.Errorf("generate separate Pop live and installed initramfs images: %w", err)
	}
	return nil
}
