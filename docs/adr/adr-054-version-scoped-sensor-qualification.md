---
id: adr-054-version-scoped-sensor-qualification
title: "ADR054: Version-Scoped Sensor Qualification"
description: Keep the metadata successor's source, ABI, producer and package proofs separate from the accepted sensor baseline.
---

# ADR054: Version-Scoped Sensor Qualification

## Status

Proposed on 2026-10-23. This is an additive qualification path, not a sensor
release or installation approval. Complete successor producer, package,
relocation and offline-transaction evidence must be recorded separately.

The successor-renderer deferral below is partially superseded by
[ADR055](adr-055-version-scoped-sensor-service-qualification.md). The remaining
source, package, version-isolation and activation restrictions are unchanged.

## Context

The accepted 0.4.7 sensor composition has exact source, ABI, archive and
qualification-package contracts. The downstream 0.4.8 development library adds
owned metadata diagnostics and ten public functions. Reusing the old version
or accepting either version at unrelated checks would permit mixed source,
binary, recipe and evidence lineages.

A successful native library test does not make the old runtime package a new
one. Static archive validation also cannot establish which producer actually
executed, even when a caller supplies a newly created archive.

## Decision

- Keep the existing 0.4.7 entry points and their exact policies unchanged.
  Add fixed `048` qualification entry points, with private closed profiles in
  their owning packages. Accept no caller-supplied version maps or source pins.
- Retain the selected profile in opaque source, artefact and package handles.
  Every construction boundary must require matching authenticated profiles.
  Diagnostic snapshots, version strings and compatibility manifests cannot
  substitute for those handles.
- Independently pin all four source metadata records for each composition.
  The successor has 955 raw regular files, six links and four metadata records;
  its corresponding-source archive has 968 members including three recipes.
- Require the successor's exact 107 public functions and five version nodes.
  Preserve all 97 older name/version assignments, SONAME and loader checks.
  New public definitions must be strong globals. Do not union the two ABI
  predicates or treat a matching SONAME as a complete contract.
- Extend only the successor development archive to eighteen regular members
  and three aliases. The two additional members retain the real SSC metadata
  test logs. Require both exact executables and all six helper/five parser
  cases without skips; hashing an unexamined log is insufficient.
- Keep the runtime payload at nine private files and three aliases, with five
  canonical outer package assets. Authenticate notices against corresponding
  source and retain the exact unverified qualification declaration.
- Use successor metadata schema 2 to describe static archive verification and
  compiled recipe requirements. Actual execution needs a separately retained
  receipt binding source, image, script, invocation, terminal state and output
  identities. Neither decoder manufacture nor a checksum establishes execution.
- Require a new package-pinned ARM64 relocated-loader observation and fresh
  offline install/repeat/conflict/tamper/rollback tests. The old proof remains
  evidence only for its old package.
- Require the default inert service renderer to match the package's source
  lineage to its accepted 0.4.7 source. Do not add a successor renderer or use
  the new package as authority for activation.

## Consequences

The old workflow remains reproducible while the successor advances under its
own source and evidence identities. Some implementation is shared, but fixed
wrappers select the policy; unknown or mixed profiles fail closed.

Dependency-image reuse must be identified as reuse. A direct native producer
is not a Lexr CLI invocation, and current recipe requirements are not proof
that an older image or binary was built with those bytes. Preserve failed
attempts and report skipped subcases and uninstrumented dependencies honestly.

The live installer, catalogue actions and default source builder do not advance
merely because these internal APIs exist. Service-manager integration and
physical sensor roles, axes, calibration, clocks and desktop behaviour remain
separate gates. No tested target or redistribution authority follows from a
synthetic compatibility fixture.

## Verification

Preserve old package/replay tests. Add profile-crossing, unknown-source,
missing/extra export, weak-definition, missing/duplicate/skipped test-log,
recipe-substitution and rehashed inner-corruption controls. Exercise the new
real package in fresh roots; do not replace synthetic failure tests with only
a successful installation. Independently review actual producer and loader
receipts before accepting the corresponding software gates.

This decision adds to [ADR051](adr-051-sensor-qualification-packages-and-offline-transactions.md).
It does not supersede the accepted baseline or rewrite its historical evidence.
