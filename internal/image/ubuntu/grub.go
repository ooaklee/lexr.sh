package ubuntu

import (
	"fmt"
	"slices"
	"strings"
)

// liveKernelArguments are required by the explicitly selected Surface profiles.
// Keep them on each linux command: firmware detection and GRUB environment
// variables must not decide whether clocks and power domains survive live boot.
const liveKernelArguments = "boot=casper clk_ignore_unused pd_ignore_unused arm64.nopauth systemd.tpm2_wait=0 modprobe.blacklist=qcom_q6v5_pas"

// grubConfig renders the direct-GRUB menu for both supported Surface Pro 11
// device-tree variants while binding every entry to the supplied kernel ABI.
func grubConfig(abi string) string {
	return fmt.Sprintf(`set timeout=30
set default=0

insmod part_gpt
insmod iso9660
insmod search
insmod search_fs_file
insmod fdt

search --no-floppy --file --set=iso_root /casper/vmlinuz
set root=$iso_root

loadfont unicode

set menu_color_normal=white/black
set menu_color_highlight=black/light-gray

# These entries explicitly select a Snapdragon X Surface device tree.
function surface_firmware_workaround {
  if [ $lockdown != "y" ]; then
    cutmem 0x8800000000 0x8fffffffff
  fi
}

menuentry "Ubuntu for Surface Pro 11 X1E/OLED (%[1]s)" {
    surface_firmware_workaround
    set gfxpayload=keep
    linux /casper/vmlinuz %[2]s --- quiet splash console=tty0
    devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb
    initrd /casper/initrd
}

menuentry "Ubuntu for Surface Pro 11 X1P/LCD (%[1]s, hardware qualification pending)" {
    surface_firmware_workaround
    set gfxpayload=keep
    linux /casper/vmlinuz %[2]s --- quiet splash console=tty0
    devicetree /sp11/dtb/x1p64100-microsoft-denali.dtb
    initrd /casper/initrd
}

menuentry "Ubuntu for Surface Pro 11 X1E/OLED (text diagnostics)" {
    surface_firmware_workaround
    set gfxpayload=keep
    linux /casper/vmlinuz %[2]s debug systemd.unit=multi-user.target plymouth.enable=0 --- console=tty0
    devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb
    initrd /casper/initrd
}

menuentry 'Boot from next volume' {
    exit 1
}
menuentry 'UEFI Firmware Settings' {
    fwsetup
}
`, abi, liveKernelArguments)
}

// validateLiveKernelArguments checks actual kernel commands rather than mere
// occurrences in comments or conditional variable assignments in the menu.
func validateLiveKernelArguments(config string) error {
	commands := 0
	for _, line := range strings.Split(config, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "linux" {
			continue
		}
		commands++
		if len(fields) < 2 || fields[1] != "/casper/vmlinuz" {
			return fmt.Errorf("unexpected live kernel command %q", line)
		}
		for _, argument := range strings.Fields(liveKernelArguments) {
			if !slices.Contains(fields[2:], argument) {
				return fmt.Errorf("live kernel command is missing explicit %s", argument)
			}
		}
	}
	if commands == 0 {
		return fmt.Errorf("GRUB menu contains no live kernel commands")
	}
	return nil
}
