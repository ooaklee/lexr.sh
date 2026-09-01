# Automation and release channels

Lexr automation needs to run beside the implementation it validates, but hardware-support releases still need to remain on the established OE channel. The repository keeps those concerns separate so OE does not need a credential merely to execute Lexr, and Lexr CLI releases do not become a mixture of unrelated device payloads.

This page is for maintainers changing workflows, configuring the kernel builder, or operating a release. For local release assembly before any remote change, see [release preparation](release-preparation.md).

## Know which workflow owns the job

Lexr-dependent automation is owned and run by this repository.

| Workflow | Responsibility |
| --- | --- |
| [Lexr CLI](https://github.com/ooaklee/lexr.sh/blob/main/.github/workflows/lexr.yml) | Formats, tests, vets, builds, and packages the CLI. |
| [IPTSD integration](https://github.com/ooaklee/lexr.sh/blob/main/.github/workflows/iptsd-integration-tests.yml) | Sparsely checks out the two public OE contract directories into temporary runner storage, validates them from Lexr, and selects the bounded OE ref described below. |
| [SP11 kernel build](https://github.com/ooaklee/lexr.sh/blob/main/.github/workflows/sp11-kernel-build.yml) | Runs only by manual dispatch, builds and validates an experimental kernel from the checked-out Lexr source, and can publish an experimental prerelease in OE. |

The kernel workflow asks Lexr for Stubble mode explicitly, so its PE sections,
embedded Denali device tree and public provenance are checked under the same
policy. It retains only the verified Debian packages and `SHA256SUMS` as its
build artefact. Absolute-path build provenance stays inside the trusted job,
the displayed output path is redacted, and the separately prepared path-free
release remains the public provenance record.

The OE repository therefore needs neither a personal access token for Lexr nor an Actions checkout of the Lexr gitlink. [ADR024](../adr/adr-024-lexr-owned-automation.md) records this ownership boundary.

## Test a linked OE change

When one change touches both Lexr and the public OE integration contract, test
the two refs together before either is merged. Set `LEXR_REF` to the Lexr branch
or tag containing the workflow and tests, and set `OE_REF` to the OE branch,
tag, or commit you intend to test. This complete baseline dispatches Lexr
`main` against OE `main` and can be run from any checkout with an authenticated
GitHub CLI:

```sh
LEXR_REF=main
OE_REF=main

gh workflow run iptsd-integration-tests.yml \
  --repo ooaklee/lexr.sh \
  --ref "$LEXR_REF" \
  --raw-field "oe_ref=$OE_REF"
```

`--ref` selects the Lexr branch or tag containing the workflow and tests.
`oe_ref` selects the OE branch, tag, or commit fetched for that run. A manual
dispatch uses both values exactly and never falls back. GitHub CLI prints the
run URL when GitHub makes it available, so you can follow the result.

Ordinary branch CI applies the same boundary without making nightly checks
depend on a feature branch:

- a branch push or same-repository pull request uses the exact, same-named
  branch from the canonical OE repository when that head exists;
- a fork pull request, scheduled run, or canonical branch lookup which succeeds
  but finds no exact head uses OE `main`;
- an unsafe candidate name, transport error, or failure after discovering a
  companion branch fails closed.

The canonical OE URL is hard-coded and fetched anonymously. The workflow logs
both the selected ref and the exact detached revision used by the tests, while
the fork guard prevents a contributor from selecting a canonical feature
fixture merely by choosing a matching branch name.

## Provide the dedicated kernel runner

Kernel compilation is intentionally excluded from standard GitHub-hosted runners. The workflow requires a dedicated, isolated, self-hosted Linux machine with:

- an x86-64 or ARM64 processor;
- the `lexr-kernel` runner label, in addition to the workflow's `self-hosted` and `linux` selection;
- an accessible Docker daemon;
- at least 64 GiB free in Docker storage; and
- at least 8 GiB free in the Actions workspace.

An x86-64 host uses the workflow-provided ARM64 binfmt emulation. An ARM64 host runs the build container natively. Dedicate the machine to trusted manual dispatches; do not expose this build boundary to untrusted work.

The workflow's compiled Docker-volume guard requires 40 GiB. GitHub's standard hosted runners are excluded because the documented private-repository storage is 14 GB, which cannot satisfy that guard. Follow GitHub's [self-hosted runner requirements](https://docs.github.com/en/actions/reference/runners/self-hosted-runners) when provisioning the machine.

Runner registration is external state which the repository cannot prove.
Confirm it before dispatch; whenever no matching runner is available, the job
queues until that prerequisite is configured.

## Keep CLI and hardware releases apart

Kernel and hardware-support releases remain in the [`ooaklee/linux-surface-pro-11-oe` release channel](https://github.com/ooaklee/linux-surface-pro-11-oe/releases). Keeping that channel stable preserves the defaults and links used by `lexr image create`, `lexr kernel release list`, and the userspace catalogue.

Releases in `ooaklee/lexr.sh` are reserved for the exact CLI set described in
[project and support boundaries](../reference/project-boundaries.md#what-the-lexr-release-contains).
Do not add kernel, firmware, driver, userspace, catalogue, collector, or other
project documentation to that release. Workflow-created kernel tags retain the
established `sp11-qcom-x1e-<package-version>` form; for example,
`sp11-qcom-x1e-7.2.2-jg-0sp11v1`. The Lexr repository's `v*` namespace remains
exclusive to CLI releases.

## Constrain the OE publication token

Remote OE publication uses one dedicated fine-grained GitHub token, stored only as the `OE_RELEASE_TOKEN` Actions secret in the Lexr repository.

Configure and maintain it with all of these limits:

- restrict it to `ooaklee/linux-surface-pro-11-oe`;
- grant only repository Contents read-and-write permission, which is the permission required to create tags and releases;
- choose a short expiry and rotate it before expiry or immediately after suspected disclosure; and
- never store it in OE, a runner configuration, a local environment file, or repository content.

The workflow exposes the token only to the GitHub-hosted publication step of a manual dispatch on exact Lexr `main`. The self-hosted kernel builder never receives it. Protect Lexr `main` when the repository plan makes branch protection available; the workflow still enforces the exact branch boundary itself.

## Publish draft-first, then verify the remote bytes

Enable the `release` input only on a manual dispatch which is intended to continue from a successful build through local release preparation to remote publication. The workflow then applies these gates:

1. The GitHub-hosted publisher revalidates the self-hosted builder's manifest release name, requires it to equal `sp11-qcom-x1e-<package-version>`, binds the public provenance to explicit Stubble mode, and checks the complete checksum set, corresponding source and licence assets, and experimental state.
2. It resolves OE `main` to one exact revision and refuses to reuse an existing release tag.
3. It creates an OE draft targeting that revision and uploads the complete closed release set.
4. It rechecks the draft identity, creates its lightweight tag at the resolved revision, and verifies the new remote ref.
5. It downloads every remote asset into a fresh directory, compares the complete local and remote digest sets, checks `SHA256SUMS`, and runs `lexr kernel release validate` against the downloaded bytes.
6. It verifies the tag again immediately before promotion, then promotes the draft to an experimental OE prerelease only when every check has passed.

A failure after draft creation leaves a recoverable OE draft rather than a promoted release. Inspect the draft and its tag before choosing a recovery action: the workflow deliberately refuses to replace or reuse an existing tag, so rerunning the same release name is not an automatic repair path.

This draft-first sequence makes the bytes downloaded from the remote service, rather than the pre-upload directory alone, the final promotion gate.
