---
id: adrs-adr053
title: "ADR053: Passive sensor service observations"
description: Collect bounded diagnostic service evidence without loading units or activating a sensor proxy.
---

## Status

Proposed on 2026-10-23. Extends
[ADR052](adr-052-isolated-sensor-service-lifecycle-qualification.md).
It supersedes only that decision's absence of a real read-only observation
implementation. The fixture lifecycle backend and the prohibition on live
registration, activation and installation remain unchanged.

## Context

The runtime package, relocated loader and offline filesystem transaction have
local software evidence. The service lifecycle model still trusts simulated
observations. Implementing privileged effects before independently gathering
service evidence would obscure that boundary. Successful process execution,
a bus owner or a matching PID alone cannot prove the intended proxy, mapped
libraries, vendor policy or sensor readiness.

The existing ARM64 dependency image contains D-Bus tooling but neither
`busctl` nor a real system service manager. A synthetic manager, a user manager
or a capability-restricted container cannot establish the full system-unit
policy and sandbox behaviour required for activation.

## Decision

Add an internal diagnostic observer with no CLI registration or lifecycle
backend implementation. On Linux it can invoke only the absolute
`/usr/bin/busctl` path, with a fixed system-bus address, frozen environment,
closed generated arguments, disabled automatic activation and interactive
authorisation, bounded output and deadlines. Do not use a shell, inherited
bus configuration or caller-selected commands, names, paths or addresses.

Use only bus identity, name-ownership and connection-PID queries, plus
`ListUnitsByPatterns` for the two fixed literal service names against the
observed unique manager owner. This lists already loaded units;
`ListUnitsByNames` can load units and is excluded. All other manager operations
are outside this observer's vocabulary, including `GetUnit`, even though
systemd's implementation of that method itself queries already loaded units.
See the upstream
[manager implementation](https://github.com/systemd/systemd/blob/v259/src/core/dbus-manager.c).

Strictly decode bounded typed replies. An explicit owner-absence reply is
different from a failed call, unavailable tool, malformed result or changing
identity. Bracket bus, boot, manager and proxy ownership information; reject
detected drift instead of returning partial positive evidence. These separate
queries are a bounded diagnostic sample, not an atomic snapshot or assurance
against intervening changes.

Return an opaque diagnostic result, not `lifecycle.Observation`. It cannot
assert `KnownVendor`, authorise a stop, adopt a runtime file or establish
process start time, cgroup membership, mapped-library identity or firmware
quiescence. An omitted unit means only that the manager did not list it as
loaded at that sample, not that no unit file exists.

Use the existing tool rather than add a D-Bus library dependency for this
bounded step. Missing or incompatible `busctl` fails closed. Its absolute
location is a portability requirement, not a claim of binary attestation.
Reconsider the transport separately if qualified target environments cannot
supply it.

## Consequences

Scripted protocol fixtures and process-runner controls can exercise reply
validation, absence/error handling, cancellation and identity drift without
contacting a working device. They cannot prove actual D-Bus flags, systemd
mask precedence, policies or process identity. A separately approved isolated
Linux system-manager experiment remains necessary, followed by independently
reviewed mutation and recovery logic. Physical discovery and streaming still
require explicit approval and target-specific qualification.

The accepted runtime package, compatibility declaration, source composition
and existing builds remain unchanged. No public sensor installer, automatic
rule, global policy, service activation or hardware-support claim is enabled.
