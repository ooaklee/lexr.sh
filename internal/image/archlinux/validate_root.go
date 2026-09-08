package archlinux

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	userspaceinstall "github.com/ooaklee/lexr.sh/internal/userspace/install"
)

// validateRoot compares the live system with source packages using only trusted
// container tools. No chroot or executable from the ISO runs during validation.
func (v *Validator) validateRoot(ctx context.Context, toolsImage, workspace, volume string, m imagecontract.Manifest) error {
	abi := m.KernelBundle.ABI
	if err := v.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "bash", "-ceu", inspectRootScript, "lexr-arch-inspect", abi); err != nil {
		return err
	}
	for _, pkg := range m.KernelBundle.Packages {
		if pkg.Role != kernel.RoleImage && pkg.Role != kernel.RoleModules {
			continue
		}
		name := "linux-" + string(pkg.Role) + "-" + abi
		if err := v.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "bash", "-ceu", inspectKernelPackageScript, "lexr-arch-package-inspect", "/work/"+pkg.Path, name, m.KernelBundle.Version, abi); err != nil {
			return err
		}
	}
	if err := v.validateEarlyModules(ctx, toolsImage, workspace, volume, abi); err != nil {
		return err
	}
	for _, tree := range m.KernelBundle.DeviceTrees {
		if err := v.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "cmp", "/linux-work/rootfs/"+tree.Path, "/work/arch/aarch64/boot/dtb-"+abi+"/"+tree.Basename); err != nil {
			return err
		}
	}
	for _, pair := range []struct{ path, text string }{
		{"etc/lexr/mkinitcpio-installed.conf", InstalledInitramfsConfig()},
		{"etc/lexr/mkinitcpio-live.conf", LiveInitramfsConfig()},
		{"etc/initcpio/install/lexr_sp11", EarlySupportHook()},
		{"usr/local/bin/lexr-arch-setup", setupScript},
		{"usr/share/lexr/LEXR_GETTING_STARTED.txt", gettingStarted},
		{"usr/local/bin/archinstall", installerLauncher},
		{"usr/share/lexr/archinstall/guided.py", installerGuided},
		{"usr/share/lexr/archinstall/policy.py", installerPolicy},
		{"usr/share/lexr/archinstall/target.py", installerTarget},
	} {
		actual, err := v.Docker.CaptureInWorkspaceVolume(ctx, toolsImage, workspace, volume, "cat", "/linux-work/rootfs/"+pair.path)
		if err != nil {
			return err
		}
		if string(actual) != pair.text {
			return fmt.Errorf("Arch terminal setup differs at %s", pair.path)
		}
	}
	if err := v.validateInstaller(ctx, toolsImage, workspace, volume, abi); err != nil {
		return err
	}
	for _, record := range companionArtifacts(m.CompanionBundle) {
		if err := v.Docker.RunInWorkspaceVolume(ctx, toolsImage, workspace, volume, "bash", "-ceu", `root=/linux-work/rootfs/usr/share/lexr/arch-media
relative=$1
actual="$root/$relative"
test -f "$actual" && test ! -L "$actual"
test "$(realpath "$actual")" = "$actual"
cmp "$actual" "/work/$relative"
`, "lexr-arch-retained-companion", record.Path); err != nil {
			return err
		}
	}
	const boardCopy = `mkdir -p /work/firmware-evidence
for name in board.bin board-2.bin; do
  file=/linux-work/rootfs/usr/lib/firmware/ath12k/WCN7850/hw2.0/$name
  test -f "$file" && test ! -L "$file"
  test "$(realpath "$file")" = "$file"
  test "$(stat -c %s "$file")" -le 16777216
  cp "$file" /work/firmware-evidence/
done
`
	if err := v.Docker.RunWithReadOnlyVolumeAsHostUser(ctx, toolsImage, workspace, volume, "bash", "-ceu", boardCopy); err != nil {
		return err
	}
	database, err := imagecontract.ReadBoundedExtractedFile(workspace, "firmware-evidence/board-2.bin", 16<<20)
	if err != nil {
		return err
	}
	_, expected, err := userspaceinstall.SurfaceWiFiBoard(database)
	if err != nil {
		return err
	}
	actual, err := imagecontract.ReadBoundedExtractedFile(workspace, "firmware-evidence/board.bin", 1<<20)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, expected) {
		return errors.New("Arch Wi-Fi board data does not match the Surface entry")
	}
	return nil
}

// companionArtifacts flattens the validated inventory for retained-copy checks.
func companionArtifacts(record imagecontract.CompanionBundleRecord) []imagecontract.ArtifactRecord {
	if !record.Included {
		return nil
	}
	result := []imagecontract.ArtifactRecord{record.Executable.Artifact, *record.SourceArchive}
	result = append(result, record.Catalogues...)
	result = append(result, record.Licences...)
	for _, bundle := range record.Userspace {
		result = append(result, bundle.Artifacts...)
	}
	return result
}

// inspectRootScript checks terminal services, identity and both initramfs images.
const inspectRootScript = `set -euo pipefail
abi=$1
root=/linux-work/rootfs
(cd /work/arch/aarch64 && sha512sum -c airootfs.sha512)
unsquashfs -no-progress -xattrs-exclude '^trusted\.' -d "$root" /work/arch/aarch64/airootfs.sfs
regular() {
 test -f "$1" && test ! -L "$1"
 test "$(realpath "$1")" = "$1"
}
for file in "boot/vmlinuz-$abi" "boot/initramfs-$abi.img" "usr/lib/modules/$abi/modules.dep" usr/lib/os-release etc/passwd etc/shadow etc/lexr/mkinitcpio-installed.conf etc/lexr/mkinitcpio-live.conf etc/initcpio/install/lexr_sp11 usr/local/bin/lexr-arch-setup usr/share/lexr/LEXR_GETTING_STARTED.txt; do regular "$root/$file"; done
cmp "$root/boot/vmlinuz-$abi" "/work/arch/aarch64/boot/vmlinuz-$abi"
cmp "$root/usr/share/lexr/LEXR_GETTING_STARTED.txt" /work/LEXR_GETTING_STARTED.txt
cmp "$root/home/arch/LEXR_GETTING_STARTED.txt" /work/LEXR_GETTING_STARTED.txt
cmp "$root/usr/share/lexr/arch-media/packages.lock.json" /work/sp11/packages.lock.json
for executable in usr/bin/bash usr/bin/pacman usr/bin/nmcli usr/bin/nmtui usr/bin/lsblk usr/bin/sudo usr/local/bin/lexr-arch-setup; do regular "$root/$executable"; test -x "$root/$executable"; done
grep -qx ID=archarm "$root/usr/lib/os-release"
test "$(readlink "$root/etc/systemd/system/default.target")" = /usr/lib/systemd/system/multi-user.target
test -L "$root/etc/systemd/system/multi-user.target.wants/NetworkManager.service"
test ! -e "$root/etc/pacman.d/gnupg"
test ! -s "$root/etc/machine-id"
test ! -e "$root/var/lib/systemd/random-seed"
test -z "$(find "$root/etc/ssh" -name 'ssh_host_*' -print -quit)"
python3 - "$root" "$abi" <<'PY'
import base64,os,pathlib,struct,sys
root=pathlib.Path(sys.argv[1]);abi=sys.argv[2]
# These capability bytes are carried by the pinned upstream rootfs. Prove that
# archive extraction, compression and validator extraction preserve them.
for name,encoded in (('newuidmap','AQAAAoAAAAAAAAAAAAAAAAAAAAA='),('newgidmap','AQAAAkAAAAAAAAAAAAAAAAAAAAA=')):
 path=root/'usr/bin'/name
 assert path.resolve()==path
 assert os.getxattr(path,'security.capability',follow_symlinks=False)==base64.b64decode(encoded),name
packages={}
for desc in (root/'var/lib/pacman/local').glob('*/desc'):
 assert desc.is_file() and not desc.is_symlink()
 fields={}
 key=None
 for line in desc.read_text().splitlines():
  if line.startswith('%') and line.endswith('%'): key=line.strip('%');fields[key]=[]
  elif key and line: fields[key].append(line)
 packages[fields['NAME'][0]]=fields['VERSION'][0]
assert 'lexr-kernel-sp11' in packages
assert 'linux-aarch64' not in packages
assert not {'gdm','gnome-shell','plasma-desktop','xfce4-session'}.intersection(packages)
for name in ('archinstall','networkmanager','grub','mkinitcpio-archiso','linux-firmware-qcom','arch-install-scripts','wireless-regdb'):
 assert name in packages,name
assert packages['archinstall']=='4.4-1','unsupported Archinstall API'
published=dict(line.split(' ',1) for line in pathlib.Path('/work/sp11/packages.installed').read_text().splitlines())
assert published==packages,'package inventory mismatch'
users=dict((line.split(':')[0],line.split(':')) for line in (root/'etc/passwd').read_text().splitlines())
assert 'alarm' not in users and users['arch'][2]=='1000'
shadow=dict((line.split(':')[0],line.split(':')[1]) for line in (root/'etc/shadow').read_text().splitlines())
assert shadow['arch']=='' and shadow['root'].startswith(('!','*'))
assert (root/'etc/sudoers.d/lexr-live').read_text()=='arch ALL=(ALL:ALL) NOPASSWD: ALL\n'
autologin=(root/'etc/systemd/system/getty@tty1.service.d/autologin.conf').read_text()
assert autologin=='[Service]\nExecStart=\nExecStart=-/usr/bin/agetty --autologin arch --noclear %I $TERM\n'
for name in ('bash','pacman','nmcli','nmtui'):
 data=(root/'usr/bin'/name).read_bytes()[:64]
 assert data[:4]==b'\x7fELF' and struct.unpack_from('<H',data,18)[0]==183,name
for path in (root/'usr/lib/modules').iterdir():
 assert path.name==abi,'unexpected generic kernel module tree'
PY
unmkinitramfs "/work/arch/aarch64/boot/initramfs-$abi.img" /linux-work/live-initrd
unmkinitramfs "$root/boot/initramfs-$abi.img" /linux-work/installed-initrd
regular /linux-work/live-initrd/main/hooks/archiso
test ! -e /linux-work/installed-initrd/main/hooks/archiso
for kind in live installed; do
 initrd=/linux-work/$kind-initrd/main
 regular "$initrd/usr/bin/busybox"
 test ! -e "$initrd/usr/lib/systemd/systemd"
 for relative in qcom/gen70500_gmu.bin qcom/gen70500_sqe.fw ath12k/WCN7850/hw2.0/board.bin ath12k/WCN7850/hw2.0/board-2.bin ath12k/WCN7850/hw2.0/amss.bin ath12k/WCN7850/hw2.0/m3.bin; do
  regular "$root/usr/lib/firmware/$relative"
  regular "$initrd/usr/lib/firmware/$relative"
  cmp "$root/usr/lib/firmware/$relative" "$initrd/usr/lib/firmware/$relative"
 done
done
printf '%s\n' '5dfba247d548cabcb892ffa716e8dc82a345fd54b5dbae46ba523230e7ae37dd  /linux-work/rootfs/usr/lib/firmware/qcom/gen70500_gmu.bin' '05ae89e6dea62268cec3f4abb5d7d6db2c95270ff105fd056f77262e39d2e527  /linux-work/rootfs/usr/lib/firmware/qcom/gen70500_sqe.fw' | sha256sum -c -
mkdir /linux-work/native-kernel-package
bsdtar -xpf /work/sp11/lexr-kernel-sp11.pkg.tar.gz -C /linux-work/native-kernel-package
regular /linux-work/native-kernel-package/.PKGINFO
grep -qx 'pkgname = lexr-kernel-sp11' /linux-work/native-kernel-package/.PKGINFO
grep -qx 'arch = aarch64' /linux-work/native-kernel-package/.PKGINFO
while IFS= read -r -d '' file; do
 relative=${file#/linux-work/native-kernel-package/}
 regular "$root/$relative"
 cmp "$file" "$root/$relative"
done < <(find /linux-work/native-kernel-package/boot /linux-work/native-kernel-package/usr -type f -print0)
`

// inspectKernelPackageScript compares every packaged kernel/module/DTB object.
const inspectKernelPackageScript = `set -euo pipefail
archive=$1
package=$2
version=$3
abi=$4
root=/linux-work/rootfs
test "$(dpkg-deb -f "$archive" Package)" = "$package"
test "$(dpkg-deb -f "$archive" Version)" = "$version"
test "$(dpkg-deb -f "$archive" Architecture)" = arm64
unpacked=/linux-work/package-$package
dpkg-deb -x "$archive" "$unpacked"
while IFS= read -r -d '' file; do
 relative=${file#"$unpacked/"}
 actual="$root/$relative"
 test -f "$actual" && test ! -L "$actual"
 test "$(realpath "$actual")" = "$actual"
 cmp "$file" "$actual"
done < <(find "$unpacked" -type f \( -name '*.ko' -o -name '*.ko.xz' -o -name '*.ko.zst' -o -name '*.dtb' -o -path '*/boot/vmlinuz-*' \) -print0)
`
