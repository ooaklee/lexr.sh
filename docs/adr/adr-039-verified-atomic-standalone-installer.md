---
id: adrs-adr039
title: "ADR039: Verified and atomic standalone installer"
description: Architecture decision for release identity, provenance verification, atomic replacement and shell integration in the standalone installer.
---

## Status

Accepted on 2026-09-27.

## Context

`install.sh` is both the quickest supported installation path and a program
commonly fetched directly into a shell. It therefore has to make security and
failure behaviour visible without adding a second runtime or package manager.

The original installer verified a downloaded executable against a SHA-256
manifest, but several boundaries remained implicit. It ran an existing
destination to discover its version, copied directly over that destination,
accepted unvalidated release and platform selectors, and relied on the network
client's default timeout behaviour. Its PATH update assumed POSIX shell startup
files and did not safely represent every valid installation directory. The
local `--binary` path also changed the source file's mode even though the
installer only owns the destination.

Issue [#65](https://github.com/ooaklee/lexr.sh/issues/65) additionally asks the
installer not to download and reinstall a latest release that is already
present. A displayed version string is insufficient evidence for that choice:
an unrelated or modified executable can print the expected text. The release
manifest provides the identity the installer actually needs.

The checksum manifest and executable previously came from the same unsigned
release. An attacker able to replace both could make the pair internally
consistent, so the release process also needs an independently verifiable
statement binding the final manifest to the repository's trusted workflow.

## Decision

We will treat the SHA-256 digest in the release manifest as the authoritative
identity of an installed release. The installer downloads and validates the
manifest first, hashes an existing regular destination without executing it,
and skips the binary download only when those digests match for an implicit
latest-version install. `--force` remains the repair path, and an explicit
`--version` remains an intentional reinstall.

The installer writes the selected SemVer to a sibling `lexr.version` file
after an official release install. This metadata exists only to produce honest
`from` and `to` progress messages; it never authorises a skip or establishes
the executable's identity. A legacy installation without the sidecar is
reported as an existing installation rather than being executed to infer a
version. Installing a local `--binary` removes stale release metadata.

Replacement will be staged in a mode-`0755` sibling temporary file and then
renamed over the stable destination. Keeping the temporary file in the target
directory makes the rename a same-filesystem operation and prevents an
interrupted copy from truncating the previous executable. The POSIX shell
installer does not claim crash durability because it cannot portably `fsync`
both the file and directory.

Inputs will be constrained before they influence paths or release URLs:

- explicit versions must be strict SemVer without a leading `v`;
- operating system and architecture overrides must select a supported release
  target;
- `LEXR_INSTALL_DIR` must be absolute and contain no control characters; and
- contradictory `--binary`, `--version` and `--force` combinations are
  rejected.

Downloads will use bounded connection and transfer times and retry transient
failures. PATH edits will quote custom directories for POSIX shells, select an
existing Bash login file where appropriate, remain idempotent through the
installer marker, and give Fish users a native `fish_add_path` instruction
instead of writing POSIX syntax. The local-binary flow will copy the source and
set only the staged destination's executable mode.

The release workflow will attest the final checksum manifest with GitHub's
keyless `actions/attest` flow. Its OIDC-backed statement will be published
beside the release as
`lexr-v<version>.sha256sums.intoto.jsonl`. The bundle is not listed inside the
manifest it attests, avoiding a circular digest. The installer will use a
capable GitHub CLI to verify the manifest against this repository, the tagged
source ref, the release workflow and a GitHub-hosted runner before trusting its
digests.

Stable `v0.5.0` is the provenance enforcement boundary. For `v0.5.0` and later
releases, a present and capable GitHub CLI makes a missing or invalid
attestation fatal. If that verifier is unavailable, the installer warns and
continues with mandatory checksum verification so the one-command bootstrap
does not acquire a new hard dependency. Releases before the boundary remain
installable through checksum verification; if they publish a bundle, a capable
GitHub CLI verifies it opportunistically.

## Consequences

An unchanged official install no longer downloads or rewrites its executable,
while a same-version binary whose bytes differ from the manifest is repaired.
No existing destination is run during installation. A failed stage leaves the
previous executable in place, and temporary files are cleaned on normal exits
and handled signals.

The release set gains one provenance bundle. The checksum manifest continues
to enumerate the shipped executables and legal documents, while the bundle
authenticates that final manifest through GitHub's release workflow. Users with
a capable GitHub CLI receive cryptographic workflow-identity verification;
users without it receive an explicit warning and retain checksum-integrity
verification rather than a silent trust downgrade.

The installer remains a portable POSIX shell script and keeps historical
releases usable, but those choices leave limits. GitHub and the selected
release assets must remain reachable, provenance verification is advisory when
GitHub CLI attestation support is absent, and the atomic rename guarantees
visibility rather than power-loss durability. The version sidecar can be
deleted or edited without changing trust decisions, so messages may become
less specific but installation safety does not regress.
