---
id: adrs-adr023
title: "ADR023: Lexr.sh standalone repository and compatibility boundary"
description: Architecture decision for extracting the complete CLI history, adopting the Lexr.sh identity, and staging the OE repository cut-over without breaking established contracts.
---

## Status

Accepted on 2026-08-31.

## Context

The project began as Linux Armer beneath `cli/linux-armer` in the Surface Pro
11 OE repository. Its command, source, tests, catalogues, architecture records,
Windows collector and release automation became a cohesive product with an
independent lifecycle. Keeping that product nested in the OE repository couples
ordinary CLI development and releases to a larger integration repository.

The public product is now named Lexr.sh, its command is `lexr`, and its
standalone repository is
[`ooaklee/lexr.sh`](https://github.com/ooaklee/lexr.sh). Moving only the latest
tree would discard the reasoning and authorship needed to audit the tool.
Moving the source without its workflow history would also separate release
policy from the commits which introduced it.

Some former product-name strings are more than presentation. They occur in
versioned wire domains, schema-3 image paths, private state locations, installed
system paths, unit names, bundle filenames and existing OE release provenance.
Renaming those values in place would make old images or installations
unreadable, invalidate domain-separated hashes, or require a risky state
migration unrelated to the public rename.

## Decision

The standalone repository owns the Lexr.sh source, issues, documentation,
catalogues, test suites and release workflow. The Go module is
`github.com/ooaklee/lexr.sh`, the source command is `./cmd/lexr`, and published
archives expose the `lexr` executable. New command output, cache directories,
temporary workspaces and locally generated artefact names use the `lexr` name.

The repository history is extracted from a fresh clone with `git filter-repo`.
The filter selects both the complete `cli/linux-armer/` history and the
dedicated release workflow, removes the CLI directory prefix so its contents
become the new repository root, and leaves the workflow beneath
`.github/workflows/`. The rebrand is applied only after the filtered history is
validated, as an ordinary follow-on commit. Rewriting necessarily changes
commit object IDs, but it must not rewrite author or committer identities,
timestamps or commit messages.

The migration audit starts at OE source tip
`cccbd342f69d7427b871e0f5f6ec8d54492e6f44` and produces pre-rebrand filtered
tip `04583868b513ab475c3b07832a3a1ff563bf1280`. It proves all of the following:

- 35 source commits map in linear order, including both commits which changed
  the release workflow;
- all 315 selected source paths are present at the standalone root;
- each mapped tree is byte-equivalent after removing the historical
  `cli/linux-armer/` prefix;
- author and committer names, email addresses and timestamps, together with
  every commit message, compare exactly;
- no Git replacement refs remain; and
- `git fsck --full` reports a valid object graph.

The following compatibility identifiers remain deliberately unchanged:

- `/sp11/linux-armer-manifest.json`,
  `/sp11/companion/bin/linux-arm64/linux-armer`, and the schema-3 companion
  source-archive path;
- `linux-armer.windows-handoff`, its manifest name, and every existing binding,
  application, purge, restore and target-observation hash domain;
- `.linux-armer-handoffs`, `/etc/linux-armer`,
  `/usr/libexec/linux-armer`, `/usr/lib/linux-armer`,
  `/var/lib/linux-armer/backups`, hand-off receipt paths, and existing
  `linux-armer-sp11-*` units and kernel hooks;
- established kernel and userspace bundle, provenance and release filenames;
  and
- OE repository and release URLs recorded by existing catalogues, manifests or
  provenance records.

These strings are compatibility contracts and are not the current public
product name. New code must not reuse them for unrelated state. A future
version may introduce a separately versioned migration, but must continue to
read every supported existing contract or provide an explicit, reversible
upgrade path.

The OE cut-over is staged. The standalone repository is populated and verified
first. Only after a clean clone builds, tests pass, the release workflow is
present, the history audit passes, and the intended remote branch is confirmed
will the OE branch remove its tracked CLI tree and workflow. That clean-up will
add the standalone repository as a submodule beneath `cli/`, update maintained
OE documentation to link to Lexr.sh, and preserve OE release references needed
by current Lexr compatibility policy. The OE branch is not cleaned up merely
because the filtered repository exists locally.

> **Implementation note (2026-08-31):** The staged cut-over above was completed
> after verification. OE now pins Lexr as `cli/lexr` and no longer carries the
> embedded source or any GitHub Actions workflow which executes Lexr. The
> subsequent automation-ownership decision is recorded in
> [ADR024](adr-024-lexr-owned-automation.md).

Earlier accepted ADRs remain historical records of decisions made under the
Linux Armer name. Their original product-name references are historical facts
or stable compatibility identifiers. An additive naming note links each
affected ADR to this decision without rewriting past context as though the
Lexr.sh name existed at that time.

## Consequences

- Lexr.sh can evolve and publish independently while the OE repository retains
  an explicit, discoverable integration point.
- The full CLI feature and release-workflow history remains available for
  blame, audit and archaeology in the standalone repository. Earlier IPTSD and
  kernel workflow commits remain in OE history.
- Existing image manifests, companion payloads, hand-offs, installations,
  receipts and release provenance remain readable after the public rename.
- Contributors must distinguish current branding from compatibility strings;
  a remaining `linux-armer` value is not automatically a missed rename.
- Filtered commits have different object IDs from their OE ancestors, so the
  verified mapping and immutable source tip are part of the migration evidence.
- The two repositories briefly contained the same source during verification;
  the completed OE cut-over now records one pinned Lexr gitlink instead.
