---
id: adrs-adr051
title: "ADR051: Sensor qualification packages and offline transactions"
description: Separate closed inert packages, private-loader software proof and fresh-root recovery from live installation.
---

## Status

Proposed on 2026-10-22. Extends [ADR050](adr-050-sensor-runtime-package-plans.md).
It supersedes only the planning-only boundary for a new internal qualification
workflow. Historical source-builder, public catalogue and live-installer
restrictions remain unchanged.

2026-10-22: [ADR052](adr-052-isolated-sensor-service-lifecycle-qualification.md)
proposes a separate inactive integration renderer and fixture-only service
handover/compensation engine. Its live-registration prohibition remains intact.

## Context

A verified development archive is not an installable release. The next stage
needs actual package bytes, authenticated corresponding source and a native
loader check at the intended final path, without modifying a working device.
Kernel qualification is not physical sensor evidence.

## Decision

Introduce a distinct qualification-only package. Its canonical USTAR envelope
contains exactly five assets: the closed runtime payload, corresponding source,
the independently pinned qualification declaration, portable package metadata
and checksums. Bound inputs before parsing. Require exact member order, modes,
ownership, timestamps and namespace. Re-encoding and comparing the archive
rejects alternate representations. An adjacent checksum is not independent
public release authority.

Retain ADR050's nine private runtime files and three relative aliases.
Authenticate the complete source and notices against compiled inventories.
Include the three current recipes, but record machine-readable provenance:
saved-archive replay does not prove those recipes produced the retained ELF
bytes. Do not turn lineage identifiers into observed checkout revisions.

The separate declaration in OE's sensor packaging directory is a bounded
software-test fixture: ARM64, SP11 OLED, Ubuntu 26.04, scoped 7.2.9 generation 2
and stable Lexr 0.7.0 minimum. It has no tested Lexr, OS or kernel evidence.
Its tuple is not a discovered functional minimum or support claim. Its bytes
have an independent compiled pin. It is not a production catalogue declaration.
Matching assessments remain `unverified`; an explicit experiment override
cannot relax hard mismatches.

Add a fresh-root-only qualifier, not an alternate public installer. Its
constructor exclusively creates a private session and cannot adopt an existing
root or `/`. Planning does not create a session. Use rooted descriptor-relative
operations, no-follow regular reads, retained identity checks and exclusive
same-parent publication. Passive metadata stays inside the fixed prefix;
diagnostic journals stay outside the target fixture and grant no deletion
authority.

Stage and synchronise the complete prefix before publication. Check available
space and synchronise its parent after publication. Exact repeat is idempotent
only through the same process-local managed handle and exact tree. Unknown
files, changed bytes, different packages and identity substitutions refuse
without overwrite. Rollback revalidates the owned tree, exclusively renames it
into owned quarantine and removes only known members and owned empty
directories. Retain uncertain partials on failure. Crash recovery, restart
adoption and managed upgrades are not implemented by this decision.

Prove the final loader route in an isolated, unprivileged native ARM64 image
with no network, hardware access or writable existing-build mounts. Validate
every loader row and required private provider; do not discard unexpected rows
or accept system-provider fallback. Execute only reviewed help/version exits
and a missing-provider inverse, never discovery or streaming. Exercise the
offline tests on native Linux as well as the host. These qualify one pinned
software environment and filesystem contract, not physical sensors or desktop
behaviour.

Do not add systemd, udev, D-Bus or polkit integration, global loader policy,
package hooks, a public catalogue entry or a live-root command. Exclusive
service ownership and activation recovery remain separate gates. Physical
qualification requires a separately approved experiment before any tested or
supported claim.

## Consequences

Actual package and recovery checks can proceed without changing a working
device, accepted builds or existing volumes. Receipts must distinguish saved
replay, native loader observations, offline fixture tests and unperformed
hardware work. This workflow alone authorises neither public binary publication
nor activation. See the
[qualification procedure](../operator-manual/sensor-runtime-packaging.md).
