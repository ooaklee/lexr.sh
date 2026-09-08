# Install Arch Linux ARM on Surface Pro 11

Boot the Lexr Arch Linux ARM USB, connect with `sudo nmtui`, then run:

```bash
sudo archinstall
```

Option 6 in `lexr-arch-setup` opens the same installer. The USB includes the
reviewed Archinstall 4.4 package and Lexr's Surface integration. Use the bundled
version before updating packages in the live session. X1E/OLED live boot,
installation and boot from an internal ext4 root are confirmed with v23. See the
[hardware test record](#hardware-test-record) below and
[issue #52](https://github.com/ooaklee/lexr.sh/issues/52) for the remaining checks.

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

For KDE Plasma, choose the applications you want under **Additional packages**:

| Package | Purpose |
| --- | --- |
| `konsole` | Terminal window; also needed to launch terminal applications such as Vim from Plasma |
| `dolphin` | File manager and directory-opening handler |
| `plasma-nm` | NetworkManager controls in Plasma's system tray |
| `plasma-pa` | Optional volume controls; installing the widget does not configure Surface audio firmware/userspace |

The bundled upstream Plasma profile selects the desktop packages; it does not
explicitly request Konsole or Dolphin. A minimal `plasma-desktop` installation
can also lack the network widget even when NetworkManager is connected.
To add the terminal, file manager and network controls after installation,
log in on **Ctrl+Alt+F3** and run:

```bash
sudo pacman -Syu --needed konsole dolphin plasma-nm
```

Return to your graphical session with **Ctrl+Alt+F1** or **Ctrl+Alt+F2** and retry
the application. If the newly installed network widget does not appear, save
your work and log out and back into Plasma. These are optional desktop package
choices available in the ARM repositories; Lexr does not force a desktop or
replace upstream profile recipes.

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
findmnt -no SOURCE,FSTYPE /
pacman -Q lexr-kernel-sp11
sudo nmtui
```

The kernel must match the USB's custom ABI, and `/` must be the installed ext4
partition rather than the live overlay. In `nmtui`, choose **Activate a connection**
and enter your Wi-Fi password again; live connection credentials are not copied.
If you cannot find a terminal in your chosen desktop, press **Ctrl+Alt+F3**
(with **Fn** if your keyboard requires it) and log in to the text console.

The matching Lexr companion and
source remain at `/usr/share/lexr/arch-media/sp11/companion`. Copy its binary to
your home directory as described in `LEXR_GETTING_STARTED.txt` before running it.

To regenerate this kernel's installed initramfs after a deliberate change:

```bash
sudo mkinitcpio -p lexr-sp11
```

The GRUB entries and DTBs are bound to that ABI. Cross-ABI upgrades and rollback
remain unqualified. Retain the Surface kernel package; a generic `linux-aarch64`
package or generic regenerated GRUB entries do not replace this boot integration.

### GPU firmware and software rendering

The image includes public GPU firmware but does not redistribute the private
Surface GPU firmware `qcdxkmsuc8380.mbn`. During the installed KDE test, KWin
reported `llvmpipe` software rendering and the kernel reported that
`qcom/x1e80100/microsoft/Denali/qcdxkmsuc8380.mbn` could not be loaded.
Installing Mesa or choosing another installer does not supply that file.

The [Windows firmware hand-off](../user-guide/windows-handoff.md) describes
collecting authorised firmware from the same physical Surface. Its application
and the resulting hardware acceleration still need qualification on Arch;
do not treat a working desktop as proof of accelerated graphics.

## Pen and audio after installation

Choose **Applications > Audio > PipeWire** in Archinstall if you want sound.
The installed v23 test already had PipeWire and WirePlumber running: its missing
audio support was the Surface firmware and FullIO configuration, not an x86
sound-server selection. Audio remains optional for terminal installations.

The retained `LEXR_GETTING_STARTED.txt`, available through `lexr-arch-setup`,
contains the full copy-and-run commands, dry-runs and post-install checks.
Lexr's existing portable IPTSD and FullIO releases are shared across distributions;
there is no separate Arch firmware release. Install pen independently with
`userspace pull iptsd` and `userspace install iptsd`, or select `recommended`
for audio plus IPTSD. The guide gives the exact paired cache directories.

The Arch ALSA selector fix in this PR permits only the distribution's exact
`../../Qualcomm/x1e80100/x1e80100.conf` link at
`/usr/share/alsa/ucm2/conf.d/x1e80100/x1e80100.conf`. It preserves the link in the
backup and replaces only that selector, leaving the shared Qualcomm profile
unchanged. Other target links remain rejected. A package update can replace the
selector again, so check audio support after upgrading `alsa-ucm-conf`.

The public audio release does not include private aDSP firmware. Follow the
[Windows hand-off guide](../user-guide/windows-handoff.md) for same-device
collection and reviewed application; Arch application remains under qualification.
A working Ubuntu installation on the same Surface can also hold the device's
existing firmware, but copying it locally is a separate recovery operation,
not an input accepted by `lexr handoff import`. Never manufacture a Windows
hand-off manifest from Linux files or put private firmware into the public ISO.
The updated installed Surface preset includes only its fixed list of locally
present DSP/GPU firmware files and the audio topology before early driver
probing. Missing files remain optional; the live preset does not include this
private set. Rebuild the installed initramfs after firmware changes and reboot
before checking ALSA cards, PipeWire devices, speakers and microphone. Updating
Lexr alone does not replace the older preset on an already-installed candidate.

## Hardware test record

On 2026-09-08, the maintainer completed the bundled `sudo archinstall` flow on a
Surface Pro 11 X1E/OLED and booted the resulting installation from the internal
NVMe disk. The reported command output confirms:

| Check | Result |
| --- | --- |
| Running kernel (`uname -r`) | `7.2.0-jg-0sp11v23-qcom-x1e` |
| Root (`findmnt -no SOURCE,FSTYPE /`) | Internal NVMe partition, `ext4` |
| Native kernel package (`pacman -Q lexr-kernel-sp11`) | `lexr-kernel-sp11 7.2.0_jg_0sp11v23-1` |
| Touchscreen | Maintainer confirmed working in the installed system |
| Pen | Maintainer confirmed pen input after Lexr installed the paired IPTSD portable userspace |
| Wi-Fi | Maintainer confirmed connected after reconnecting |
| KDE terminal | Maintainer confirmed Konsole works after installing its package |
| Network widget | Missing `plasma-nm` installed; widget registered after refreshing Plasma, visual check pending |
| File manager | Missing Dolphin installed; process and directory handler verified, visual check pending |
| GPU acceleration | KWin uses `llvmpipe`; kernel reports missing private Surface GPU firmware |

The tested image was candidate 4, created with Lexr `2e0d384`, independently
validated and written by the matching native Lexr binary with a complete USB
read-back. Its SHA-256 is
`bd60290edeab9fd4b183b00b8ff361751c32898f56e723c9ef99c8aa0d0aac41`.
Later documentation updates do not change or requalify those image bytes.

This confirms the first installed boot and the features listed above. Repeated
boot selection, returning to Ubuntu/Windows after this installation, audio,
broader desktop behaviour, recovery, cross-ABI upgrades and X1P/LCD still require
testing. The image remains experimental; this record is not a published ISO release.

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
