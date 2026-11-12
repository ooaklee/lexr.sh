---
id: adrs-adr052
title: "ADR052: Isolated sensor service lifecycle qualification"
description: Qualify exclusive proxy handover and compensation without installing or starting a host service.
---

## Status

Proposed on 2026-10-22. Extends
[ADR051](adr-051-sensor-qualification-packages-and-offline-transactions.md).
It supersedes only the no-integration-model boundary for a new software-only
fixture workflow. It does not enable live registration or activation.

On 2026-10-23, [ADR053](adr-053-passive-sensor-service-observations.md) proposed
superseding only the absence of a real passive diagnostic observer; live
lifecycle effects remain outside this decision.

## Context

The private runtime has package and offline filesystem evidence, but the proxy
shares the distribution's `net.hadess.SensorProxy` identity. Its inherited udev
rule can start the distribution service automatically. Starting the native
proxy performs sensor discovery even before a desktop client claims a sensor;
it is a hardware operation, not a passive check.

An ExecStart drop-in on the distribution unit admits an autoactivation window
while switching between implementations. A second `Type=dbus` unit is also
unsuitable: systemd allows only one loaded unit to watch a given bus name.
See the upstream
[unit watcher](https://github.com/systemd/systemd/blob/main/src/core/unit.c)
and [service setup](https://github.com/systemd/systemd/blob/main/src/core/service.c).
Neither successful start status nor acquisition of a bus name proves sensor
readiness or the intended runtime identity.

## Decision

Keep the accepted runtime package and its closed private-prefix tree unchanged.
Render a separate, opaque inactive bundle from verified package bytes and the
authenticated source composition. Retain the four raw integration templates
and proxy licence as inert review assets. Generate a unique static manual
`lexr-native-sensors.service` using `Type=exec`, without a `BusName` property,
and require/order the D-Bus socket explicitly. Use the fixed ARM64 interpreter,
private library directory and private proxy program. Preserve the reviewed
source hardening. Do not add enablement, automatic rules, a D-Bus activation
file, `Conflicts`, replacement flags or global policy installation.

Add an executable qualification engine with a closed typed effect boundary.
There is no production systemd, D-Bus, command-runner or live filesystem
adapter in this decision. Exercise its operations against fixture observations
and actual files in exclusively new roots. A backend observation is a trusted
test input, not evidence collected from a live manager.

The handover first recognises the exact vendor configuration, policies and
stable prior active/inactive state. Reject pre-existing masks, unknown
drop-ins, competing owners and changing boot/manager identities. Create an
owned runtime exclusion before stopping the vendor, reload and verify it is
effective, then require the vendor to be quiescent and its bus name free.
Register the separate private unit, reload, start and independently verify
unit invocation, process and bus-owner identity bound to the package. Observe
ownership again rather than treating a successful command as attestation.

Compensation must account for errors after effects and caller cancellation.
Use a separate bounded cleanup context. If our start might have occurred,
stop and prove quiescence, including no remaining unit processes and a free
bus name, before removing exact owned files or restoring the vendor. Restart
the recognised vendor only if it was previously active; this restores its
functional state, not its old process identifier. Unknown owners, substituted
files, uncertain stop or failed recovery leave explicit recovery-required
evidence and retain exclusion where still owned. Do not stop arbitrary PIDs,
remove unknown files or use broad service reversion.

Serialise transitions and bind rollback to an opaque process-local handle.
Receipts describe outcomes but cannot authorise adoption or recovery after a
restart. The qualification engine does not promise crash recovery or protection
against an arbitrary privileged actor. Runtime masks are boot-local, not a
persistent support policy; see
[systemctl](https://www.freedesktop.org/software/systemd/man/latest/systemctl.html).

## Consequences

The next failure boundaries can be tested without touching a working device,
starting a proxy or changing accepted package bytes. Software fixtures cannot
establish actual systemd masking/precedence, D-Bus policy interaction, loader
process attestation or remote firmware quiescence. An isolated integration
adapter, reviewed experimental target and independent manifest authority remain
separate gates before an explicitly approved physical experiment. Sensor
identity, axes, calibration, desktop behaviour, power and suspend/resume are
still unqualified. No catalogue or CLI installation action is enabled.
