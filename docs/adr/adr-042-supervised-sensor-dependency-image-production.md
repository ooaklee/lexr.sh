---
id: adrs-adr042
title: "ADR042: Supervised sensor dependency image production"
description: Separate networked dependency preparation from offline native sensor source builds.
---

## Status

Proposed on 2026-10-20. This records the next producer stage after
[ADR041](adr-041-unqualified-sensor-source-builds.md). It does not supersede or
rewrite that record. A sensor build command, persistent image cache, installation
adapter and physical sensor qualification are not delivered by this stage.

## Context

The native QMI, SSC and desktop sensor sources need a complete Linux toolchain.
A private development image is not a public dependency producer. Installing
packages while compiling those sources would combine network acquisition and
source execution in one container and weaken the intended offline boundary.

Cancelling a Docker client does not establish that a daemon-side build stopped.
The sensor producer needs an observable owned resource, bounded lifetime and
terminal-state evidence before it endorses an image. Shared BuildKit tasks are
not used for this initial implementation.

## Decision

Prepare dependencies in a fresh, uniquely named container from the compiled
public Ubuntu 26.04 digest. Supply a fixed embedded script as an argument, not
a caller-selected Dockerfile, checkout, bind mount or build context. The script
uses the distribution's authenticated APT repositories with a fixed package-name
allow-list. It inhibits service activation, rejects partial APT updates and
records all installed package versions plus observed toolchain/API versions.
No Windows executable, driver, library or service is an input to this producer.
The service inhibitor intentionally remains in the committed development image.
It blocks package helpers' init-service activation, not direct private D-Bus
fixture processes; those remain subject to the separate source-build policy.

This phase explicitly permits bridge networking and a writable container root
for package installation. It permits only the root capabilities needed for
package ownership and APT's sandbox: CHOWN, DAC_OVERRIDE, FOWNER, SETUID and
SETGID. It does not grant privileged mode, host PID/IPC namespaces, host binds,
volumes, device requests or source access. Apply no-new-privileges, memory,
CPU and PID limits, a pre-APT free-space gate, a container-side timeout and a
separate controller deadline.
Actual capability sufficiency and dependency availability require a real run;
static tests cannot establish them.

Independently check the controller's supported operating system/architecture and
the actual Linux ARM64 Docker engine, engine identity and selected base object.
Do not use architecture emulation as an implicit fallback. Inspect the created
container's command, identity, labels, environment and isolation before starting
it. Labels are consistency checks, not an independent grant of image authority.

After starting, inspect the same full container ID. Image production requires
exited state, PID zero, exit zero, no OOM/error and valid start/finish timestamps.
An interrupted client, timeout, unknown state or successful log line is not a
substitute. When necessary, attempt to stop only the identity-checked owned
container using a fresh bounded observation context. If termination cannot be
proved, retain that uncertainty and withhold the prepared-image handle.

Copy the two fixed inventory members from the stopped container as bounded tar
streams. Accept only the requested regular member and inspect it without
extracting paths or following archive links. Validate the text, preserve the
complete inventories and record their SHA-256 digests.

Commit only the successfully stopped exact container ID, replace its preparation
entrypoint with `/bin/false` and its command with the literal
`lexr-explicit-offline-runner-required`, and set an unprivileged default user.
An empty `ENTRYPOINT []` change was observed retaining the container's timeout
entrypoint on Docker 29; accepting that mismatch is not an option. The explicit
fail-closed default must match exactly on inspection. A later source runner must
unconditionally override both entrypoint and command with its reviewed offline
execution policy; this image does not start a shell or reuse the APT command by
default. Inspect the resulting immutable image ID and recheck the producer's
unchanged terminal state before returning authority. Retain the container and
failure diagnostics; this stage does not delete earlier caches, images or
containers.
Inherited base-image metadata and the non-secret per-run ownership label remain
for traceability. They do not create cache or execution authority. Ordinary
terminal signals may end the attached preparation container; image authority
still requires the complete terminal checks and an uncancelled operation.

Return an opaque prepared-image value with unexported fields, bound to the
Manager that completed this producer chain. A mutable tag, recipe label,
adjacent editable JSON or arbitrary operator image does not construct that
value. The actual source-build phase must use its exact immutable image ID and
still apply its own unconditional no-network, read-only source/root,
capability-drop and output-validation policy.

For this stage, `NoPull` reuses only a handle already produced by the same
Manager in the same process, after checking the engine and exact image again.
Without that handle it refuses before Docker. It must not fall through to
pulling, APT installation or image creation. This is not yet a persistent
cross-process cache or a delivered CLI option. A practical persistent cache
needs a separately reviewed owner/permissions/record-publication boundary;
labels alone are insufficient, and signing secrets must not be invented to
pretend to defend against a hostile same-UID process or Docker administrator.

## Consequences

- Dependency acquisition and source compilation have separate network and
  provenance boundaries. No source is executed during preparation.
- A normal future build can prepare its own dependencies; it is not dependent
  on a maintainer's private image or a manual-only toolchain workaround.
- Package versions are recorded, not locked. A pinned base does not make moving
  APT repositories bit-reproducible or prove compatibility with other distros.
- Failure handling can report terminal uncertainty without falsely publishing a
  usable image. Bounded client observation is not a universal daemon-health
  guarantee.
- The host user and Docker administrator remain trusted. This design does not
  protect a running Lexr process from another process with equivalent authority.
- Image preparation is not source-build, packaging, desktop or hardware proof.
  The complete producer and refusal tests must pass before a sensor workflow is
  added to the shipped catalogue or CLI. Installation remains a separate gate.
