# Install Arch Linux ARM on Surface Pro 11

Boot the Lexr Arch Linux ARM USB, connect with `sudo nmtui`, then run:

```bash
sudo archinstall
```

Option 6 in `lexr-arch-setup` opens the same installer. The USB includes the
reviewed Archinstall 4.4 package and Lexr's Surface integration. Use the bundled
version before updating packages in the live session. The live terminal and
Wi-Fi have been tested on X1E/OLED; installed-system reboot qualification is
still pending in [issue #52](https://github.com/ooaklee/lexr.sh/issues/52).

## Prepare space and select the layout

For an installation alongside other operating systems, prepare unallocated
space first; allow at least 16 GiB and more for your intended software. Do not
use a whole-disk erase layout on a disk containing systems you want to keep.

In **Disk configuration → Manual Partitioning**:

1. Select the disk containing the ESP and your unallocated space.
2. Create an ext4 partition in the free space and assign `/`.
3. Assign `/boot/efi` to the existing FAT ESP. Keep its EFI flag and leave
   formatting disabled.
4. Leave the other OS partitions unchanged.
5. Keep `/boot` on the root filesystem, and leave encryption and LVM disabled.

Archinstall performs the disk operations you select and confirm. Lexr does not
add a separate partition-preservation policy. Its Install preview checks
the Surface boot requirements: an ext4 root, a FAT ESP on GPT at `/boot/efi`,
and GRUB
without UKI, removable fallback or Plymouth. Other layouts need further Surface
boot integration. The boot hook needs 32 MiB free on the ESP and refuses to
replace an existing `EFI/LexrArch` installation.

## Choose the rest of your system

Set your language, keyboard, locale, hostname, timezone and user account in the
normal menus. Create a user with sudo access. Leave **Profile** unset or select
**Minimal** for a terminal installation. Optional environments remain available;
no desktop is preselected. Graphics are fixed to **Qualcomm Adreno (Mesa)**,
including `vulkan-freedreno` so Vulkan dependencies select the Adreno provider.
PC GPU choices from older saved configurations are rejected; start a new
configuration to select the supported graphics option.

Before formatting, Lexr checks the effective ARM repositories and resolves the
selected profile and additional packages, including dependencies, with pacman.
An unavailable or incompatible package stops that attempt before disk changes.
Later application, network and greeter package transactions also check architecture
and Surface compatibility. This does not preflight every later transaction or
guarantee download success or that an optional desktop works on the Surface.
You can also add your preferred environment after the terminal installation.

Keep **NetworkManager** for networking. You can reconnect with `sudo nmtui` after
installation. Lexr uses Archinstall's normal network setup and does not add a
Wi-Fi credential-copying helper. **Copy ISO configuration** is upstream's
iwd/networkd flow and does not copy this image's NetworkManager Wi-Fi profiles.

Lexr fixes the kernel to `lexr-kernel-sp11` and repositories to Arch Linux ARM.
GRUB is preselected in the normal bootloader menu; retain it for Surface boot.
Lexr installs matching modules, DTBs, public
Wi-Fi/GPU firmware and an initramfs for the installed system. The fresh root
receives your selected account; it does not inherit live autologin or
passwordless sudo. Audio and other optional userspaces need separate Arch
qualification.

Review the complete configuration, select **Install**, then confirm it. To
preview without partitioning or installing:

```bash
sudo archinstall --dry-run
```

Saved `--config` and `--creds` files are supported. `--silent` is accepted only
with `--dry-run`; real installation retains the confirmation screen. Saved
configurations may use bundled profiles, but external profile files/URLs,
replacement scripts/plugins and custom commands are outside this reviewed flow.
The dry-run resolves packages against the available live package databases;
normal installation refreshes those databases before showing the menus.

## First boot

Choose **Exit** after installation to read the boot information. Lexr installs
ARM64 GRUB at `EFI/LexrArch`, preserves the EFI files present when its boot hook
starts and the existing BootOrder, and creates a separate **Lexr Arch Linux ARM** firmware entry. Select that entry
in firmware, or use the exact one-time `efibootmgr --bootnext` command printed
by the installer before rebooting. Keep the USB until the installed system has
booted successfully.

After boot:

```bash
uname -r
pacman -Q lexr-kernel-sp11
sudo nmtui
```

The kernel must match the USB's custom ABI. The matching Lexr companion and
source remain at `/usr/share/lexr/arch-media/sp11/companion`. Copy its binary to
your home directory as described in `LEXR_GETTING_STARTED.txt` before running it.

To regenerate this kernel's installed initramfs after a deliberate change:

```bash
sudo mkinitcpio -p lexr-sp11
```

The GRUB entries and DTBs are bound to that ABI. Cross-ABI upgrades and rollback
remain unqualified. Retain the Surface kernel package; a generic `linux-aarch64`
package or generic regenerated GRUB entries do not replace this boot integration.

## Following the Archinstall video

The [step-by-step video](https://www.youtube.com/watch?v=LiG2wMkcrFE) is useful
for the account, locale, profile, timezone and confirmation menus. Follow these
Surface-specific choices where its example differs:

| Video step | Lexr Surface choice |
| --- | --- |
| Download an x86_64 Arch ISO | Boot the Lexr AArch64 image |
| Connect with `iwctl` | Use `sudo nmtui` |
| Choose an x86 mirror region | Keep the Arch Linux ARM mirror |
| Use the best-effort whole-disk layout | Select Manual Partitioning and preserve the existing ESP/OSes |
| Select stock kernel or systemd-boot | Keep Lexr's Surface kernel and GRUB |
| Choose a desktop/GPU vendor | Leave the profile unset for a terminal; customise your environment after installing |
