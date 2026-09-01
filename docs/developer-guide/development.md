# Development and testing

Lexr is a Go CLI with host-specific media, filesystem and privilege boundaries.
The normal development loop is small; the complete CI matrix supplies the
cross-platform checks which may not be available on your workstation.

## Prepare a checkout

The project requires Go 1.26 or newer and pins its current development toolchain
in [`.tool-versions`](https://github.com/ooaklee/lexr.sh/blob/main/.tool-versions).

```sh
git clone https://github.com/ooaklee/lexr.sh.git
cd lexr.sh
go run ./cmd/lexr-build
./bin/lexr version
```

Use `go run ./cmd/lexr-build` for a runnable source build. It supplies explicit
Lexr version metadata and disables automatic VCS stamping, preventing a
submodule build from borrowing the containing repository's revision. An
exported tree reports `unknown` provenance when it cannot prove a commit.

## Run the local checks

From the repository root:

```sh
go fmt ./...
go test -race ./...
go vet ./...
go run ./cmd/lexr-build
```

Use a narrower package test while iterating, but run the complete set before
review. Changes to public prose must also pass the repository-wide British
English, branding and documentation contracts in `internal/quality`.

Continuous integration builds raw executables for Linux, macOS and Windows on
AMD64 and ARM64. It also runs the Windows collector self-test and pinned Pester
suite. A non-Windows workstation is not expected to reproduce that native
PowerShell boundary locally.

## Follow the feature boundary

Keep feature behaviour in its owning package. The CLI and interactive wizard
should deliver the same service rather than reimplementing it. Platform process,
Docker, filesystem and privilege operations belong behind the existing
interfaces so host-independent policy can be tested without mutating a real
machine.

Tests should cover the refusal path as deliberately as the successful path:
mixed or incomplete inputs, changed files, raced destinations, wrong devices,
missing privilege, stale confirmations and recovery after partial progress are
all part of the public contract.

## Protect private and generated data

Do not commit downloaded images, kernel packages, build workspaces, generated
ISOs, Windows hand-offs, private diagnostics, firmware payloads, device
identifiers or camera captures. Inspect every log or fixture before adding it;
use synthetic and redacted evidence whenever possible.

Security or privacy vulnerabilities go privately to
[leon@boasi.io](mailto:leon@boasi.io), not into a public issue.

## Prepare the pull request

Explain the problem first, then the approach and how you checked it. Call out
privacy, privilege, recovery, compatibility, persisted-data and release effects.
User-visible behaviour belongs in the Unreleased changelog; documentation-only
and internal-test changes do not need a ceremonial entry.

Use a concise Conventional Commit subject such as
`docs: make image verification easier to follow`. See
[`CONTRIBUTING.md`](https://github.com/ooaklee/lexr.sh/blob/main/CONTRIBUTING.md)
for the complete terms and checklist.
