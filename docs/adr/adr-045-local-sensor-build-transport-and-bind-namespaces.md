---
id: adrs-adr045
title: "ADR045: Local sensor-build transport and bind namespaces"
description: Bind sensor builds to one local Docker endpoint and distinguish host snapshot paths from daemon mount coordinates.
---

## Status

Proposed on 2026-10-20. This supersedes the transport-selection and source-bind
path assumptions in [ADR044](adr-044-supervised-offline-sensor-source-builds.md),
not its source, image, lifecycle, resource or publication requirements. The
earlier record remains intact for historical context.

Native end-to-end source compilation is not yet accepted. This decision does
not enable installation, catalogue entries or physical sensor qualification.

On 2026-10-22, the native producer completed local software compilation and
durable publication. The [dated evidence below](#2026-10-22-local-producer-evidence)
updates that execution status without changing the historical decision or
claiming installation or hardware qualification.

## Context

An authenticated host snapshot and a daemon-reported mount path use different
coordinate systems on Docker Desktop. A supervised run successfully initialised
its private volume, then refused the source container before starting it:
Docker reported the same translated path in both requested and effective mount
metadata, while the verifier expected the host path verbatim.

Docker documents that bind mounts refer to the daemon's filesystem and that
Desktop shares native host directories into a Linux VM. That is not a portable
guarantee for a particular translation prefix. The observed macOS Desktop form
must not become a blanket alias rule or permission to use a remote daemon.
See [Docker's bind-mount constraints](https://docs.docker.com/engine/storage/bind-mounts/#considerations-and-constraints).

## Decision

Keep planning read-only and independent of current Docker readiness. Before
dependency acquisition or resource creation, execution must establish one
private, immutable local transport binding. Resolve client selection through
bounded read-only configuration/context observations; reject remote or
unsupported transports before contacting a selected daemon. Conflicting
nonempty host/context selectors, TLS overrides and unsupported API overrides
are explicit refusals, not requests to rewrite the user's configuration.

Pin the canonical local Unix socket and its ownership/identity, the canonical
Docker executable and the selected client configuration location. Forward
dependency, initialisation, source, inspection and recovery commands through
the same bound runner, with explicit endpoint/configuration arguments and
neutralised inherited endpoint/context/TLS/API selectors. Later environment or
current-context changes must not redirect an operation. Recheck local identities
and engine identity at the execution boundaries; fail without automatic
fallback or rebinding when they differ.

Retain the original dependency producer and its process-local `Prepared`
ownership. Replacing its runner before first preparation does not permit
reconstructing a handle, borrowing an image or bypassing dependency validation.

Keep two distinct source coordinates:

- The canonical host snapshot path remains authoritative for staging,
  verification and the original container-creation argument.
- The expected daemon path is derived only from the admitted local transport.
  A supported direct local Linux daemon uses the host path unchanged. A
  specifically admitted local macOS Desktop connection may use exactly
  `/host_mnt` plus the complete canonical host path.

The macOS rule requires the selected canonical current-user Desktop socket,
native Linux ARM64 engine identity and Desktop engine metadata together. Neither
a context/display name, host OS alone nor an inspected path grants translation
authority. Unknown mappings and remote TCP/SSH endpoints are not supported.
There is no caller-supplied path-map or "allow Desktop" override.

Both requested and effective source mounts must independently equal the full
derived daemon coordinate. Preserve read-only/private settings, unique mount
destinations and every existing ownership, capability, resource and lifecycle
check. Do not trim prefixes, try alternative paths, permit matching suffixes or
adopt the observed mount as its own expected value. Initialisation still has no
source mount; a created-but-never-started container is not successful execution.

Record host/daemon coordinates only in the private local operation result.
Published receipts remain free of local endpoint and checkout paths. Retain
failed containers, volumes, diagnostics and partials; never start or reuse a
previously refused source container to validate a new policy.

## Trust and validation limits

Local socket/executable/configuration checks and engine metadata establish
consistency within the trusted same-user and Docker-administrator boundary.
They are not cryptographic proof of daemon locality, VM file-sharing identity
or immunity to privileged check/use races. The local Desktop sharing service
and its mapping remain trusted. Darwin is not Linux daemon-namespace
mount-topology qualification; the limitations recorded in ADR044 still apply.

Tests must cover selector and endpoint rejection, frozen commands despite
environment changes, socket/engine replacement, retained producer ownership,
Linux identity mapping, exact Desktop translation, differing requested/effective
paths, misleading prefixes/suffixes and unchanged mount/lifecycle refusals.
Software fixtures do not prove runtime source readability or a successful C
build. A fresh normal producer run, complete terminal/artefact validation and
diagnostic disposition remain required before CLI delivery.

## Consequences

- No Windows driver, DLL or service becomes a runtime dependency.
- There is no diagnostic-container shortcut, arbitrary-image fallback or new
  source-volume import workflow.
- Unsupported transport/mapping configurations fail before acquiring
  dependencies wherever they can be identified read-only.
- Installation, deployment conflicts, rollback, physical sensor roles, axes,
  calibration, rotation and suspend/resume remain separate unfinished work.

## 2026-10-22 local producer evidence

The normal native CLI producer completed at source commit
[`73aafd72beb05c89e18e0b1e1fccb9874ef1d9d9`](https://github.com/ooaklee/lexr.sh/commit/73aafd72beb05c89e18e0b1e1fccb9874ef1d9d9),
tree `98d00b4ac85332e5e5eba80411c1405b7db69443`, using an experimental
build-only sensor catalogue. Dependency preparation, volume initialisation and
source compilation each reached a verified terminal exit of zero. Seven native
software fixture targets passed. The producer validated and durably published
the complete local set of 16 regular artefact files, three library aliases and
its descriptive receipt. A separate replay of the retained archive passed the
unchanged strict consumer; successful log lines alone did not establish this
result.

This evidence qualifies local software production and publication for that
source tree. The sensor producer and source-preparation code remain unchanged
in the alpha/main integration. No fresh native producer run was performed for
the combined tree. The observation does not qualify Linux daemon-namespace
mount topology beyond the run's actual controller/daemon environment, publish
GitHub release assets, enable a shipped sensor catalogue entry, or establish
installation, service activation, physical sensor roles, axes, calibration,
rotation, suspend/resume, desktop behaviour or rollback. Those boundaries and
the original decision above remain in force.
