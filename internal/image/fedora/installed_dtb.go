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
