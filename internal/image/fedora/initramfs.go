package fedora

import (
	"strings"

	"github.com/ooaklee/lexr.sh/internal/image/sp11"
)

// earlyDrivers includes platform dependencies absent from ELF dependencies:
// QMI opens QRTR sockets, PMIC GLINK supplies USB role notifications, and the
// display controller needs the panel drivers before taking over EFI output.
// Dracut copies these for normal coldplug; it must not restart a running DSP.
var earlyDrivers = []string{
	"qcom_q6v5_pas", "qrtr", "qrtr_smd", "qcom_pd_mapper",
	"i2c_qcom_geni", "ucsi_glink", "typec_ucsi", "ps883x",
	"usb_storage", "uas", "usbhid", "hid_generic",
	"msm", "panel_samsung_atna33xc20", "panel_edp",
	"surface_aggregator_hub", "ath12k",
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

// initramfsValidationScript checks the actual archive, including dependency
// bytes and builtins, instead of treating a single ABI directory as proof that
// the live root can be discovered. All queried drivers are compiled constants.
func initramfsValidationScript() string {
	return `set -o pipefail
root=/linux-work/rootfs
abi=$1
unpacked=$(chroot "$root" mktemp -d /tmp/lexr-initrd-check.XXXXXX)
trap 'rm -rf -- "$root$unpacked"' EXIT
chroot "$root" /usr/bin/lsinitrd -m "/boot/initramfs-$abi.img" | grep -Fx dmsquash-live
chroot "$root" /usr/bin/bash -ceu 'cd "$1"; /usr/bin/lsinitrd --unpack "/boot/initramfs-$2.img"' lexr-unpack "$unpacked" "$abi"
initrd="$root$unpacked"
drivers="` + strings.Join(earlyDrivers, " ") + `"
if modinfo --basedir "$root" --set-version "$abi" ath12k_pci >/dev/null 2>&1; then drivers+=" ath12k_pci"; fi
for driver in $drivers; do
    dependencies=$(modprobe --dirname "$root" --set-version "$abi" --ignore-install --show-depends "$driver")
    [ -n "$dependencies" ]
    while read -r kind module extra; do
        case "$kind" in
            builtin) continue ;;
            insmod) [ -z "$extra" ] ;;
            *) echo "lexr: invalid dependency record for $driver" >&2; exit 1 ;;
        esac
        case "$module" in
            "$root/lib/modules/$abi/"*|"$root/usr/lib/modules/$abi/"*) ;;
            *) echo 'lexr: module dependency escapes the selected ABI' >&2; exit 1 ;;
        esac
        relative=${module#"$root/"}
        case "$relative" in *'/../'*|*'/./'*|*'//'*) exit 1 ;; esac
        test -s "$initrd/$relative"
        cmp "$module" "$initrd/$relative"
    done <<< "$dependencies"
done
for firmware in ` + strings.Join(earlyFirmware(), " ") + `; do
    found=
    for suffix in '' .xz .zst; do
        source="$root/usr/lib/firmware/$firmware$suffix"
        if [ -s "$source" ]; then
            test -s "$initrd/usr/lib/firmware/$firmware$suffix"
            cmp "$source" "$initrd/usr/lib/firmware/$firmware$suffix"
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
