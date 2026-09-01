# Contributing to Lexr

Lexr is an open-source project run with ❤️ by Leon Silcott.

Thanks for wanting to help. A careful bug report, a small documentation fix, or
a well-tested change can all make the project better. You do not need to know
the whole codebase before getting involved.

## Before you start

Small fixes, tests, and documentation improvements can usually go straight to
a pull request. Please open an issue before investing in a substantial feature,
architecture change, persisted-data change, or privileged workflow. A short
conversation can save everyone a lot of work.

Changes to a trust boundary or a lasting project contract may also need an
[architecture decision record](docs/adr/_adr-XXXX-template.md) after the
direction is agreed. Please do not write an ADR merely to make an unreviewed
decision look settled.

Lexr itself covers the CLI, catalogues, tests, documentation, and release
automation. Kernel and hardware-support releases have their own source and
publication boundaries, even when Lexr builds or consumes them. If you are
unsure where an idea belongs, open a Lexr issue and I will help work it out.

## Report a bug safely

You do not need perfect wording or a complete fix to report a problem. A useful
report normally includes:

- the output of `lexr version`;
- the host operating system and architecture;
- the Surface Pro 11 model involved;
- the exact command or workflow;
- what you expected and what happened instead; and
- the smallest redacted error or log excerpt that shows the problem.

Please never trade your privacy for a better bug report. Do not upload a
Windows hand-off directory, diagnostic archive, firmware payload, raw device
identifier, Bluetooth address, adapter instance ID, token, credential, or
private capture. Inspect every attachment and remove local paths and personal
details before posting it.

Report a vulnerability or any other sensitive security problem privately to
[leon@boasi.io](mailto:leon@boasi.io), not in a public issue.

## Make a change

The repository pins its Go toolchain in [`.tool-versions`](.tool-versions).
Keep a change focused, add tests for changed behaviour, and update user-facing
documentation when the command or its output changes. Named Go declarations
need useful documentation comments, including declarations in tests, and
public prose uses British English.

If you are improving an explanation rather than changing the CLI, start with
the [documentation contribution guide](docs/developer-guide/documentation.md).
It shows where each kind of guidance belongs, how to keep safety notes beside
the action they protect, and how to check links and navigation before review.

Safety boundaries are part of the feature, not polish to add later. Preserve
dry runs, revalidation before mutation, explicit confirmation, rollback, and
privacy rules wherever the surrounding workflow relies on them. Do not commit
generated images, build directories, private diagnostics, captured device
data, or secrets.

Run the main local checks from the repository root:

```sh
go fmt ./...
go test -race ./...
go vet ./...
go run ./cmd/lexr-build
```

Continuous integration also builds all six supported operating-system and
architecture targets. Changes to the Windows collector receive its PowerShell
self-test and Pester checks there.

## Pull requests and commits

In a pull request, explain the problem first, then the approach you chose and
how you checked it. Link the issue for substantial work and call out any
privacy, privilege, recovery, compatibility, or release impact. Update the
Unreleased section of [the changelog](CHANGELOG.md) for a user-visible change;
purely internal test or documentation changes do not need a ceremonial entry.

The project uses concise [Conventional Commit](https://www.conventionalcommits.org/en/v1.0.0/)
subjects, such as `fix: reject an incomplete bundle` or
`docs: explain the recovery path`. Those subjects become part of release notes,
so describe the useful result rather than the mechanics of the edit.

This is a small, single-maintainer project, so replies may take time. A quiet
period is not a rejection, and a polite follow-up is welcome.

## Licence and contribution terms

Contributors keep their copyright. Unless you explicitly state otherwise, a
contribution intentionally submitted for inclusion in Lexr is provided under
`Apache-2.0`, without additional terms or conditions, as described in the
[project licence](LICENSE). The project does not currently require a CLA or
DCO sign-off.

Only submit work you have the right to contribute. If a change includes or is
derived from third-party material, explain its origin, confirm that its terms
are compatible, and preserve every required notice.

Dependency updates need the same care. Review
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md) against the supported release
binaries, and update it whenever linked code, generated data, copyright notices,
or applicable terms change.
