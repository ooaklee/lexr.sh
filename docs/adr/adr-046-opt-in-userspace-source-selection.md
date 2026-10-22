---
id: adrs-adr046
title: "ADR046: Opt-in userspace source selection"
description: Pin a reviewed support repository ref without widening component build or installation authority.
---

## Status

Proposed on 2026-10-22. This decision introduces a source-acquisition boundary;
it does not record a native build, deployment or hardware qualification.

This is a new decision, not a rewrite of earlier source, userspace or
installation decisions. Existing component-specific build and installation
contracts remain in force.

## Context

Contributors need to test a support repository branch or fork before its
changes become an immutable userspace release. Existing builds can consume a
local checkout, while downloaded installation bundles remain bound to compiled
publisher, release and artefact identities. Treating an arbitrary repository
ref as installation authority would bypass those checks.

The camera build's dry-run authenticates local inputs without creating build
directories or contacting Docker. Implicitly cloning a repository during that
operation would change its meaning. A mutable branch name alone also cannot
identify the source that was reviewed.

## Decision

### Prepare source explicitly

Add `userspace source prepare <camera|iptsd>` with an explicit `--experimental`
opt-in. Require a canonical public HTTPS repository, a fully qualified
`refs/heads/...` or `refs/tags/...` ref, an independently selected full commit
SHA and a new output directory. Reject unsupported components and malformed
or incomplete requests before acquisition. Source preparation is supported on
Linux and macOS; this does not change the native camera builder's ARM64 Linux
requirement.

Fetch the requested ref once and require its peeled commit to match the
expected commit; an annotated tag's object SHA is not the expected commit.
Check out that commit detached and record its complete HEAD tree, independent
of a component-scoped sparse checkout. A moved branch fails rather than
silently selecting a different commit or fetching an
old commit while claiming that it still represents the requested branch.

The source-preparation dry-run validates local choices and reports a plan
without network or filesystem mutation. It cannot authenticate source which
has not been acquired and must not report a prepared or component-validated
checkout.

### Isolate acquisition from repository policy

Run Git directly with fixed arguments and isolated child configuration and
environment. Inherit only the executable search path, adding fixed Git and
locale settings rather than the caller's home, temporary-directory variables,
tokens, proxies or loader overrides. Disable system/global Git configuration,
replacement objects, lazy fetching, hooks, templates, external filters,
interactive credential helpers and configuration injection. Permit public HTTPS
acquisition only and refuse all redirects, including same-host redirects. Do
not recursively acquire submodules. A legitimate unrelated gitlink does not
itself invalidate an OE repository when it is outside the selected component.
The selected component inputs must satisfy their existing file contracts.

Bound each Git command to two minutes, metadata capture and standard error to
64 KiB, and ordinary Git command output to 8 MiB. The larger output bound preserves
the existing camera validator's 4 MiB patch-input limit; it does not widen that
component limit.

Source preparation executes no repository-provided installer or host script.
This does not make the selected source non-executable: camera patch data is
still compiled by the existing component build workflow. An expected commit
establishes identity, not contributor permission, safety or trustworthiness.

### Retain component authentication

Before publication, authenticate the selected component with its existing
validator:

- Camera retains its fixed tracked-input and source-format checks through the
  non-mutating build-preparation path.
- IPTSD retains its exact integration-file identities and pinned upstream
  source. Selecting a different repository does not authorise arbitrary
  integration or payload changes.

Do not add generic source installers for audio, Wi-Fi, sensors, compute or an
`all` selection. Existing builders continue to use compiled recipes. Downloaded
installers retain their compiled release identities; native-camera installation
retains its independent artefact-authority digest and Git-backed provenance
validation.

### Publish only a fresh checked source tree

Create a private staging directory beside the new destination. Keep the
checkout in `repository/` and the selection receipt beside it, so recording
provenance does not dirty the checkout. After commit, tree and component checks
pass, publish with an atomic no-replace operation. Existing source trees and
output directories are never reset or overwritten.

On failure, retain staging evidence and report its location rather than
publishing an unvalidated source tree. Failed staging is not resumable; another
preparation requires a fresh destination. A prepared source receipt records the
component, repository, ref, expected and resolved commit, tree and validation
result. It is not a build receipt, privileged installation authority or
hardware certification.

If a final directory-identity or parent-flush check fails after the atomic
rename, report a command failure which explicitly says the validated source
was already published. Do not relabel it as incomplete staging, overwrite it
or contradict its on-disk receipt. A caller must require a successful command
exit as well as a prepared receipt before using the checkout.

## Consequences

- Contributors can test reviewed branches without changing the audited
  catalogue or weakening component validators.
- Acquisition is an explicit network and disk operation, separate from build
  dry-runs and privileged installation.
- Source and artefact provenance remain separate; installation cannot be
  authorised merely by editing a selection receipt.
- Regression tests must cover moved refs, annotated tags, malformed selectors,
  cancellation, destination races, inherited Git execution policy and unchanged
  component authentication.
- Native producer and target-device qualification remain separate work. A
  successful source preparation does not make draft sensor or compute stacks
  deployable, and no new release tag is implied by this decision.
