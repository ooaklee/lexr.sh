# Install Arch Linux ARM

This walkthrough takes a prepared Lexr Arch Linux ARM USB through installation,
first boot, with an optional step to add Arch to another OS's GRUB menu. It
follows the Surface Pro 11 **X1E/OLED** test with the **v23** kernel. The live
image starts in a terminal; you choose whether to add a desktop later.

Keep the USB available until the installed system boots successfully. The
[installation reference](../operator-manual/arch-linux-arm-install.md) covers
other choices, troubleshooting and the current hardware test results.

## 1. Boot the prepared USB and connect

Start after Lexr reports successful image validation and a completed USB write
with full read-back verification. If you still need to prepare the USB, follow
the [Arch image preparation instructions](../operator-manual/arch-linux-arm-source.md).
A previous build's successful write does not verify a newly created image.

Unplug and reconnect the USB after Lexr ejects it, then reboot from USB. Disable
Secure Boot for this unsigned kernel and external device tree. Select
**X1E/OLED** in the USB's GRUB menu on the tested Surface model.

At the terminal, connect to your network:

```bash
sudo nmtui
```

In `nmtui`, choose **Activate a connection**, select your Wi-Fi network and enter
its password. Exit `nmtui` once connected. USB phone tethering is another option.

## 2. Select only the space reserved for Arch

Inspect the disk layout before starting the installer:

```bash
lsblk -o NAME,SIZE,FSTYPE,LABEL,MOUNTPOINTS
```

Then start the bundled installer before upgrading packages in the live session:

```bash
sudo archinstall
```

Option **6** in `lexr-arch-setup` opens the same installer.
Choose **Disk configuration → Manual Partitioning**. Select partitions by their
purpose and size on your own disk. You can install Arch on its own or alongside
other operating systems; an existing Linux installation is not required.

| Partition | Mount point | Format? |
| --- | --- | --- |
| New partition in free space, or a partition you have reserved for Arch | `/` | **Yes, ext4 — erases this partition** |
| Existing EFI System Partition (ESP), if present | `/boot/efi` | **No — reuse it** |
| New ESP, only if you do not already have one to use | `/boot/efi` | **Yes, FAT32** |
| Other partitions you want to keep, if any | Leave unassigned | **No** |

Use one ESP on a GPT disk: reuse the existing one or create one in free space
if needed. Keep or set its EFI flag. It needs at least 32 MiB free for Lexr's
boot files. If you have not prepared space yet, follow
[Prepare space and select the layout](../operator-manual/arch-linux-arm-install.md#prepare-space-and-select-the-layout)
before installing.

Keep Arch's `/boot` inside its new ext4 root. If another Linux installation has
a separate `/boot` partition, leave that partition unassigned and unchanged.
Leave encryption and LVM disabled for this Surface boot flow.

Before confirming installation, check that **only the Arch root and any newly
created ESP will be formatted**. Do not format an existing ESP. If you want to
keep any existing data or operating systems, do not select a whole-disk erase
layout. Archinstall executes the layout you confirm; Lexr does not protect
other partitions from an incorrect formatting choice.

## 3. Keep the Surface defaults and choose your account

| Setting | Choose |
| --- | --- |
| Bootloader | **GRUB**, without UKI, removable fallback or Plymouth |
| Kernel | Keep **`lexr-kernel-sp11`** |
| Network configuration | **NetworkManager** |
| Profile | Leave unset or choose **Minimal** for a terminal system |
| Authentication | Create your own user and password, with sudo access |
| Language, keyboard, locale, timezone and hostname | Your preferences |

No desktop is preselected. If you choose an optional desktop, keep **Qualcomm
Adreno (Mesa)** graphics; Lexr includes the Freedreno Vulkan provider and checks
ARM packages and repositories. Use a fresh configuration if an older saved
configuration selects PC graphics drivers. The tested Plasma desktop used
software rendering; see the [desktop limitations](../operator-manual/arch-linux-arm-install.md#choose-the-rest-of-your-system)
before expecting hardware acceleration.

For sound, optionally choose **Applications → Audio → PipeWire**. For selectable
power modes, optionally choose **Applications → Power management →
power-profiles-daemon**. Surface pen and audio configuration are covered in
[Set up hardware](#7-set-up-hardware-and-customise-arch) below.

Review the complete configuration, select **Install** and confirm it. If a
previous attempt left `EFI/LexrArch`, the installer refuses to replace it. Keep
the error and inspect the previous installation before retrying; do not delete
EFI directories or repeat formatting to clear the error.

## 4. Exit the installer and select Arch for the next boot

When installation completes, choose **Exit archinstall** so you can read Lexr's
boot instructions. Run the exact `sudo efibootmgr --bootnext …` command it prints.
If the message has scrolled away, list the current firmware entries:

```bash
sudo efibootmgr
```

Find **Lexr Arch Linux ARM**. Its `BootXXXX` prefix contains the four hexadecimal
digits to use. Replace `XXXX` below with that entry's actual number:

```bash
sudo efibootmgr --bootnext XXXX
sudo efibootmgr
```

Check that `BootNext` now shows the number you selected, then reboot:

```bash
sudo reboot
```

This selects Arch for **one boot** without changing your existing firmware
BootOrder. The firmware consumes `BootNext` after use; it does not add an entry
to another OS's GRUB menu. See the [efibootmgr documentation](https://github.com/rhboot/efibootmgr)
and the persistent-menu step below.

If **Lexr Arch Linux ARM** is missing, do not guess a boot number. Keep the USB
available. If another installed Linux OS has a GRUB menu, use
[optional GRUB registration](#6-optionally-add-arch-to-another-grub-menu)
from that OS, or option **7** on a newer live image with the required filesystems
mounted. Otherwise, report the firmware-entry output in
[issue #52](https://github.com/ooaklee/lexr.sh/issues/52) for help. A missing menu
entry alone does not mean Arch needs reinstalling.

## 5. Check the installed system

In Arch's own GRUB menu, select the normal **Arch Linux ARM for Surface Pro 11**
kernel entry, then log in with the account you created. The **X1E/OLED** label
belongs to the live USB menu; the installer already selected the matching
device tree for the installed menu.

Run:

```bash
uname -r
findmnt -no SOURCE,FSTYPE /
pacman -Q lexr-kernel-sp11
sudo nmtui
```

For the tested v23 image, check these results:

| Check | Expected result |
| --- | --- |
| `uname -r` | `7.2.0-jg-0sp11v23-qcom-x1e` |
| Root filesystem | The partition you selected for Arch, with `ext4` |
| Kernel package | `lexr-kernel-sp11 7.2.0_jg_0sp11v23-1` |
| Network | Reconnect through NetworkManager; live Wi-Fi credentials are not copied |

An overlay root means you are still in the live USB session. Keep these results
in your local test notes with the image's version and validation/write receipts.
Include the relevant results when reporting a problem in
[issue #52](https://github.com/ooaklee/lexr.sh/issues/52). If a later image uses a
different custom ABI, compare against that image's kernel version.

## 6. Optionally add Arch to another GRUB menu

This step is optional. Use it if another installed Linux OS owns the GRUB menu
you normally use and you want Arch listed there. The command also works with an
already installed Lexr Arch candidate; it does not require reinstalling Arch.

Boot the existing Linux OS that owns your usual GRUB menu. Use an updated Lexr
with `kernel boot register-arch` (introduced in [PR #55](https://github.com/ooaklee/lexr.sh/pull/55));
the original `2e0d384` USB companion lacks this command. See
[Install Lexr](../getting-started/install.md) for release and source-build options,
then check:

```bash
lexr kernel boot register-arch --help
lsblk -f
```

Identify the installed Arch root UUID. Replace `ARCH_ROOT_UUID` below with that
value and mount it read-only at an unused mount point:

```bash
sudo mkdir -p /mnt/arch
sudo mount -o ro,noload /dev/disk/by-uuid/ARCH_ROOT_UUID /mnt/arch
sudo lexr kernel boot register-arch --arch-root /mnt/arch --grub-directory /boot/grub --dry-run
```

Review the preview, then apply it and unmount Arch:

```bash
sudo lexr kernel boot register-arch --arch-root /mnt/arch --grub-directory /boot/grub --yes
sudo umount /mnt/arch
```

Use your Lexr binary's absolute path if it
is not on sudo's PATH. These commands use the existing OS's `/boot/grub` and the
shared ESP mounted at `/boot/efi`; supply `--esp /path/to/esp` if its mount point differs.
For the live USB's option **7**, mount the existing OS and its separate `/boot`
first, then select its actual GRUB directory. See the
[registration reference](../operator-manual/arch-linux-arm-install.md#add-arch-to-the-existing-grub-menu).

On the next restart, choose **Arch Linux ARM (Surface Pro 11)** in your usual
GRUB menu, followed by the normal entry in Arch's own menu. Lexr checks the
installed loader and appends a custom entry, preserving existing menu entries,
EFI files and BootOrder. Repeating registration does not add duplicates.

## 7. Set up hardware and customise Arch

Open the retained instructions for pen, audio, camera, Wi-Fi and power profiles:

```bash
cat /usr/share/lexr/LEXR_GETTING_STARTED.txt
```

The guide includes copying the retained Lexr binary, installing IPTSD for the
pen, checking audio prerequisites and configuring power-profiles-daemon.
The [front-camera setup](../operator-manual/arch-linux-arm-install.md#front-camera-after-installation)
uses Arch's native libcamera packages and includes a preview check.
PipeWire alone does not supply Surface audio firmware. The
[hardware setup and test record](../operator-manual/arch-linux-arm-install.md#pen-and-audio-after-installation)
distinguishes confirmed features from pending tests.

Choose your editor, terminal tools, window manager or desktop when you are
ready. If you selected Plasma and cannot open a terminal or folders, use the
[optional desktop package instructions](../operator-manual/arch-linux-arm-install.md#choose-the-rest-of-your-system).

## Image versions and test status

The earlier `8f56f20` candidate was superseded by `2e0d384`, which corrected the
ARM graphics/package and partition-type issues. The latter image passed
validation and full native Lexr USB read-back, and its installed v23 system
booted on the X1E/OLED Surface. Persistent-menu registration was added later
and passed native preview/apply/repeat checks; physical selection of that new
entry remains pending in the current record.

Use the [hardware test record](../operator-manual/arch-linux-arm-install.md#hardware-test-record)
for the exact image digest and remaining checks, and
[issue #52](https://github.com/ooaklee/lexr.sh/issues/52) for progress. These
historical results do not mean a newly built USB is ready or every peripheral
has been qualified.
