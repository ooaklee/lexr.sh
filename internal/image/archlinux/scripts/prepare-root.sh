set -euo pipefail
root=/linux-work/rootfs
test ! -e "$root"
mkdir -p "$root" /linux-work/iso /linux-work/kernel-payload
# libarchive restores the numeric ownership, ACLs and capability xattrs carried
# by the authenticated Arch archive on the Linux-native scratch filesystem.
bsdtar --numeric-owner --xattrs --acls -xpf /work/source.tar.gz -C "$root"
grep -qx 'ID=archarm' "$root/usr/lib/os-release"
test -x "$root/usr/bin/pacman"
test -x "$root/usr/bin/mkinitcpio"
for package in /work/kernel/*.deb; do
    case "${package##*/}" in
        linux-image-*|linux-modules-*) dpkg-deb -x "$package" /linux-work/kernel-payload ;;
    esac
done
cp -a /work/packages "$root/lexr-packages"
mount --bind "$root" "$root"
mount -t proc proc "$root/proc"
mount --rbind /dev "$root/dev"
mount --make-rslave "$root/dev"
# Remove the generic kernel only from this new disposable root before package
# upgrade hooks can try host-dependent autodetection for its obsolete preset.
chroot "$root" pacman -Rdd --noconfirm linux-aarch64
chroot "$root" pacman-key --init
chroot "$root" pacman-key --populate archlinuxarm
# Local archive signatures remain required for publisher packages. The generated
# Lexr kernel is installed separately and has its own verified source manifest.
sed -i 's/^LocalFileSigLevel.*/LocalFileSigLevel = Required/' "$root/etc/pacman.conf"
packages=()
for package in "$root"/lexr-packages/*.pkg.tar.xz; do packages+=("${package#"$root"}"); done
chroot "$root" env SYSTEMD_OFFLINE=1 pacman -U --noconfirm "${packages[@]}"
chroot "$root" pacman -Q > /work/packages.installed
for name in networkmanager grub mkinitcpio-archiso linux-firmware-qcom wireless-regdb; do
    chroot "$root" pacman -Q "$name"
done
rm -rf "$root/lexr-packages"
