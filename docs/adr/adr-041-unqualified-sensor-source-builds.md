---
id: adrs-adr041
title: "ADR041: Unqualified native sensor source builds"
description: Separate authenticated sensor source builds from installation and hardware compatibility claims.
---

## Status

Proposed on 2026-10-20. Catalogue semantics and source authentication are the
first implementation stage. Dependency-image production, supervised native
build execution, output validation and CLI delivery must be verified before
the sensor workflow is advertised. This record does not announce a release.

This extends the component-specific boundary in
[ADR006](adr-006-userspace-companion.md) and the static-host decision in
[ADR036](adr-036-operation-level-static-host-capability-guardrails.md).
It does not rewrite or supersede those historical decisions. Existing audio,
IPTSD, camera, Wi-Fi and recommended-component behaviour remains unchanged.

## Context

The OE repository publishes native Linux QMI, SSC and desktop sensor sources
separately from the kernel. Their software fixtures do not qualify physical
sensor roles, axes, calibration, clocks, automatic rotation or suspend/resume.
No kernel/OS support range or installable sensor bundle has been established.

The existing catalogue requires compatibility evidence even for source builds.
Neither an exact tested kernel pairing nor inherited kernel-source validation
describes an unqualified sensor development stack. Reusing either category
would manufacture a support claim. Editable metadata must also not become a
source-code execution grant or an installer.

A complete build needs both source authority and a usable dependency-image
producer. A local development image identity is not a publicly fetchable image
contract. Reusing the camera builder unchanged would combine networked package
acquisition with compilation; its `no-pull` option does not make that recipe
offline. A sensor build must keep those phases distinct.

## Decision

Add the closed capability token `sensors` and evidence token
`unqualified_source`. The latter requires experimental maturity, exactly
`build: true` with `status`, `pull` and `install` false, and absent release and
kernel-compatibility metadata. Keep schema version 2: the JSON shape is
unchanged, and older strict binaries reject the new vocabulary rather than
silently accepting it. Do not add a shipped entry until its compiled producer
actually exists and passes end-to-end validation.

Keep existing evidence/action rules unchanged. In particular, installation
does not universally imply a downloaded release: the bounded Wi-Fi adapter
derives board data from the selected distribution root. Component-specific
compiled policy remains authoritative for every executable action.

Authenticate a complete source composition against SHA-256 authorities compiled
into Lexr, independently of adjacent editable checksums. Initially these are
the three component inventories and checksum list from OE commit
`649cc875d08c04ce33bc8afc2a0ff7a22ce362e0`. Verify every declared source byte,
size, Git execution bit and exact internal symlink target, with closed-set and
special-file checks. An approved lineage field identifies that composition;
it must not be presented as an observed checkout HEAD. A matching source
archive need not contain `.git`.

Use confined, descriptor-based reads and inspect opened regular files before
reading. Refuse escaping links, unexpected entries and invalid file types.
Capture verified bytes, then stage them into an exclusively new private
directory, rechecking the captured copies. Never mount a mutable checkout as
the build authority. Refuse overlapping or reused destinations and retain
failed private outputs for diagnosis. A new composition requires reviewed
compiled authority, not just replacement JSON.

The intended delivered workflow owns dependency-image preparation as well as
the source build. Its reviewed, embedded dependency recipe may acquire public
distribution packages in an explicitly networked phase with no OE sources or
host secrets in its context. Record its base digest, complete recipe, resulting
image identity and installed toolchain inventory. Do not compile a private
development image ID into a public default or claim bit-reproducibility from
a base-image pin alone. Package snapshot/version locking is a separate proof.

The source-build phase always uses an already resolved immutable native ARM64
image with network disabled, a read-only root/source/policy, dropped
capabilities, no privilege escalation or physical-device mounts, and bounded
fresh writable output/scratch. Build QMI, then SSC against that private QMI,
then the proxy against that exact stack. Verify provider selection and ELF
loader/dependency identity rather than only version strings. Execute only the
reviewed local fixtures and CLI help/version checks; no discovery, streaming,
service activation or system installation belongs in this stage.

Define `no-pull` for this new domain as requiring an already prepared local
builder: no image pull or networked dependency preparation may occur through
that option. Do not change the existing camera option's semantics implicitly.
The final dependency-image/cache authority and refusal tests are an open
producer gate, not permission to trust a mutable image tag or labels alone.

Planning may authenticate source files, but must not contact Docker or create
outputs. An unsupported controller can report a truthful non-executable plan;
real execution must fail before expensive work. Apply the static host guard
in the owning domain and independently check the actual Linux ARM64 engine
and image. Do not use QEMU as an implicit fallback.

Observe the owned container's terminal exit, PID and OOM/error state, including
after cancellation. An interrupted client or failed observation is not proof
that a container terminated. Withhold publication if the outcome is unknown.
Validate the complete output set and hashes before atomic no-replace
publication. Report a source build/software check, not an installed component
or working physical sensor.

## Consequences

- Source snapshots can be compiled without Windows drivers, DLLs or services,
  while retaining upstream licences and provenance.
- Catalogue metadata can describe experimental work without inventing a
  kernel generation or enabling installation. Existing recommendations do
  not expand.
- Unqualified builds remain separate from package conflicts, privileges,
  activation, rollback and hardware/desktop qualification. Those remain
  required before an installation adapter can be enabled.
- Image acquisition and compilation have different network boundaries and
  separately inspectable evidence. A verified default producer costs more
  implementation work than an operator-supplied-image-only demonstration;
  that demonstration is not the final user workflow.
- Initial schema/source tests do not establish a usable CLI build command.
  Delivery remains gated on real-tree end-to-end production, refusal tests,
  clean dry-run behaviour and honest toolchain/output provenance.
