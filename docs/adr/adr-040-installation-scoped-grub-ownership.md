---
id: adrs-adr040
title: "ADR040: Installation-scoped GRUB ownership"
description: Architecture decision for validating one installed kernel without treating another Linux root as the same installation.
---

## Status

Accepted scope on 2026-09-28. Implementation and review are tracked in
[#71](https://github.com/ooaklee/lexr.sh/issues/71).

This decision partially supersedes [ADR029](adr-029-self-contained-kernel-dtb-delivery.md):
matching an ABI is no longer sufficient to include a GRUB entry in native
installation, fallback or diagnostic boot evidence. Its bundle, package
lifecycle and device-tree delivery contracts remain unchanged.

## Context

Updating Ubuntu can regenerate a GRUB menu containing another installation's
entries. Two distributions can legitimately use the same kernel ABI while
having different root filesystems, initramfs filenames and initramfs contents.
Applying Ubuntu's naming rules to an Arch entry wrongly rejects an otherwise
valid Ubuntu update. Accepting identical filenames is worse: hashing Ubuntu's
files does not prove what an entry selecting another filesystem will load.

An ABI identifies a kernel release, not the installation which owns its boot
path. Per-ABI naming also does not prevent two installations from overwriting
the same physical files if they share a boot filesystem.

## Decision

### Establish ownership before inspecting entry files

Native inspection will obtain the selected Linux root and boot views from
bounded live mount metadata and stat-confirmed device identifiers. GRUB
inspection will retain bounded literal boot-filesystem selection and Linux
root selection, including explicit Btrfs subvolume selection. It will not
execute GRUB code, use a menu title as identity, or infer ownership from a
matching ABI.

Entries will be classified as:

- `proven-owned`: boot filesystem and Linux root agree with the selected
  mounted installation;
- `proven-foreign`: the entry selects a different Linux installation; its
  bootability has not been verified;
- `unresolved`: supported static evidence is missing, ambiguous or insufficient.

Owned entries retain exact kernel/initramfs names, byte comparisons, DTB
delivery checks and normal/recovery cardinality. Proven-foreign entries do
not count as local evidence and are not opened through host-local paths.
Unresolved entries cannot qualify a required target, fallback or default.

GRUB paths are relative to the selected filesystem, not necessarily to Linux
`/`. Inspection maps each token through that exact mounted view; it never
tries both `/boot` and `/` and accepts whichever happens to contain a file.
Root and boot identity is pinned for a native transaction and rechecked
around reads and before subsequent mutations.

### Keep updates local

An Ubuntu installation updates Ubuntu only. Regenerating its GRUB menu may
rediscover other operating systems; this is not permission to update their
packages, initramfs images or device trees.

Preflight checks the current menu for foreign or unresolved references to
physical boot paths the transaction would change. Such conflicts block the
operation, including an explicit overwrite. Existing discovery capability or
non-local entries produce a plan warning because package hooks can discover
additional entries later. The generated menu is checked again after package
mutation; an earlier dry run is not proof of the later configuration.

Normal failed installations still attempt bounded target-package rollback,
retain shared boot support, and restore the previous GRUB configuration.
If mounted identity changes, recovery must not mutate the replacement root.
If a newly discovered shared-file conflict makes purging the target unsafe,
automatic purge stops and the recovery receipt explains the conflict rather
than claiming complete rollback.

### Share semantics and report limits

Preflight, install, target-state classification, rollback verification and
`doctor boot` use the same ownership rules. Doctor reports entry scope without
serialising filesystem UUIDs or unrelated kernel arguments. It does not
attribute another installation's default entry to a local DTB or silently
change that default.

The native boundary covers an exactly mounted Linux root with boot files on
that filesystem or its mounted `/boot`, plus explicitly selected Btrfs
subvolumes. An implicit Btrfs default subvolume is not inferred from the view
currently mounted. Numeric subvolume selection other than the explicit
top-level ID is unresolved. Arbitrary GRUB scripts, sourced menus, BLS loaders,
chainloaders and boot files selected from other mounted locations are not
executed or certified as local boot evidence.

An unpacked offline directory is not evidence of the destination disk's
runtime identity. [ADR030](adr-030-structural-offline-root-boot-preparation.md)
continues to govern structural image preparation separately. Tests inject
explicit synthetic mounted evidence through the internal Go dependency;
there is no CLI flag or persisted manifest which asserts runtime ownership.

## Consequences

- Same-ABI entries on different installations no longer require synchronised
  distro updates or a broader initramfs filename exception.
- Shared-file conflicts remain the installer's responsibility when its own
  operation would cause the damage; this is not a universal multiboot package
  or dependency manager.
- Operators still maintain each distro and its recovery kernel independently.
  Reinstalling an ABI alone is not guaranteed to repair an incorrect bootloader
  binding or shared-file layout.
- Ambiguous layouts may need operator review rather than an automatic update.
  Lexr does not disable discovery, hide foreign entries or bypass fallback
  verification to make an installation pass.
- Direct `dpkg` installation retains the package lifecycle from ADR029, not
  the CLI's additional ownership checks and transaction recovery.
- Fixture and static checks do not establish physical boot or peripheral
  qualification; the original hardware test remains a separate workstream.
