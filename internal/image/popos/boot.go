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

// diagnosticKernelArguments expose EFI and kernel startup on the Surface's
// system-memory framebuffer before the normal display driver takes over.
const diagnosticKernelArguments = "debug earlycon=efifb,ram efi=debug loglevel=8 ignore_loglevel initcall_debug systemd.unit=multi-user.target plymouth.enable=0 console=tty0"

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
# A failed diagnostic must not select an inherited fallback entry.
unset fallback

insmod part_gpt
insmod iso9660
insmod search
insmod search_fs_file
insmod fdt

search --no-floppy --file --set=iso_root /%[1]s
set root=$iso_root
set menu_color_normal=white/black
set menu_color_highlight=black/light-gray

function lexr_boot_failed {
    echo "Boot stopped: $1"
    echo "Photograph the messages above. Returning to firmware in 60 seconds."
    echo "Press Escape to return sooner."
    sleep --interruptible 60
    exit 1
}

menuentry "Pop!_OS for Surface Pro 11 X1E/OLED (%[4]s, experimental)" {
    terminal_output console
    set gfxpayload=keep
    set debug=
    if ! insmod peimage; then lexr_boot_failed "GRUB EFI loader"; fi
    echo "[1/4] Loading Surface kernel %[4]s"
    if ! linux /%[1]s %[3]s quiet splash console=tty0 ---; then lexr_boot_failed "kernel"; fi
    echo "[2/4] Loading X1E/OLED device tree"
    if ! devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb; then lexr_boot_failed "device tree"; fi
    echo "[3/4] Registering Pop live initramfs"
    if ! initrd /%[2]s; then lexr_boot_failed "initramfs"; fi
    echo "[4/4] Starting kernel through GRUB EFI loader"
    boot
    lexr_boot_failed "kernel returned to GRUB"
}

menuentry "Pop!_OS for Surface Pro 11 X1P/LCD (%[4]s, hardware qualification pending)" {
    terminal_output console
    set gfxpayload=keep
    set debug=
    if ! insmod peimage; then lexr_boot_failed "GRUB EFI loader"; fi
    echo "[1/4] Loading Surface kernel %[4]s"
    if ! linux /%[1]s %[3]s quiet splash console=tty0 ---; then lexr_boot_failed "kernel"; fi
    echo "[2/4] Loading X1P/LCD device tree"
    if ! devicetree /sp11/dtb/x1p64100-microsoft-denali.dtb; then lexr_boot_failed "device tree"; fi
    echo "[3/4] Registering Pop live initramfs"
    if ! initrd /%[2]s; then lexr_boot_failed "initramfs"; fi
    echo "[4/4] Starting kernel through GRUB EFI loader"
    boot
    lexr_boot_failed "kernel returned to GRUB"
}

menuentry "Pop!_OS for Surface Pro 11 X1E/OLED (text diagnostics)" {
    terminal_output console
    set gfxpayload=keep
    set debug=linux,efi,peimage
    if ! insmod peimage; then lexr_boot_failed "GRUB EFI loader"; fi
    echo "[1/4] Loading Surface kernel %[4]s"
    if ! linux /%[1]s %[3]s %[5]s ---; then lexr_boot_failed "kernel"; fi
    echo "[2/4] Loading X1E/OLED device tree"
    if ! devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb; then lexr_boot_failed "device tree"; fi
    echo "[3/4] Registering Pop live initramfs"
    if ! initrd /%[2]s; then lexr_boot_failed "initramfs"; fi
    echo "[4/4] Starting kernel through GRUB EFI loader"
    boot
    lexr_boot_failed "kernel returned to GRUB"
}

menuentry "Pop!_OS for Surface Pro 11 X1E/OLED (firmware loader diagnostics)" {
    terminal_output console
    set gfxpayload=keep
    set debug=linux,efi,peimage
    if ! rmmod peimage; then lexr_boot_failed "selecting firmware EFI loader"; fi
    echo "[1/4] Loading Surface kernel %[4]s"
    if ! linux /%[1]s %[3]s %[5]s ---; then lexr_boot_failed "kernel"; fi
    echo "[2/4] Loading X1E/OLED device tree"
    if ! devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb; then lexr_boot_failed "device tree"; fi
    echo "[3/4] Registering Pop live initramfs"
    if ! initrd /%[2]s; then lexr_boot_failed "initramfs"; fi
    echo "[4/4] Starting kernel through firmware EFI loader"
    boot
    lexr_boot_failed "kernel returned to GRUB"
}

menuentry 'Boot from next volume' {
    exit 1
}
menuentry 'UEFI Firmware Settings' {
    fwsetup
}
`, layout.kernel, layout.initrd, arguments, abi, diagnosticKernelArguments), nil
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
