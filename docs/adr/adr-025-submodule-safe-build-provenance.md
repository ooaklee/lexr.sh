---
id: adrs-adr025
title: "ADR025: Submodule-safe local build provenance"
description: Architecture decision for preventing Go's containing-repository discovery from becoming Lexr source provenance when the standalone project is consumed as a Git submodule.
---

## Status

Accepted on 2026-08-31.

## Context

Lexr can be cloned directly or checked out through OE's `cli/lexr` Git
submodule. A normal submodule represents its repository metadata with a `.git`
pointer file. The selected Go toolchain's automatic VCS discovery recognises a
Git root by its `.git` directory and can therefore walk past that pointer to the
containing OE repository.

A direct `go build` from the submodule consequently embedded the OE commit in
the executable's `vcs.revision` setting. Lexr's version package accepted that
setting as a fallback, so `lexr version` reported a valid-looking but unrelated
revision. Using that value as image or companion provenance would be worse than
reporting that the development identity is unknown.

GoReleaser already injects the release version, Lexr commit and build date with
linker flags. The companion builder also disables automatic VCS stamping and
injects the clean selected source identity. Those controlled build paths were
not affected.

## Decision

The version package trusts explicit linker values for the version, commit and
date. It may use the main Go module's non-development version as a display
fallback, but it never accepts automatic `vcs.revision` or `vcs.time` values as
Lexr provenance.

Local source builds use the Go-native `cmd/lexr-build` helper. The helper:

- validates the exact Lexr module root;
- asks Git for the revision and descriptive version in that root, including a
  dirty marker when tracked or untracked source differs;
- records the actual UTC build time;
- invokes Go through argument-separated process execution with CGO disabled,
  path trimming and `-buildvcs=false`; and
- injects all three metadata values through fixed linker variables.

An exported source tree without Git metadata remains buildable. Its helper-built
binary reports `dev` and `unknown` revision provenance while retaining the real
build time. A bare `go build` also fails closed to an unknown commit and date;
the documented helper is required when an exact local Git identity is wanted.

## Consequences

- Standalone and submodule source builds report the selected Lexr checkout
  rather than whichever repository happens to contain it.
- Release and companion build contracts remain unchanged and continue to inject
  their independently verified identities.
- The local build path remains portable Go code and introduces no shell script
  or Make dependency.
- Developers who deliberately bypass the helper receive less metadata, but it
  is honest and cannot silently contaminate downstream provenance.
