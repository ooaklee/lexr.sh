set -euo pipefail
root=/linux-work/rootfs
abi=$1
boot=/linux-work/iso/arch/aarch64/boot
mkdir -p "$boot/dtb-$abi" /linux-work/iso/boot/grub /linux-work/iso/sp11
mount --bind "$root" "$root"
mount -t proc proc "$root/proc"
mount --rbind /dev "$root/dev"
mount --make-rslave "$root/dev"
chroot "$root" depmod -a "$abi"
chroot "$root" mkinitcpio -c /etc/lexr/mkinitcpio-installed.conf -k "$abi" -g "/boot/initramfs-$abi.img"
chroot "$root" mkinitcpio -c /etc/lexr/mkinitcpio-live.conf -k "$abi" -g /boot/lexr-live-initramfs.img
cp "$root/boot/vmlinuz-$abi" "$boot/vmlinuz-$abi"
mv "$root/boot/lexr-live-initramfs.img" "$boot/initramfs-$abi.img"
for tree in x1e80100-microsoft-denali-oled.dtb x1p64100-microsoft-denali.dtb; do
    cp "$root/usr/lib/firmware/$abi/device-tree/qcom/$tree" "$boot/dtb-$abi/$tree"
done
cp /work/grub.cfg /linux-work/iso/boot/grub/grub.cfg
cp "$root/usr/share/grub/unicode.pf2" /linux-work/iso/boot/grub/unicode.pf2
cp /work/grub.cfg /work/bootstrap.cfg "$root/"
chroot "$root" grub-script-check /grub.cfg
chroot "$root" grub-mkstandalone -O arm64-efi --modules='part_gpt iso9660 search search_fs_file fdt linux normal configfile font gfxterm all_video fwsetup' -o /boot/lexr-BOOTAA64.EFI boot/grub/grub.cfg=/bootstrap.cfg
cp "$root/boot/lexr-BOOTAA64.EFI" /work/BOOTAA64.EFI
rm "$root/boot/lexr-BOOTAA64.EFI" "$root/grub.cfg" "$root/bootstrap.cfg"
chroot "$root" pacman -Q > /work/packages.installed
cp -a /work/sp11/. /linux-work/iso/sp11/
cp /work/packages.lock.json /work/packages.installed /linux-work/iso/sp11/
cp /work/LEXR_GETTING_STARTED.txt /linux-work/iso/
cp "$boot/vmlinuz-$abi" /work/vmlinuz
cp "$boot/initramfs-$abi.img" /work/live-initramfs.img
cp "$root/boot/initramfs-$abi.img" /work/installed-initramfs.img
cp -a "$boot/dtb-$abi" /work/dtbs
