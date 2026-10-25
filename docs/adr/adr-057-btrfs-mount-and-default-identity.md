---
id: adr-057-btrfs-mount-and-default-identity
title: "ADR057: Btrfs mount and default-subvolume identity"
description: Reconcile Btrfs inode devices and positively attest the top-level default without weakening installation ownership.
---

# ADR057: Btrfs mount and default-subvolume identity

## Status

Proposed on 2026-10-25 for [#79](https://github.com/ooaklee/lexr.sh/issues/79).
This extends [ADR040](adr-040-installation-scoped-grub-ownership.md) without
changing its multiboot attribution, privacy or transaction boundaries.

## Context

The issue reports a stable Btrfs inode-device/mountinfo-device difference.
Linux Btrfs reports a subvolume's anonymous device from `getattr`; it need not
be the device in the mounted view. The issue's later reproduction does not
establish anonymous-device renumbering as the cause.

The [Btrfs subvolume contract](https://btrfs.readthedocs.io/en/latest/btrfs-subvolume.html)
also permits changing which subvolume mounts by default. A mounted `FSRoot=/`
does not by itself attest the current default. The proposed local workaround
must not attribute a bare GRUB root when that default selects another tree.

## Decision

- Reconcile a Btrfs device mismatch only when `statx` returns the requested
  mount-ID evidence and agrees with the selected mountinfo ID and the complete
  live directory mode, inode and device. Missing masks, syscall support or
  matching identity remain unavailable. Other filesystems retain exact device
  checks; no numerical skew or blanket Btrfs tolerance is introduced.
- Reject a root-local boot directory on a different live Btrfs subvolume unless
  it has an explicit mounted boot view. Recheck selected mount and statx views
  before returning, retaining the transaction's existing identity pinning.
- Attribute the narrow bare-root/default-ID-5 case only after a fixed system
  `btrfs subvolume get-default` command positively attests it. Pin the selected
  directory through an inherited read-only descriptor, use a C locale, bound
  output and execution, and accept only the exact `ID 5 (FS_TREE)` result with
  no diagnostics. No shell, caller-controlled executable search or new
  dependency installation is permitted.
- Missing tools, permissions, timeouts, other default IDs or ambiguous output
  provide no attestation. Recheck it before returning and include it in the
  private comparable identity. Explicit subvolume selection is unchanged;
  other implicit default layouts remain unsupported.

The [upstream command](https://github.com/kdave/btrfs-progs/blob/master/cmds/subvolume.c)
reads the default ID through libbtrfsutil and emits the accepted line for ID 5.
It opens a directory read-only. This path never calls `set-default`, changes a
mount, or includes raw command output or filesystem identifiers in receipts.

## Verification and limits

Portable regressions cover exact output parsing, bounded capture, implicit
attested/unattested roots, explicit subvolumes, separate boot views, foreign
roots and pinned-identity changes. Linux CI must execute the Linux-only
mount-ID/mask/inode/device/mode and resolver recheck fixtures; a macOS host or
cross-compilation alone cannot close that execution gate.

Software tests do not establish a real Btrfs installation, boot or peripheral
qualification. Existing sensor work, kernel builds, installed userspace and
the Surface are outside this correction.
