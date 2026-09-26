---
id: adrs-adr036
title: "ADR036: Operation-level static host capability guardrails"
description: Reject wholly unavailable host operations early without centralising dynamic workflow checks or maintaining an exhaustive command matrix.
---

## Status

Accepted on 2026-09-25 as the narrowed resolution of
[issue #1](https://github.com/ooaklee/lexr.sh/issues/1), following OpenCode
design and implementation review.

This decision rejects the proposed exhaustive per-command platform matrix,
wizard filtering, help annotation and generated capability documentation as
standing requirements. They would add a second inventory which could drift
from domain behaviour. A future operation may add a static guard when its
requirements meet the criteria below; it does not need to populate a global
matrix first.

## Context

Lexr publishes executables for Linux, macOS and Windows on AMD64 and ARM64.
That release set describes where the command can start, not where every
operation can execute. Some operations depend on a host primitive which is
entirely absent from one or more targets, while other operations can still
inspect portable inputs, prepare a useful plan or operate on a mounted target.

Previously, these distinctions were expressed through scattered runtime
checks, build-tagged implementations or failures from low-level tools. A user
could therefore supply inputs or trigger source inspection before learning
that the requested operation could never execute on that host. The direct
checks also produced inconsistent messages and were awkward to test on one
development platform.

A complete command-by-platform declaration would appear authoritative while
combining different questions. Operating system and architecture are static.
Tool availability, effective privilege, free space, container support, input
validity and target-root compatibility are discovered at runtime and can vary
between two machines with the same operating system. Command roots also group
read-only and mutating leaves which do not necessarily share requirements.

## Decision

Use `internal/hostcap` for conservative, operation-level static exclusions.
The package represents a complete `GOOS` and `GOARCH` host identity, a named
requirement with operating-system and architecture allow-lists, and an
availability result containing `Executable` and `ExecutionBlocker`. Empty
allow-lists mean that dimension is unrestricted. An incomplete injected host
identity fails closed. The package performs no probing, filesystem access or
process execution.

Attach a requirement only when the complete operation is unavailable because
of a static host property. `Executable: true` means only that the operation was
not statically excluded. It does not establish that the host is configured,
that its tools are present, or that the request is valid.

Reject an unsupported real operation before expensive source inspection,
external tool invocation or filesystem mutation. Split planning where needed
so the inexpensive path and host decision precede authenticated source
snapshots or package inspection. Preserve read-only planning on unsupported
hosts when the domain can still return a truthful plan; such plans record
`Executable: false` and the stable blocker. A workflow which necessarily
inspects unavailable native devices, such as camera capture, remains blocked
even when requested in dry-run mode.

Keep dynamic checks in the domain which owns their meaning. `hostcap` does not
decide whether Docker is usable, an executable exists, a user has sufficient
privilege, a target root is compatible, storage is sufficient, or inputs are
authentic. Moving those checks into a global service would duplicate domain
policy and turn a static exclusion into an unreliable environment validator.

Retain build-tagged unsupported implementations as the final safety floor for
platform-specific primitives. The static guard is the early entry decision;
the build-tagged implementation still refuses an unsupported publication if a
caller ever bypasses that decision. Tests inject complete host identities and
prove early rejection and, for portable planning domains, truthful
unsupported-host plans.

The initial guarded operations are:

- native camera package building and Surface camera capture on Linux ARM64;
- atomic local kernel, audio and camera release publication on Linux or macOS;
  and
- GRUB registration on Linux.

The camera capture rule deliberately narrows its former Linux-only check to
Linux ARM64 because the native Surface camera workflow cannot execute on an
AMD64 host. Portable image inspection and validation do not receive a static
gate. Image creation and publication have more involved planning and
publication boundaries and are deferred until a concrete early-gating change
can preserve their useful read-only behaviour.

Do not add a blanket Cobra-root gate, hide whole wizard branches, or generate
public support claims from these requirements. Delivery-layer checks are
appropriate only for operations, such as GRUB registration, whose boundary is
owned directly by that layer. Otherwise the domain manager remains the
authoritative enforcement point for direct callers as well as the CLI.

## Consequences

- Wholly unsupported real operations fail with one deterministic explanation
  before work which cannot affect the outcome.
- Host policy is dependency-free, injectable and testable without pretending
  to emulate another operating system.
- Useful plans remain available across host targets, and additive plan fields
  distinguish static executability from successful environment validation.
- Dynamic readiness failures still occur later in their owning workflows. This
  is intentional and prevents a central policy package from becoming a second
  implementation of each domain's preflight.
- Static guards and build-tagged backstops form two layers which must remain
  aligned. A new requirement needs an early-rejection test and, where planning
  is portable, an unsupported-host plan test.
- The package is not a complete support catalogue. New commands do not acquire
  declarations merely to satisfy a closed matrix, and public documentation
  continues to describe workflow requirements in human-reviewed terms.
- Linux AMD64 camera capture is rejected earlier than before. Other deferred
  workflows may continue to report a lower-level platform error until a
  similarly bounded guard is justified.
