// Package archlinux builds and validates terminal Arch Linux ARM live media.
package archlinux

import (
	"fmt"
	"regexp"
	"strings"
)

// abiPattern limits kernel identities used in paths and GRUB commands.
var abiPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,126}$`)

// imageIDPattern accepts a fresh 128-bit identifier generated for each image.
var imageIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// rootUUIDPattern accepts canonical ext4 filesystem identifiers.
var rootUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// surfaceArguments retain the clock, power and console policy tested on elementary.
const surfaceArguments = "clk_ignore_unused pd_ignore_unused arm64.nopauth systemd.tpm2_wait=0"

// LiveBoot binds the ISO label, discovery marker and versioned kernel paths.
// ImageID must be generated before assembling the image, then retained in its
// manifest. It is an identity, not an assertion that the image is authentic.
type LiveBoot struct {
	ABI     string
	ImageID string
}

// Validate rejects unsafe boot identities before rendering executable syntax.
func (boot LiveBoot) Validate() error {
	if !abiPattern.MatchString(boot.ABI) || !imageIDPattern.MatchString(boot.ImageID) {
		return fmt.Errorf("Arch live boot requires a safe kernel ABI and a 128-bit image ID")
	}
	return nil
}

// Label returns the ISO9660-compatible volume label used by archiso discovery.
func (boot LiveBoot) Label() (string, error) {
	if err := boot.Validate(); err != nil {
		return "", err
	}
	return "LEXR_ARCH_" + strings.ToUpper(boot.ImageID[:20]), nil
}

// Marker returns the full-identity marker used by the GRUB bootstrap.
func (boot LiveBoot) Marker() (string, error) {
	if err := boot.Validate(); err != nil {
		return "", err
	}
	return "/arch/" + boot.ImageID + ".uuid", nil
}

// GRUBConfig pairs each live boot option with its exact kernel and device tree.
// copytoram=n retains /run/archiso/bootmnt for companion use. The builder must
// calculate airootfs.sha512 after the final squashfs is closed; checksum=y checks
// the media contents at boot but does not authenticate a publisher.
func (boot LiveBoot) GRUBConfig() (string, error) {
	label, err := boot.Label()
	if err != nil {
		return "", err
	}
	marker, err := boot.Marker()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`set timeout=30
set default=0
unset fallback
insmod part_gpt
insmod iso9660
insmod search_fs_file
insmod fdt
search --no-floppy --file --set=root %[1]s
set gfxmode=auto
loadfont /boot/grub/unicode.pf2
insmod all_video
insmod gfxterm
terminal_output gfxterm

menuentry "Arch Linux ARM for Surface Pro 11 X1E/OLED (%[2]s, experimental)" {
    set gfxpayload=keep
    linux /arch/aarch64/boot/vmlinuz-%[2]s arch=aarch64 archisobasedir=arch archisolabel=%[3]s copytoram=n checksum=y cow_spacesize=50%% %[4]s console=tty0
    devicetree /arch/aarch64/boot/dtb-%[2]s/x1e80100-microsoft-denali-oled.dtb
    initrd /arch/aarch64/boot/initramfs-%[2]s.img
}
menuentry "Arch Linux ARM for Surface Pro 11 X1P/LCD (%[2]s, hardware qualification pending)" {
    set gfxpayload=keep
    linux /arch/aarch64/boot/vmlinuz-%[2]s arch=aarch64 archisobasedir=arch archisolabel=%[3]s copytoram=n checksum=y cow_spacesize=50%% %[4]s console=tty0
    devicetree /arch/aarch64/boot/dtb-%[2]s/x1p64100-microsoft-denali.dtb
    initrd /arch/aarch64/boot/initramfs-%[2]s.img
}
menuentry "Arch Linux ARM for Surface Pro 11 X1E/OLED (text diagnostics)" {
    terminal_output console
    set gfxpayload=keep
    linux /arch/aarch64/boot/vmlinuz-%[2]s arch=aarch64 archisobasedir=arch archisolabel=%[3]s copytoram=n checksum=y cow_spacesize=50%% %[4]s earlycon=efifb,ram loglevel=4 systemd.unit=multi-user.target console=tty0
    devicetree /arch/aarch64/boot/dtb-%[2]s/x1e80100-microsoft-denali-oled.dtb
    initrd /arch/aarch64/boot/initramfs-%[2]s.img
}
menuentry "Arch Linux ARM for Surface Pro 11 X1E/OLED (firmware display diagnostics)" {
    terminal_output console
    set gfxpayload=keep
    linux /arch/aarch64/boot/vmlinuz-%[2]s arch=aarch64 archisobasedir=arch archisolabel=%[3]s copytoram=n checksum=y cow_spacesize=50%% %[4]s module_blacklist=msm earlycon=efifb,ram loglevel=4 systemd.unit=multi-user.target console=tty0
    devicetree /arch/aarch64/boot/dtb-%[2]s/x1e80100-microsoft-denali-oled.dtb
    initrd /arch/aarch64/boot/initramfs-%[2]s.img
}
menuentry 'Boot from next volume' {
    exit 1
}
menuentry 'UEFI Firmware Settings' {
    fwsetup
}
`, marker, boot.ABI, label, surfaceArguments), nil
}

// BootstrapConfig selects the menu on this image from an independently built ESP.
func (boot LiveBoot) BootstrapConfig() (string, error) {
	marker, err := boot.Marker()
	if err != nil {
		return "", err
	}
	return "search --no-floppy --file --set=root " + marker + "\nset prefix=($root)/boot/grub\nconfigfile ($root)/boot/grub/grub.cfg\n", nil
}

// InstalledBoot describes the initial ext4-root contract with /boot on that root.
// The shared ESP is separate; kernel updates never place large initrds on it.
type InstalledBoot struct {
	ABI      string
	RootUUID string
	Device   string
}

// GRUBConfig renders explicit, version-bound normal and diagnostic entries. It
// does not discover other OSes or label a generic Arch kernel as Surface recovery.
// Retaining a previous verified ABI is a separate installer lifecycle requirement.
func (boot InstalledBoot) GRUBConfig() (string, error) {
	if !abiPattern.MatchString(boot.ABI) || !rootUUIDPattern.MatchString(boot.RootUUID) {
		return "", fmt.Errorf("Arch installed boot requires a safe kernel ABI and ext4 root UUID")
	}
	var dtb string
	switch boot.Device {
	case "surface-pro-11-x1e-oled":
		dtb = "x1e80100-microsoft-denali-oled.dtb"
	case "surface-pro-11-x1p-lcd":
		dtb = "x1p64100-microsoft-denali.dtb"
	default:
		return "", fmt.Errorf("Arch installed boot requires an explicit Surface Pro 11 variant")
	}
	return fmt.Sprintf(`set timeout=10
set default=0
insmod part_gpt
insmod ext2
insmod search_fs_uuid
insmod fdt
search --no-floppy --fs-uuid --set=root %[1]s

menuentry "Arch Linux ARM for Surface Pro 11 (%[2]s)" {
    linux /boot/vmlinuz-%[2]s root=UUID=%[1]s rootfstype=ext4 rw %[3]s console=tty0
    devicetree /boot/dtb-%[2]s/%[4]s
    initrd /boot/initramfs-%[2]s.img
}
menuentry "Arch Linux ARM for Surface Pro 11 (%[2]s, text diagnostics)" {
    linux /boot/vmlinuz-%[2]s root=UUID=%[1]s rootfstype=ext4 rw %[3]s systemd.unit=multi-user.target console=tty0
    devicetree /boot/dtb-%[2]s/%[4]s
    initrd /boot/initramfs-%[2]s.img
}
`, boot.RootUUID, boot.ABI, surfaceArguments, dtb), nil
}
