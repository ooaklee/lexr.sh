package fedora

import (
	"strings"

	"github.com/ooaklee/lexr.sh/internal/image/sp11"
)

// earlyDrivers includes platform dependencies absent from ELF dependencies:
// QMI opens QRTR sockets, PMIC GLINK supplies USB role notifications, and the
// display controller needs the panel drivers before taking over EFI output.
// The SSAM keyboard also needs its DT UART parent and platform client registry,
// neither of which is an ELF dependency of the aggregator hub.
// Dracut copies these for normal coldplug; it must not restart a running DSP.
var earlyDrivers = []string{
	"qcom_q6v5_pas", "qrtr", "qrtr_smd", "qcom_pd_mapper",
	"i2c_qcom_geni", "ucsi_glink", "typec_ucsi", "ps883x",
	"usb_storage", "uas", "usbhid", "hid_generic",
	"msm", "panel_samsung_atna33xc20", "panel_edp",
	"qcom_geni_serial", "surface_aggregator_registry", "surface_aggregator_hub", "ath12k",
}

// dracutConfiguration is owned by the native kernel RPM so installed kernel
// updates retain the same early hardware dependencies. GPU firmware names are
// requested at runtime and are absent from msm's MODULE_FIRMWARE metadata.
func dracutConfiguration(abi string) string {
	return `# Surface Pro 11 dependencies for normal udev coldplug.
# Leave distribution-kernel initramfs generation to its own module policy.
if [[ "${kernel:-}" == "` + abi + `" ]]; then
add_drivers+=" ` + strings.Join(earlyDrivers, " ") + ` "
if modinfo -k "$kernel" ath12k_pci >/dev/null 2>&1; then
    add_drivers+=" ath12k_pci "
fi
for lexr_firmware in ` + strings.Join(earlyFirmware(), " ") + `; do
    lexr_found=
    for lexr_suffix in '' .xz .zst; do
        lexr_path="/usr/lib/firmware/$lexr_firmware$lexr_suffix"
        if [ -s "$lexr_path" ]; then
            install_items+=" $lexr_path "
            lexr_found=1
            break
        fi
    done
    if [ -z "$lexr_found" ]; then
        echo "lexr: required Surface firmware is missing: $lexr_firmware" >&2
        exit 1
    fi
done
unset lexr_firmware lexr_suffix lexr_path lexr_found
fi
`
}

// initramfsValidationScript checks both the root's expected module closure and
// the archive's shipped lookup data, including built-in records. It never
// regenerates archive indexes: doing so could repair the defect being checked.
// All queried drivers are compiled constants.
func initramfsValidationScript() string {
	return `set -o pipefail
root=/linux-work/rootfs
abi=$1
unpacked=$(chroot "$root" mktemp -d /tmp/lexr-initrd-check.XXXXXX)
trap 'rm -rf -- "$root$unpacked"' EXIT
chroot "$root" /usr/bin/lsinitrd -m "/boot/initramfs-$abi.img" | grep -Fx dmsquash-live
chroot "$root" /usr/bin/bash -ceu 'cd "$1"; /usr/bin/lsinitrd --unpack "/boot/initramfs-$2.img"' lexr-unpack "$unpacked" "$abi"
initrd="$root$unpacked"
for metadata in modules.dep modules.dep.bin modules.alias modules.alias.bin modules.builtin modules.builtin.bin; do
    file="$initrd/usr/lib/modules/$abi/$metadata"
    if [ ! -f "$file" ] || [ -L "$file" ] || { [[ "$metadata" == *.bin ]] && [ ! -s "$file" ]; }; then
        echo "lexr: initramfs module metadata is missing or unsafe: $metadata" >&2
        exit 1
    fi
done
module_closure() {
    local view=$1 driver=$2 dependencies kind module extra relative
    dependencies=$(modprobe --dirname "$view" --set-version "$abi" -C /dev/null --ignore-install --show-depends "$driver") || return 1
    [ -n "$dependencies" ] || { echo "lexr: empty module closure for $driver" >&2; return 1; }
    while read -r kind module extra; do
        [ -z "$extra" ] || { echo "lexr: invalid dependency record for $driver" >&2; return 1; }
        case "$kind" in
            builtin)
                [[ "$module" =~ ^[a-zA-Z0-9_+-]+$ ]] || return 1
                printf 'builtin %s\n' "${module//-/_}"
                ;;
            insmod)
                case "$module" in
                    "$view/lib/modules/$abi/"*) relative=${module#"$view/lib/modules/$abi/"} ;;
                    "$view/usr/lib/modules/$abi/"*) relative=${module#"$view/usr/lib/modules/$abi/"} ;;
                    *) echo 'lexr: module dependency escapes the selected ABI' >&2; return 1 ;;
                esac
                [[ "$relative" =~ ^[a-zA-Z0-9_./+-]+$ ]] || return 1
                case "/$relative" in *'/../'*|*'/./'*|*'//'*) return 1 ;; esac
                case "$relative" in *.ko|*.ko.xz|*.ko.zst) ;; *) return 1 ;; esac
                printf 'insmod %s\n' "$relative"
                ;;
            *) echo "lexr: invalid dependency record for $driver" >&2; return 1 ;;
        esac
    done <<< "$dependencies"
}
drivers="` + strings.Join(earlyDrivers, " ") + `"
if modinfo --basedir "$root" --set-version "$abi" ath12k_pci >/dev/null 2>&1; then drivers+=" ath12k_pci"; fi
for driver in $drivers; do
    expected=$(module_closure "$root" "$driver" | LC_ALL=C sort -u)
    actual=$(module_closure "$initrd" "$driver" | LC_ALL=C sort -u) || {
        echo "lexr: initramfs cannot resolve required driver $driver" >&2; exit 1;
    }
    [ "$actual" = "$expected" ] || {
        echo "lexr: initramfs module closure differs for $driver" >&2; exit 1;
    }
    while read -r kind relative; do
        [ "$kind" != builtin ] || continue
        source="$root/usr/lib/modules/$abi/$relative"
        target="$initrd/usr/lib/modules/$abi/$relative"
        if [ ! -s "$target" ] || [ -L "$target" ]; then
            echo "lexr: initramfs module is missing or unsafe: $relative" >&2
            exit 1
        fi
        cmp "$source" "$target" || {
            echo "lexr: initramfs module bytes differ: $relative" >&2; exit 1;
        }
    done <<< "$expected"
done
for firmware in ` + strings.Join(earlyFirmware(), " ") + `; do
    found=
    for suffix in '' .xz .zst; do
        source="$root/usr/lib/firmware/$firmware$suffix"
        if [ -s "$source" ]; then
            test -s "$initrd/usr/lib/firmware/$firmware$suffix" || {
                echo "lexr: initramfs firmware is missing: $firmware$suffix" >&2; exit 1;
            }
            cmp "$source" "$initrd/usr/lib/firmware/$firmware$suffix" || {
                echo "lexr: initramfs firmware bytes differ: $firmware$suffix" >&2; exit 1;
            }
            found=1
            break
        fi
    done
    [ -n "$found" ] || { echo "lexr: missing early firmware $firmware" >&2; exit 1; }
done
`
}

// earlyFirmware includes public source firmware and the derived Wi-Fi board
// before the first GPU or WCN7850 probe, including compressed source variants.
func earlyFirmware() []string {
	return append(append([]string(nil), sp11.LiveGPUFirmware...), sp11.WiFiBoard,
		"ath12k/WCN7850/hw2.0/board-2.bin", "ath12k/WCN7850/hw2.0/amss.bin", "ath12k/WCN7850/hw2.0/m3.bin")
}
