---
id: adrs-adr037
title: "ADR037: Committed local kernel source snapshots"
description: Architecture decision for building an exact local kernel commit without requiring a remote branch or accepting mutable working-tree input.
---

## Status

Accepted on 2026-09-25.

## Context

The native kernel builder fetches a branch or tag from a credential-free HTTPS
remote. That contract gives every build an exact revision and tree, but it
forces hardware developers to publish a temporary branch before testing a
candidate which may not boot.

Accepting staged, unstaged and selected untracked files would require Lexr to
define a second Git index, filesystem race, symbolic-link and archive identity
model. The working tree could change between review and capture, and a changed
file inventory would become persisted provenance in addition to the Git
identity. The immediate developer need does not require that authority: Git can
record a local commit without publishing it.

## Decision

`kernel build --source-dir <worktree>` selects the exact `HEAD` commit of one
canonical local Git worktree. Local mode is mutually exclusive with
`--git-url` and `--git-branch`. The worktree must have no staged, unstaged or
untracked changes; a developer commits the intended candidate locally but does
not need to push it.

Lexr validates the committed tree, rejects submodules, unsupported Git modes,
case-folded path collisions and symbolic links which can escape the tree, then
creates a deterministic `git archive` from the immutable commit object inside
the private build transaction. It never mounts or copies the live checkout.
The container verifies the archive digest, validates every extraction path,
reconstructs the Git tree and commit object, and refuses any identity mismatch
before compilation.

Build plans and provenance use a source-kind union. HTTPS builds retain their
URL, ref and fetched-ref kind. Local builds instead record the common exact
revision and tree plus `local_source_revision`, archive digest, archive size and
file count. The absolute local path is runtime-only and is omitted from
serialised receipts and public metadata. The exact archive is retained in the
closed local build output.

Managed build-volume reuse is allowed only for the same archive identity.
`--skip-clean` refuses a changed local snapshot, while `--reset-source` may
remove only the Lexr-owned managed source. Kernel release preparation rejects
local-source provenance. A release build must fetch the published commit from
an HTTPS branch or tag so remote publication never implies that a local-only
object is independently available.

## Consequences

- Developers can test a local commit without creating or pushing a temporary
  branch.
- The commit and tree identify the compiled source, and the retained archive
  preserves the exact corresponding bytes even if the local commit is later
  removed.
- New files must be committed before a build. Dirty working-tree snapshots are
  deliberately deferred rather than represented by incomplete provenance.
- Existing HTTPS builds remain the default and keep their remote-fetch
  behaviour.
- A locally sourced build is suitable for inspection and guarded local
  installation, but not release preparation or hosted publication.
