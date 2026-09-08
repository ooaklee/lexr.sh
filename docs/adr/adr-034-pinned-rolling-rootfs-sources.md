---
id: adrs-adr034
title: "ADR034: Pinned rolling root filesystem sources"
description: Represent authenticated root filesystem snapshots without weakening source pins or losing cached inputs on refresh.
---

## Status

Accepted on 2026-09-06 for the source-intake portion of
[issue #52](https://github.com/ooaklee/lexr.sh/issues/52), following Ooaklee's
Arch Linux ARM source selection and OpenCode design review. The live-image
adapter and installation design remain separate, unfinished work.

Extends [ADR002](adr-002-validated-image-catalog.md) with catalogue version 3 and
strict version-2 read compatibility. Does not change the existing ISO or raw
image support declarations.

## Context

Arch Linux ARM publishes a signed, rolling AArch64 rootfs tarball. It is neither
an ISO nor a bootable disk image. Reclassifying it as an ISO would let an adapter
apply the wrong filesystem and boot contracts. The `latest` URL can change while
the accepted source digest must stay fixed.

The source resolver previously named every remote input as an ISO and removed
the cached file before attempting an explicit refresh. If a rolling download
changed or failed, that discarded the previously accepted input.

## Decision

Catalogue version 3 adds `rootfs-tar-gz` with a `.tar.gz` filename and mandatory
SHA-256 pin. The loader continues to accept version 2 under its original format
rules, and rejects rootfs entries labelled as version 2. No existing adapter
accepts the new kind. Catalogue-only remains the support state until creation
and independent structural validation exist.

Authenticate the selected source against the publisher's detached signature and
independently checked signing fingerprint before recording its SHA-256 in the
catalogue. Preserve the signature and intake evidence. The compiled catalogue
pin is the runtime byte authority; an observed checksum or mutable sidecar is
never promoted automatically into a trusted expected value.

The dated entry identifies an accepted snapshot. Its `mutable` flag describes
the URL, not permission to change the snapshot's expected bytes. A later accepted
snapshot needs a deliberate new pin and entry review. Source timestamp and valid
signature do not establish freshness or hardware compatibility.

Cache pinned inputs under their catalogue ID and digest, retaining the declared
file extension. Stage explicit refreshes separately, verify completely, then
publish. A failed refresh leaves the previous input intact and reports the error.
Selecting another expected digest creates another cache identity. Reuse legacy
ISO/raw cache entries only after verifying their bytes against the selected pin;
keep their original files. Durable retention outside a rolling
upstream URL remains an operator responsibility until a public snapshot source
is established.

## Consequences

- Old binaries reject version-3 catalogues; new binaries still read version-2
  ISO/raw catalogues with their existing checksum rules.
- A recorded SHA-256 may be maintainer-derived from an authenticated source,
  rather than copied from an upstream SHA-256 sidecar. Document that distinction.
- An upstream change produces a useful mismatch instead of silently changing
  the build input or deleting the last accepted copy.
- Additional pacman package inputs need their own versions, signatures and
  digests in image provenance; a rootfs pin alone cannot freeze an online upgrade.
- Tarball recognition does not add an installer, live boot support or permission
  to write an existing OS disk.
