---
id: adr-056-explicit-sensor-producer-profiles
title: "ADR056: Explicit Sensor Producer Profiles"
description: Connect the separately authenticated sensor successor to the supervised source builder without changing legacy defaults or deployment authority.
---

# ADR056: Explicit Sensor Producer Profiles

## Status

Proposed on 2026-10-23. This extends the separately scoped source and package
contracts in [ADR054](adr-054-version-scoped-sensor-qualification.md); it does
not supersede their native-execution, deployment or physical-test gates.

## Context

The downstream 0.4.8 source has separate recipe, archive, ELF and package
validators. Its saved qualification package was produced with direct pinned
recipes, not the normal Lexr source-build controller. Wiring only its decoder
into that controller would leave source snapshots, Docker ownership labels,
publication and completion reporting on the older contract.

Both versions must remain independently rebuildable. An archive's version
label or a compatibility manifest must not choose executable build policy.

## Decision

- Keep `build.New` and all default source, Docker and publication entry points
  fixed to 0.4.7. Add `build.NewMetadata048` as an explicit compiled selector.
  Retain the closed profile privately throughout the operation.
- Add explicit `source.Verify048` and `source.StageRepository048` paths for
  the complete, independently authenticated successor composition. Preserve
  retained-descriptor snapshot verification and read-only copy permissions.
- Bind the selected source, script, recipe digest, volume and container
  ownership checks, decoder, opaque artefact profile and publication roster
  together. Reject cross-profile substitutions rather than infer a profile
  from returned JSON or artefact contents.
- Expose a hidden, sensor-only `--sensor-source-profile` option with exactly
  `legacy-047` and `metadata-048` values. The omitted option retains the old
  behaviour. Reject unsupported values and use with unrelated components.
- Match CLI completion reporting to the profile selected by the request,
  not a profile claimed by the result. Preserve actual terminal, ownership,
  durability and source-change checks. Retain resources and uncertainty on
  failure; do not add cleanup, automatic retry or borrowed readiness handles.
- Keep the component experimental and build-only. Neither the selector nor
  the resulting development publication grants installation, service
  registration, firmware discovery or physical-sensor qualification.

## Verification

Use actual frozen source trees for both authentication and snapshot tests.
Exercise each orchestration path with recorded/mock transport observations,
including mixed source, recipes, ownership labels, artefact profiles,
interrupted phases and publication failures. Keep default-path regressions.

Software and mocked-controller tests alone do not establish native execution.
Record producer execution separately from package, relocation, offline,
real-manager and physical qualification. Do not relabel a saved direct-recipe
build as evidence for the normal controller.

### Local execution evidence — 2026-10-23

The reviewed local implementation based on `e76d19f` completed a fresh normal
CLI build with the explicit `metadata-048` profile. Its dependency, initialisation
and source containers exited zero, and the eighteen-file development output
with three library aliases was published durably on the local host. Seven
proxy fixture programs and both metadata fixture programs passed: 304 proxy
cases ran, one French-locale case was explicitly skipped, and all eleven
metadata cases ran. This is observed producer and local publication evidence,
not a public binary upload or a warning-free build claim.

The resulting archive also passed strict host-side verification, qualification
package composition and passive fresh-root install/repeat/rollback, unmanaged
conflict and managed-tamper checks. Its six inactive integration assets passed
version-pairing and simulated lifecycle checks with both initial vendor states,
including refusal of a substituted legacy process identity. These were host
software tests, not real systemd or D-Bus handover.

The package is byte-identical to the earlier direct-recipe 0.4.8 qualification
package. That earlier package has its own ARM64 relocation and native offline
evidence; it is not a new execution by the normal CLI follow-through. The
later receipts retain fresh ARM64 relocated-loader, native Linux offline and
service-contract replay as open gates. Controlled real-manager handover and
rollback, physical sensor qualification, and public installer/release acceptance
also remain open. No installation, activation or tested-target authority is
enabled by these results.

## Consequences

The successor gains an explicit route through the same supervised source-build
workflow while old callers remain pinned. The source, package, service and
hardware boundaries remain separate. This decision does not expose a public
sensor installer or change the current kernel or userspace configuration.

The shipped catalogue still excludes this component. The reproducible
[source-build procedure](../operator-manual/sensor-runtime-packaging.md#reproduce-the-source-build-selection)
uses an explicit bounded build-only catalogue override; the hidden selector
alone must not bypass the catalogue's authority ceiling.
