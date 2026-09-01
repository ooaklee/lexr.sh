package fedora

import "fmt"

// installedBootArguments are safe and required after installation to internal storage.
var installedBootArguments = []string{
	"clk_ignore_unused",
	"pd_ignore_unused",
	"systemd.tpm2_wait=0",
	"soundwire_qcom.sp11_feedback_active_offset2_zero=1",
}

// liveOnlyBootArguments isolate ADSP while Fedora is running from USB media.
var liveOnlyBootArguments = []string{
	"modprobe.blacklist=qcom_q6v5_pas",
	"rd.driver.blacklist=qcom_q6v5_pas",
}

// grubConfig keeps Fedora's marker-based self-location and carries the source
// kernel/initramfs as a recovery path. Every USB-rooted entry blacklists ADSP;
// the installed-system policy deliberately does not.
func grubConfig(abi string) string {
	return fmt.Sprintf(`# Fedora Workstation Live 44, remastered by Lexr.
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

search --file --set=root /boot/0x503d6c7e

set sp11_args="clk_ignore_unused pd_ignore_unused systemd.tpm2_wait=0 soundwire_qcom.sp11_feedback_active_offset2_zero=1"
set usb_safe_args="modprobe.blacklist=qcom_q6v5_pas rd.driver.blacklist=qcom_q6v5_pas"
set live_root="root=live:CDLABEL=%s rd.live.image"

menuentry "Fedora 44 for Surface Pro 11 X1E/OLED (%s)" --class fedora --class gnu-linux --class gnu --class os {
	linux ($root)/boot/aarch64/loader/linux quiet rhgb $live_root $sp11_args $usb_safe_args
	initrd ($root)/boot/aarch64/loader/initrd
}

submenu "Troubleshooting -->" {
	menuentry "Surface Pro 11 X1E/OLED basic graphics" --class fedora --class gnu-linux --class gnu --class os {
		linux ($root)/boot/aarch64/loader/linux quiet rhgb $live_root $basicgfx $sp11_args $usb_safe_args
		initrd ($root)/boot/aarch64/loader/initrd
	}
	menuentry "Fedora 44 stock-kernel fallback for Surface Pro 11 X1E/OLED" --class fedora --class gnu-linux --class gnu --class os {
		linux ($root)/boot/aarch64/loader/linux-fedora quiet rhgb $live_root $sp11_args $usb_safe_args
		devicetree ($root)/sp11/dtb/x1e80100-microsoft-denali-oled.dtb
		initrd ($root)/boot/aarch64/loader/initrd-fedora
	}
	menuentry "Fedora 44 stock-kernel live-only path for Surface Pro 11 X1P/LCD" --class fedora --class gnu-linux --class gnu --class os {
		linux ($root)/boot/aarch64/loader/linux-fedora quiet rhgb $live_root $sp11_args $usb_safe_args
		devicetree ($root)/sp11/dtb/x1p64100-microsoft-denali.dtb
		initrd ($root)/boot/aarch64/loader/initrd-fedora
	}
}
`, SourceVolumeID, abi)
}
