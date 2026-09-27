# Prepare a Surface Pro 11 USB image on Windows

Use this guide to download a prebuilt distro image, join its split parts and
flash a USB drive with [balenaEtcher](https://etcher.balena.io/). This is the
canonical Windows workflow for Lexr image releases and the
[Surface Pro 11 OE repository](https://github.com/ooaklee/linux-surface-pro-11-oe).
Each release supplies its own filenames, checksums and hardware limitations.

**Already running a Linux distro?** Follow the release's Lexr download and
validation commands, then the [Lexr USB workflow](installation-media.md#2-review-the-usb-target).

## Before you start

- Choose an image for your Surface model from the
  [distro releases](https://github.com/ooaklee/linux-surface-pro-11-oe#compatible-distributions-and-image-downloads).
  These images and their unsigned custom kernels remain experimental; read the
  selected release's limitations before proceeding.
- Back up the USB drive. Etcher will erase the **entire selected drive**. Its
  capacity must exceed the uncompressed image size; a 16 GB USB drive is a
  practical choice for the current ISO releases.
- Use a folder on your Windows disk with enough free space for the downloaded
  parts, the joined compressed file and the extracted image at the same time.
  Use NTFS or exFAT: [FAT32 cannot hold files larger than 4 GiB](https://learn.microsoft.com/en-us/windows/win32/fileio/filesystem-functionality-comparison).
- Install a current [7-Zip](https://www.7-zip.org/download.html), which supports
  Zstandard (`.zst`) extraction. Choose its ARM64 build for Windows on a Surface
  Pro 11, or x64 for an Intel/AMD Windows PC.
- Download Etcher from its [official site](https://etcher.balena.io/).
  The current Windows download is x64. Windows 11 on ARM can emulate x64 apps,
  but this project has not qualified Etcher USB writing under that emulation.
  If it cannot run or access your USB drive, use an x64 Windows PC to prepare
  the drive. The image still targets your ARM64 Surface.

## 1. Download one complete release

Open the chosen GitHub release and expand **Assets**. Save all its image parts
and verification files into a fresh folder, for example
`Downloads\surface-image\assets`. Do not mix parts from different releases.
GitHub's automatic **Source code** archives are not the bootable image.

Current ISO releases include:

- `<image>.iso.zst.part-0000`, `part-0001`, and any further numbered parts;
- `SHA256SUMS`, `image-release-manifest.json`, `<image>.iso.manifest.json`, and
  `RELEASE-NOTES.md`.

For these ISO releases, open `SHA256SUMS` and `image-release-manifest.json`
in a text editor. The manifest's `parts` list specifies the complete ordered
set; its `image` entry records the final filename, size and SHA-256. Older raw
`.img` releases instead provide their own text manifest and reconstruction
commands in the release description; use those filenames, part order and hashes.
Keep the downloaded assets in their original folder and create the
joined/extracted files one folder above.

## 2. Check and join the parts in Command Prompt

Press the **Windows key**, type **cmd**, and press **Enter**. These commands
are for **Command Prompt**, not PowerShell's `copy` alias.

Navigate to your assets folder, adjusting the path if needed:

```bat
cd /d "%USERPROFILE%\Downloads\surface-image\assets"
```

Check each part with [Windows certutil](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/certutil#-hashfile).
Replace the example name with the actual filename, and repeat for every part:

```bat
certutil -hashfile "image.iso.zst.part-0000" SHA256
```

Compare the printed hash with that filename's entry in `SHA256SUMS`. If a part
is missing or its hash differs, download that part again before continuing.

Join **all and only** the image parts, in the order listed by the manifest.
This example has three parts; replace the names and add or remove terms to
match your release:

```bat
copy /b "image.iso.zst.part-0000"+"image.iso.zst.part-0001"+"image.iso.zst.part-0002" "..\image.iso.zst"
```

The [`/b` switch](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/copy)
copies binary data. List the files explicitly rather than using a wildcard so
the part order and completeness are visible. A successful copy alone does not
prove that the parts were complete or correct.

If your release has a single `.iso.zst`, no joining is needed. If it already
has an uncompressed `.iso`, go straight to the final image checksum below.
Older raw-image releases use `.img.zst.part-aa`, `part-ab`, and so on: use the
exact order and checksums in that release's notes, join to `.img.zst`, and
extract an `.img` instead of an ISO.

## 3. Extract and verify the image

Open the joined `.iso.zst` in **7-Zip** and extract it into the parent
`surface-image` folder. This produces the `.iso` that Etcher will flash.
Extract the Zstandard layer only; leave the ISO intact. Renaming `.zst` to
`.iso` does not decompress it.

In Command Prompt, calculate the extracted image's checksum:

```bat
certutil -hashfile "..\image.iso" SHA256
```

Compare it with the release's **ISO SHA-256** or `image.sha256` in
`image-release-manifest.json`. It must match exactly before flashing. For an
older raw image, substitute `.img` and use its published uncompressed-image
checksum. Manual checksum verification confirms the file bytes; it does not
establish that every peripheral or installation path has been tested.

## 4. Flash the USB with Etcher

1. Connect the USB drive and open **balenaEtcher**.
2. Choose **Flash from file** and select the verified, uncompressed `.iso`
   (or the older release's `.img`). Do not select an individual part.
3. Choose **Select target**. Check the drive's identity and capacity carefully;
   disconnect unrelated removable drives if that makes the choice clearer.
4. Choose **Flash!** and approve Windows' administrator prompt if shown.
5. Wait for writing **and validation** to finish successfully, then safely eject
   the drive.

Etcher writes the image's partition layout; no separate formatting step is
needed. If Windows offers to format a partition after the write, **cancel**.
See [Etcher's documentation](https://etcher-docs.balena.io/USER-DOCUMENTATION/)
for its flashing workflow.

## 5. Configure Surface UEFI and boot the USB

If Windows uses BitLocker or device encryption, keep your
[recovery key](https://support.microsoft.com/en-us/windows/security/encryption/find-your-bitlocker-recovery-key)
accessible on another device before changing firmware settings. Changes to
Secure Boot or boot order can cause Windows to request it on the next boot.

1. Shut the Surface down fully and wait about ten seconds.
2. Hold **Volume Up**, press and release **Power**, and keep holding Volume Up
   until Surface UEFI opens.
3. In **Security**, open **Secure Boot** and disable it (some firmware calls
   this **Change configuration → None**). These images use an unsigned custom
   kernel. The setting is named **Secure Boot**, not Windows Safe Mode.
4. In **Boot configuration**, enable USB boot and move **USB Storage** above
   the internal drive/Windows Boot Manager. Enable the alternate boot sequence
   if your firmware exposes that option.
5. Connect the flashed USB drive, save/exit UEFI and restart.

If normal startup still selects the internal drive, shut down again with the
USB connected. Hold **Volume Down**, press and release **Power**, and keep
holding Volume Down until USB startup begins, then release it. A correctly
written and compatible image should reach its **GRUB** menu. Select the entry
for your Surface model and follow that distro's live-session instructions.

Microsoft documents the [Surface UEFI settings](https://learn.microsoft.com/en-us/surface/manage-surface-uefi-settings)
and [USB boot procedure](https://support.microsoft.com/en-us/surface/drivers-firmware/boot-surface-from-a-usb-device).
If GRUB does not appear, confirm Etcher's validation succeeded, recheck the
image checksum and USB boot settings, and try a direct USB connection or a
different adapter/port. Formatting the drive after flashing would destroy the
boot image.

## After the live session starts

Use the release's installation instructions and the included
`LEXR_GETTING_STARTED.txt`. Reaching the desktop or installer does not establish
a completed installation. Audio may initially show **Dummy Output**, and
Bluetooth may need the post-install Lexr setup described by that distro's
guide. Keep the release's hardware limitations in view when testing.
