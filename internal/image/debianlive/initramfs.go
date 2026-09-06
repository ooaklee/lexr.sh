package debianlive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ooaklee/lexr.sh/internal/image/sp11"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// buildInitramfs creates an installed-system image without live-boot, restores
// the source's live hooks, then generates a separately named live image with
// an explicit media UUID. Only the installed image remains under /boot.
func buildInitramfs(ctx context.Context, docker *platform.Docker, image, workspace, volume, abi string) error {
	if !kernelABIPattern.MatchString(abi) {
		return fmt.Errorf("invalid Debian initramfs kernel ABI")
	}
	if err := os.WriteFile(filepath.Join(workspace, "sp11-firmware-hook"), []byte(sp11.LiveFirmwareHook()), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(workspace, "sp11-module-hook"), []byte(sp11.EarlyModuleHook()), 0o644); err != nil {
		return err
	}
	const script = `set -o pipefail
root=/linux-work/rootfs
abi=$1
backup=/linux-work/lexr-live-backup
test ! -e "$backup"
test ! -L "$backup"
test -f "$root/boot/vmlinuz-$abi"
test -d "$root/usr/lib/modules/$abi"
test -x "$root/usr/sbin/mkinitramfs"
test -f "$root/usr/share/initramfs-tools/hooks/live"
test -f "$root/usr/share/initramfs-tools/scripts/live"
install -D -m 0755 /work/sp11-firmware-hook "$root/etc/initramfs-tools/hooks/lexr-sp11-firmware"
install -D -m 0755 /work/sp11-module-hook "$root/etc/initramfs-tools/hooks/lexr-sp11-modules"
mkdir -p "$backup/hooks" "$backup/scripts"

restore_live() {
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
    restore_live
    exit "$status"
}
trap cleanup EXIT HUP INT TERM

# The single Debian filesystem is also the installer payload. Temporarily hide
# only live-boot's initramfs inputs; retain the package database and restore the
# exact files even if generation fails. Calamares later removes the live
# packages and regenerates the installed initramfs on its real target.
for kind in hooks scripts; do
    for member in "$root/usr/share/initramfs-tools/$kind"/live; do
        [ -e "$member" ] || [ -L "$member" ] || continue
        mv "$member" "$backup/$kind/"
    done
done
chroot "$root" env -u LIVE_GENERATE_UUID mkinitramfs -o "/boot/initrd.img-$abi" "$abi"
restore_live
trap - EXIT HUP INT TERM

# Debian's stock hook generates conf/uuid.conf only when this variable is set.
# Do not persist it in /etc: installed kernel updates must never default to
# live-boot or regenerate a requirement for installation media.
chroot "$root" env LIVE_GENERATE_UUID=1 mkinitramfs -o /boot/lexr-live-initrd "$abi"
cp "$root/boot/vmlinuz-$abi" /work/live-vmlinuz
cp "$root/boot/initrd.img-$abi" /work/installed-initrd
mv "$root/boot/lexr-live-initrd" /work/live-initrd
chmod a+r /work/live-vmlinuz /work/installed-initrd /work/live-initrd
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", script, "lexr-debian-initramfs", abi); err != nil {
		return fmt.Errorf("generate separate Debian live and installed initramfs images: %w", err)
	}
	return nil
}

// unpackInitramfsScript preserves ordered multi-archive sections and wraps a
// single flat archive in main, matching the shared SP11 inspection boundary.
const unpackInitramfsScript = `unpack_image() {
    archive=$1
    unpacked=$2
    if [ -e "$unpacked" ] || [ -L "$unpacked" ]; then
        echo "Refusing an existing initramfs extraction directory" >&2
        return 1
    fi
    unmkinitramfs "$archive" "$unpacked" || return 1
    if [ -e "$unpacked/init" ] || [ -L "$unpacked/init" ]; then
        test -f "$unpacked/init" || return 1
        test ! -L "$unpacked/init" || return 1
        test ! -e "$unpacked/main" || return 1
        test ! -L "$unpacked/main" || return 1
        test ! -e "$unpacked-flat" || return 1
        test ! -L "$unpacked-flat" || return 1
        mv "$unpacked" "$unpacked-flat" || return 1
        mkdir "$unpacked" || return 1
        mv "$unpacked-flat" "$unpacked/main" || return 1
    fi
}
`
