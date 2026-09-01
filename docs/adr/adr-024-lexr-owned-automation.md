---
id: adrs-adr024
title: "ADR024: Lexr-owned automation and dedicated OE publication authority"
description: Architecture decision assigning Lexr-dependent integration and build workflows to the standalone repository while publishing hardware-support releases to OE through a dedicated, narrowly scoped credential.
---

## Status

Accepted on 2026-08-31.

## Context

Lexr began inside the Surface Pro 11 OE repository. Its IPTSD integration test
and kernel build workflow consequently remained there when the CLI source and
release workflow first moved to the standalone repository. After the OE tree
replaced its embedded CLI with the private `cli/lexr` submodule, those jobs
could no longer check out their implementation with the OE repository's
automatic GitHub token. That token belongs to the workflow repository and does
not grant access to another private repository.

Adding a personal or cross-repository token to OE would invert ownership. OE
would hold a credential solely to execute Lexr's implementation, untrusted
pull-request contexts would require special gating, and a future public Lexr
transition would leave temporary authentication policy embedded in the wrong
repository.

Moving execution ownership does not move the release channel. Existing kernel
and userspace links, the CLI's default kernel resolver, and previously shared
instructions all identify OE releases. Publishing hardware-support assets in
Lexr would split that contract and mix the CLI's own binary releases with
device-specific payloads.

The IPTSD contract spans both projects. Lexr owns the compiled validation and
build policy, while OE owns the BitBake recipe and the small integration tree
consumed by an OE image. The kernel build policy is entirely compiled into
Lexr and only needs a writable containment directory, so it has no technical
dependency on an OE checkout.

## Decision

The standalone Lexr repository owns and runs every workflow which executes the
Lexr implementation:

- `.github/workflows/lexr.yml` owns general quality checks, platform builds,
  Windows hand-off validation, and CLI release packaging;
- `.github/workflows/iptsd-integration-tests.yml` owns the optional
  cross-repository IPTSD contract check; and
- `.github/workflows/sp11-kernel-build.yml` owns manually dispatched kernel
  builds, validated build artefacts, and optional publication of experimental
  kernel prereleases to OE.

The OE repository contains the `cli/lexr` gitlink for discoverability and local
operator workflows, but it does not run Lexr from GitHub Actions and stores no
long-lived Lexr credential. The standalone repository instead stores the
cross-repository publication credential beside the workflow which requires it.

The IPTSD workflow checks out Lexr normally, fetches the public OE repository
anonymously into temporary runner storage, and supplies its absolute path only
to the optional cross-repository Go test. It runs for Lexr pull requests and
pushes, on a nightly schedule, and by manual dispatch with a validated `oe_ref`
input. Branch pushes and same-repository pull requests select an exact,
same-named head from the canonical OE repository when it exists, falling back
to OE `main` only when the canonical remote query succeeds and reports no
such head. Fork pull requests and scheduled runs use `main`; unsafe candidate
names and transport failures fail closed. This gives Lexr a bounded view of the
separately owned recipe and integration files without nesting the OE checkout
inside the Lexr source tree or granting write access to either repository.

The kernel workflow runs from the standalone Lexr root. Its build job requires
a dedicated, isolated, self-hosted Linux x86-64 or ARM64 runner labelled
`lexr-kernel`. The runner must provide Docker, at least 64 GiB of free Docker
storage, and at least 8 GiB in its Actions workspace. An x86-64 runner uses
workflow-provided ARM64 binfmt emulation; an ARM64 runner runs the build
container natively. Standard GitHub-hosted runners are excluded because their
14 GB storage cannot satisfy the compiled 40 GiB Docker-volume guard. No
matching runner was registered when this decision was accepted, so dispatch
remains queued until that external prerequisite is configured.
Each validated kernel Git URL receives a deterministic, isolated build work
directory, so a persistent runner can move between compatible source
repositories without reusing another remote's managed Docker volume.
The retained Actions build artefact contains only the verified Debian packages
and their `SHA256SUMS` file. Absolute package paths and the managed Docker
volume identity remain in job-local build provenance and are excluded from the
retained artefact. The workflow also redacts the absolute output directory from
the streamed command receipt; the separately prepared, path-free release
contract remains the publication authority.

The optional publication job may run only from exact Lexr `main` during a
manual dispatch. A dedicated fine-grained GitHub token is stored as the
`OE_RELEASE_TOKEN` Actions secret in the Lexr repository. The token is limited
to `ooaklee/linux-surface-pro-11-oe`, receives only the repository Contents
read-and-write permission required to create tags and releases, and uses a
short, reviewed expiry. The self-hosted build job never receives it. Only the
GitHub-hosted publication step references the secret, after all untrusted
builder output has crossed the hosted validation boundary. Lexr `main` should
be protected when the account plan exposes branch protection; the workflow
enforces the exact branch independently so publication does not silently widen
while the repository remains private.

Before entering that secret-bearing step, the GitHub-hosted publication job
independently revalidates the self-hosted builder's release directory, exact
manifest release name, checksum set, experimental state, required source and
licence assets, and the established `sp11-qcom-x1e-<package-version>` tag
contract. Publication then creates an
OE draft targeting the exact revision resolved from OE `main`. It refuses an
existing tag and verifies the newly created tag against that revision both
after creation and immediately before promotion. It downloads every uploaded
asset into a fresh directory, compares the complete remote and local digest
sets, repeats checksum and native release validation, and promotes the draft to
an experimental OE prerelease only after those checks pass. A failed check
leaves the OE draft recoverable. GitHub Releases in `ooaklee/lexr.sh` accept
only six raw Lexr
platform executables for Linux, macOS and Windows on AMD64 and ARM64, together
with their versioned checksum manifest. They do not contain kernel, firmware,
driver, userspace, catalogue, collector, or documentation assets.

The prior IPTSD and kernel workflow commits remain reachable in OE history.
Lexr records this ownership transfer as a new commit rather than rewriting its
already verified filtered history.

## Consequences

- OE needs no credential for reading or executing Lexr; the one
  cross-repository publication token is stored and used by Lexr.
- Lexr code and the automation which executes it are reviewed and versioned
  together.
- Pull requests cannot expose a private Lexr checkout through an OE workflow.
- Kernel and hardware-support release links remain in OE, while Lexr releases
  contain only the six CLI executables and their checksum manifest.
- A dedicated runner is an explicit operational prerequisite; the workflow
  does not pretend a standard hosted runner can satisfy the build contract.
- Draft-first publication makes the remotely downloaded bytes, rather than the
  pre-upload directory alone, the final promotion gate.
- Existing image creation continues to use the established OE release default;
  the workflow move does not change runtime release selection or shared links.
- The dedicated token removes an external identity-service deployment but adds
  explicit expiry, rotation, revocation, and secret-exposure responsibilities.
  Workload identity can replace it in a later decision when deployment and
  maintenance resources are available.
- A change made only in OE cannot directly trigger a workflow in Lexr. The
  nightly run, same-named companion-branch selection, and explicit `oe_ref`
  dispatch bound that delay and provide ways to validate an OE branch before
  it is merged.
- The public OE recipe and integration tree remain authoritative for OE image
  composition; the cross-repository test does not transfer that ownership to
  Lexr.
