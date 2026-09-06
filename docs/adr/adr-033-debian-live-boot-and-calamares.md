---
id: adrs-adr033
title: "ADR033: Debian live-boot and Calamares hand-off"
description: Preserve Debian live-media discovery and installed GRUB device-tree support.
---

## Status

Proposed on 2026-09-06 for [issue #50](https://github.com/ooaklee/lexr.sh/issues/50).
Implementation and OpenCode review are in progress. Physical live boot,
installation and recovery remain unqualified.

## Context

The maintainer supplied the official Debian ARM64 GNOME live ISO at the mutable
[weekly live URL](https://cdimage.debian.org/cdimage/weekly-live-builds/arm64/iso-hybrid/debian-live-testing-arm64-gnome.iso).
The accepted bytes identify the **2024-09-02** testing snapshot, not the current
testing distribution. The 3,598,430,208-byte ISO has SHA-256
`3260c69821f85464974e2136a0cda5d3954818467dda168bdfcb69547c4d7abc`.
Its publisher checksum signature was verified against the Debian Testing CDs
key fingerprint `F41D30342F3546695F65C66942468F4009EA8AC3`, independently
published on [Debian's verification page](https://www.debian.org/CD/verify).
The Debian Installer DVD catalogue entry is a separate product.

Actual source inspection found AArch64 kernel, executable and EFI files, one
`/live/filesystem.squashfs`, live-boot 1:20240525, Calamares 3.3.9-1 and
calamares-settings-debian 13.0.11-1. The source has an optical EFI image but no
USB partition table. Its direct GRUB searches for `/.disk/info` and forwards
to the ISO menu. The stock GRUB menu also exposes Debian Installer entries;
those are not the custom live-image installation contract.

The live-boot hook generates `conf/uuid.conf` when `LIVE_GENERATE_UUID` is set.
Its scanner compares this identity against `.disk/live-uuid*` on candidate
media. The payload path defaults to `live`, and the mounted medium is
`/run/live/medium`. These semantics differ from Casper despite Debian and
Ubuntu sharing package-management and initramfs tooling.

Calamares unpacks `/run/live/medium/live/filesystem.squashfs`, installs GRUB,
removes live-only packages, runs autoremove and regenerates initramfs images.
The source contains grub-common 2.12-5 but not grub2-common or grub-install;
the required ARM64 packages are in its ISO pool. Its native `10_linux` has
no device-tree handling. Elementary's native generator already has that
support, so its installed boot flow cannot simply be copied.

## Decision

Use a dedicated `debian-live` adapter with a checksum-pinned snapshot and
explicit source layout, live-boot identity, EFI bootstrap and Calamares input
checks. Keep the historical testing identity visible in the catalogue and
documentation. Reject uninspected source changes instead of silently accepting
a newer download at the mutable URL.

Reuse the shared Debian runtime package transaction, SP11 kernel and firmware
checks, offline companion, immutable publication and identity-bound USB writer.
Move the proven elementary early-module closure and public GPU firmware
supplement into shared SP11 helpers. Preserve their existing regression tests
and check the closure against the actual selected kernel package.

Generate separate installed and live initramfs images. Temporarily hide only
live-boot's hook and script for the installed image, restoring them on failure
as well as success. Generate the live UUID from the real live hook, then write
that identity to `.disk/live-uuid`. Do not persist `LIVE_GENERATE_UUID` or add a
live-media requirement to installed kernel updates.

Use a canonical SP11 live GRUB menu with `boot=live components
live-media-path=/live`, paired custom kernel, modules and external device trees.
Replace alternate menus and bind optical and USB boot to the same appended GPT
EFI image containing the inspected direct ARM64 GRUB. Secure Boot remains
unsupported for this custom unsigned kernel and external-DTB path.

Retain Calamares's inspected unpack, partitioning, package-removal and bootloader
sequence. Supply the missing GRUB packages offline. Add distribution-specific
GRUB device-tree handling while preserving native root-device, separate `/boot`,
normal-entry and recovery-entry generation. The generic kernel boot-support
package continues to own device-profile selection and exact-ABI DTB staging.
Package ownership must survive GRUB updates, and required kernel and support
packages must survive Calamares's autoremove step.

Retain the companion, corresponding source, licences, kernel packages and
boot-support evidence under `/usr/share/lexr/debian-media/sp11`. The desktop
guide uses Debian's live mount and firmware package names, and includes updating
Lexr independently after the bundled version has been tested. Private platform
firmware and device-specific credentials are not added to the image.

## Consequences

The first candidate uses v23 as a hardware baseline without restricting the
adapter to that kernel version. Each compatible bundle still requires matching
modules, device trees and an explicit installed-system profile. New source
snapshots require renewed inspection, especially of live-boot and Calamares.

Structural validation must independently verify EFI extents, complete artefact
bytes, UUID pairing, initramfs separation, module closure, firmware, package
ownership, retained companion and installed GRUB support. Its creation journal
includes Debian's offline GRUB preparation step and cannot be substituted by
another distro's journal. Passing these checks does not qualify live desktop,
peripherals, an installation target, installed boot or recovery.
