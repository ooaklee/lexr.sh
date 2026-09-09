---
id: adrs-adr035
title: "ADR035: Arch guided Surface installation"
description: Use Archinstall plugin hooks for the Surface kernel and ARM64 boot while retaining its normal installation flow.
---

## Status

Accepted on 2026-09-08 for the initial implementation in
[issue #52](https://github.com/ooaklee/lexr.sh/issues/52). Ooaklee confirmed
X1E/OLED installation and boot from an internal ext4 root with v23, touchscreen
input and Wi-Fi on the same date. The
[hardware test record](../operator-manual/arch-linux-arm-install.md#hardware-test-record)
identifies the candidate and remaining qualification work.

## Context

The Arch Linux ARM source supplies a root filesystem, not an installer ISO.
Lexr creates a terminal live image with the Surface kernel and firmware.
Archinstall already owns partitioning, formatting, accounts, profiles and
network setup. Duplicating those features adds a second installer to maintain.

The bundled Archinstall 4.4 does need platform integration: it cannot download
Lexr's local kernel archive from the ARM repositories, does not supply the
matching Surface DTB, and derives `aarch64-efi` for GRUB where `arm64-efi` is
required. Its generic bootloader invocation also changes firmware boot order.

## Decision

Bundle the signed, unmodified Archinstall package with its dependencies pinned
in the image lock. Use its plugin callbacks for package requests, initramfs,
bootloader installation and final boot verification. Keep only small adaptations
for the ARM keyring, platform menu fields and boot compatibility feedback;
there is no upstream configuration-validation hook. Test these against the
bundled API before updating its version.

The first desktop installation attempt exposed another x86 assumption: the
All open-source graphics choice requested Intel/ATI packages unavailable in the
ARM repositories. Fix graphics to Mesa plus `vulkan-freedreno` for Adreno.
Without the explicit Vulkan provider, pacman can select ARM64 NVIDIA utilities
for a desktop's virtual `vulkan-driver` dependency. CPU architecture alone does
not establish Surface hardware compatibility.

Keep effective pacman architecture and repositories restricted to the image's
ARM configuration. Resolve selected profile/additional packages before formatting,
and check each later pacstrap request and its resolved package metadata. This
consumes upstream profile data and pacman's resolver; it does not duplicate
application, greeter or networking recipes. Later transactions and downloads
can still fail. Block external profile code before deserialization and reject
custom commands, since arbitrary extensions fall outside the reviewed flow.

Disable the upstream x86 mirror-status lookup. Adapt the formatter's module-local
root-type constant to the [ARM64 root GUID](https://uapi-group.org/specifications/specs/discoverable_partitions_specification/).
The upstream formatter still owns partition creation and geometry. Test the
GUID with real libparted and sfdisk on a disposable file, preserving a neighbour.

Archinstall owns disk actions and the confirmation screen. Lexr does not compare
GPT geometry, replace the formatter, enforce a new-partition-only policy, or
replace account, networking and profile handlers. No desktop is preselected.
The alongside-install guide instructs users to prepare free space, reuse the
existing ESP without formatting and preserve other OS partitions. Those are
choices the user reviews in Archinstall, not guarantees supplied by a Lexr
partitioning policy.

The current Surface boot payload supports a plain ext4 root containing `/boot`
and a FAT ESP at `/boot/efi`, with GRUB and no UKI, encryption or LVM. Check that
compatibility in the normal Install preview and saved-config path before
formatting. The limitation belongs to the Surface boot payload, not Archinstall.

Keep the offline kernel/boot helper independent of Archinstall. It verifies the
kernel archive, firmware, DTB identities and Go-rendered GRUB templates, installs
the local package without weakening repository signature policy, and retains
the `lexr-sp11` initramfs preset and offline companion. Moving this helper to a
new language would not remove duplicated installer behaviour.

Install GRUB under `EFI/LexrArch` with `--no-nvram`, then create its firmware
entry using `efibootmgr --create-only`. Preserve the EFI files present when this
hook starts and the existing BootOrder. Verify the kernel, DTBs, initramfs,
GRUB and UUID-based fstab before the normal completion dialog. This boot-hook
verification does not undo partition formatting selected earlier by the user.

Persistent selection from another OS's GRUB is a separate, optional post-install
action in the native Lexr binary. `kernel boot register-arch` also accepts
previously installed candidates with valid receipts. It verifies mounted
identities and the recorded ARM64 loader, then appends a chainloader to the
existing menu's supported `custom.cfg` loading path. The setup menu exposes a
preview and confirmation. It does not mount another OS, regenerate its menu,
change BootOrder or duplicate Arch's kernel/DTB entries. This closes the gap
between a successful one-time boot and a persistent multi-OS menu without
adding another Archinstall callback or runtime Python file.

## Consequences

- The user gets the standard guided setup with Surface defaults and a terminal
  starting point. Most future installer changes remain upstream's responsibility.
- Dry-run and confirmation remain upstream-owned. Real unattended installation
  and replacement entry-point scripts/plugins are outside this initial flow.
- Live autologin and passwordless sudo do not transfer. Use NetworkManager and
  reconnect after installation; Lexr does not add a connection-copying feature.
- API tests and native loop-image installation test the hooks without maintaining
  a second disk-policy test suite. Simulated firmware variables cannot qualify
  physical installed boot, cross-ABI kernel upgrades or recovery.
