set -euo pipefail
root=/linux-work/rootfs
mkdir -p "$root/etc/lexr" "$root/etc/initcpio/install" "$root/usr/share/lexr/arch-media"
install -m 0644 /work/installed.conf "$root/etc/lexr/mkinitcpio-installed.conf"
install -m 0644 /work/live.conf "$root/etc/lexr/mkinitcpio-live.conf"
install -m 0644 /work/lexr_sp11 "$root/etc/initcpio/install/lexr_sp11"
cp -a /work/sp11 "$root/usr/share/lexr/arch-media/"
install -m 0644 /work/packages.lock.json "$root/usr/share/lexr/arch-media/packages.lock.json"
install -m 0644 /work/LEXR_GETTING_STARTED.txt "$root/usr/share/lexr/LEXR_GETTING_STARTED.txt"
mount --bind "$root" "$root"
mount -t proc proc "$root/proc"
mount --rbind /dev "$root/dev"
mount --make-rslave "$root/dev"
# Remove the generic archive's known account. This is only a new live root.
if chroot "$root" id alarm >/dev/null 2>&1; then chroot "$root" userdel -r alarm; fi
chroot "$root" passwd -l root
chroot "$root" useradd -m -u 1000 -G wheel -s /bin/bash arch
chroot "$root" passwd -d arch
printf 'arch ALL=(ALL:ALL) NOPASSWD: ALL\n' > "$root/etc/sudoers.d/lexr-live"
chmod 0440 "$root/etc/sudoers.d/lexr-live"
printf 'lexr-arch\n' > "$root/etc/hostname"
printf 'en_GB.UTF-8 UTF-8\nen_US.UTF-8 UTF-8\n' > "$root/etc/locale.gen"
chroot "$root" locale-gen
printf 'LANG=en_GB.UTF-8\n' > "$root/etc/locale.conf"
install -m 0644 /work/LEXR_GETTING_STARTED.txt "$root/home/arch/LEXR_GETTING_STARTED.txt"
install -m 0755 /work/lexr-arch-setup "$root/usr/local/bin/lexr-arch-setup"
chroot "$root" chown -R arch:arch /home/arch
mkdir -p "$root/etc/systemd/system/getty@tty1.service.d"
printf '[Service]\nExecStart=\nExecStart=-/usr/bin/agetty --autologin arch --noclear %%I $TERM\n' > "$root/etc/systemd/system/getty@tty1.service.d/autologin.conf"
printf '\nWelcome to Arch Linux ARM for Surface Pro 11.\nRun lexr-arch-setup for networking, kernel checks and setup guidance.\nThis live session is temporary; no desktop is preselected.\n\n' > "$root/etc/motd"
systemctl --root="$root" enable NetworkManager.service getty@tty1.service
systemctl --root="$root" set-default multi-user.target
# The rootfs archive enables services for a generic board. NetworkManager owns
# networking here, and remote login is an explicit choice for the live user.
systemctl --root="$root" disable sshd.service systemd-networkd.service systemd-networkd-wait-online.service systemd-resolved.service dhcpcd.service 2>/dev/null || true
# A fresh live session must create its own machine identity and private trust
# material. Never ship the build container's keyring private keys or random seed.
rm -rf "$root/etc/pacman.d/gnupg" "$root/var/cache/pacman/pkg" "$root/var/log"/*
rm -f "$root/etc/machine-id" "$root/var/lib/dbus/machine-id" "$root/var/lib/systemd/random-seed"
: > "$root/etc/machine-id"
rm -f "$root/etc/resolv.conf"
ln -s /run/NetworkManager/resolv.conf "$root/etc/resolv.conf"
printf 'Server = https://ca.us.mirror.archlinuxarm.org/$arch/$repo\n' > "$root/etc/pacman.d/mirrorlist"
# The live USB has no SSH server enabled and carries no authorisation keys.
find "$root/etc/ssh" -maxdepth 1 -name 'ssh_host_*' -delete 2>/dev/null || true
