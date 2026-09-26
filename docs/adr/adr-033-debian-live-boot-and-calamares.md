---
id: adrs-adr033
title: "ADR033: Debian live-boot and Calamares hand-off"
description: Preserve Debian live-media discovery and installed GRUB device-tree support.
---

## Status

Proposed on 2026-09-06 for [issue #50](https://github.com/ooaklee/lexr.sh/issues/50).
The implementation checkpoint has been reviewed with OpenCode, and structural
checks have passed.
Investigation was paused on 2026-09-07 after failed X1E/OLED live boots, including
a reported blank screen when trying `toram`. Successful RAM-backed operation was
not confirmed. Issue #50 retains the failure evidence and resumption plan.
Investigation resumed in [PR #61](https://github.com/ooaklee/lexr.sh/pull/61).
On 2026-09-25, photographs showed a USB device going offline, followed by loop
and SquashFS read errors. Separate photographs showed a RAM copy in progress,
without confirming completion or a RAM-backed root. The triggering driver or
firmware failure had not been identified; a black screen alone does not isolate
graphics from loss of the live filesystem.
On 2026-09-26, Ooaklee confirmed that adding `regulator_ignore_unused` to the
MSM-blacklisted initramfs diagnostic kept the screen visible and keyboard input
working. Without it, the display went black while typing before root userspace
started. This supports suppressing unused-regulator cleanup for the firmware
display diagnostics; it does not identify a particular failing supply or
explain the normal desktop and USB failures. A subsequent firmware-display boot
reached a text login and working D-Bus, but lost access to USB-backed executables.
Saved logs place UCSI initialisation and a USB PHY mode change immediately before
a SuperSpeed reset, disk read errors and SquashFS failures. The USB's complete
image bytes still match the original ISO. The tested Debian initramfs omits
`ucsi_glink`, `typec_ucsi` and `ps883x`, which are present in the working Ubuntu
and elementary images. Candidate `3339659` includes those drivers in the early
initramfs. On 2026-09-26, Ooaklee reported that this candidate with v23 reached
the X1E/OLED live desktop through the first GRUB entry, with Wi-Fi, internet
access, power profiles and Bluetooth working. Rebuilt candidate `37c21ca`,
including main through `15a236b`, subsequently booted with the same reported
behaviour. Its Calamares installer displayed Debian 13, reached location and
partition selection, and detected existing Windows, Ubuntu and Arch partitions;
no installation was performed. This supersedes the initial installer-startup
concern. Audio still exposes Dummy Output, with Lexr setup and sound verification
pending. The accepted source remains the 2024-09-02 testing snapshot.
See the [hardware test record](../user-guide/installation-media.md#debian-arm64-gnome-live-image)
for the tested scope and source identity. The specific USB reset trigger and
controlled cold-boot repeatability remain unconfirmed. Installation, installed
boot and recovery remain unqualified; this proposal has not been accepted as a
working Debian installation path.

## Context

The maintainer supplied the official Debian ARM64 GNOME live ISO at the mutable
[weekly live URL](https://cdimage.debian.org/cdimage/weekly-live-builds/arm64/iso-hybrid/debian-live-testing-arm64-gnome.iso).
The accepted bytes identify the **2024-09-02** testing snapshot, not the current
testing distribution. The 3,598,430,208-byte ISO has SHA-256
`3260c69821f85464974e2136a0cda5d3954818467dda168bdfcb69547c4d7abc`.
Its publisher checksum signature was verified against the Debian Testing CDs
key fingerprint `F41D30342F3546695F65C66942468F4009EA8AC3`, independently
published on [Debian's verification page](https://www.debian.org/CD/verify).
Debian Installer DVD media is a separate product and is not offered in the
catalogue. The live entry uses the release label `Debian 13 (2024-09-02)` while
retaining its verified testing-snapshot ID, source bytes and provenance.

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

Include `ucsi_glink` and `ps883x` in this early module closure so normal initramfs
coldplug can configure the USB-C service and retimers before mounting the live
filesystem. Their auxiliary-bus and device-tree relationships are not ELF
dependencies of the USB host controller. Resolve `typec_ucsi` and other module
dependencies with the selected kernel's metadata, accepting built-in drivers.
Verify both live and installed initramfs images. Do not force module loads or add
a fixed delay: module presence alone does not prove asynchronous Type-C setup
has completed. Hardware validation must check that initialisation happens before
live-root access and that no later reset makes the backing device unavailable.

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

Keep RAM copying optional and use whole-medium `toram`, preserving `/live` and
`/sp11` for Calamares and the companion. The pinned live-boot implementation
does not reliably propagate copy errors, so verify RAM backing and copied media
before allowing that entry to continue. Module-only `toram=filesystem.squashfs`
changes the directory layout and is not an installer-compatible alternative.
The separate initramfs storage diagnostic entry pauses at `break=bottom`, before
root userspace starts, with only `msm` blacklisted for firmware-console access.
Both MSM-blacklisted diagnostics set `regulator_ignore_unused` for that boot,
preserving unused supplies while the firmware display has no native graphics
driver. Keep ordinary desktop, RAM and native text boot regulator policy unchanged.
Retain the SSAM UART parent and client registry, which the keyboard hub's ELF
dependencies do not cover. Keyboard input has been confirmed in the v23
initramfs diagnostic; that observation does not qualify all input devices.

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
