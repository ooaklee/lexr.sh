---
id: adrs-adr030
title: "ADR030: Structural preparation of Ubuntu offline installed roots"
description: Architecture decision for preparing and validating installed-system boot assets without treating an anonymous image-build root as a mounted target system.
---

## Status

Accepted on 2026-09-04.

Structural acceptance does not constitute an installed-system boot or physical
hardware qualification.

This decision supersedes the offline-root GRUB and initramfs preparation parts
of [ADR004](adr-004-ubuntu-hybrid-iso-remaster.md) and
[ADR007](adr-007-installed-system-handoff.md). It supplements the delivery
contract in [ADR029](adr-029-self-contained-kernel-dtb-delivery.md).

## Context

Ubuntu image creation prepares a deployable root in an anonymous Docker volume.
That volume has no destination disk, partition identity or mounted boot
filesystem. Consequently, `grub-probe` cannot derive truthful target state and
a generated `grub.cfg` would describe the build environment rather than the
eventual installation.

The extracted root is also input from the source image. Using its Dracut
modules, configuration or helper programs as root-controlled build logic would
cross the image builder's trust boundary. Passing that root through Dracut's
`--sysroot` option does not solve the problem because it also redirects every
`dracut-install` source—including tooling-owned modules and transient `/dev/fd`
paths—through the extracted filesystem. In practice, that can produce
installation errors while still returning a successful Dracut exit status and
leave an incomplete installed-system initramfs.

The build therefore needs to prepare everything which can be proven without a
real target device, while reserving device-dependent GRUB generation for the
Ubuntu installer and installed-system validation.

## Decision

The image builder will treat offline boot preparation as structural evidence,
not as a simulated installed-system boot transaction. It will not pseudo-mount
host `/proc`, `/sys` or `/dev`, grant broader container privileges, fabricate a
block-device identity, or run `update-grub`, `grub-probe` or
`grub-script-check` against the anonymous root.

Instead, the deployable root must contain the exact kernel and installed
initramfs, delivery-specific device-tree state, package-owned lifecycle files,
safe installed defaults, and a bounded, executable, non-symlink stock
`10_linux` generator. The generator must prefer `dtb-${version}` before
fallback names and emit the selected device-tree path. Ubuntu's installer owns
concrete `grub.cfg` generation after it has mounted the destination filesystem.
Installed-target verification and `lexr doctor boot` then prove the generated
entries and selected default.

The installed initramfs remains a build-time artefact. Lexr will generate it
with the isolated ARM64 tools image's trusted Dracut engine, modules, userspace,
configuration and helper programs. For this operation, the extracted
deployable root contributes only validated data:

- its validated exact-ABI module directory is supplied with `--kmoddir`;
- redistributed firmware is copied into the disposable tools container's
  canonical firmware directory and selected with `--fwdir`; and
- no extracted-root Dracut modules, configuration or executables are run.

Lexr will not use `--sysroot` for this operation. It will disable host-only
state and command-line capture, request reproducible output and treat both a
non-zero exit and any Dracut installation error as fatal. Output is written to
a same-directory temporary file and published atomically only after inspection.

The initramfs publication gate requires an entry point, shell, mount and module
loading tools, Dracut's core library and root parser, the exact ABI's
`modules.dep`, and at least one regular kernel module. Casper scripts are
forbidden. Failure removes temporary output and preserves no partial published
initramfs or inspection artefact.

Image validation will prove these structural inputs and archive capabilities.
Its failure report contains bounded, sanitised check names and details before
workspace cleanup. It will not claim that Ubuntu completed installation,
generated the final GRUB configuration, or booted the installed system. A real
installation and boot on supported hardware remain release gates.

## Consequences

- The image build no longer mistakes an anonymous filesystem tree for a mounted
  operating system.
- Source-image Dracut policy is not executed during initramfs generation; the
  versioned tools image owns that privileged operation.
- The tools image's Dracut, userspace and boot-tool dependencies become part of
  the versioned image-build contract.
- Initramfs success requires usable contents rather than only a zero process
  exit status.
- Image creation can prove GRUB generator readiness, but only the installer and
  installed-target checks can prove concrete entries and defaults.
- Physical installation and boot tests remain necessary because structural
  validation cannot establish firmware or peripheral behaviour.

## Alternatives rejected

Running an offline chroot with host pseudo-filesystems was rejected because it
widens privileges and executes source-image policy. Fabricating a concrete GRUB
configuration was rejected because the anonymous volume has no truthful target
device. A mixed-root Dracut invocation using `--sysroot`, mirroring Dracut trees
into the extracted root, and passing firmware from noncanonical source paths
were rejected because they confuse tool and target ownership or produce
incorrect archive paths.

## Related decisions

- [ADR004](adr-004-ubuntu-hybrid-iso-remaster.md) records the original Ubuntu
  remaster architecture.
- [ADR007](adr-007-installed-system-handoff.md) records the original
  installed-system hand-off design.
- [ADR029](adr-029-self-contained-kernel-dtb-delivery.md) defines embedded and
  external device-tree delivery and the package lifecycle consumed here.
