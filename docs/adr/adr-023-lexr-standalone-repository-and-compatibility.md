---
id: adrs-adr023
title: "ADR023: Lexr.sh standalone repository and naming boundary"
description: Architecture decision for extracting the complete CLI history, adopting the Lexr.sh identity, and completing the OE repository cut-over.
---

## Status

Accepted and amended on 2026-08-31.

## Context

The project began as an embedded CLI beneath a differently named directory in
the Surface Pro 11 OE repository. Its command, source, tests, catalogues,
architecture records, Windows collector and release automation became a
cohesive product with an independent lifecycle. Keeping that product nested in
the OE repository couples ordinary CLI development and releases to a larger
integration repository.

The public product is now named Lexr.sh, its command is `lexr`, and its
standalone repository is
[`ooaklee/lexr.sh`](https://github.com/ooaklee/lexr.sh). Moving only the latest
tree would discard the reasoning and authorship needed to audit the tool.
Moving the source without its workflow history would also separate release
policy from the commits which introduced it.

An initial migration plan retained some predecessor identifiers in versioned
wire domains, media paths, private state, installed system paths, unit names and
bundle filenames. That approach would leave the new standalone repository with
two product identities before its first public release. It would also make new
artefacts look only partly renamed and force every future contract to explain a
pre-release identity indefinitely.

Existing OE repository and release URLs are external provenance rather than
Lexr-owned identifiers. They must remain accurate so kernel releases and shared
links continue to resolve.

## Decision

The standalone repository owns the Lexr.sh source, issues, documentation,
catalogues, test suites and release workflow. The Go module is
`github.com/ooaklee/lexr.sh`, the source command is `./cmd/lexr`, and published
release assets expose the raw `lexr` executables. New command output, cache
directories, temporary workspaces and locally generated artefact names use the
`lexr` name.

The repository history is extracted from a fresh clone with `git filter-repo`.
The filter selects both the complete former embedded CLI history and the
dedicated release workflow, removes the former directory prefix so its contents
become the new repository root, and leaves the workflow beneath
`.github/workflows/`. The rename is applied only after the filtered history is
validated, as an ordinary follow-on commit. Rewriting necessarily changes
commit object IDs, but it must not rewrite author or committer identities,
timestamps or commit messages.

The migration audit starts at OE source tip
`cccbd342f69d7427b871e0f5f6ec8d54492e6f44` and produces pre-rebrand filtered
tip `04583868b513ab475c3b07832a3a1ff563bf1280`. It proves all of the following:

- 35 source commits map in linear order, including both commits which changed
  the release workflow;
- all 315 selected source paths are present at the standalone root;
- each mapped tree is byte-equivalent after removing the former embedded
  directory prefix;
- author and committer names, email addresses and timestamps, together with
  every commit message, compare exactly;
- no Git replacement refs remain; and
- `git fsck --full` reports a valid object graph.

Every active Lexr-owned identity uses the Lexr name. This includes the command,
Go module, media inventory and companion paths, source archives, private
hand-off stores and documents, hash domains, installed configuration and
library paths, service units, kernel hooks, receipts, bundle manifests,
provenance documents and release filenames. Current producers do not emit a
predecessor identifier, and current readers do not silently reinterpret one as
Lexr.

Where an identifier participates in a serialised, installed or domain-separated
contract, this rename is a deliberate breaking contract revision. The image
manifest advances to schema 4, the Windows hand-off advances to schema 3 with
collector `3.0.0`, and private hand-off application receipts advance to schema
2. Validators reject mixed-identity documents, and generated digests use only
the Lexr domain. This clean boundary is acceptable before `0.1.0`: pre-release
images and hand-offs must be recreated or recollected, and installations must
be reinstalled rather than silently migrated. Recovery receipts must not be
recreated. They must instead be completed or restored with the exact predecessor
binary before upgrading, or retained with that binary and their complete
recovery material until recovery is no longer required. Any future migration
reader requires its own bounded, reversible decision and must never cause a
current producer to emit the former identity.

OE repository and release URLs recorded by catalogues, manifests or provenance
remain unchanged because they identify the external kernel publication channel,
not the Lexr product.

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

Earlier accepted ADRs retain their technical decisions, but their product,
command, path and wire terminology is normalised to Lexr. Each affected record
links to this decision so readers can distinguish the technical history from
the later standalone naming boundary without carrying the retired identity in
the current tree.

## Consequences

- Lexr.sh can evolve and publish independently while the OE repository retains
  an explicit, discoverable integration point.
- The full CLI feature and release-workflow history remains available for
  blame, audit and archaeology in the standalone repository. Earlier IPTSD and
  kernel workflow commits remain in OE history.
- Every current product-owned path, wire value, manifest and example has one
  unambiguous Lexr identity.
- Pre-release artefacts using the predecessor identity are intentionally not
  accepted as current Lexr contracts; operators must recreate them.
- Filtered commits have different object IDs from their OE ancestors, so the
  verified mapping and immutable source tip are part of the migration evidence.
- The two repositories briefly contained the same source during verification;
  the completed OE cut-over now records one pinned Lexr gitlink instead.
