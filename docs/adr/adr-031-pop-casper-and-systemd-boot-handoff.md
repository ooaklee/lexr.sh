---
id: adrs-adr031
title: "ADR031: Pop Casper and systemd-boot hand-off"
description: Preserve Pop's live and installer contracts while pairing external SP11 device trees with installed and recovery boot.
---

## Status

Proposed on 2026-09-05. Implementation and OpenCode review are in progress.
Physical live boot, installation, peripheral and recovery qualification remain
tracked in [#48](https://github.com/ooaklee/lexr.sh/issues/48).

## Context

The checksum-pinned Pop!_OS 24.04 ARM64 Generic 3 ISO uses one SquashFS under
`casper_pop-os_24.04_arm64_generic_debug_217`, with `/casper` pointing to it.
Its kernel is `vmlinuz.efi`, its live initramfs is `initrd.gz`, and its original
`.disk/info` identifies Pop. This differs from Ubuntu Concept's layered Casper
filesystem and installer source declarations.

The source has an optical EFI boot image but no hybrid system area. Its ARM64
GRUB 2.12 bootstrap searches for `.disk/info` and loads the ISO configuration;
it does not provide Concept's `cutmem` command. Replaying this source's boot
metadata alone would not create a USB-visible EFI partition.

Pop's [Distinst bootloader stage](https://github.com/pop-os/distinst/blob/69dac84/src/installer/steps/bootloader.rs)
installs systemd-boot on ARM64. Its
[configuration stage](https://github.com/pop-os/distinst/blob/69dac84/src/installer/steps/configure/chroot_conf.rs)
invokes kernelstub, creates the native recovery copy, and regenerates the
installed initramfs after removing live packages. The source's kernelstub
creates kernel/initramfs entries without a device tree. Distinst's recovery
entry also omits a device tree and its recovery copy excludes `/sp11`.

Systemd-boot supports the Type 1 `devicetree` field when Secure Boot is disabled
([Boot Loader Specification](https://uapi-group.org/specifications/specs/boot_loader_specification/)).
The custom SP11 kernel requires its matching external DTB. Treating Pop as an
Ubuntu GRUB target would leave installation and recovery incomplete.

## Decision

Use a separate `pop-casper` adapter and validate the actual source paths and
bootstrap before changing it. Preserve Pop's identity, installer and live
directory. Generate a direct ARM64 GRUB menu for X1E/OLED, an explicitly
unqualified X1P/LCD entry, and text diagnostics. Use the same prepared FAT image
for El Torito and an appended GPT ESP, with a partition-relative ISO view.
Reject a changed uninspected GRUB bootstrap instead of assuming it routes to
the generated menu.

The initial X1E/OLED physical test reached GRUB and then reportedly showed a
black screen for both desktop and text entries. The exact failing stage is
unconfirmed. Keep the existing desktop loader while adding visible loading
stages and a paired diagnostic comparison. Pop's pinned Ubuntu GRUB includes
the `peimage` module, which overrides EFI image loading. The alternate diagnostic
entry unloads that module before loading Linux to use firmware EFI services;
the baseline explicitly selects it. Both use identical kernel, initramfs, DTB
and early-console arguments. Refuse a failed loader selection and keep any
loading error visible before exiting to firmware, avoiding GRUB's implicit
boot of a partially prepared entry. This introduces no new bootloader binary,
does not change the installed systemd-boot contract, and does not establish a
hardware fix. The 64 GB memory workaround is not evidence for a failure on a
16 GB Surface.

Share verified Debian package registration, Casper UUID pairing, SP11 firmware
preparation, bounded file reads and publication services with Ubuntu. Keep Pop's
filesystem, boot arguments, installer and EFI lifecycle explicit. Do not add a
v23-only kernel gate; v23 is the initial hardware test baseline.

Build two initramfs images for the selected ABI. The live image contains Casper
and a generated UUID paired with the medium. The installed image excludes
Casper's scripts and UUID requirement. Only the installed image remains in
`/boot`; the actual installer later regenerates it against the target storage.
Prepare the distribution's SP11 Wi-Fi board data before either image is built.

Install additive systemd-boot support in the root copied by Distinst. Runtime
hooks follow kernelstub, select the device through the generic boot-support
package and publish verified per-ABI kernel, initramfs and DTB generations.
Do not identify the Docker build host as an SP11 or publish ESP state offline.
Preserve unrelated EFI entries, kernelstub files and operator defaults.

Retain the original media manifest, DTBs and optional redistributable companion
inside the installer root. After Distinst creates recovery, verify both its
kernel/initramfs copies and Casper UUID, copy the companion, and add a Lexr
recovery entry using the original live kernel's DTB. An installed-kernel update
must not pair its newer DTB with the older recovery kernel. The native recovery
entry remains owned by Distinst; SP11 users select the Lexr recovery entry.

Structural validation independently checks the finished ISO, package payloads,
boot files, firmware and installed/recovery support. Release preparation selects
the journal sequence by adapter and retains known Ubuntu journal generations.
Structural success does not assert physical boot or successful installation.

### EFI diagnostic verification

Use the pinned source GRUB binary and its matching modules in an isolated
AArch64 EFI virtual machine when changing loader selection or failure handling.
Check `lsmod`, `rmmod peimage`, `lsmod`, `insmod peimage`, `lsmod`: removal and
restoration must succeed. Do not insert `peimage` immediately before removing
it; inserting an already embedded module raises its reference count and blocks
removal in this build.

Check the generated menu with `grub-script-check`. In a disposable probe image,
retain the actual kernel but omit the selected DTB, choose the firmware-loader
diagnostic entry, and shorten only the menu timeout and failure pause. Verify
that the kernel loads, the missing-DTB error is displayed, and GRUB exits to
firmware without reaching the initramfs or kernel-start stages. This exercises
a partially prepared entry; string comparisons cannot prove that runtime
invariant. Keep these altered probe images separate from Lexr's validated media.

The pinned GRUB passed module removal/restoration and this failure-path check.
Separate probes reached v23 EFI-stub decompression and early Linux output with
both EFI loaders on virtual hardware. These probes used the virtual machine's
serial console, not an SP11 live desktop, and do not resolve the physical
black-screen report. Repeat both diagnostic entries on the Surface and retain
the last visible stage before choosing a permanent boot-path change.

## Consequences

The ISO retains Pop's installer behaviour and needs no private firmware copied
from a developer's machine. Including the companion also keeps Lexr and its
corresponding source available after installation and in recovery.

The external DTB path requires Secure Boot to remain disabled. Explicit
`kernelstub --force-update` can later reset the menu default without invoking
Lexr's hook; users can select the Lexr entry again. A custom layout may omit
Pop's recovery partition, so removable recovery media should be retained until
the installed system and any internal recovery environment have been tested.

These contracts require validation on an actual Surface before qualification.
No ISO should be labelled hardware-qualified from filesystem inspection alone.
