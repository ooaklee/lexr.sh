package popos

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// popRecoveryRefresh binds Distinst's recovery copies to the original live
// kernel, device trees and redistributable companion files.
//
//go:embed pop_recovery_refresh.py
var popRecoveryRefresh string

// popRecoveryHook runs after kernelstub and the installed-system DTB hook.
const popRecoveryHook = `#!/bin/sh
set -eu
abi=${1:-}
[ -n "$abi" ] || exit 0
case "$abi" in *[!a-z0-9.+-]*|.*) exit 0 ;; esac
[ "${#abi}" -le 127 ] || exit 0
exec /usr/libexec/lexr/pop-recovery-refresh --root / --abi "$abi"
`

// installRecoverySupport retains a small original-media contract and the
// companion in the installer root. Distinst otherwise copies no /sp11 data
// to its native recovery partition; no host-specific firmware is collected.
func installRecoverySupport(ctx context.Context, docker *platform.Docker, toolsImage, workspace, volume string, manifest []byte, included bool) error {
	for name, contents := range map[string][]byte{
		"pop-recovery-refresh":    []byte(popRecoveryRefresh),
		"pop-recovery-hook":       []byte(popRecoveryHook),
		"pop-original-media.json": manifest,
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), contents, 0o644); err != nil {
			return err
		}
	}
	const script = `root=/linux-work/rootfs
media=$root/usr/share/lexr/pop-media
test ! -e "$media"
test ! -L "$media"
install -d -m 0755 "$media/sp11"
install -m 0644 /work/pop-original-media.json "$media/lexr-manifest.json"
cp -a /work/sp11/dtb "$media/sp11/dtb"
if [ "$1" = included ]; then
    cp -a /work/sp11/companion "$media/sp11/companion"
fi
install -D -m 0755 /work/pop-recovery-refresh "$root/usr/libexec/lexr/pop-recovery-refresh"
install -D -m 0755 /work/pop-recovery-hook "$root/etc/initramfs/post-update.d/zzzz-lexr-pop-recovery"
`
	mode := "omitted"
	if included {
		mode = "included"
	}
	return docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "bash", "-ceu", script, "lexr-pop-recovery-support", mode)
}

// validateRecoverySupport proves that the filesystem retained by Distinst
// contains the exact maintained recovery hook, original-media contract and
// companion. Runtime behaviour is covered separately by installer fixtures.
func (v *Validator) validateRecoverySupport(ctx context.Context, toolsImage, workspace, volume string, manifest imagecontract.Manifest) error {
	const script = `root=/linux-work/rootfs
media=$root/usr/share/lexr/pop-media
for file in "$root/usr/libexec/lexr/pop-recovery-refresh" "$root/etc/initramfs/post-update.d/zzzz-lexr-pop-recovery" "$media/lexr-manifest.json"; do
    test -f "$file" && test ! -L "$file" && test "$(realpath "$file")" = "$file"
done
test -x "$root/usr/libexec/lexr/pop-recovery-refresh"
test -x "$root/etc/initramfs/post-update.d/zzzz-lexr-pop-recovery"
cmp "$media/lexr-manifest.json" /work/sp11/lexr-manifest.json
cp "$root/usr/libexec/lexr/pop-recovery-refresh" /work/recovery-helper
cp "$root/etc/initramfs/post-update.d/zzzz-lexr-pop-recovery" /work/recovery-hook
if [ -d "$media/sp11/companion" ]; then
    cp -R --no-dereference --preserve=mode,timestamps "$media/sp11/companion" /work/recovery-companion
fi
diff -r --no-dereference "$media/sp11/dtb" /work/sp11/dtb
`
	if err := v.Docker.RunWithReadOnlyVolumeAsHostUser(ctx, toolsImage, workspace, volume, "bash", "-ceu", script); err != nil {
		return err
	}
	for name, expected := range map[string]string{"recovery-helper": popRecoveryRefresh, "recovery-hook": popRecoveryHook} {
		actual, err := imagecontract.ReadBoundedExtractedFile(workspace, name, 1<<20)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, []byte(expected)) {
			return fmt.Errorf("Pop recovery support differs at %s", name)
		}
	}
	return companion.ValidateDirectory(manifest.CompanionBundle, filepath.Join(workspace, "recovery-companion"))
}
