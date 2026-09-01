# Lexr.sh

Lexr (Linux Exchanger) makes it easy to run ARM64 Linux on the Microsoft Surface Pro 11. It bundles a ready-to-use image, a tailored kernel, and essential device support, then helps you track and manage what works out of the box. Fast, auditable, and Surface Pro 11-focused.

Build a repeatable ARM64 Linux installer for the Microsoft Surface Pro 11, then
see which device support is actually ready on the installed system.

[Download Lexr](https://github.com/ooaklee/lexr.sh/releases) ·
[Get started](docs/getting-started/index.md) ·
[Read the docs](docs/index.md) ·
[Contribute](CONTRIBUTING.md)

## Why Lexr exists

Running Linux on the Surface Pro 11 currently means bringing together an ARM64
image, a compatible custom kernel, the matching modules and device trees, and a
small set of hardware-support components. It is easy to combine the wrong
versions or to finish an installation without knowing which pieces are still
missing.

Lexr turns that work into a checked, reviewable workflow. The CLI builds and
validates installation media, writes it to removable storage with explicit
confirmation and a full read-back, and audits the support present after boot.
Where Lexr can make a change, it favours dry runs, exact checksums and recovery
receipts so you can see what will happen first.

Lexr does **not** publish a ready-made Linux image or redistribute restricted
firmware. It starts from a supported upstream image and either downloads a
verified project kernel release or accepts a local bundle.

> [!WARNING]
> The generated media and its custom kernel are experimental. Back up important
> data, keep another bootable recovery device available, and disable Secure Boot
> before booting an unsigned custom kernel.

## What works today

The first implemented image adapter supports the experimental Ubuntu Concept
Resolute Desktop image for the Surface Pro 11 with Snapdragon X Elite. Debian,
elementary OS, Fedora and Pop!_OS entries are visible in the catalogue, but are
`catalog-only` until their image layouts have dedicated adapters. X Plus boot
support is present in the generated media, but still awaits hardware
qualification.

Lexr can help you:

- create and validate Surface Pro 11 installation media;
- inspect, download, build and install version-bound kernel bundles;
- write a validated image to a whole USB device on Linux or macOS;
- audit firmware, audio, pen, camera, wireless, Bluetooth and power support;
- move device-bound Windows evidence into Linux without exposing private values;
- install the explicitly supported userspace components; and
- plan, apply and reverse the recognised legacy clean-up operations.

The CLI has release binaries for Linux, macOS and Windows on AMD64 and ARM64,
but not every workflow runs on every host. See the
[requirements by task](docs/reference/requirements.md) before choosing where to
build or write an image.

## Get Lexr

The [releases page](https://github.com/ooaklee/lexr.sh/releases) provides six raw
executables and a versioned SHA-256 manifest. Use the latest stable release for
the most conservative starting point, or choose a clearly marked prerelease if
you want to test newer behaviour. A CLI release does not contain an ISO, kernel
packages, firmware, drivers or userspace bundles.

Choose the file matching your host:

| Host | Release filename |
| --- | --- |
| Linux x86-64 | `lexr-v<version>-linux-amd64` |
| Linux ARM64 | `lexr-v<version>-linux-arm64` |
| macOS Intel | `lexr-v<version>-darwin-amd64` |
| macOS Apple silicon | `lexr-v<version>-darwin-arm64` |
| Windows x86-64 | `lexr-v<version>-windows-amd64.exe` |
| Windows ARM64 | `lexr-v<version>-windows-arm64.exe` |

The [installation guide](docs/getting-started/install.md) walks through checksum
verification and adding the command to your `PATH`.

### Build from source

Source builds require Go 1.26 or newer. The repository pins the exact development
toolchain in [`.tool-versions`](.tool-versions).

```sh
git clone https://github.com/ooaklee/lexr.sh.git
cd lexr.sh
go run ./cmd/lexr-build
./bin/lexr version
```

Use the project builder shown above rather than a plain `go build`; it records
Lexr's source provenance without accidentally borrowing metadata from a
containing repository.

## Create your first image

For the current Ubuntu image workflow you need Docker with a running daemon and
Linux ARM64 container support, at least 24 GiB of free workspace storage, and
network access for any downloads. Start with the non-destructive readiness
check:

```sh
lexr doctor
lexr image create --output lexr-ubuntu-sp11.iso
lexr image validate lexr-ubuntu-sp11.iso
```

The short command uses the catalogue's dated Ubuntu snapshot and its default
kernel release selection. Canonical does not publish a checksum beside that
snapshot. If you need a reproducible trust decision, download the source image
yourself, record its SHA-256 digest, and pass both `--source` and
`--source-sha256`. The [installation-media guide](docs/user-guide/installation-media.md)
shows both paths and explains how to review a USB write safely.

Running `lexr` in an interactive terminal opens the guided image wizard. Every
wizard choice uses the same image services as the scriptable commands, so you
can start interactively and move to repeatable commands later.

## Find the right guide

| I want to… | Start here |
| --- | --- |
| Download or build the CLI | [Install Lexr](docs/getting-started/install.md) |
| Create, validate and write an image | [Installation media](docs/user-guide/installation-media.md) |
| Carry Lexr and IPTSD on the live medium | [Offline companion](docs/user-guide/offline-companion.md) |
| Check or add hardware support | [Userspace support](docs/user-guide/userspace-support.md) |
| Use private evidence collected from Windows | [Windows hand-offs](docs/user-guide/windows-handoff.md) |
| Inspect or install kernel bundles | [Kernel management](docs/operator-manual/kernel-management.md) |
| Remove recognised old workarounds | [Reversible clean-up](docs/user-guide/reversible-cleanup.md) |
| Look up a command | [Command reference](docs/reference/command-reference.md) |
| Understand the design | [Architecture and decisions](docs/developer-guide/architecture.md) |
| Work on Lexr | [Developer guide](docs/developer-guide/index.md) |

The [documentation home](docs/index.md) includes maintainer release workflows
and the complete architecture decision record index as well.

## A note about privilege and privacy

Most checks, downloads, builds, image creation, hand-off imports and dry runs
work as your regular user. Raw USB writing, kernel or userspace installation,
hand-off application or restoration, and clean-up against a real system root
need elevated access for that specific operation. Lexr never elevates itself.

Windows hand-offs, hardware captures and diagnostics may contain private device
data. Keep them out of issues, releases and source control. The focused guides
explain what can be shared safely and where recovery records must be retained.

## Contributing and licence

Lexr is a small open-source project maintained by Leon Silcott. A careful bug
report, a documentation fix or a well-tested change can all help; you do not
need to understand the whole codebase first. Read
[`CONTRIBUTING.md`](CONTRIBUTING.md) and follow the
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) before getting started.

The project is available under the [`Apache-2.0` licence](LICENSE). Attribution
is recorded in [`NOTICE`](NOTICE), and dependency terms are listed in
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).
