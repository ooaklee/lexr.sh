package popos

import (
	"fmt"
	"regexp"
	"strings"
)

// AdapterID identifies Pop's single-filesystem Casper and systemd-boot contract.
const AdapterID = "pop-casper"

// kernelABIPattern bounds a kernel identity before placing it in a GRUB menu.
var kernelABIPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,126}$`)

// surfaceKernelArguments preserve the SP11 clocks, power domains and Type-C
// USB path used by the live root. In particular the DSP driver is not blocked.
const surfaceKernelArguments = "clk_ignore_unused pd_ignore_unused arm64.nopauth systemd.tpm2_wait=0"

// grubConfig pairs Pop's original live directory with the custom kernel and
// its matching device trees. The source's GRUB 2.12 lacks Concept's cutmem
// command, so the menu uses only commands supported by its own bootloader.
func grubConfig(layout sourceLayout, abi string) (string, error) {
	if !kernelABIPattern.MatchString(abi) || !liveDirectoryPattern.MatchString(layout.liveDirectory) ||
		layout.kernel != layout.member("vmlinuz.efi") || layout.initrd != layout.member("initrd.gz") {
		return "", fmt.Errorf("invalid Pop boot paths or kernel ABI")
	}
	arguments := "boot=casper live-media-path=/" + layout.liveDirectory + " hostname=pop-os username=pop-os noprompt " + surfaceKernelArguments
	return fmt.Sprintf(`set timeout=30
set default=0

insmod part_gpt
insmod iso9660
insmod search
insmod search_fs_file
insmod fdt

search --no-floppy --file --set=iso_root /%[1]s
set root=$iso_root
set menu_color_normal=white/black
set menu_color_highlight=black/light-gray

menuentry "Pop!_OS for Surface Pro 11 X1E/OLED (%[4]s, experimental)" {
    set gfxpayload=keep
    linux /%[1]s %[3]s quiet splash console=tty0 ---
    devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb
    initrd /%[2]s
}

menuentry "Pop!_OS for Surface Pro 11 X1P/LCD (%[4]s, hardware qualification pending)" {
    set gfxpayload=keep
    linux /%[1]s %[3]s quiet splash console=tty0 ---
    devicetree /sp11/dtb/x1p64100-microsoft-denali.dtb
    initrd /%[2]s
}

menuentry "Pop!_OS for Surface Pro 11 X1E/OLED (text diagnostics)" {
    set gfxpayload=keep
    linux /%[1]s %[3]s debug systemd.unit=multi-user.target plymouth.enable=0 console=tty0 ---
    devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb
    initrd /%[2]s
}

menuentry 'Boot from next volume' {
    exit 1
}
menuentry 'UEFI Firmware Settings' {
    fwsetup
}
`, layout.kernel, layout.initrd, arguments, abi), nil
}

// hybridBootArguments constructs both optical and USB EFI boot paths. Pop's
// source has no partition table to replay; its FAT image becomes an appended
// GPT ESP, and the ISO gets a partition-relative filesystem view as well.
// Paths are fixed private-workspace names, never interpolated source commands.
func hybridBootArguments() []string {
	return []string{
		"xorriso", "-indev", "/work/source.iso", "-outdev", "/work/output.partial.iso",
		"-padding", "0",
		"-boot_image", "any", "discard",
		"-boot_image", "any", "partition_offset=16",
		"-append_partition", "2", "0xef", "/work/esp.img",
		"-boot_image", "any", "appended_part_as=gpt",
		"-boot_image", "any", "efi_path=--interval:appended_partition_2:all::",
		"-boot_image", "any", "cat_path=/boot.catalog",
	}
}

// validateGRUBConfig checks the entire generated menu so an extra command,
// missing profile or changed media selector cannot hide behind a valid line.
func validateGRUBConfig(config []byte, layout sourceLayout, abi string) error {
	expected, err := grubConfig(layout, abi)
	if err != nil {
		return err
	}
	if string(config) != expected {
		return fmt.Errorf("Pop GRUB configuration differs from its kernel and live-media contract")
	}
	if strings.Contains(string(config), "qcom_q6v5_pas") {
		return fmt.Errorf("Pop live configuration must not block the DSP driver used by Type-C USB")
	}
	return nil
}
