---
id: adrs-adr032
title: "ADR032: elementary Casper and GRUB hand-off"
description: Preserve elementary OS installer and bootloader contracts while sharing verified image services.
---

## Status

Proposed on 2026-09-06 for [issue #49](https://github.com/ooaklee/lexr.sh/issues/49).
Implementation review and physical installation qualification remain pending.

## Context

The checksum-pinned elementary OS 8.1 ARM64 source contains one Casper filesystem,
`/casper/vmlinuz`, `/casper/initrd.lz`, an optical EFI image and no USB partition
table. Its direct ARM64 GRUB has the same inspected bootstrap bytes as the Pop
source, but the installed-system contracts differ.

The installed Distinst version identifies revision
[d34fd00](https://github.com/pop-os/distinst/commit/d34fd00), which adds elementary
ARM64 GRUB package selection. Its name normaliser maps elementary OS to `ubuntu`;
its EFI bootloader step generates `/boot/efi/EFI/ubuntu/grub/grub.cfg`.
Elementary's packaged `update-grub` already updates that file as well as the
normal `/boot/grub/grub.cfg`. The packaged `10_linux` generator selects exact-ABI
DTBs for normal and recovery entries. Distinst copies the live kernel through
`/vmlinuz` on the target. Elementary's automatic partitioning creates an ESP,
root and, for encryption, a separate boot partition; it does not create Pop's
recovery volume. Package/source inspection is not physical installation evidence.

The source's older Linux firmware lacks two GPU files needed by the custom X1E
kernel. Its compressed WCN7850 board database does supply the qualified SP11
board data. Private platform firmware remains outside image redistribution.

## Decision

Use a dedicated `elementary-casper` adapter with explicit source path, identity,
GRUB bootstrap and native installed-generator checks. Reuse the common Debian
package transaction, SP11 kernel/DTB/firmware helpers, Casper identity contract,
companion, publication and USB services. Do not import Pop's BLS or kernelstub
lifecycle into elementary.

Generate separate live and installed initramfs images. The latter has no Casper
scripts or media UUID dependency. Preserve the source installer removal list and
reject removal of required kernel/GRUB support. Stage one declared installed
profile through the generic package-owned boot helper and pair both live device
choices with their bundle DTBs. Set root and boot kernel/initramfs links to the
selected ABI before Distinst copies the kernel. Retain native update hooks.

Replace shim with the inspected direct GRUB, canonicalise the alternate ARM64
and loopback menus, and put the same EFI image in the USB GPT ESP and optical
boot catalogue. Keep Secure Boot disabled for unsigned custom-kernel media.

Download only the missing public GPU GMU/SQE files and their full licence/notice
set from a fixed Linux firmware commit. Pin every file's size and SHA-256, bound
downloads, retain provenance on the ISO and installed root, and validate actual
root and initramfs firmware. Keep the offline companion, its source/licences and
kernel packages in the installer payload after live packages are removed.

## Consequences

Elementary's native GRUB recovery entries and the removable image provide its
recovery paths; this change does not invent a recovery partition. The adapter
requires a compatible external-DTB bundle and an explicit installed profile.
A changed source bootloader or generator requires renewed inspection. Initial
image preparation downloads a small pinned firmware supplement. Existing Ubuntu
and Fedora contracts retain regression coverage.

Structural validation checks media identity, EFI extents, complete package and
boot artefact bytes, the installed payload and source/licence inventory. Physical
desktop, Wi-Fi, input, installer, authorised installation and recovery boot still
require separate evidence. No installation target is selected by image creation.
