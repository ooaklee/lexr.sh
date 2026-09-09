package fedora

import (
	"fmt"
	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// installedBootArguments are safe and required after installation to internal storage.
var installedBootArguments = []string{
	"clk_ignore_unused",
	"pd_ignore_unused",
	"arm64.nopauth",
	"systemd.tpm2_wait=0",
	"soundwire_qcom.sp11_feedback_active_offset2_zero=1",
}

// liveOnlyBootArguments never block the DSP driver that supplies USB Type-C
// notifications. Live discovery is recorded separately in the manifest.
var liveOnlyBootArguments = []string{}

// The untouched distribution kernel retains Fedora's documented workaround.
// Custom-kernel USB evidence does not qualify stock remoteproc behaviour.
var stockFallbackArguments = []string{"modprobe.blacklist=qcom_q6v5_pas", "rd.driver.blacklist=qcom_q6v5_pas"}

// grubConfig keeps Fedora's marker-based self-location and carries the source
// kernel/initramfs as a recovery path. Device discovery retains the DSP and
// QRTR services needed by the USB Type-C controllers.
func grubConfig(abi string, delivery kernel.DTBDelivery, layout sourceLayout) string {
	customDTB := ""
	if delivery == kernel.DTBDeliveryExternalRequired {
		customDTB = "\n\tdevicetree ($root)/sp11/dtb/x1e80100-microsoft-denali-oled.dtb"
	}
	return fmt.Sprintf(`# Fedora Workstation Live %[5]s, remastered by Lexr.
set default="0"

function load_video {
	insmod efi_gop
	insmod efi_uga
	insmod video_bochs
	insmod video_cirrus
	insmod all_video
}
set basicgfx="nomodeset"

load_video
set gfxpayload=keep
insmod gzio
insmod part_gpt
insmod ext2

terminal_input console
terminal_output console
set timeout=20
set timeout_style=menu

search --file --set=root %[4]s

set sp11_args="clk_ignore_unused pd_ignore_unused arm64.nopauth systemd.tpm2_wait=0 soundwire_qcom.sp11_feedback_active_offset2_zero=1"
set stock_args="modprobe.blacklist=qcom_q6v5_pas rd.driver.blacklist=qcom_q6v5_pas"
set live_root="root=live:CDLABEL=%[1]s rd.live.image"

menuentry "Fedora %[5]s for Surface Pro 11 X1E/OLED (%[2]s)" --class fedora --class gnu-linux --class gnu --class os {
	linux ($root)/boot/aarch64/loader/linux quiet rhgb $live_root $sp11_args%[3]s
	initrd ($root)/boot/aarch64/loader/initrd
}

submenu "Troubleshooting -->" {
	menuentry "Surface Pro 11 X1E/OLED basic graphics" --class fedora --class gnu-linux --class gnu --class os {
		linux ($root)/boot/aarch64/loader/linux quiet rhgb $live_root $basicgfx $sp11_args%[3]s
		initrd ($root)/boot/aarch64/loader/initrd
	}
	menuentry "Surface Pro 11 X1E/OLED text diagnostics" {
		linux ($root)/boot/aarch64/loader/linux $live_root $sp11_args rd.debug rd.shell loglevel=7 earlycon=efifb,ram console=tty0 systemd.unit=multi-user.target plymouth.enable=0%[3]s
		initrd ($root)/boot/aarch64/loader/initrd
	}
	menuentry "Surface Pro 11 X1E/OLED firmware display diagnostics" {
		linux ($root)/boot/aarch64/loader/linux $live_root $sp11_args module_blacklist=msm rd.debug rd.shell loglevel=7 earlycon=efifb,ram console=tty0 systemd.unit=multi-user.target plymouth.enable=0%[3]s
		initrd ($root)/boot/aarch64/loader/initrd
	}
	menuentry "Fedora %[5]s stock-kernel fallback for Surface Pro 11 X1E/OLED" --class fedora --class gnu-linux --class gnu --class os {
		linux ($root)/boot/aarch64/loader/linux-fedora quiet rhgb $live_root $sp11_args $stock_args
		devicetree ($root)/sp11/dtb/x1e80100-microsoft-denali-oled.dtb
		initrd ($root)/boot/aarch64/loader/initrd-fedora
	}
	menuentry "Fedora %[5]s stock-kernel live-only path for Surface Pro 11 X1P/LCD" --class fedora --class gnu-linux --class gnu --class os {
		linux ($root)/boot/aarch64/loader/linux-fedora quiet rhgb $live_root $sp11_args $stock_args
		devicetree ($root)/sp11/dtb/x1p64100-microsoft-denali.dtb
		initrd ($root)/boot/aarch64/loader/initrd-fedora
	}
}
`, layout.VolumeID, abi, customDTB, layout.Marker, supportedFedoraRelease)
}
