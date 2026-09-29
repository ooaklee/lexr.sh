---
id: adrs-adr041
title: "ADR041: Authenticated userspace component compatibility"
description: Authority, canonical manifests, scoped evaluation and migration for portable userspace compatibility evidence.
---

## Status

Proposed for the implementation of
[issue #44](https://github.com/ooaklee/lexr.sh/issues/44).

## Context

The userspace catalogue supplies repository policy, while downloaded bundles
carry a transfer receipt and compiled component adapters own mutation. A
transfer receipt cannot establish the compatibility of a release with an
offline target. Central SP11 generation fields also omit the operating system,
architecture, device profile and Lexr release.

The parent [issue #27](https://github.com/ooaklee/lexr.sh/issues/27) remains the
owner of generic kernel packaging, bundle SemVer and supported-machine
migration. This decision introduces the typed parser needed by component
compatibility without claiming that wider migration is complete. It extends
[ADR006](adr-006-userspace-companion.md),
[ADR014](adr-014-native-iptsd-release-transactions.md),
[ADR015](adr-015-native-imx681-package-and-release-contracts.md) and
[ADR019](adr-019-native-audio-release-preparation.md); their historical release
contracts remain unchanged.

## Decision

Each newly prepared downloadable component release carries
`lexr-component-compatibility.json`, schema 1. Its source repository authors
one declaration for that component release. Release preparation copies its
canonical bytes, covers them in `SHA256SUMS`, and records their digest and
size in the existing release authority. SHA-256 identities and hosting-service
digests authenticate against independent repository pins; they are not
signatures.

Userspace catalogue schema 3 records the stable component ID and exact manifest
size and SHA-256. It does not repeat the manifest's compatibility ranges.
`lexr-userspace-bundle.json` records transfer verification only. Neither that
receipt nor a colocated manifest can grant install actions, destinations,
capabilities, redistribution permission or hardware support. Catalogue
overrides cannot replace the embedded release authority. Compiled payload pins
and adapters remain in force when new metadata is added.

Canonical JSON uses the schema's field order, two-space indentation and one
terminal line feed. The decoder bounds the document, nesting and collections
and rejects ambiguous, unknown, mis-cased, duplicate, null, noncanonical or
malformed input. Optional upper bounds are omitted, never null or empty.
Unknown schemas fail closed.

A target rule is an AND across architecture, device, OS and kernel. Rules are
alternatives; evidence from two rules is never combined. Lexr constraints use
SemVer 2.0 precedence. Persisted versions omit `v` and build metadata. Runtime
build metadata does not change precedence; development, dirty and noncanonical
builds are unverified. A tested-through value is evidence, not a hard ceiling.

Kernel ABI generations are ordered only within the exact patch line, scope and
platform flavour. The shared parser recognises registered scopes and current
legacy and Ubuntu package-derived ABI spellings. Parsing another scope does
not grant support for another device. OS comparison is selected by exact
`ID`: Ubuntu calendar releases, Fedora and Debian integer releases, and
explicit elementary OS numeric releases. There is no lexical universal
version comparison or implicit `ID_LIKE` inheritance. Device declarations use
canonical IDs from the shared hardware profile registry, initially
`x1e80100-microsoft-denali-oled` and `x1p64100-microsoft-denali` with ARM64
userspace. Kernel platform aliases are not accepted as manifest identities.
Detection and explicit image profiles resolve to these same canonical IDs.
The initial component declarations include both profiles with empty OS
qualification evidence, so both remain unverified. Qualification for only one
profile requires its own complete target rule.

All consumers share these stable decisions:

| Decision | Read-only report | Mutation |
| --- | --- | --- |
| `tested` | Pass | Allow |
| `unverified` | Warn | Require `--allow-unverified-compatibility` |
| `incompatible` | Fail | Block |
| `unavailable` | Unavailable | Block |

`--yes` confirms a mutation but does not override compatibility. The dedicated
override is recorded with the exact manifest digest, target tuple and matched
zero-based rule index. A missing target identity cannot be overridden.

Pull authenticates bytes without comparing the download host. Build and
release evaluate the declared payload target. Install reads the selected root
before privilege and repeats the decision immediately before applying changes.
Status and doctor inspect offline evidence without network access or executing
target binaries. Image companions read the extracted image's `os-release` and
use the selected kernel and explicit device profile, never the container host.
Image manifest schema 6 carries the versioned compatibility assessment; older
image schemas cannot silently acquire these new fields.

Schema-2 catalogues remain readable as migration inputs. Schema 3 explicitly
identifies the existing pinned audio, IPTSD and camera releases as legacy
profiles. Their exact compiled contracts continue to work without a manifest.
An unknown release cannot claim a legacy exemption. Source declarations for
future packaging do not rewrite historical releases, and an old Lexr binary
is not expected to consume the new catalogue schema.

## Consequences

Portable evidence now supports one attributable compatibility decision across
installation, diagnosis and image preparation. New releases require coordinated
source, catalogue and compiled-pin review; publishing new metadata cannot
silently authorise new payload bytes.

Host command availability, structural validation, component compatibility and
physical hardware qualification remain separate claims. A manifest records
existing evidence; generating it or passing software tests performs no hardware
qualification. No per-device identifiers belong in these public declarations.

## Producer migration

Audio release authority advances to schema 2 (eight files). Native camera
build and release authority advance to schema 2 (nine and twelve files).
Camera validates the same explicit payload tuple at build and preparation;
its declaration must also exist with identical bytes at the recorded support
commit. Schema-1 native camera preparations must be rebuilt for this new path;
exact pinned downloaded camera v1 remains supported through its legacy adapter.
IPTSD keeps its closed native payload contract and records the declaration and
assessment beside that payload, with checksum coverage. These build sidecars
do not grant new downloadable-release or installation authority.

Initial declarations intentionally contain no tested OS versions. A dedicated
unverified override is necessary until maintainers add qualification evidence
through a reviewed source declaration and independent pin update.

Existing schema-5 images remain readable only with their legacy companion
records; compatibility assessments require schema 6. Unknown schemas still
fail closed.
