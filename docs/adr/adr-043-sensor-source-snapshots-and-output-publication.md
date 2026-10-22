---
id: adrs-adr043
title: "ADR043: Sensor source snapshots and output publication"
description: Keep authenticated source authority, build evidence and development artefacts separate.
---

## Status

Proposed on 2026-10-20. This extends [ADR041](adr-041-unqualified-sensor-source-builds.md)
and [ADR042](adr-042-supervised-sensor-dependency-image-production.md) without
rewriting or superseding either record. The snapshot, recipe, decoder and
publication components do not deliver the source-build controller, a CLI,
installation support or physical sensor qualification.

## Context

A mutable repository cannot remain the execution authority after an initial
checksum pass. Likewise, a manifest emitted by a compiler job cannot authorise
its own outputs. A failed or cancelled job must not leave artefacts that look
like a completed build, and a successful filesystem rename must not be reported
as though it never happened if a later durability check fails.

## Decision

Capture the complete source composition using compiled authorities. Place a
verified copy in a fresh repository-shaped snapshot beneath an existing
current-user-owned private parent. Hold directory handles, close the copied
entry roster and remove write permission only from that copy. Recheck source
bytes, links, execution bits, readonly modes and path identities immediately
around future controller use. A snapshot path or descriptive lineage commit
alone grants no execution authority. Do not modify the original checkout.

The compiled native recipe separates minimal volume initialisation from
unprivileged offline compilation. It builds QMI, SSC and the proxy against
private providers, checks the proxy's six logical installation destinations
before compilation, and stages installation into a private root. Only the
selected local fixtures and explicit help/version smoke checks are permitted.
The future controller must independently enforce network, mounts, identity,
capabilities, resources and terminal-state checks; a script is not a substitute
for that supervisor.

Export a fixed flat archive of two shared libraries, three executables,
retained licences and software-build evidence, with three exact library
aliases. This is a development artefact set, not a complete SDK or installable
bundle. The in-band report records actual hashes and observations but remains
descriptive. The host decoder independently enforces the compiled closed file
roster, modes, limits, licence digests, link targets and report consistency.
It parses bounded tar streams without filesystem extraction, rejects additional
members and checks selected ELF and private-provider metadata. Static ELF
checks are not complete relocation, executable-behaviour or hardware proof.
Keep the fixed loader's isolated output as separate evidence.

After all future supervisor and decoder gates pass, publish into an exclusive
private staging directory. Verify the complete staged contents through
directory handles, then use a same-parent descriptor-relative no-replace
rename: `renameat2(RENAME_NOREPLACE)` on Linux or
`renameatx_np(RENAME_EXCL)` on macOS. Refuse unsupported platforms before
creating outputs. Never replace an existing result or fall back to a
non-atomic directory copy. Preserve unsuccessful staging directories for
diagnosis; do not silently reuse or delete them.

Distinguish publication, durability and verified location in the result. If a
post-rename check fails, retain the fact that publication occurred and report
the uncertainty; do not pretend rollback or absence. Receipts describe source
build observations and cannot enable an installer or a compatibility claim.

## Consequences

- Source checkout, container execution and published output have separate
  authority boundaries. No Windows runtime artefact is required.
- The host user and Docker administrator remain trusted. Readonly copies do
  not defend against another process with equivalent authority.
- Software fixtures, dependency identities and filesystem checks remain
  distinct from physical sensor roles, calibration, clocks, desktop behaviour
  and suspend/resume qualification.
- A real supervised normal-path build and its failure/refusal tests remain
  required before CLI or catalogue delivery. This record does not announce
  that workflow or a release.
