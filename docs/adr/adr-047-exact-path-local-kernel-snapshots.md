---
id: adrs-adr047
title: "ADR047: Exact-path local kernel snapshots"
description: Proposed representation checks for legitimate case-distinct kernel source paths without weakening committed-tree or extraction identity.
---

## Status

Proposed on 2026-10-21, with a local implementation for review. This is not an
adopted project decision or evidence of a completed kernel build.

On 2026-10-22, this record was renumbered from the alpha branch's ADR041 to
ADR047 when integrating that branch with main's independent sensor ADR041–046.
The [published alpha record](https://github.com/ooaklee/lexr.sh/blob/v0.6.0-alpha/docs/adr/adr-041-exact-path-local-kernel-snapshots.md)
retains its original number. This status note and the current identifier do
not change the historical context, proposed decision or consequences below.

If adopted, this decision partially supersedes the blanket case-folded-path
rejection in [ADR037](adr-037-committed-local-kernel-source-snapshots.md).
The original record remains intact. Its clean committed source, deterministic
archive, provenance, identity and release-publication boundaries are unchanged.

## Context

Linux contains legitimate case-distinct paths, including netfilter headers
such as `xt_CONNMARK.h` and `xt_connmark.h`. Rejecting every lowercased or
case-folded collision rejects an otherwise valid clean kernel checkout, even
when its destination can preserve both files. Whole-path folding is also not
a filesystem identity check: implicit directories can alias independently of
their children, and filesystems do not share one Unicode equivalence policy.

The host archives immutable Git objects, not working-tree file contents. Its
filesystem therefore does not need to represent the archived paths during
capture. The source worktree must still be clean; a case-insensitive checkout
which has lost distinct committed paths does not qualify merely because its
Git objects remain intact.

The extraction destination must represent the complete source namespace
without aliasing. Removing the case-folded check alone does not prove this,
and checking only the operating-system name or Git's `core.ignorecase` setting
would not establish the destination filesystem's behaviour.

## Proposed decision

Validate host Git paths by their exact spelling. Continue rejecting duplicate
exact names, reserved Git metadata components, submodules, unsupported modes,
non-canonical paths, escaping links and archive attributes which would change
the committed tree. Keep the existing file, path, archive and output bounds;
also bound archive members and namespace entries, including implicit parents,
to two million each.

Before extracting any payload, validate the entire archive namespace by exact
path components, including implicit directories. Reject repeated members,
regular-file or symlink ancestors in either archive order, hardlinks and other
unsupported member types. The destination must be a fresh non-symlink directory.

Create a private temporary namespace on that same destination filesystem.
Materialise every required directory and an exclusively created empty regular
sentinel for every file or symlink. Never create archive symlinks in this probe.
Reserve the future `.git` directory in the probe as well, so filesystem-specific
metadata aliases cannot become source payloads.
Any alias, unsupported name or probe error fails closed before payload
extraction. This tests the actual names and parents rather than inferring
filesystem behaviour from a lowercase comparison or a single ASCII example.
Check every probe directory's exact entry spellings once the namespace exists,
so normalisation without an immediate collision also fails closed.
Remove only this newly created probe before extracting the verified archive.

Apply Python's data extraction filter to every member. Where it normalises
symlink text, retain the original committed target after those filter checks;
the complete namespace has already excluded symlink ancestors. Verify the exact
extracted link text and final containment after all members exist, before Git
reconstruction. A lexically internal link can still resolve outside through
another link; per-member validation must not depend on archive order.

Reconstruct and compare the exact Git tree and raw commit as before. The
filesystem probe proves that paths can coexist; it does not prove contents,
executable modes or link targets. Only the final identity checks qualify the
extracted source for compilation. No bypass flag or kernel filename rename is
introduced, and remote-source builds retain their existing path.

## Consequences

- Legitimate case-distinct kernel files can be retained on a destination which
  represents their complete namespace faithfully.
- A clean source checkout is still required. Use a case-sensitive source
  filesystem when the committed tree contains case-distinct names; do not
  reset or hide the resulting changes on a case-insensitive checkout.
- The destination probe consumes temporary directory entries and empty-file
  inodes proportional to the bounded source namespace. It does not copy source
  contents or consume another full source archive's data space.
- Duplicate, ancestor, metadata and final link checks fail before compilation.
  Failed private candidates are not accepted as reusable completed snapshots.
- Host-only mocks or a small source fixture do not demonstrate a complete
  kernel build, package validation, bootability or hardware qualification.
- Adoption should follow a focused issue and review of compatibility, safety
  and test evidence, as required by the contribution guide.
