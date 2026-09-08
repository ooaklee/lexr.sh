---
id: adrs-adr035
title: "ADR035: Arch guided Surface installation"
description: Reuse Archinstall's guided choices while retaining the Surface kernel and shared-ESP boot contract.
---

## Status

Accepted on 2026-09-08 for the initial installation implementation in
[issue #52](https://github.com/ooaklee/lexr.sh/issues/52), following the
maintainer's request for Archinstall, a terminal default and GRUB alongside
existing operating systems. Installed-system hardware qualification is pending.

## Context

The Arch Linux ARM source is a root filesystem, not an installer ISO. Lexr now
produces a terminal live image with the coherent Surface kernel and firmware.
Upstream Archinstall offers useful account, locale, profile and network menus,
but its stock kernel packages and bootloader paths do not implement this ARM64
Surface boot chain. Its whole-disk defaults also conflict with retaining other
operating systems on the test device.

## Decision

Bundle the signed Archinstall 4.4 package as a separate, unmodified dependency.
An external Python adapter uses its guided flow and replaces kernel, bootloader
and storage-validation stages. Pin the package and dependency bytes in the
image lock and require the reviewed API version at runtime. Do not silently
upgrade the installer in the live session.

Keep account, locale, network and optional profile choices. Preselect no desktop.
Fix platform choices to Arch Linux ARM repositories, the local Surface kernel,
Adreno/Mesa and ARM64 GRUB. The first supported storage plan creates one ext4
root in unallocated space on an existing GPT disk and reuses its FAT ESP at
`/boot/efi`. Preserve every existing partition record. Reject wiping, formatting
existing partitions, encryption, LVM and separate boot partitions in this first
implementation. Re-read the actual disk after confirmation and before mutation.

Bind the native kernel archive, installed mkinitcpio configuration, firmware,
DTB identities and GRUB templates in a closed payload manifest. Verify these
bytes independently from the completed ISO and again before target writes.
Install the local unsigned kernel through a temporary, repository-free pacman
configuration; do not weaken the installed repository trust policy. Retain a
separate `lexr-sp11` initramfs preset and the offline companion on the target.

Install GRUB only under `EFI/LexrArch`, with `--no-nvram`; create its dedicated
firmware entry using `efibootmgr --create-only`. Preserve other EFI files and
BootOrder, refuse an existing Lexr installation, and print an optional one-time
BootNext command. Verify the kernel, DTBs, initramfs, GRUB, firmware entry and
UUID-based fstab before the guided flow reports success.

## Consequences

- Familiar guided choices remain available with explicit Surface constraints.
  The operator prepares unallocated space and confirms the final installation.
- Saved configurations receive the same validation; dry runs cannot reach the
  filesystem mutation stage. Executable extensions and unattended real installs
  are outside this adapter's initial contract.
- The installed system receives fresh account configuration. Live autologin and
  passwordless sudo do not transfer. Copying saved Wi-Fi connections requires
  selecting Archinstall's Copy ISO networking option.
- The wrapper depends on reviewed Archinstall interfaces. An upstream version
  change requires API tests and native installation validation before updating
  the pin. Keep upstream licensing separate from Lexr-authored integration code.
- Loop-image tests can check package installation and EFI file preservation;
  simulated firmware variables cannot qualify a physical installed boot.
  Cross-ABI upgrades, recovery and other storage layouts need further work.
