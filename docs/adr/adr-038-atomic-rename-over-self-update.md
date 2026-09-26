---
id: adrs-adr038
title: "ADR038: Atomic rename-over self-update from GitHub releases"
description: Architecture decision for updating the lexr executable in place from published release artefacts, and the best-effort online version check.
---

## Status

Accepted on 2026-09-25.

## Context

Lexr ships as one static binary per platform, published by GoReleaser as
`lexr-vX.Y.Z-<os>-<arch>` assets with a `lexr-vX.Y.Z.sha256sums` manifest.
Updating meant re-running `install.sh` — re-downloading a full binary even
when the installed build was already current — and `lexr version` could not
answer the natural question: "am I on the latest release?" A user had to
open the releases page in a browser and compare tags by hand.

Adding an update path raised a design question with a long history. A
legacy pattern for self-updating CLIs embeds a separate updater binary that
waits for the CLI to exit and then swaps the executable, premised on the
idea that a running executable cannot be replaced. That premise is wrong
on modern Unix: a process can write a temporary file in the executable's
own directory, make it executable, sync it, and call `rename()` over the
old path. The running process keeps the old inode and finishes normally;
the next launch picks up the new binary. No helper process, exit handshake
or restart choreography is required. Windows is the exception, because the
operating system locks the running image: there the running executable is
renamed aside first so the replacement can take its name, and restored if
the apply fails.

The remaining choice was how much of the flow to own and how much to take
from a library.

## Decision

We will self-update in place with a hand-rolled apply path, and no updater
binary.

`lexr upgrade` checks GitHub's latest release endpoint
(`api.github.com/repos/ooaklee/lexr.sh/releases/latest`) with a
`lexr-cli` user agent and a three-second timeout, compares versions with
`golang.org/x/mod/semver` (our single new dependency, used for semver
parsing), downloads the GoReleaser binary asset for the running
GOOS/GOARCH, verifies its SHA-256 digest against the release
`.sha256sums` manifest, and replaces the running executable by writing a
sibling temporary file, `chmod 0755`, `fsync`, and rename over the
symlink-resolved `os.Executable()` path. On Windows the running image is
renamed aside first and restored on failure.

Checksum verification is mandatory: a release without a checksum manifest
is refused rather than applied unverified, and a digest mismatch aborts
the update.

Version comparison follows semver strictly, including prereleases:
`0.4.0 → 0.5.0-rc.1` is an upgrade, `0.5.0-rc.1 → 0.5.0` is an upgrade,
but `0.5.0 → 0.5.0-rc.1` is not, because a prerelease never outranks its
own release. A `dev` build has no semantic version and never offers an
update.

The offline contract differs by command, and both halves are deliberate:

- `lexr version` performs the release check best-effort. Offline,
  rate-limited (HTTP 429) and failed checks print exactly the previous
  version output — no notice, no error, no delay. A strictly newer release
  adds one line:

  ```text
  A new release is available: v0.5.0-rc.1 (current v0.4.0). Run `lexr upgrade` to update.
  ```

- `lexr upgrade` requires the network. Offline it fails immediately with
  `unable to check for updates: no network connection` (exit 1) rather
  than retrying or hanging. When the installed build is current it prints
  an already-up-to-date message and exits successfully.

We considered three options:

1. An embedded or external updater binary (rejected). Rename semantics
   make it unnecessary; it adds a second artefact to ship, sign and
   verify, plus an exit/restart handshake, for no capability gain.
2. A library such as `minio/selfupdate` or
   `creativeprojects/go-selfupdate` (rejected as a dependency). They
   implement the same rename-over approach we need, but our flow is small
   enough to own: the release naming is fixed by our GoReleaser
   configuration, and the repository prefers a minimal dependency
   footprint. The one dependency we did add is `golang.org/x/mod
   v0.41.0` for semver comparison rather than hand-rolled version
   parsing.
3. Hand-rolled discovery, verification and apply in `internal/update`
   with a thin command layer in `internal/cli` (chosen). The whole flow
   is testable offline through `httptest` servers and fixture releases.

## Consequences

The single-binary distribution is preserved: updates consume the exact
artefacts GoReleaser already publishes, so the release process is
unchanged, and no signing, packaging or helper distribution is added.

Trade-offs we accept:

- The release feed is GitHub Releases only. There is no mirror or
  air-gapped story; hosts without GitHub connectivity cannot update.
- The release check is unauthenticated and therefore subject to GitHub
  API rate limits. By design this degrades silently for `lexr version`
  (no notice) — a rate-limited user simply does not hear about the new
  release until the limit clears.
- The Windows apply path follows the documented rename-aside convention
  but has not been host-tested; it is compile-checked only, by CI's
  build of the six release targets.
- The atomic apply relies on the temporary file living on the same
  filesystem as the executable, which is why it is created in the
  executable's own directory rather than the system temporary
  directory.
