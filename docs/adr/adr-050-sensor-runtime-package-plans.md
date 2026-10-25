---
id: adrs-adr050
title: "ADR050: Sensor runtime package plans"
description: Separate verified sensor development outputs, inert package layout and independently qualified runtime installation.
---

## Status

Proposed on 2026-10-22. This is the packaging foundation, not acceptance of a
runtime package, installer, catalogue entry or physical sensor support.

The later [ADR051](adr-051-sensor-qualification-packages-and-offline-transactions.md)
extends this record with an internal qualification package and fresh-root-only
transaction. Its planning-only boundary is superseded for that workflow, not
for the public installer or catalogue; this historical decision is retained.

This extends [ADR049](adr-049-component-compatibility-manifests.md) with an
explicit representation of absent kernel test evidence and the closed
`sensors` capability token. It does not change that record's independently
pinned authority or hard target bounds. The source-only decisions in
[ADR041](adr-041-unqualified-sensor-source-builds.md),
[ADR043](adr-043-sensor-source-snapshots-and-output-publication.md) and
[ADR045](adr-045-local-sensor-build-transport-and-bind-namespaces.md) remain
intact. A future installation decision must supersede the relevant boundaries
explicitly rather than relabelling historical build evidence.

## Context

The qualified native producer exports 16 regular development files and three
library aliases. Five files are native ELF products and four retain licence
text; the other seven are development reports or build evidence. The closed
archive does not contain the actual systemd, udev, D-Bus or polkit integration
files. Recording their logical destinations in a build report does not grant
installation authority.

Those six destinations describe the upstream installation observation, not a
partially assembled runtime package. A private runtime layout deliberately
relocates the proxy and monitor rather than filling in those global paths.

The existing provider proof uses `/work/job/providers/lib`. The exported ELF
files have no RPATH or RUNPATH, so copying them to another prefix does not
establish which QMI and SSC libraries a runtime loader will select. Replacing
distribution libraries or configuring a global library search path would
also affect unrelated applications.

The desktop proxy owns the global `net.hadess.SensorProxy` D-Bus identity.
The upstream udev rule requests `iio-sensor-proxy.service` automatically.
Installing that rule can therefore activate a service later even when an
installer never calls `systemctl start`. A passive package and a service
activation transaction need distinct boundaries.

The compatibility manifest could previously express absent Lexr and OS test
evidence, but required a positive tested kernel generation. That would force
a new sensor declaration to manufacture evidence. Successful kernel tests or
native compilation do not establish sensor runtime compatibility.

## Decision

Retain `native-sensors` as the historical experimental build-only identity.
Keep its compiled action ceiling and the shipped catalogue unchanged. Reserve
the separate runtime planning identity `native-sensors-runtime-v1` and proposed
release `sp11-native-sensors-v1`; reservation does not publish either one.

Introduce a pure package-layout planner which accepts only the opaque result
of the existing strict artefact decoder. It maps the five ELF products, four
licence files and three exact relative aliases beneath the fixed private
prefix `/usr/lib/lexr/sensors/sp11-native-sensors-v1/`. It copies identities,
not executable bytes, to the plan. It neither extracts an archive nor writes
files, runs commands, contacts Docker, reads a target root or installs a
package. The seven development/evidence files are not runtime payloads or
automatically public provenance.

Every plan reports `qualification_required`, with installation and activation
disabled. It records the approved source composition rather than inventing an
observed checkout HEAD. The four source inventory/checksum identities and
required recipe hashes describe expected compiled policy; they are not proof
that a saved archive was produced by that policy. Its fixed paths and pending
gates cannot be replaced by editable metadata. A plan is not a downloadable
release, transfer receipt, compatibility decision or installation grant.

Register `sensors` in the closed manifest capability vocabulary without
granting any component action. Permit kernel `tested_through_inclusive` to be
omitted when no kernel qualification exists. The internal zero value denotes
absence; canonical JSON never writes an explicit zero. Explicit zero, null,
negative, malformed and ambiguous evidence remains invalid. Hard kernel
domain and generation bounds remain mandatory. An absent tested-through value
produces `unverified` within those bounds, never `tested`.

Existing nonzero evidence retains its canonical bytes and independent pins.
Older consumers reject the new omitted-evidence form rather than accepting it
with different meaning. Alternative complete target rules still cannot share
test evidence. The dedicated unverified override cannot relax hard bounds,
missing identity or compiled action restrictions.

Do not author a sensor compatibility declaration until maintainers review its
narrow experimental target and hard bounds. No minimum kernel generation is
derived from v26/v27, the working 7.2.9 kernel or the source producer. A future
declaration needs an independent reviewed pin and its own recorded evidence.

Before proposing an opt-in experimental installer, require:

1. A complete inert package contract covering corresponding source, retained
   notices, build recipe, provenance and applicable relinking requirements.
2. Native proof of the final private loader route and target dependencies,
   without a global loader configuration or vendor library replacement.
3. An offline-root transaction which performs no target execution or service
   activation, refuses unmanaged conflicts and preserves exact rollback data.
4. Exclusive ownership of the desktop proxy identity, plus a separate explicit
   activation transaction which can restore prior units, drop-ins, masking,
   active state and ownership after failure or removal.

Physical qualification remains a separate gate before recording tested target
evidence or claiming sensor support: obtain explicit approval to check sensor
roles, axes, calibration, clocks, desktop behaviour and suspend/resume. A
future experimental installer may be used for that separately approved work
only after its software and lifecycle gates pass; the missing hardware evidence
must remain unverified. This planning foundation enables no installer.

Keep global service, udev, D-Bus and polkit files absent during passive staging.
An eventual package may retain reviewed templates inertly, but must not place
them in auto-discovered system locations before activation is authorised.

## Consequences

The package layout and missing-evidence semantics can be reviewed and tested
without changing a target device or rebuilding accepted outputs. Saved-archive
replay remains a host-side check, not a new native producer run.

The staged provider stack remains private rather than replacing distribution
QMI, SSC or proxy files. This adds explicit packaging, dependency and lifecycle
work before installation can be enabled, but makes coexistence and recovery
testable instead of relying on service-start timing.

No install command, source declaration, catalogue entry or hardware support
claim is created by this decision. The
[sensor packaging qualification procedure](../operator-manual/sensor-runtime-packaging.md)
tracks the next gates separately from completed source-build evidence.
