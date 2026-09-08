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

The first supported layout uses an existing GPT disk with an existing FAT EFI
System Partition (ESP) and at least 16 GiB of **unallocated space**. Allow more
space for your intended software. Prepare that space before opening the
installer; this flow does not shrink or replace an existing OS partition.

In **Disk configuration → Manual Partitioning**:

1. Select the disk containing the ESP and your unallocated space.
2. Create one ext4 partition in the free space and assign `/`.
3. Assign `/boot/efi` to the existing FAT ESP. Retain its existing status and
   EFI flag; leave formatting disabled.
4. Leave all other existing partitions unchanged and without a mountpoint.
5. Leave encryption disabled. Separate `/boot`, LVM, encryption and formatting
   existing partitions are outside this first supported layout.

The Install preview explains invalid selections. Lexr checks the real partition
table and active mounts again before modifying the disk. It refuses whole-disk
wiping, changes to existing partitions, overlapping space and an existing
`EFI/LexrArch` directory. The shared ESP needs at least 32 MiB free.

## Choose the rest of your system

Set your language, keyboard, locale, hostname, timezone and user account in the
normal menus. Create a user with sudo access. Leave **Profile** unset or select
**Minimal** for a terminal installation. Optional environments remain available;
no desktop is preselected. Graphical profiles use Surface Adreno/Mesa.

Keep **NetworkManager** for networking. You can reconnect with `sudo nmtui` after
installation. Selecting **Copy ISO configuration** explicitly copies the live
NetworkManager connections, including saved Wi-Fi credentials, to the new root.

Lexr fixes the kernel to `lexr-kernel-sp11`, the bootloader to GRUB and the
repositories to Arch Linux ARM. It installs matching modules, DTBs, public
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
with `--dry-run`; real installation retains the confirmation screen.

## First boot

Choose **Exit** after installation to read the boot information. Lexr installs
ARM64 GRUB at `EFI/LexrArch`, preserves the existing EFI files and BootOrder,
and creates a separate **Lexr Arch Linux ARM** firmware entry. Select that entry
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
| Choose a desktop/GPU vendor | Leave the profile unset for a terminal; optional graphics use Adreno/Mesa |
