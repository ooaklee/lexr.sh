---
id: adrs-adr038
title: "ADR038: Atomic rename-over self-update from GitHub releases"
description: Architecture decision for updating the lexr executable in place from published release artefacts, and the best-effort online version check.
---

## Status

Accepted on 2026-09-25.

## Context

Lexr is a single static binary published by GoReleaser as
`lexr-vX.Y.Z-<os>-<arch>` assets with a `lexr-vX.Y.Z.sha256sums` manifest.
Until now the only update path was re-running `install.sh`, and `lexr
version` could not say whether the installed build was current.

A legacy pattern for self-updating CLIs embeds a separate updater binary
that runs after the CLI exits, premised on the idea that a running
executable cannot be replaced. That premise is wrong on modern Unix: a
process writes a temporary file in the executable's directory, makes it
executable, syncs it, and calls `rename()` over the old path. The running
process keeps the old inode and continues unaffected; the next launch
picks up the new binary. No helper process, exit handshake or restart
choreography is required. Windows is the exception, because the operating
system locks the running image: there the running executable is renamed
aside first so the replacement can take its name, and restored if the
apply fails.

## Decision

We will self-update in place with a small hand-rolled apply path, and no
updater binary. `lexr upgrade` checks GitHub's latest release endpoint
(`api.github.com/repos/ooaklee/lexr.sh/releases/latest`, `lexr-cli` user
agent, three-second timeout), compares versions with
`golang.org/x/mod/semver` (our single new dependency, used for semver
parsing), downloads the GoReleaser binary asset for the running
GOOS/GOARCH, verifies its SHA-256 digest against the release
`.sha256sums` manifest, and replaces the running executable by writing a
sibling temporary file, `chmod 0755`, `fsync`, and rename over the
symlink-resolved `os.Executable()` path. On Windows the running image is
renamed aside first and restored on failure.

Checksum verification is mandatory: a release without a checksum
manifest is refused rather than applied unverified. Version semantics
follow semver strictly, including prereleases: `0.4.0 → 0.5.0-rc.1` is an
upgrade, `0.5.0-rc.1 → 0.5.0` is an upgrade, but `0.5.0 → 0.5.0-rc.1`
is not, and `dev` builds never offer an update.

The offline contract differs by command. `lexr version` performs the
release check best-effort: offline, rate-limited (HTTP 429) and failed
checks print exactly the previous version output with no notice, error
or delay; a strictly newer release adds an upgrade notice naming both
versions. `lexr upgrade` requires the network and fails with a clear
no-network error rather than hanging.

We considered three options:

1. An embedded or external updater binary (rejected). Rename semantics
   make it unnecessary; it adds a second artefact to ship, sign and
   verify, plus an exit/restart handshake, for no capability gain.
2. A library such as `minio/selfupdate` or
   `creativeprojects/go-selfupdate` (rejected as a dependency). They
   implement the same rename-over approach we need, but our flow is
   small enough to own: the release naming is fixed by our GoReleaser
   config, and the repo prefers a minimal dependency footprint. The one
   dependency we did add is `golang.org/x/mod v0.41.0` for semver
   comparison rather than hand-rolled version parsing.
3. Hand-rolled discovery, verification and apply in `internal/update`
   with a thin command layer in `internal/cli` (chosen).

## Consequences

The single-binary distribution is preserved: updates consume the exact
artefacts GoReleaser already publishes, with no release-process changes,
and the whole flow is testable offline through `httptest` servers and
fixture releases.

Trade-offs we accept: the Windows apply path follows the documented
rename-aside convention but is not host-tested here; the release check
is unauthenticated and therefore subject to GitHub API rate limits,
which degrade silently by design; updates are only possible from GitHub
releases, with no mirror or air-gapped story; and the atomic apply
relies on the temporary file living on the same filesystem as the
executable, which is why it is created in the executable's own
directory.
