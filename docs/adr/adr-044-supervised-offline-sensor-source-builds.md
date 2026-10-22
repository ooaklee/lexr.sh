---
id: adrs-adr044
title: "ADR044: Supervised offline sensor source builds"
description: Compose authenticated sensor sources and prepared dependencies without exposing hardware or installer authority.
---

## Status

Proposed on 2026-10-20. This extends [ADR042](adr-042-supervised-sensor-dependency-image-production.md)
and [ADR043](adr-043-sensor-source-snapshots-and-output-publication.md), without
rewriting either record. The internal source-build controller remains subject
to native end-to-end validation before CLI or catalogue delivery. Installation
and physical sensor qualification are separate, unfinished work.

On 2026-10-22, the normal native CLI producer completed the local end-to-end
build and durable publication. See the
[dated evidence and qualification limits](adr-045-local-sensor-build-transport-and-bind-namespaces.md#2026-10-22-local-producer-evidence).
The original decision below is retained unchanged.

The transport-selection and source-bind path assumptions are superseded by
[ADR045](adr-045-local-sensor-build-transport-and-bind-namespaces.md). The other
decisions below are retained unchanged.

## Context

Source verification, a dependency image, a successful process log and an
artefact decoder do not by themselves establish a completed source build.
The controller must connect their authority boundaries, prevent compilation
from starting before initialisation succeeds, and account for daemon-side
containers after a cancelled Docker client.

## Decision

The sensor build Manager privately owns its dependency-image producer and only
caches a handle returned by that producer. Requests select source, work and
output directories, job count, dry-run and no-pull behaviour; they cannot supply
an image, script, reconstructed handle, source authority or decoder.

Planning is read-only. Report unsupported hosts or missing same-process no-pull
readiness as non-executable plans, not current Docker availability. For an
executable plan, authenticate the selected source and require non-overlapping
selected source/work/output trees. Actual execution independently checks the native
Linux ARM64 daemon and exact prepared image. No implicit emulation, mutable-tag
execution or manually supplied private image substitutes for this chain.

Directory identities and namespace ancestry alone do not discover every Linux
descendant bind-mount alias. On Linux, also map each selected directory through
bounded `/proc/self/mountinfo` metadata to its device and filesystem-relative
coordinate, rejecting overlap and ambiguous containing mount stacks. Repeat
that read-only check before workspace creation and atomic publication. This is
not protection against an administrator changing mounts concurrently.
The device/root/mountpoint mapping and treatment of stacked mounts follow the
[Linux mountinfo ABI](https://man7.org/linux/man-pages/man5/proc_pid_mountinfo.5.html).
Unknown optional fields are ignored as that ABI requires; they do not authorise
paths. Malformed known coordinate fields or ambiguous containing stacks refuse.
These are controller-namespace observations, not proof of a different Docker
daemon's mount namespace or file-sharing implementation. Darwin retains the
directory-identity/ancestry checks but does not run the Linux topology checker.
The selected daemon and its host-path mapping remain trusted; a Docker Desktop
run must not be reported as native Linux mount-topology qualification.

Create an exclusive private job directory and authenticated readonly repository
snapshot. Journal the source/init container and work-volume names before their
remote creation; dependency preparation has its separate result lineage. Use a
fresh randomly named local work volume; reject an existing name, non-local
driver/scope or any driver options, including host-bind and network-filesystem
options. Volume mountpoints returned by Docker are bookkeeping, never grants
to read, write or delete host paths.

Run two separately inspected containers. Initialisation has no source mount,
uses a readonly root and no network, and grants only CHOWN after dropping all
capabilities. It initialises the empty work directory and case probes. Only
after its exact container exits successfully may the source container start.
Compilation runs as UID/GID 65532 with no capabilities, network or physical
device mounts, a readonly root/source, the fresh work volume and a bounded
writable `/tmp` tmpfs. No writable host bind is allowed.
Use fixed compiled commands, private environment paths and explicit CPU,
memory, PID and lifetime limits. Inspect effective mounts and configuration,
not merely the arguments submitted to Docker.

Track dependency lineage separately from fresh init/source terminal states.
After cancellation or failure, use a fresh bounded context to inspect the
owned container identity before stopping it. Never stop an object based on a
partial ID, borrowed label or an unchecked name. Unknown termination withholds
publication. Retain resources, diagnostics and partials; do not prune or reuse
them automatically.

Copy only the fixed archive from the exact successfully stopped source
container, preserve that bounded transport for diagnosis, and run the unchanged
strict decoder. Recheck source, volume, engine/image authority and both phase
terminals before publication. Check cancellation between phases and again
immediately before atomic rename. A cancellation or error after rename must
retain the truthful published outcome, not pretend the files were rolled back.

Publish a host-path-free descriptive receipt with source metadata, dependency
inventories, recipes, exact terminal observations and artefact hashes. Keep
local paths and captured diagnostics in the private operation result. Neither
receipt nor successful software fixtures enable sensor installation or claim
physical roles, axes, calibration, rotation or suspend/resume compatibility.

## Consequences

- The ordinary producer owns dependency preparation, source execution and
  output acceptance; it does not rely on an arbitrary-image manual shortcut.
- Native runtime and refusal tests must establish the complete composition.
  Synthetic ELF fixtures can test controller/decoder integration but cannot
  demonstrate a successful C build or physical sensor behaviour.
- No Windows driver, DLL or service is a runtime dependency of this path.
- Same-UID and Docker-administrator integrity remain trusted. Random names and
  labels establish consistency within that boundary, not protection against an
  adversary with equivalent authority.
- Persistent cross-process cache authority, CLI delivery, deployment conflicts,
  activation and rollback remain distinct gates before user-facing support.
