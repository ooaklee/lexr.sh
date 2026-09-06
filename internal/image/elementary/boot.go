package elementary

import (
	"fmt"
	"regexp"
)

// AdapterID identifies elementary's fixed Casper filesystem and ARM64 GRUB contract.
const AdapterID = "elementary-casper"

// kernelABIPattern bounds kernel identities used in generated GRUB commands.
var kernelABIPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,126}$`)

// surfaceKernelArguments preserve clocks, power domains and the live USB path.
const surfaceKernelArguments = "clk_ignore_unused pd_ignore_unused arm64.nopauth systemd.tpm2_wait=0"

// forwardingGRUBConfig makes the source bootstrap's alternate path select the same menu.
const forwardingGRUBConfig = "source /boot/grub/grub.cfg\n"

// espGRUBConfig preserves the source's search for the ISO before selecting its menu.
const espGRUBConfig = "search --set=root --file /.disk/info\nset prefix=($root)/boot/grub\nconfigfile ($root)/boot/grub/grub.cfg\n"

// grubConfig retains elementary's graphical console initialisation and pairs
// every boot choice with a matching kernel, live initramfs and device tree.
func grubConfig(layout sourceLayout, abi string) (string, error) {
	if !kernelABIPattern.MatchString(abi) || layout != (sourceLayout{"casper", "casper/vmlinuz", "casper/initrd.lz"}) {
		return "", fmt.Errorf("invalid elementary boot paths or kernel ABI")
	}
	return fmt.Sprintf(`set timeout=30
set default=0
unset fallback
insmod part_gpt
insmod iso9660
insmod search_fs_file
insmod fdt
search --no-floppy --file --set=root /casper/vmlinuz
set gfxmode=auto
loadfont /boot/grub/unicode.pf2
insmod all_video
insmod gfxterm
terminal_output gfxterm
set menu_color_normal=white/black
set menu_color_highlight=black/light-gray

menuentry "elementary OS for Surface Pro 11 X1E/OLED (%[1]s, experimental)" {
    set gfxpayload=keep
    linux /casper/vmlinuz boot=casper live-media-path=/casper %[2]s quiet splash console=tty0 ---
    devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb
    initrd /casper/initrd.lz
}
menuentry "elementary OS for Surface Pro 11 X1P/LCD (%[1]s, hardware qualification pending)" {
    set gfxpayload=keep
    linux /casper/vmlinuz boot=casper live-media-path=/casper %[2]s quiet splash console=tty0 ---
    devicetree /sp11/dtb/x1p64100-microsoft-denali.dtb
    initrd /casper/initrd.lz
}
menuentry "elementary OS for Surface Pro 11 X1E/OLED (text diagnostics)" {
    terminal_output console
    set gfxpayload=keep
    linux /casper/vmlinuz boot=casper live-media-path=/casper %[2]s debug=vc earlycon=efifb,ram loglevel=4 systemd.unit=multi-user.target plymouth.enable=0 console=tty0 ---
    devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb
    initrd /casper/initrd.lz
}
menuentry "elementary OS for Surface Pro 11 X1E/OLED (firmware display diagnostics)" {
    terminal_output console
    set gfxpayload=keep
    linux /casper/vmlinuz boot=casper live-media-path=/casper %[2]s module_blacklist=msm debug=vc earlycon=efifb,ram loglevel=4 systemd.unit=multi-user.target plymouth.enable=0 console=tty0 ---
    devicetree /sp11/dtb/x1e80100-microsoft-denali-oled.dtb
    initrd /casper/initrd.lz
}
menuentry 'Boot from next volume' {
    exit 1
}
menuentry 'UEFI Firmware Settings' {
    fwsetup
}
`, abi, surfaceKernelArguments), nil
}

// hybridBootArguments binds USB and optical boot to one appended EFI image.
func hybridBootArguments() []string {
	return []string{"xorriso", "-indev", "/work/source.iso", "-outdev", "/work/output.partial.iso",
		"-padding", "0", "-boot_image", "any", "discard", "-boot_image", "any", "partition_offset=16",
		"-append_partition", "2", "0xef", "/work/esp.img", "-boot_image", "any", "appended_part_as=gpt",
		"-boot_image", "any", "efi_path=--interval:appended_partition_2:all::", "-boot_image", "any", "cat_path=/boot.catalog"}
}

// validateGRUBConfig rejects any extra or altered boot choice.
func validateGRUBConfig(config []byte, layout sourceLayout, abi string) error {
	expected, err := grubConfig(layout, abi)
	if err != nil {
		return err
	}
	if string(config) != expected {
		return fmt.Errorf("elementary GRUB configuration differs from its complete boot contract")
	}
	return nil
}
