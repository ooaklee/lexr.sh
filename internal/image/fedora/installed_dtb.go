package fedora

import (
	"fmt"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// installedDTBHook runs after Fedora's 20-grub.install, which does not adjust
// its devicetree field alongside linux/initrd for non-separate /boot layouts.
// Only the selected external-required custom ABI is changed automatically.
func installedDTBHook(bundle kernel.Bundle) string {
	if bundle.EffectiveDTBDelivery != kernel.DTBDeliveryExternalRequired {
		return "#!/usr/bin/bash\n# Embedded kernel DTB selection needs no BLS override.\nexit 0\n"
	}
	return fmt.Sprintf(`#!/usr/bin/bash
set -eu
case "${1:-}:${2:-}" in
    add:%s) exec /usr/lib/lexr/sp11/bind-installed-dtb "$2" ;;
    *) exit 0 ;;
esac
`, bundle.ABI)
}

// rescueDTBHook runs after Fedora's 51-dracut-rescue.install has copied the
// selected EFI kernel and rewritten its BLS entry to the rescue identity.
func rescueDTBHook(bundle kernel.Bundle) string {
	if bundle.EffectiveDTBDelivery != kernel.DTBDeliveryExternalRequired {
		return "#!/usr/bin/bash\n# Embedded rescue kernels select their own DTB.\nexit 0\n"
	}
	return fmt.Sprintf(`#!/usr/bin/bash
set -eu
case "${1:-}:${2:-}" in
    add:%s) exec /usr/lib/lexr/sp11/bind-rescue-dtb ;;
    *) exit 0 ;;
esac
`, bundle.ABI)
}

// bindRescueDTBScript binds Fedora's flat layout=other rescue entry only when
// its EFI image matches the selected RPM-owned kernel. Alternative rescue
// layouts are rejected. The copied DTB survives normal ABI removal.
func bindRescueDTBScript(customABI string) string {
	return fmt.Sprintf(`#!/usr/bin/bash
set -euo pipefail
boot=/boot
module=/usr/lib/modules/%s
machine_id=${KERNEL_INSTALL_MACHINE_ID:-}
if [ -z "$machine_id" ] && [ -f /etc/machine-id ]; then read -r machine_id < /etc/machine-id || :; fi
[ -n "$machine_id" ] || exit 0
[[ "$machine_id" =~ ^[0-9a-f]{32}$ ]] || { echo 'invalid rescue machine ID' >&2; exit 1; }
entry="$boot/loader/entries/$machine_id-0-rescue.conf"
[ -e "$entry" ] || exit 0
test -f "$entry"
test ! -L "$entry"
rescue="$boot/vmlinuz-0-rescue-$machine_id"
test -s "$rescue"
test ! -L "$rescue"
selected="$module/vmlinuz-dtbloader.efi"
test -s "$selected"
test ! -L "$selected"
kernel_relative=$(grub2-mkrelpath "$rescue")
initrd="$boot/initramfs-0-rescue-$machine_id.img"
test -s "$initrd"
test ! -L "$initrd"
initrd_relative=$(grub2-mkrelpath "$initrd")
for relative in "$kernel_relative" "$initrd_relative"; do
    case "$relative" in /*) ;; *) echo 'invalid GRUB-relative rescue path' >&2; exit 1 ;; esac
    case "$relative" in *[[:space:]]*|*'/../'*|*'/./'*|*'//'*) exit 1 ;; esac
done
awk -v kernel="$kernel_relative" -v initrd="$initrd_relative" '
    $1 == "linux" {kernels++; if (NF != 2 || $2 != kernel) bad=1}
    $1 == "initrd" {initrds++; if ($2 != initrd || (NF != 2 && !(NF == 3 && $3 == "$tuned_initrd"))) bad=1}
    END {exit (bad || kernels != 1 || initrds != 1)}' "$entry" || {
    echo 'unsupported rescue BLS layout or mismatched rescue image references' >&2; exit 1;
}
# An existing rescue image may belong to an older or unrelated kernel.
comparison=0
cmp -s "$selected" "$rescue" || comparison=$?
case "$comparison" in
    0) ;;
    1) exit 0 ;;
    *) echo 'cannot compare rescue EFI bytes' >&2; exit "$comparison" ;;
esac
source="$module/dtb/qcom/x1e80100-microsoft-denali-oled.dtb"
test -s "$source"
test ! -L "$source"
for owned in "$selected" "$source"; do
    [ "$(rpm -q --qf '%%{NAME}' --file "$owned")" = lexr-kernel-sp11 ]
done
target="$boot/dtb-0-rescue-$machine_id/qcom/x1e80100-microsoft-denali-oled.dtb"
test ! -L "$boot/dtb-0-rescue-$machine_id"
test ! -L "$boot/dtb-0-rescue-$machine_id/qcom"
test ! -L "$target"
install -d -m 0755 "$boot/dtb-0-rescue-$machine_id/qcom"
install -m 0644 "$source" "$target"
relative=$(grub2-mkrelpath "$target")
case "$relative" in /*) ;; *) echo 'invalid GRUB-relative rescue DTB path' >&2; exit 1 ;; esac
case "$relative" in *[[:space:]]*|*'/../'*|*'/./'*|*'//'*) exit 1 ;; esac
temporary=$(mktemp "$entry.lexr.XXXXXX")
trap 'rm -f -- "$temporary"' EXIT
awk '$1 != "devicetree" {print}' "$entry" > "$temporary"
printf 'devicetree %%s\n' "$relative" >> "$temporary"
chmod --reference="$entry" "$temporary"
mv -f -- "$temporary" "$entry"
cmp "$source" "$target"
grep -Fx "devicetree $relative" "$entry" >/dev/null
if command -v restorecon >/dev/null 2>&1; then restorecon -R "$boot/dtb-0-rescue-$machine_id" "$entry"; fi
`, customABI)
}

// bindInstalledDTBScript binds exactly one BLS entry to RPM-owned X1E bytes.
// Stock fallback restoration calls the same helper only after checking its
// RPM ownership and generating the exact stock ABI's installed initramfs.
func bindInstalledDTBScript(customABI string) string {
	return fmt.Sprintf(`#!/usr/bin/bash
set -euo pipefail
abi=${1:?kernel ABI is required}
case "$abi" in ''|*[!A-Za-z0-9.+_~-]*) echo 'invalid kernel ABI' >&2; exit 1 ;; esac
boot=/boot
source=/usr/lib/modules/%s/dtb/qcom/x1e80100-microsoft-denali-oled.dtb
test -s "$source" && test ! -L "$source"
entry=
for candidate in "$boot"/loader/entries/*-"$abi".conf; do
    [ -f "$candidate" ] && [ ! -L "$candidate" ] || continue
    grep -Fx "version $abi" "$candidate" >/dev/null || continue
    [ -z "$entry" ] || { echo 'ambiguous BLS kernel entry' >&2; exit 1; }
    entry=$candidate
done
[ -n "$entry" ] || { echo 'missing exact-ABI BLS kernel entry' >&2; exit 1; }
target="$boot/dtb-$abi/qcom/x1e80100-microsoft-denali-oled.dtb"
test ! -L "$boot/dtb-$abi"
test ! -L "$boot/dtb-$abi/qcom"
test ! -L "$target"
install -d -m 0755 "$boot/dtb-$abi/qcom"
install -m 0644 "$source" "$target"
relative=$(grub2-mkrelpath "$target")
case "$relative" in /*) ;; *) echo 'invalid GRUB-relative DTB path' >&2; exit 1 ;; esac
case "$relative" in *[[:space:]]*|*'/../'*|*'/./'*|*'//'*) exit 1 ;; esac
temporary=$(mktemp "$entry.lexr.XXXXXX")
trap 'rm -f -- "$temporary"' EXIT
awk '$1 != "devicetree" {print}' "$entry" > "$temporary"
printf 'devicetree %%s\n' "$relative" >> "$temporary"
chmod --reference="$entry" "$temporary"
mv -f -- "$temporary" "$entry"
cmp "$source" "$target"
grep -Fx "devicetree $relative" "$entry" >/dev/null
if command -v restorecon >/dev/null 2>&1; then restorecon -R "$boot/dtb-$abi" "$entry"; fi
`, customABI)
}

// validateExternalProfile limits installed external DTB selection to X1E/OLED.
func validateExternalProfile(bundle kernel.Bundle) error {
	if bundle.EffectiveDTBDelivery != kernel.DTBDeliveryExternalRequired {
		return nil
	}
	required := 0
	for _, tree := range bundle.DeviceTrees {
		if tree.Required {
			required++
			if tree.Device != "surface-pro-11-x1e-oled" {
				return fmt.Errorf("Fedora external-DTB installation is available only for the X1E/OLED profile")
			}
		}
	}
	if required != 1 {
		return fmt.Errorf("Fedora requires exactly one selected external-DTB profile")
	}
	return nil
}
