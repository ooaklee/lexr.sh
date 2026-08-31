---
id: adrs-adr026
title: "ADR026: Apache-2.0 project terms and release notices"
description: Architecture decision adopting Apache-2.0 for Lexr, recording contribution terms, and making legal documents part of every CLI release.
---

## Status

Accepted on 2026-08-31.

## Context

Lexr had reached a slightly awkward point: the repository could build a useful
CLI and prepare release binaries, but it had no project-wide redistribution
terms. The companion builder honestly recorded that state as `not-declared`,
and tag publication stopped before GoReleaser. That was safer than inventing
terms, but it also meant nobody had clear permission to use, change, or share
the project.

The project needs permissive terms that work for community contributions and
do not prevent a separately developed hosted service or paid support offering
later. An express patent grant is also useful for contributors and users.
`Apache-2.0` provides those qualities without requiring software that merely
communicates with Lexr to adopt the same terms.

Lexr can build and consume a Linux kernel, but the CLI and the kernel are
separate works with separate source and release boundaries. Choosing
`Apache-2.0` for Lexr does not alter the kernel's GPL terms or the terms of any
hardware-support payload. The two GitHub workflows that carried
`GPL-2.0-only` SPDX declarations are first-party Lexr automation rather than
kernel source; the repository history attributes their authorship to the
project owner.

The release executables also link MIT, Apache, and BSD code, the Go runtime,
and generated Unicode data. Publishing a raw executable without the
corresponding notices would make those obligations too easy to miss. The legal
documents should therefore be release payloads, not checks that disappear once
the build starts.

## Decision

Lexr adopts `Apache-2.0` for its first-party work. The repository root contains:

- `LICENSE`, the Apache 2.0 terms with Lexr's completed copyright notice;
- `NOTICE`, carrying the project name and copyright notice; and
- `THIRD_PARTY_NOTICES.md`, carrying the audited dependency inventory and the
  applicable third-party terms.

The two existing workflow SPDX declarations change from `GPL-2.0-only` to
`Apache-2.0`. The root documents remain the single project authority, so the
rest of the source tree will not receive hundreds of repetitive per-file
headers. This keeps the change reviewable without weakening the declaration.

Contributions intentionally submitted for inclusion are provided under
`Apache-2.0` unless the contributor explicitly says otherwise, matching section
5 of the project terms. Contributors keep their copyright, and Lexr does not
require a CLA or DCO sign-off. `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, and
`MAINTAINERS` explain the human side of taking part.

Every Lexr CLI release contains exactly ten files:

- six raw executables for Linux, macOS, and Windows on AMD64 and ARM64;
- `LICENSE`, `NOTICE`, and `THIRD_PARTY_NOTICES.md`; and
- one versioned SHA-256 manifest covering all nine payload files.

The companion builder already treats the three legal files as authoritative
project documents. It records `project_licence: declared` and carries those
files in the deterministic source archive and on-media licence directory.
Governance documents are left out of that deliberately narrow, buildable
source snapshot; they remain in the tagged repository and its source archive.

This decision supersedes only the Lexr CLI release-asset portions of ADR009 and
ADR024. Legal documents are now published rather than acting only as gates.
Their companion, ownership, kernel-publication, credential, and raw-binary
decisions remain unchanged.

## Consequences

- People can use, modify, and redistribute Lexr under explicit terms, including
  the patent grant and conditions in `Apache-2.0`.
- A future service can charge for separately developed features or support
  without changing the CLI's open-source terms.
- Building or downloading GPL-covered kernel material does not relicence Lexr,
  and Lexr does not relicence that material.
- Every raw binary is published beside the exact project and dependency terms,
  with one manifest protecting the integrity of all nine payloads.
- Dependency changes now include a notice review. New linked code or generated
  data must be reflected in `THIRD_PARTY_NOTICES.md` before release.
- CLI releases grow from seven files to ten, while still excluding kernel,
  firmware, driver, userspace, catalogue, collector, and unrelated
  documentation assets.
