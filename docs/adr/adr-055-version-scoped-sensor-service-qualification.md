---
id: adr-055-version-scoped-sensor-service-qualification
title: "ADR055: Version-Scoped Sensor Service Qualification"
description: Advance the qualified sensor successor into explicit inactive service rendering without changing legacy defaults or granting live activation.
---

# ADR055: Version-Scoped Sensor Service Qualification

## Status

Proposed on 2026-10-23. This partially supersedes the successor-renderer deferral
in [ADR054](adr-054-version-scoped-sensor-qualification.md). Its source, package,
version-isolation and activation restrictions remain in force.

## Context

The downstream 0.4.8 package has completed separate local software gates:
complete native production, authenticated package composition, relocated ARM64
loader checks and fresh-root offline transactions. These are not physical
sensor tests or a live service handover.

The original inactive renderer intentionally accepts only the authenticated
0.4.7 composition. Removing that restriction globally would silently advance
existing callers. The two versions also use the same private paths and static
unit bytes, so those values alone cannot distinguish their runtime identities.

## Decision

- Preserve `integration.Render` and `lifecycle.NewQualification` as positive,
  fixed 0.4.7 selectors. Add explicit `Render048` and `NewQualification048`
  selectors rather than accepting a caller-selected version or policy.
- Retain a private, closed profile in the opaque integration bundle. Both
  lifecycle constructors require their exact valid profile; a negative check
  for only the other known version is insufficient. Nil, zero, unknown and
  mixed-version handles grant no authority.
- Reauthenticate the complete successor source composition, not just its
  version label or diagnostic snapshot. Require its matching opaque runtime
  package, exact source lineage, notices and four independently pinned proxy
  integration templates.
- Keep the six inactive assets, fixed loader arguments and static `Type=exec`
  unit unchanged. Assets remain separate from the closed runtime package and
  installed prefix. Do not write unit, udev, D-Bus or polkit registration paths.
- Reuse the existing finite, fixture-only lifecycle engine after profile
  admission. Keep exact runtime-package and proxy-byte identity checks through
  apply, managed repeat and compensation. Identical paths or unit bytes do not
  authorise a process belonging to the older package.
- Exercise the actual accepted package and authenticated corresponding source
  for both initial vendor states. Test exact file/state restoration, mixed
  constructor refusals and substituted process identity. Unknown process
  identity must retain exclusion and require recovery, not stop an untrusted
  process or start a competing vendor service.
- Keep fixture service observations separate from real manager attestation.
  The passive observer's diagnostic snapshot cannot supply trusted process,
  unit-definition or mutation ownership evidence to a live backend.

## Consequences

The successor can advance through the existing inactive integration workflow
without rebuilding or modifying its accepted runtime package. Legacy asset and
snapshot output remains unchanged. No public installation command, live root
adoption, service activation or hardware-support claim follows.

The next execution gate requires a disposable ARM64 Linux environment with its
own systemd manager, system bus, procfs and cgroup hierarchy. It must establish
real job completion, runtime-mask ownership, unit precedence, bus release,
process epochs and conservative compensation before any live adapter is
exposed. A private bus alone, simulated manager observations or a successful
command exit cannot substitute for that gate.

Physical discovery, streaming, sensor roles, axes, calibration, clocks, desktop
integration and suspend/resume remain separately approved target tests.
Compatibility fixtures remain unverified until corresponding target evidence
exists; software rendering does not populate tested-device records.

## Verification

Retain the original saved-package tests and golden unit. Add the complete
old/new package-source-constructor matrix, deterministic and defensive-copy
checks, positive profile-seal controls and actual successor fixture lifecycle
tests. Preserve previously accepted package bytes and source history.

This decision does not supersede the real-service and physical-target gates in
[ADR052](adr-052-isolated-sensor-service-lifecycle-qualification.md) and
[ADR053](adr-053-passive-sensor-service-observations.md).
