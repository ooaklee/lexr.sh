# Install Lexr

## Quick start (Linux and macOS)

The install script downloads the latest release for your platform, verifies its
SHA-256 checksum against the release manifest, and puts `lexr` on your `PATH`:

```sh
curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh
```

Useful variants:

```sh
# Install a specific release version
curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh -s -- --version 0.2.0

# Install a local executable you downloaded and verified yourself
curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh -s -- --binary ./lexr-v<version>-linux-<arch>

# Do not edit shell startup files; the script only prints PATH guidance
curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh -s -- --no-modify-path
```

Run `install.sh --help` for all options. Windows users should follow the manual
steps below instead. Prefer to review each download yourself? The rest of this
page describes the manual flow the script automates.

## Choose a release

Lexr releases are deliberately simple: each supported host gets one raw
executable, accompanied by a SHA-256 manifest and the project legal documents.
They do not include a Linux image, kernel packages, firmware or userspace
support bundles.

Open the [Lexr releases page](https://github.com/ooaklee/lexr.sh/releases).
GitHub marks the latest stable release and labels newer test releases as
prereleases. Documentation on `main` can describe behaviour that has not reached
the latest stable executable, so read the notes for the version you choose.

Download `lexr-v<version>.sha256sums` and the executable matching your host:

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

To update, repeat the download and checksum steps for the new version, then
replace only the installed `lexr` or `lexr.exe` file. Read release notes first,
especially when you have outstanding recovery receipts or private hand-off
state that may require the exact predecessor binary.

To remove the CLI, delete that one installed executable. This does not remove
images, caches, hand-off stores, installed components, recovery receipts or
backups. Review each of those with the version that created it; do not treat
uninstalling the command as a system rollback.

Continue with [Get started with Lexr](index.md).
