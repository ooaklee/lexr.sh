# Install Lexr

## Quick start (Linux and macOS)

The install script downloads the latest release manifest, verifies its GitHub
build provenance when a capable GitHub CLI is available, verifies the selected
binary's SHA-256 checksum, and puts `lexr` on your `PATH`:

```sh
curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh
```

Useful variants:

```sh
# Install a specific release version
curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh -s -- --version 0.2.0

# Reinstall the latest release even when it is already installed
curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh -s -- --force

# Install a local executable you downloaded and verified yourself
curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh -s -- --binary ./lexr-v<version>-linux-<arch>

# Do not edit shell startup files; the script only prints PATH guidance
curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh -s -- --no-modify-path
```

Without `--version`, the installer hashes an existing destination without
running it and skips the binary download when its bytes match the latest
release manifest. A small `lexr.version` sidecar supplies accurate update,
downgrade and reinstall progress messages but is never trusted for that
decision. Use `--force` to reinstall the latest release for a repair. An
explicit `--version` always reinstalls the requested release.

`--binary`, `--version` and `--force` have deliberately separate meanings and
cannot be combined where they conflict. A custom `LEXR_INSTALL_DIR` must be an
absolute path. Installing with `--binary` leaves the source file unchanged,
makes only the installed copy executable, and removes stale release-version
metadata. Run `install.sh --help` for all options. Windows users should follow
the manual steps below instead. Prefer to review each download yourself? The
rest of this page describes the manual flow the script automates.

## Choose a release

Lexr releases are deliberately simple: each supported host gets one raw
executable, accompanied by a SHA-256 manifest, its GitHub build-provenance
bundle, and the project legal documents. They do not include a Linux image,
kernel packages, firmware or userspace support bundles.

Open the [Lexr releases page](https://github.com/ooaklee/lexr.sh/releases).
GitHub marks the latest stable release and labels newer test releases as
prereleases. Documentation on `main` can describe behaviour that has not reached
the latest stable executable, so read the notes for the version you choose.

Download `lexr-v<version>.sha256sums`,
`lexr-v<version>.sha256sums.intoto.jsonl`, and the executable matching your
host:

| Host | Executable |
| --- | --- |
| Linux x86-64 | `lexr-v<version>-linux-amd64` |
| Linux ARM64 | `lexr-v<version>-linux-arm64` |
| macOS Intel | `lexr-v<version>-darwin-amd64` |
| macOS Apple silicon | `lexr-v<version>-darwin-arm64` |
| Windows x86-64 | `lexr-v<version>-windows-amd64.exe` |
| Windows ARM64 | `lexr-v<version>-windows-arm64.exe` |

These targets describe where the CLI can start. Some workflows have narrower
host requirements; check the [requirements reference](../reference/requirements.md)
before building an image or writing removable media.

## Verify the download

For releases from stable `v0.5.0` onwards, verify that GitHub attested the final
checksum manifest from the tagged release workflow. This requires a recent
[GitHub CLI](https://cli.github.com/) and the downloaded bundle:

```sh
gh attestation verify lexr-v<version>.sha256sums \
  --repo ooaklee/lexr.sh \
  --bundle lexr-v<version>.sha256sums.intoto.jsonl \
  --signer-workflow ooaklee/lexr.sh/.github/workflows/lexr.yml \
  --source-ref refs/tags/v<version> \
  --deny-self-hosted-runners
```

Earlier releases may not have a provenance bundle and remain verifiable by
checksum. The one-command installer performs the provenance check automatically
when GitHub CLI has attestation support. For `v0.5.0` and later, an invalid or
missing attestation is fatal when that verifier is available; otherwise the
installer prints a warning and continues with mandatory checksum verification.

Calculate the executable's SHA-256 digest and compare it with the exact filename
in the downloaded manifest. Both values must match before you run the file.

On Linux:

```sh
sha256sum lexr-v<version>-linux-<arch>
grep ' lexr-v<version>-linux-<arch>$' lexr-v<version>.sha256sums
```

On macOS:

```sh
shasum -a 256 lexr-v<version>-darwin-<arch>
grep ' lexr-v<version>-darwin-<arch>$' lexr-v<version>.sha256sums
```

On Windows PowerShell:

```powershell
Get-FileHash -Algorithm SHA256 .\lexr-v<version>-windows-<arch>.exe
Select-String -Path .\lexr-v<version>.sha256sums `
    -Pattern ' lexr-v<version>-windows-<arch>\.exe$'
```

Replace `<version>` and `<arch>` with the values in the filenames. Do not paste
the angle-bracket placeholders into a shell.

## Put Lexr on your PATH

On Linux or macOS, install the verified file under the stable command name. The
following user-local destination does not need administrator access, but
`$HOME/.local/bin` must be in your `PATH`:

```sh
mkdir -p "$HOME/.local/bin"
install -m 0755 lexr-v<version>-<os>-<arch> "$HOME/.local/bin/lexr"
lexr version
```

On Windows, rename the verified executable to `lexr.exe`, move it into a folder
you control, add that folder to your user `PATH`, open a new PowerShell window,
and run:

```powershell
lexr version
```

Downloading a Windows binary does not make Linux- or macOS-only media workflows
available. The private hand-off is collected by the separate PowerShell script
in tagged source or an on-media companion; the Windows CLI executable does not
contain that collector.

## Build from source

Source builds need Go 1.26 or newer. The exact development version is pinned in
the repository's [`.tool-versions`](https://github.com/ooaklee/lexr.sh/blob/main/.tool-versions).

```sh
git clone https://github.com/ooaklee/lexr.sh.git
cd lexr.sh
go run ./cmd/lexr-build
./bin/lexr version
```

The Go-native builder records the selected Lexr checkout explicitly and disables
automatic VCS stamping. That keeps builds honest when Lexr is a submodule or an
exported source archive: a build reports `unknown` provenance when it cannot
prove the Lexr revision instead of borrowing one from a containing repository.

## Update or remove Lexr

Run `lexr upgrade` to update an installed release in place. You can also repeat
the quick-start installer command: it verifies the latest manifest and skips
the binary download when the installed bytes already match. Pass `--force` to
that installer command only when you need to repair the current release by
reinstalling it. Downloads use bounded timeouts and retry transient failures.
Read release notes first, especially when you have outstanding recovery
receipts or private hand-off state that may require the exact predecessor
binary.

To remove the default user-local installation, delete both the executable and
its non-authoritative version sidecar:

```sh
rm -f "$HOME/.local/bin/lexr" "$HOME/.local/bin/lexr.version"
```

Use the corresponding paths if you selected a different installation
directory. If the installer added a line ending in `# lexr install` to
`.profile`, `.bash_profile`, `.bash_login` or `.zshrc`, remove that line too.
Fish users receive a `fish_add_path` instruction instead of a POSIX startup-file
edit. Removing Lexr does not remove images, caches, hand-off stores, installed
components, recovery receipts or backups. Review each of those with the version
that created it; do not treat uninstalling the command as a system rollback.

Continue with [Get started with Lexr](index.md).
