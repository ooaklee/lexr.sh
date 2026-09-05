package popos

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// popBootRefreshHelper is the original Python 3 helper embedded into the
// staged installed-system support tree. Pop!_OS ships Python 3 by default.
//
//go:embed pop_boot_refresh.py
var popBootRefreshHelper string

// popBootInitramfsHookName sorts strictly after kernelstub's zz-kernelstub
// post-update hook so kernelstub's own ESP reconciliation always runs first.
const popBootInitramfsHookName = "zzz-lexr-pop-boot-refresh"

// popBootKernelHookName keeps the kernel postinst/postrm lifecycle aligned
// with the generic package hooks while sorting after them deterministically.
const popBootKernelHookName = "zzz-lexr-pop-boot"

// popBootInitramfsHook re-publishes the owned BLS entry at runtime, after
// kernelstub and update-initramfs have reconciled their own state. It runs
// the generic DTB staging helper first, but only for a custom lexr ABI — an
// ordinary stock kernel is skipped. On a mounted installed target a missing
// verified DTB fails the update; deferrable outcomes (75/76 without a mounted
// ESP, or a live root) are recorded, never silently swallowed where a real
// target expects success.
const popBootInitramfsHook = `#!/bin/sh
set -eu

abi=${1:-}
[ -n "$abi" ] || exit 0
case "$abi" in *[!a-z0-9.+-]*|.*) exit 0 ;; esac
[ "${#abi}" -le 127 ] || exit 0

# Only a custom lexr ABI carries generic boot-support state; an ordinary
# stock kernel is skipped entirely before either helper runs.
custom=false
if [ -d "/var/lib/lexr/kernel-boot/$abi" ]; then
	custom=true
elif [ -r /usr/lib/lexr/kernel-build/abi ] && grep -Fqx -- "$abi" /usr/lib/lexr/kernel-build/abi; then
	custom=true
fi
[ "$custom" = true ] || exit 0

# Live image preparation and unmounted targets may defer (0/75/76); on a
# mounted installed target every non-zero outcome fails the update rather
# than being silently swallowed.
esp_mounted=false
if grep -q " /boot/efi vfat " /proc/mounts 2>/dev/null; then
	esp_mounted=true
fi

if [ "$esp_mounted" = true ]; then
	/usr/libexec/lexr/kernel-boot-refresh refresh --root / --abi "$abi" \
		--image "/boot/vmlinuz-$abi" --platform auto --defer-grub
	/usr/libexec/lexr/pop-boot-refresh refresh --root / --abi "$abi" \
		--image "/boot/vmlinuz-$abi" --platform auto --defer-grub
	exit 0
fi

status=0
/usr/libexec/lexr/kernel-boot-refresh refresh --root / --abi "$abi" \
	--image "/boot/vmlinuz-$abi" --platform auto --defer-grub || status=$?
[ "$status" -eq 0 ] || [ "$status" -eq 75 ] || exit "$status"
status=0
/usr/libexec/lexr/pop-boot-refresh refresh --root / --abi "$abi" \
	--image "/boot/vmlinuz-$abi" --platform auto --defer-grub || status=$?
[ "$status" -eq 0 ] || [ "$status" -eq 75 ] || [ "$status" -eq 76 ] || exit "$status"
exit 0
`

// popBootKernelPostInstall reconciles the owned entry whenever a supported
// custom kernel image is installed or upgraded; stock kernels are skipped.
// It reuses the initramfs post-update hook once update-initramfs has produced
// the initrd, so the generic DTB helper and the Pop helper always run in the
// same order and with the same bounded exit-code handling.
const popBootKernelPostInstall = `#!/bin/sh
set -eu

abi=${1:-}
[ -n "$abi" ] || exit 0
case "$abi" in *[!a-z0-9.+-]*|.*) exit 0 ;; esac
[ "${#abi}" -le 127 ] || exit 0

# Defer until update-initramfs has produced the initrd; the initramfs hook
# performs the full reconciliation and handles the stock/custom ABI split.
[ -r "/boot/initrd.img-$abi" ] || exit 0
[ -x "/etc/initramfs/post-update.d/zzz-lexr-pop-boot-refresh" ] || exit 0

exec /etc/initramfs/post-update.d/zzz-lexr-pop-boot-refresh "$abi"
`

// popBootKernelPostRemove removes only the owned entry and artefacts whose
// receipt still matches the unchanged digests, after the generic helper has
// dropped its own per-ABI state.
const popBootKernelPostRemove = `#!/bin/sh
set -eu

abi=${1:-}
[ -n "$abi" ] || exit 0
case "$abi" in *[!a-z0-9.+-]*|.*) exit 0 ;; esac
[ "${#abi}" -le 127 ] || exit 0
maintainer_action=${DEB_MAINT_PARAMS:-}
maintainer_action=${maintainer_action%% *}
case "$maintainer_action" in remove|purge) ;; *) exit 0 ;; esac
if [ -d "/var/lib/lexr/pop-boot" ] && [ ! -d "/var/lib/lexr/kernel-boot/$abi" ]; then
	exec /usr/libexec/lexr/pop-boot-refresh remove --root / --abi "$abi" --defer-grub
fi
exit 0
`

// stageInstalledSupport stages the Pop-specific additive systemd-boot support
// tree under <workspace>/pop-support. No EFI or device-identity state is
// generated here: both exist only on the real installed target, never during
// Docker or chroot image preparation.
func stageInstalledSupport(workspace string) error {
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	directory := filepath.Join(workspace, "pop-support")
	if err := os.MkdirAll(filepath.Join(directory, "initramfs"), 0o755); err != nil {
		return fmt.Errorf("create pop-support staging directory: %w", err)
	}
	files := []struct {
		relative string
		contents string
		mode     os.FileMode
	}{
		{relative: "pop-boot-refresh", contents: popBootRefreshHelper, mode: 0o755},
		{relative: "initramfs/" + popBootInitramfsHookName, contents: popBootInitramfsHook, mode: 0o755},
		{relative: "kernel-postinst", contents: popBootKernelPostInstall, mode: 0o755},
		{relative: "kernel-postrm", contents: popBootKernelPostRemove, mode: 0o755},
	}
	for _, file := range files {
		path := filepath.Join(directory, filepath.FromSlash(file.relative))
		if err := os.WriteFile(path, []byte(file.contents), file.mode); err != nil {
			return fmt.Errorf("stage %s: %w", file.relative, err)
		}
		if err := os.Chmod(path, file.mode); err != nil {
			return fmt.Errorf("stage %s mode: %w", file.relative, err)
		}
	}
	return nil
}

// installInstalledSupport installs the Pop-specific additive systemd-boot
// lifecycle into the extracted root. It never executes either refresh helper
// inside the offline image chroot: automatic device identity is absent there
// and per-ABI device state must not be materialised offline. The runtime
// hooks perform all reconciliation after installation, on the real target.
func installInstalledSupport(ctx context.Context, docker *platform.Docker, image, workspace, volume string, bundle kernel.Bundle) error {
	if !kernelABIPattern.MatchString(bundle.ABI) {
		return fmt.Errorf("invalid Pop installed-support kernel ABI")
	}
	if bundle.EffectiveDTBDelivery != kernel.DTBDeliveryExternalRequired {
		return fmt.Errorf("Pop installed support requires external-required DTB delivery, got %q", bundle.EffectiveDTBDelivery)
	}
	const script = `set -o pipefail
root=/linux-work/rootfs
support=/work/pop-support

# Install the additive runtime helper and lifecycle hooks. Nothing here
# touches the native loader integration, its entries, EFI variables, or device state.
install -d -m 0755 "$root/usr/libexec/lexr" \
	"$root/etc/initramfs/post-update.d" \
	"$root/etc/kernel/postinst.d" \
	"$root/etc/kernel/postrm.d"
install -m 0755 "$support/pop-boot-refresh" "$root/usr/libexec/lexr/pop-boot-refresh"
# Sorts strictly after the native zz post-update hook.
install -m 0755 "$support/initramfs/zzz-lexr-pop-boot-refresh" \
	"$root/etc/initramfs/post-update.d/zzz-lexr-pop-boot-refresh"
install -m 0755 "$support/kernel-postinst" "$root/etc/kernel/postinst.d/zzz-lexr-pop-boot"
install -m 0755 "$support/kernel-postrm" "$root/etc/kernel/postrm.d/zzz-lexr-pop-boot"

# No EFI state and no materialized per-ABI device state may exist after
# offline image preparation: both live only on the real installed target.
test ! -d "$root/boot/efi/EFI/lexr" || {
	echo "image preparation unexpectedly produced ESP state" >&2
	exit 65
}
test ! -d "$root/var/lib/lexr/pop-boot" || {
	echo "image preparation unexpectedly produced pop-boot receipts" >&2
	exit 65
}
`
	arguments := []string{
		"bash", "-ceu", script, "lexr-pop-installed-support", bundle.ABI,
	}
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, arguments...); err != nil {
		return fmt.Errorf("install Pop additive systemd-boot support: %w", err)
	}
	return nil
}
