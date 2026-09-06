package elementary

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/caspermedia"
	"github.com/ooaklee/lexr.sh/internal/image/sp11"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	userspaceinstall "github.com/ooaklee/lexr.sh/internal/userspace/install"
)

// validateRoot inspects the actual installer payload without executing any
// program supplied by the image. dpkg-query and archive tools are container
// tools, and all Linux extraction remains in the private work volume.
func (v *Validator) validateRoot(ctx context.Context, toolsImage, workspace, volume string, manifest imagecontract.Manifest, contract caspermedia.Contract) error {
	abi := manifest.KernelBundle.ABI
	const extract = `set -o pipefail
unsquashfs -no-progress -xattrs-exclude '^trusted\.' -d /linux-work/rootfs /work/live/filesystem.squashfs
root=/linux-work/rootfs
abi=$1
regular() {
    path=$1
    test -s "$path" && test -f "$path" && test ! -L "$path"
    test "$(realpath "$path")" = "$path"
}
regular "$root/boot/vmlinuz-$abi"
regular "$root/boot/initrd.img-$abi"
cmp "$root/boot/vmlinuz-$abi" /work/live/vmlinuz
regular "$root/var/lib/dpkg/status"
dpkg-query --admindir="$root/var/lib/dpkg" -W -f='${Package} ${Version}\n' > /work/actual.manifest
cmp /work/live/filesystem.manifest /work/actual.manifest
test -x "$root/usr/bin/io.elementary.installer"
test -x "$root/usr/sbin/update-grub"
grep -qx ID=elementary "$root/usr/lib/os-release"
test "$(readlink "$root/vmlinuz")" = "boot/vmlinuz-$abi"
test "$(readlink "$root/initrd.img")" = "boot/initrd.img-$abi"
test "$(readlink "$root/boot/vmlinuz")" = "vmlinuz-$abi"
test "$(readlink "$root/boot/initrd.img")" = "initrd.img-$abi"
regular "$root/boot/dtb-$abi"
test -d "$root/var/lib/lexr/kernel-boot/$abi"
test ! -e "$root/boot/efi/EFI/lexr"
test ! -e "$root/boot/lexr-live-initrd"
regular "$root/usr/lib/modules/$abi/modules.dep"

unmkinitramfs /work/live/initrd.lz /linux-work/live-initrd
unmkinitramfs "$root/boot/initrd.img-$abi" /linux-work/installed-initrd
lsinitramfs /work/live/initrd.lz > /work/live-initrd.members
lsinitramfs "$root/boot/initrd.img-$abi" > /work/installed-initrd.members
regular /linux-work/live-initrd/main/conf/uuid.conf
cmp /linux-work/live-initrd/main/conf/uuid.conf /work/disk/casper-uuid-generic
regular /linux-work/live-initrd/main/scripts/casper
`
	if err := v.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "bash", "-ceu", extract, "lexr-elementary-validate-root", abi); err != nil {
		return err
	}
	// Root extraction needs Linux file ownership; evidence copied to the host
	// must instead belong to the caller so validation can remove it afterwards.
	const evidence = `set -o pipefail
root=/linux-work/rootfs
regular() {
    path=$1
    test -s "$path" && test -f "$path" && test ! -L "$path"
    test "$(realpath "$path")" = "$path"
}
mkdir /work/root-evidence /work/initrd-firmware
for relative in usr/sbin/update-grub etc/grub.d/10_linux etc/initramfs-tools/hooks/lexr-sp11-firmware; do
    regular "$root/$relative"
    test -x "$root/$relative"
    (cd "$root" && cp --parents "$relative" /work/root-evidence)
done
regular "$root/etc/default/grub.d/99-surface-pro-11.cfg"
(cd "$root" && cp --parents etc/default/grub.d/99-surface-pro-11.cfg /work/root-evidence)
if [ -f "$root/etc/skel/Desktop/LEXR_GETTING_STARTED.txt" ]; then
    regular "$root/etc/skel/Desktop/LEXR_GETTING_STARTED.txt"
    (cd "$root" && cp --parents etc/skel/Desktop/LEXR_GETTING_STARTED.txt /work/root-evidence)
fi
for relative in usr/lib/firmware/ath12k/WCN7850/hw2.0/board.bin; do
    regular "$root/$relative"
    (cd "$root" && cp --parents "$relative" /work/root-evidence)
done
database="$root/usr/lib/firmware/ath12k/WCN7850/hw2.0/board-2.bin"
output=/work/root-evidence/usr/lib/firmware/ath12k/WCN7850/hw2.0/board-2.bin
if [ -f "$database" ]; then
    regular "$database"
    head -c 16777217 "$database" > "$output"
else
    regular "$database.zst"
    test "$(stat -c %s "$database.zst")" -le 16777216
    timeout 15 zstd -dcq --memory=32MB "$database.zst" | head -c 16777217 > "$output"
fi
test "$(stat -c %s "$output")" -le 16777216
for section in /linux-work/live-initrd/*; do
    test -d "$section" && test ! -L "$section"
    if [ -d "$section/usr/lib/firmware" ]; then
        (cd /linux-work/live-initrd && find "${section##*/}/usr/lib/firmware" -type f -exec cp --parents '{}' /work/initrd-firmware/ \;)
    fi
done
chmod -R a+rX /work/root-evidence /work/initrd-firmware
`
	if err := v.Docker.RunWithReadOnlyVolumeAsHostUser(ctx, toolsImage, workspace, volume, "bash", "-ceu", evidence); err != nil {
		return err
	}
	// Compare all package-owned DTBs, kernel bytes and module objects to the
	// independently extracted archives, not just a claimed ABI or directory.
	for _, pkg := range manifest.KernelBundle.Packages {
		if pkg.Path == "" {
			continue
		}
		name, architecture := "linux-"+string(pkg.Role)+"-"+abi, "arm64"
		if pkg.Role == kernel.RoleBootSupport {
			name, architecture = "lexr-kernel-boot-support", "all"
		}
		const script = `set -o pipefail
archive=$1
package=$2
version=$3
architecture=$4
root=/linux-work/rootfs
test "$(dpkg-deb -f "$archive" Package)" = "$package"
test "$(dpkg-deb -f "$archive" Version)" = "$version"
test "$(dpkg-deb -f "$archive" Architecture)" = "$architecture"
test "$(dpkg-query --admindir="$root/var/lib/dpkg" -W -f='${Status}|${Version}|${Architecture}' "$package")" = "install ok installed|$version|$architecture"
unpacked=/linux-work/package-$package
dpkg-deb -x "$archive" "$unpacked"
while IFS= read -r -d '' file; do
    relative=${file#"$unpacked/"}
    test -f "$root/$relative" && test ! -L "$root/$relative"
    resolved=$(realpath "$root/$relative")
    case "$resolved" in "$root/"*) ;; *) exit 65 ;; esac
    cmp "$file" "$root/$relative"
done < <(find "$unpacked" -type f \( -name '*.ko' -o -name '*.ko.xz' -o -name '*.ko.zst' -o -name '*.dtb' -o -path '*/boot/vmlinuz-*' -o -path '*/usr/libexec/lexr/*' -o -path '*/etc/kernel/*' \) -print0)
`
		if err := v.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "bash", "-ceu", script, "lexr-elementary-package-validation", "/work/"+pkg.Path, name, manifest.KernelBundle.Version, architecture); err != nil {
			return err
		}
	}
	for _, tree := range manifest.KernelBundle.DeviceTrees {
		if err := v.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "cmp", "/linux-work/rootfs/"+tree.Path, "/work/sp11/dtb/"+tree.Basename); err != nil {
			return err
		}
	}
	for _, member := range []struct{ path, expected string }{
		{"etc/default/grub.d/99-surface-pro-11.cfg", installedGrubDefaults},
		{"etc/initramfs-tools/hooks/lexr-sp11-firmware", sp11.LiveFirmwareHook()},
	} {
		data, err := imagecontract.ReadBoundedExtractedFile(workspace, "root-evidence/"+member.path, 1<<20)
		if err != nil {
			return err
		}
		if string(data) != member.expected {
			return fmt.Errorf("elementary installed support differs at %s", member.path)
		}
	}
	for _, native := range []struct{ path, digest string }{
		{"usr/sbin/update-grub", inspectedUpdateGRUBSHA256},
		{"etc/grub.d/10_linux", inspectedLinuxGeneratorSHA256},
	} {
		data, err := imagecontract.ReadBoundedExtractedFile(workspace, "root-evidence/"+native.path, 1<<20)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != native.digest {
			return fmt.Errorf("native elementary GRUB support changed at %s", native.path)
		}
	}
	if manifest.CompanionBundle.Included {
		data, err := imagecontract.ReadBoundedExtractedFile(workspace, "root-evidence/etc/skel/Desktop/LEXR_GETTING_STARTED.txt", 64<<10)
		if err != nil {
			return err
		}
		if string(data) != liveGettingStarted {
			return errors.New("elementary desktop guide differs from the supported commands")
		}
	}
	for _, kind := range []string{"live", "installed"} {
		listing, err := imagecontract.ReadBoundedExtractedFile(workspace, kind+"-initrd.members", 4<<20)
		if err != nil {
			return err
		}
		if err := validateInitrdMembers(string(listing), abi, kind == "live"); err != nil {
			return err
		}
	}
	marker, err := imagecontract.ReadBoundedExtractedFile(workspace, "disk/casper-uuid-generic", 64)
	if err != nil {
		return err
	}
	actualUUID, err := caspermedia.ParseUUID(marker)
	if err != nil || actualUUID != contract.UUID {
		return errors.New("elementary media UUID differs from its recorded discovery contract")
	}
	database, err := imagecontract.ReadBoundedExtractedFile(workspace, "root-evidence/usr/lib/firmware/ath12k/WCN7850/hw2.0/board-2.bin", 16<<20)
	if err != nil {
		return err
	}
	_, expectedBoard, err := userspaceinstall.SurfaceWiFiBoard(database)
	if err != nil {
		return err
	}
	board, err := imagecontract.ReadBoundedExtractedFile(workspace, "root-evidence/usr/lib/firmware/"+sp11.WiFiBoard, 1<<20)
	if err != nil {
		return err
	}
	if !bytes.Equal(board, expectedBoard) {
		return errors.New("elementary Wi-Fi board does not match the distribution's SP11 data")
	}
	firmwareRoot := filepath.Join(workspace, "initrd-firmware")
	if err := sp11.ValidateLiveGPUFirmware(firmwareRoot, abi); err != nil {
		return err
	}
	sections, err := sp11.InitramfsSections(firmwareRoot)
	if err != nil {
		return err
	}
	for _, input := range firmwareInputs {
		if input.Path != "qcom/gen70500_gmu.bin" && input.Path != "qcom/gen70500_sqe.fw" {
			continue
		}
		if err := validateFirmwareCopies(firmwareRoot, sections, abi, input.Path, input.SHA256, input.Size); err != nil {
			return err
		}
	}
	return validateFirmwareCopies(firmwareRoot, sections, abi, sp11.WiFiBoard, fmt.Sprintf("%x", sha256.Sum256(expectedBoard)), int64(len(expectedBoard)))
}

// validateInitrdMembers requires exact-ABI modules and distinguishes Casper's
// live image from the installed-system image that must boot without the USB.
func validateInitrdMembers(listing, abi string, live bool) error {
	modules, casper, uuid := false, false, false
	for _, line := range strings.Split(listing, "\n") {
		member := strings.TrimPrefix(strings.TrimSpace(line), "./")
		if member == "scripts/casper" {
			casper = true
		}
		if member == "conf/uuid.conf" {
			uuid = true
		}
		if !live && (strings.HasPrefix(member, "scripts/casper") || member == "conf/conf.d/default-boot-to-casper.conf" || member == "conf/uuid.conf") {
			return errors.New("installed elementary initramfs still contains Casper live-boot configuration")
		}
		for _, prefix := range []string{"lib/modules/", "usr/lib/modules/"} {
			if !strings.HasPrefix(member, prefix) {
				continue
			}
			relative := strings.TrimPrefix(member, prefix)
			if relative == "" {
				continue
			}
			if relative != abi && !strings.HasPrefix(relative, abi+"/") {
				return errors.New("elementary initramfs contains modules from another kernel ABI")
			}
			if strings.HasSuffix(relative, ".ko") || strings.HasSuffix(relative, ".ko.zst") || strings.HasSuffix(relative, ".ko.xz") {
				modules = true
			}
		}
	}
	if !modules || live && (!casper || !uuid) {
		return errors.New("elementary initramfs lacks its required exact-ABI modules or live Casper inputs")
	}
	return nil
}
