---
id: adrs-adr007
title: "ADR007: Deterministic installed-system hand-off from Ubuntu media"
description: Architecture decision for carrying the selected Surface kernel and boot support through installation.
---

## Status

Accepted on 2026-08-30.

## Context

A live image can boot the custom kernel while still installing an operating system that lacks that kernel, its device trees, or usable firmware entries. Leaving packages under an ISO support directory is not an installed-system strategy. Running the historical support scripts after installation would also reintroduce retired audio, touchscreen, wireless, and service workarounds that no longer belong in the maintained path.

The X1E/OLED and X1P/LCD devices require different device trees. Installed
media also needs ordinary storage and audio behaviour rather than the temporary
USB safeguards used by the live environment. During remastering, the
deployable root lives in an anonymous Docker volume with no destination block
device. `grub-probe` cannot identify that root, so a generated `grub.cfg` would
be fabricated build-host state rather than an installed-system boot contract.

## Decision

The Ubuntu adapter will register the exact ARM64 image and modules packages in
the deployable Casper root's dpkg database. It will create a separate
non-Casper initramfs for that ABI using the isolated ARM64 tools image's trusted
Dracut engine, modules, userspace and configuration with non-hostonly settings
and a same-directory atomic replacement. The deployable root contributes only
its validated exact-ABI module tree through `--kmoddir`. Redistributed firmware
is copied as data into the disposable tools container's canonical firmware
directory and selected through `--fwdir`, so archive paths remain canonical.
This avoids a chroot which lacks mounted `/proc`, `/sys` and `/dev`, prevents
source-image Dracut scripts from executing as container root, and avoids
`--sysroot` redirecting trusted Dracut module sources through the extracted
root.

Installed GRUB defaults will contain the common Surface platform arguments. It
will not contain the live-media-only `qcom_q6v5_pas` blacklist or the retired
SoundWire compatibility parameter.

Embedded delivery retains the manifest-bound Stubble inventory without adding
an external DTB authority. For raw delivery, the adapter installs the generic
boot-support package and invokes its bounded helper with the one explicitly
selected platform. The package materialises digest-bound canonical and
stock-GRUB-compatible exact-ABI DTBs and owns their post-install and post-remove
lifecycle. It does not download content, execute catalogue data or install
historical workarounds.

The adapter will not run `update-grub`, `grub-probe` or `grub-script-check`
against the anonymous offline root. The structural publication gate will
instead extract the default minimal deployable root and verify dpkg
registration, installed kernel and initramfs contents, device-tree digests,
GRUB defaults, package lifecycle boundaries and a bounded, executable,
non-symlink stock `10_linux` generator which prefers `dtb-${version}` and emits
the selected device-tree path. Ubuntu's installer owns concrete GRUB generation
after it has mounted the real destination filesystem; post-install
`lexr doctor boot` is the first authoritative entry and default-selection
proof.

The initramfs publication gate requires an entry point, shell, mount and module
loading tools, Dracut's core library and root parser, the exact ABI's
`modules.dep`, at least one real kernel module, and no Casper scripts. Dracut
installation errors are fatal even if Dracut itself returns success.

The optional full-desktop upper layer carries its own dpkg status database and
is outside this first proven hand-off. The project still requires a real
minimal installation, confirmation that the installer runs its target
`update-grub` step, and an installed boot on Surface hardware before describing
a release as hardware-validated.

## Consequences

- Installing from the remastered Ubuntu media no longer depends on a separate all-purpose post-install script for its selected kernel and boot paths.
- Live USB safeguards remain isolated from installed-system behaviour.
- Later raw kernel packages can refresh their declared exact-ABI device tree
  through the generic package-owned lifecycle.
- Image creation proves that GRUB generation is ready, while installed-target
  diagnosis proves the concrete generated entries; neither boundary claims the
  other's evidence.
- Firmware with restricted redistribution remains outside the image and must be acquired separately from an authorised source.
- Structural checks can prove payload coherence for the default minimal root but cannot prove that every Ubuntu installer choice deploys or boots it correctly on hardware.
