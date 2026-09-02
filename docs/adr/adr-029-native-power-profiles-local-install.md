---
id: adrs-adr029
title: "ADR029: Native SP11 power-profiles local installation authority"
description: Architecture decision for installing the exact OE-owned power-profiles-daemon qualification package without a caller-selected bundle.
---

## Status

Accepted on 2026-09-02.

## Context

The Surface Pro 11 kernel exposes its device-tree platform policy through the
native `/sys/class/platform-profile/platform-profile-N` interface. Ubuntu's
unmodified `power-profiles-daemon 0.30-2` does not consume that interface, while
the OE repository carries and locally builds the reviewed native-class patch as
`power-profiles-daemon 0.30-2+sp11.1`.

The local package is a qualification artefact rather than a published Lexr
userspace release. Treating an arbitrary `--from` directory or an editable
`SHA256SUMS` file as installation authority would violate ADR006. Installing
the package name from configured repositories would also be ambiguous: the
unmodified distribution package has the same binary package name and does not
provide the required integration.

## Decision

`lexr userspace install power-profiles` is a compiled native adapter and does
not accept `--from`. It locates the OE checkout by walking upward from the
current directory, or uses an explicit `--repository-root`. Repository
discovery requires the exact compiled identity of the pinned `BASE.txt`; the
root is a canonical non-symbolic-link directory.

The adapter accepts exactly one fixed build directory:
`build/lexr/power-profiles-daemon-sp11.1`. Its closed set comprises
`SHA256SUMS`, the Debian `.buildinfo` and `.changes` records, and the ARM64
`0.30-2+sp11.1` binary package. Lexr compiles the filename, byte length, and
SHA-256 digest of all four files and independently verifies that `SHA256SUMS`
covers exactly the other three. Consequently, neither the checkout, catalogue,
nor a rewritten checksum file can authorise different package bytes.

A dry run performs all static checks without privilege or mutation. A real
install continues to require effective UID 0 and `--yes`. Lexr copies the
verified package into a private transaction directory while hashing it again,
passes only that staged path to one fixed `apt-get` transaction, and restarts
only `power-profiles-daemon.service`. Alternate target roots are rejected
because distribution package-manager and live-service semantics are not safely
portable to them. A successful package transaction followed by a failed daemon
restart is reported as durable installed state with incomplete activation.

This local adapter does not build, download, publish, or discover a package by
name. A future published package feed or release requires a separate signed or
release-manifest authority and must not silently widen this contract.

## Consequences

- Operators can install the locally qualified native-class integration without
  manufacturing a userspace bundle receipt or supplying `--from`.
- The command fails closed outside an identifiable OE checkout unless
  `--repository-root` is supplied.
- Rebuilding the package changes its bytes and therefore requires an explicit
  Lexr policy update before privileged installation.
- `recommended` remains audio plus IPTSD; power profiles remain an explicit
  machine-specific qualification choice.

## Rejected Alternatives

**Install `power-profiles-daemon` by package name.** This can select the
unmodified distribution package and does not bind the native-class patch.

**Trust the build directory's `SHA256SUMS` alone.** An attacker or accidental
local rebuild can replace both the package and its self-authored checksum.

**Reuse `--from`.** The existing flag denotes an authenticated downloaded or
native release input. This qualification output has a different, compiled
source and artefact authority and should remain visibly distinct.
