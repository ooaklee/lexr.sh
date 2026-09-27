# Lexr.sh

Lexr (Linux Exchanger) makes it easy to run ARM64 Linux on Qualcomm Snapdragon X Devices (initially focusing on Microsoft Surface Pro 11). It is a centralised tool that lets you manage and create ready-to-use images, tailored kernels, and essential device libraries to maximise your device's functionality when using Linux. With auditability as a core principle, it helps you track and manage what's implemented out of the box. Fast, auditable, and Qualcomm Snapdragon X-focused.

Build a repeatable ARM64 Linux kernel + installer for your Snapdragon X Device.

On Linux or macOS, the install script verifies the latest release checksum and,
when GitHub CLI supports it, the manifest's build provenance. It installs the
matching binary atomically and puts `lexr` on your `PATH`:

```sh
curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh
```

Re-running the command compares the installed bytes with the release manifest
without executing them and skips the binary download when they already match.
Pass `--force` to reinstall the latest release.

[Download Lexr](https://github.com/ooaklee/lexr.sh/releases) ·
[Get started](docs/getting-started/index.md) ·
[Read the docs](docs/index.md) ·
[Contribute](CONTRIBUTING.md)


## Prebuilt Linux images

Start with a prebuilt ARM64 Linux image for Surface Pro 11 from the OE repository.
These experimental images include Lexr and the SP11 v23 kernel:

- [Ubuntu Concept 26.04](https://github.com/ooaklee/linux-surface-pro-11-oe/releases/tag/sp11-ubuntu-concept-26.04-v23-20260927)
- [elementary OS 8.1](https://github.com/ooaklee/linux-surface-pro-11-oe/releases/tag/sp11-elementary-os-8.1-v23-20260927)
- [Debian Live GNOME (2024-09-02 snapshot)](https://github.com/ooaklee/linux-surface-pro-11-oe/releases/tag/sp11-debian-13-gnome-v23-20260927)
- [Fedora Workstation Live 44](https://github.com/ooaklee/linux-surface-pro-11-oe/releases/tag/sp11-fedora-workstation-44-v23-20260927)
- [Arch Linux ARM terminal image](https://github.com/ooaklee/linux-surface-pro-11-oe/releases/tag/sp11-arch-linux-arm-terminal-v23-20260927)

On Windows, follow [Prepare a USB image with Etcher](docs/user-guide/windows-image-usb.md).
Already using Linux? Follow the release commands and the
[Lexr USB workflow](docs/user-guide/installation-media.md#2-review-the-usb-target).
Read the selected release's hardware requirements and testing limitations before
choosing an image.

You can also [build your own](#create-your-first-image) image with Lexr.

Lexr images and installation guidance are intentionally designed for multi-OS
setups. They preserve upstream alongside-install options and document manual
partitioning instead of requiring a whole-drive erase. Keeping Windows or
another Linux installation available gives you a working fallback while you
test, recover, collect diagnostics and contribute support for more features.
The Arch flow makes this explicit: install into reserved space, reuse an
existing EFI System Partition without formatting it, and leave every other OS
partition unchanged. Review the
[Arch partitioning walkthrough](docs/user-guide/arch-linux-arm-quickstart.md#2-select-only-the-space-reserved-for-arch)
before making disk changes.

## Why Lexr exists

Running Linux on the Qualcomm Snapdragon X devices currently means bringing together an ARM64
image, a compatible custom kernel, the matching modules and device trees, and a
small set of hardware-support components. It is easy to combine the wrong
versions or to finish an installation without knowing which pieces are still
missing.

Lexr turns that work into a checked, reviewable workflow. The CLI builds and
validates installation media, writes it to removable storage with explicit
confirmation and a full read-back, and manages version-bound kernel bundles.
After boot, it audits hardware support and installs the explicitly supported
userspace components. It also supports Windows-to-Linux hand-offs of private
device evidence and reversible removal of recognised legacy workarounds.

Where Lexr can make a change, it favours dry runs, exact checksums and recovery
receipts so you can see what will happen first.

Lexr creates images from a supported upstream image and either downloads a
verified project kernel release or accepts a local bundle. It does not
redistribute restricted firmware. You can also use
[prebuilt Linux images](#prebuilt-linux-images) prepared with Lexr, or
[build your own](#create-your-first-image) image with Lexr.

> [!WARNING]
> The generated media and its custom kernel are experimental. Back up important
> data, keep another bootable recovery device available, and disable Secure Boot
> before booting an unsigned custom kernel.

## Get Lexr

On Linux or macOS, the install script verifies the latest release checksum and,
when GitHub CLI supports it, the manifest's build provenance. It installs the
matching binary atomically and puts `lexr` on your `PATH`:

```sh
curl -fsSL https://raw.githubusercontent.com/ooaklee/lexr.sh/refs/heads/main/install.sh | sh
```

Re-running the command compares the installed bytes with the release manifest
without executing them and skips the binary download when they already match.
Pass `--force` to reinstall the latest release.

The [releases page](https://github.com/ooaklee/lexr.sh/releases) provides six raw
executables, a versioned SHA-256 manifest, and its GitHub build-provenance
bundle. Use the latest stable release for the most conservative starting point,
or choose a clearly marked prerelease if you want to test newer behaviour. A
CLI release does not contain an ISO, kernel packages, firmware, drivers or
userspace bundles.

Choose the file matching your host:

| Host | Release filename |
| --- | --- |
| Linux x86-64 | `lexr-v<version>-linux-amd64` |
| Linux ARM64 | `lexr-v<version>-linux-arm64` |
| macOS Intel | `lexr-v<version>-darwin-amd64` |
| macOS Apple silicon | `lexr-v<version>-darwin-arm64` |
| Windows x86-64 | `lexr-v<version>-windows-amd64.exe` |
| Windows ARM64 | `lexr-v<version>-windows-arm64.exe` |

Not every workflow runs on every host. See the
[requirements by task](docs/reference/requirements.md) before choosing where
to build or write an image.

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

Choose a [prebuilt image above](#prebuilt-linux-images), or build your own with Lexr.
The prebuilt images include a getting-started guide.
Ubuntu and elementary include prepared Wi-Fi board data and the IPTSD bundle.
Arch starts in a terminal with networking and a guided installer; follow its
[installation walkthrough](docs/user-guide/arch-linux-arm-quickstart.md) to
choose your partitions and optional desktop. Follow each release's
download, verification and writing instructions using the released
[Lexr v0.5.0-rc.3](https://github.com/ooaklee/lexr.sh/releases/tag/v0.5.0-rc.3)
for your host. Download and reconstruct the split ISO, verify its checksum and
write it to USB. The release notes distinguish each rebuilt image's validation
from earlier hardware tests and record remaining limitations.

To create your own experimental Ubuntu image, you need Docker with a running daemon
and Linux ARM64 container support, at least 24 GiB of free workspace storage,
and network access for any downloads. Start with the non-destructive readiness
check:

```sh
lexr doctor
lexr profile list
lexr init x1e80100-microsoft-denali-oled
lexr image create --output lexr-ubuntu-sp11.iso
lexr image validate lexr-ubuntu-sp11.iso
```

`init` saves the Surface Pro 11 X Elite OLED profile for subsequent commands.
Choose `x1p64100-microsoft-denali` for the X Plus LCD, or use the global
`--profile` flag for one invocation. See
[hardware profiles](docs/concepts/hardware-profiles.md) and
[configuration](docs/user-guide/configuration.md) for detection and overrides.

The short command uses the catalogue's dated Ubuntu snapshot and its default
kernel release selection. Canonical does not publish a checksum beside that
snapshot. If you need a reproducible trust decision, download the source image
yourself, record its SHA-256 digest, and pass both `--source` and
`--source-sha256`. A successful build and structural validation do not prove
that the image will boot or install on physical hardware.

Fedora requires the explicit `fedora-workstation-live-44` catalogue ID and a
patch-line-qualified verified kernel bundle. The accepted floors are
7.2.0/sp11v19 and 7.2.2/sp11v1. External-DTB bundles, including v23, require
`--profile x1e80100-microsoft-denali-oled`. The
[installation-media guide](docs/user-guide/installation-media.md) shows the implemented
distribution paths, the recorded Fedora live-boot results, their hardware
limits, and how to review a USB write safely.

Running `lexr` in an interactive terminal opens the guided image wizard. Every
wizard choice uses the same image services as the scriptable commands, so you
can start interactively and move to repeatable commands later.

## Find the right guide

| I want to… | Start here |
| --- | --- |
| Download or build the CLI | [Install Lexr](docs/getting-started/install.md) |
| Create, validate and write an image | [Installation media](docs/user-guide/installation-media.md) |
| Prepare a prebuilt image and USB on Windows | [Windows USB preparation](docs/user-guide/windows-image-usb.md) |
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
