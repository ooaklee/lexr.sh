# Lexr.sh

Lexr.sh makes it easy to run ARM64 Linux on the Microsoft Surface Pro 11. It bundles a ready-to-use image, a tailored kernel, and essential device support, then helps you track and manage what works out of the box. Fast, auditable, and Surface Pro 11-focused.

The `lexr` CLI prepares ARM64 installation media and audits the userspace support installed alongside its custom kernels. It combines a supported upstream image with a version-bound kernel bundle, matching modules, an initramfs, and the Surface Pro 11 device trees. Its userspace companion can then report missing support and manage the small set of components for which the project has an audited workflow. Lexr source, CLI releases and issue tracking live in the [Lexr.sh repository](https://github.com/ooaklee/lexr.sh); established hardware-support releases retain their recorded OE locations.

Lexr has dedicated adapters for the experimental Ubuntu Concept Resolute
Desktop image and the Fedora Workstation Live 44 ISO. Debian, elementary OS,
Pop!_OS, and Fedora's compressed raw disk image remain `catalog-only` until
their layouts have dedicated adapters.

> [!WARNING]
> The generated media and its custom kernel are experimental. Keep another bootable recovery device available, back up important data, and disable Secure Boot before booting an unsigned custom kernel.

## Naming boundary

Lexr.sh is the current project and repository name, and `lexr` is the current
command. The project previously lived beneath a differently named CLI directory
in the OE repository. The standalone repository now uses the Lexr identity
consistently across commands, media paths, private state, installed integration,
wire domains, manifests and release filenames:

- schema-4 media paths use `/sp11/lexr-manifest.json`,
  `/sp11/companion/bin/linux-arm64/lexr`, and
  `lexr_<version>_source.tar.gz`;
- private hand-offs use the `.lexr-handoffs` store and `lexr.windows-handoff`
  wire domains;
- installed integration uses `/etc/lexr`, `/usr/libexec/lexr`,
  `/usr/lib/lexr`, `/var/lib/lexr`, and `lexr-sp11-*` units and hooks; and
- kernel and userspace bundle, provenance and release filenames use the
  `lexr-*.json` form.

Existing OE repository and release URLs remain valid external provenance rather
than product branding. [ADR023](docs/adr/adr-023-lexr-standalone-repository-and-compatibility.md)
records the standalone repository migration and the completed naming boundary.

## What it does

- Presents a curated, strictly validated catalogue from `supported-isos.json`.
- Resolves a candidate kernel release from GitHub or accepts a local package directory, then verifies every selected package before use.
- Refuses mixed kernel ABIs, missing runtime packages, and checksum mismatches.
- Remasters either the Ubuntu Casper live filesystem or Fedora 44's EROFS live root with the selected kernel and modules.
- Generates an adapter-appropriate initramfs for that exact ABI and copies the matching X1E and X1P device trees.
- Synchronises and validates the Casper identity shared by the generated initramfs and direct USB medium.
- Registers the exact kernel packages in the deployable Ubuntu root and supplies a non-Casper installed initramfs, paired device trees, bounded kernel hooks, and explicit installed-system GRUB entries.
- Repackages the verified Debian kernel payload as a Fedora-owned RPM, prepares its Anaconda and `kernel-install` hand-off, retains Fedora's stock kernel and initramfs as manifest-bound outer-media fallbacks with explicit Surface DTBs, validates their exact ABI/module pairing, and restores an X1E-paired stock kernel as an installed BLS fallback after the first successful custom-kernel boot.
- When the exact source-bearing IPTSD companion is requested for Fedora, rebuilds it natively as binary and source RPMs, installs the binary package into the deployable live root, and binds both RPMs in the image manifest. Omitting that companion leaves Fedora IPTSD unchanged.
- Applies `soundwire_qcom.sp11_feedback_active_offset2_zero=1` to both live and installed boot paths while keeping the USB-only `qcom_q6v5_pas` blacklist out of the installed system.
- Preserves the source image's hybrid ISO/GPT boot layout and updates both ARM64 EFI boot paths.
- Validates the finished ISO before publishing it.
- Can place a manifest-tracked Linux ARM64 companion CLI, its corresponding source and catalogues, and an eligible offline IPTSD release on the finished medium.
- Discovers whole storage devices on Linux and macOS, refuses unsafe targets, and writes a validated ISO only after an exact image-and-device-bound confirmation; success requires a complete SHA-256 read-back and safe ejection.
- Imports strictly validated, device-bound Windows evidence into a private content-addressed store and exposes only redacted summaries and exact-confirmation retention controls.
- Audits firmware, audio, pen, camera, wireless, Bluetooth, power-profile, and obsolete-workaround state without changing the installed system.
- Downloads exact, checksum-verified userspace release sets and exposes bounded build and install workflows only for explicitly supported components.
- Detects a fixed set of legacy Surface Pro 11 workarounds and can apply and reverse an exact reviewed clean-up plan with durable receipts.

## Requirements

- Go 1.26 or newer to build the CLI from source, and on the image-building host when `--companion-source-dir` is used.
- Docker with a running daemon and Linux ARM64 container support.
- At least 24 GiB of free workspace storage for an image build.
- Network access when downloading an upstream image, kernel release, or userspace release.
- `diskutil` and `plutil` on macOS, or `lsblk`, `umount`, and `udisksctl` on Linux, when discovering or writing removable media.

Run the CLI as a regular user for image creation and validation, catalogue, download, build, diagnostics, hand-off import, and every dry run whose input is readable by that user. Image tooling runs in an isolated ARM64 Docker container; the CLI does not require the entire process to run as root. Raw USB writing, kernel installation, userspace installation, hand-off application, hand-off restoration, and clean-up against a real system root require elevated access for the specific operation. A hand-off restore preview normally also needs elevation because the privileged application creates its receipt directory with private root-only permissions. The CLI never elevates itself.

## Build

Clone the standalone repository and build the command from its root:

```sh
git clone https://github.com/ooaklee/lexr.sh.git
cd lexr.sh
go run ./cmd/lexr-build
./bin/lexr doctor
```

The Go-native source builder records the selected Lexr checkout explicitly and
disables Go's automatic VCS stamping. This matters when Lexr is a Git submodule:
Go can otherwise walk past its `.git` pointer file and stamp the containing
repository's revision. An exported source archive remains buildable and reports
an honest `unknown` commit instead of inventing provenance.

## Workflow ownership

Lexr-dependent automation is owned and run by this repository. The
[main workflow](.github/workflows/lexr.yml) formats, tests, vets, builds and
packages the CLI. The
[IPTSD integration workflow](.github/workflows/iptsd-integration-tests.yml)
sparsely checks out the two public OE contract directories into temporary
runner storage, validates them from Lexr, and runs nightly as well as for Lexr
changes. A branch push or same-repository pull request uses an exact,
same-named branch from the canonical OE repository when one exists and
otherwise falls back to OE `main`; fork pull requests and scheduled runs remain
bound to `main`. Manual dispatch accepts an explicit `oe_ref` when a particular
OE branch, tag, or commit needs checking. The
[kernel workflow](.github/workflows/sp11-kernel-build.yml) is manually
dispatched here and uses the checked-out Lexr source to build, validate and
optionally publish an experimental kernel prerelease in the OE repository.
Its retained Actions artefact contains only the verified Debian packages and
their checksum manifest; path-bearing build provenance stays within the trusted
job, the displayed output path is redacted, and the separately prepared release
remains the public provenance record.

The OE repository therefore needs neither a personal access token for Lexr nor
a GitHub Actions checkout of the private submodule. Lexr holds the narrowly
scoped publication credential required to write an OE kernel release. This
ownership boundary is recorded in
[ADR024](docs/adr/adr-024-lexr-owned-automation.md).

Kernel compilation requires a dedicated, isolated, self-hosted Linux x86-64 or
ARM64 runner carrying the `lexr-kernel` label, an accessible Docker daemon, at
least 64 GiB free in Docker storage, and at least 8 GiB free in its Actions
workspace. An x86-64 host uses the workflow-provided ARM64 binfmt emulation; an
ARM64 host runs the build container natively. No matching runner was registered
when the workflow was transferred, so a dispatch will queue until that external
prerequisite is configured. Standard GitHub-hosted runners are intentionally
excluded: their
[14 GB private-repository storage](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
cannot meet Lexr's compiled 40 GiB Docker-volume guard. Follow GitHub's
[self-hosted runner requirements](https://docs.github.com/en/actions/reference/runners/self-hosted-runners)
and dedicate the machine to trusted manual workflow dispatches.

Kernel and hardware-support releases remain in
[`ooaklee/linux-surface-pro-11-oe`](https://github.com/ooaklee/linux-surface-pro-11-oe/releases).
This keeps `lexr image create`, `lexr kernel release list`, existing shared
links, and the supported userspace catalogue on one established release
channel. GitHub Releases in `ooaklee/lexr.sh` are reserved for the Lexr CLI and
contain six standalone platform executables, the three project legal documents,
and one versioned SHA-256 manifest covering all nine payload files. Kernel,
firmware, driver, userspace, catalogue, collector, and other project
documentation are never published there. Release filenames follow one
predictable raw-executable layout, with the legal documents retaining their
repository names:

```text
lexr-v<version>-darwin-amd64
lexr-v<version>-darwin-arm64
lexr-v<version>-linux-amd64
lexr-v<version>-linux-arm64
lexr-v<version>-windows-amd64.exe
lexr-v<version>-windows-arm64.exe
LICENSE
NOTICE
THIRD_PARTY_NOTICES.md
lexr-v<version>.sha256sums
```

Remote OE publication uses one dedicated fine-grained GitHub token stored only
as the `OE_RELEASE_TOKEN` Actions secret in the Lexr repository. Restrict the
token to `ooaklee/linux-surface-pro-11-oe`, grant only the repository Contents
read-and-write permission required to create tags and releases, choose a short
expiry, and rotate it before expiry or immediately after suspected disclosure.
Do not store this token in OE, a runner configuration, a local environment
file, or repository content. The workflow exposes it only to the
GitHub-hosted publication step of a manually dispatched run on exact Lexr
`main`; the self-hosted kernel builder never receives it. Protect Lexr `main`
when the repository plan makes branch protection available.

Publication is draft-first. The workflow uploads the closed release, downloads
every remote asset into a fresh directory, compares the complete digest set,
runs `lexr kernel release validate` against the downloaded bytes, and promotes
the OE draft only after every check succeeds. It first resolves OE `main` to an
exact revision, refuses to reuse an existing release tag, and verifies that the
new tag still identifies that revision immediately after creation and again
before promotion. Failure leaves a recoverable OE draft. The GitHub-hosted
publication job also revalidates the self-hosted
builder's manifest release name, checksum set, required source and licence
assets, experimental state, and `sp11-kernel-` tag namespace before entering
the secret-bearing publication step. Workflow-created kernel tags must begin
with `sp11-kernel-`; the Lexr repository's `v*` tag namespace remains exclusive
to CLI releases.

## Command overview

Running `lexr` in an interactive terminal opens the Bubble Tea wizard. Every wizard choice maps to the same services used by the non-interactive commands.

```text
lexr wizard
lexr doctor
lexr doctor userspace

lexr catalog list
lexr catalog show <id>
lexr catalog validate [path]

lexr kernel release list
lexr kernel release download [ref]
lexr kernel release prepare --help
lexr kernel release validate <release-directory>
lexr kernel inspect <directory>
lexr kernel preflight <bundle-directory> --root <path> --fallback-abi <abi>
lexr kernel install <bundle-directory> --root <path> --fallback-abi <abi> --dry-run
lexr kernel build

lexr image create --output <iso>
lexr image validate <iso>
lexr image devices
lexr image write <iso> --device <whole-device> --dry-run
lexr image release prepare <iso> --dry-run
lexr image release validate <release-directory>

HANDOFF_STORE="${HOME}/.lexr-handoffs"
lexr handoff import <directory> --store "$HANDOFF_STORE"
lexr handoff list --store "$HANDOFF_STORE"
lexr handoff apply <id> --store "$HANDOFF_STORE" --target-root <path> --dry-run
sudo lexr handoff restore <receipt-id> --target-root <path> --dry-run
lexr handoff purge <id> --store "$HANDOFF_STORE" --dry-run

lexr userspace list
lexr userspace show <component>
lexr userspace catalog validate [path]
lexr userspace status
lexr userspace pull <component|recommended>
lexr userspace build <iptsd|camera>
lexr userspace install <component|recommended> --from <directory>
lexr userspace audio release prepare --help
lexr userspace audio release validate <release-directory>
lexr userspace camera capture --dry-run
lexr userspace camera render <capture.raw> <preview.png>
lexr userspace camera release prepare --help
lexr userspace camera release validate <release-directory> --authority-sha256 <sha256>

lexr clean scan
lexr clean plan --output lexr-cleanup-plan.json
lexr clean apply --plan lexr-cleanup-plan.json --yes
lexr clean restore /var/lib/lexr/backups/<transaction>/receipt.json --yes
```

Use `lexr <command> --help` for the complete option set. Machine-readable JSON is available where a command advertises a `--json` option.

## Create a supported image

The shortest image command selects `ubuntu-concept-resolute-x1e` and the latest candidate kernel release. The release's packages become trusted only after their publisher checksums and measured contents pass verification:

```sh
lexr doctor
lexr image create --output lexr-ubuntu-sp11.iso
lexr image validate lexr-ubuntu-sp11.iso
```

Use `--source` to supply an already downloaded source ISO,
`--source-sha256` to require a known digest, `--kernel-dir` to use a local
verified image/modules Debian package bundle, or `--kernel-release` to select a
tagged release. Cache and temporary-workspace locations can also be overridden.

The catalogue pins Canonical's dated 2026-03-26 snapshot rather than its mutable latest-image alias. Canonical does not publish a checksum alongside that snapshot, so a reproducible trust decision still requires you to record the downloaded SHA-256 digest and pass both the local path and digest:

```sh
# Linux
sha256sum resolute-desktop-arm64+x1e-20260326.iso

# macOS
shasum -a 256 resolute-desktop-arm64+x1e-20260326.iso

lexr image create \
  --source resolute-desktop-arm64+x1e-20260326.iso \
  --source-sha256 <sha256> \
  --kernel-release <release-tag> \
  --output lexr-ubuntu-sp11.iso
```

`--dry-run` prints the deterministic operation plan without remastering an image. `--keep-workspace` retains intermediate files for troubleshooting.

### Fedora Workstation Live 44

Select Fedora's implemented Live ISO explicitly and provide either a local
v19-or-newer kernel bundle or a corresponding verified release:

```sh
lexr image create \
  --catalog-id fedora-workstation-live-44 \
  --kernel-dir build/kernel-v19 \
  --output lexr-fedora-44-sp11-v19.iso

# Instead of --kernel-dir, a release build can use:
#   --kernel-release <v19-or-newer-release-tag>

lexr image validate lexr-fedora-44-sp11-v19.iso
lexr image write lexr-fedora-44-sp11-v19.iso \
  --device /dev/diskX \
  --dry-run
```

To carry the companion and install the source-complete IPTSD integration as a
native Fedora package, keep the output outside the source tree and add both
companion flags:

```sh
mkdir -p ../lexr-build
lexr image create \
  --catalog-id fedora-workstation-live-44 \
  --kernel-dir build/kernel-v19 \
  --companion-source-dir . \
  --companion-userspace iptsd \
  --output ../lexr-build/lexr-fedora-44-sp11-v19-iptsd.iso
```

This exact combination activates Fedora's native `lexr-sp11-iptsd` RPM path.
The binary RPM is installed in the live root so Anaconda carries its ownership,
service, and udev rule into the installed system; the corresponding source RPM
and complete release archive remain on the ISO. Without
`--companion-userspace iptsd`, no native IPTSD RPM is built, staged, installed,
or claimed.

The catalogue supplies Fedora's publisher SHA-256 and the adapter rejects
pre-v19 kernels. The custom boot path is structurally supported for the
X1E/OLED Surface Pro 11. Secure Boot must be disabled because the custom
Stubble kernel is unsigned. X1P/LCD custom Stubble auto-DTB selection is still
awaiting hardware qualification, so the troubleshooting menu provides an
explicit-DTB stock-kernel path for live investigation only. X1P installed-system
handoff is not supported by this adapter; do not use this image to install an
X1P system. A successful structural validation does not replace burning and
booting the USB, completing an X1E installation, and booting that installed
system on the target hardware; those remain release gates.
[ADR027](docs/adr/adr-027-fedora-erofs-remaster-and-installed-handoff.md)
records the EROFS remaster and Anaconda hand-off decision.

### Carry the offline companion

An image can carry the exact Linux ARM64 CLI, a deterministic archive of the corresponding maintained source, and validated copies of both catalogues. Add the audited IPTSD release when offline pen and touchscreen installation is useful:

```sh
mkdir -p ../lexr-build
lexr image create \
  --source resolute-desktop-arm64+x1e-20260326.iso \
  --source-sha256 <sha256> \
  --kernel-release <release-tag> \
  --companion-source-dir . \
  --companion-userspace iptsd \
  --output ../lexr-build/lexr-ubuntu-sp11.iso
```

`--companion-source-dir` must identify a complete `lexr` source tree and requires a working host Go toolchain. Keep the image output, its sidecars, and any explicit `--workspace-dir` outside that source tree, as in the example. The source is snapshotted before its binary and archive are built, and a clean Git-backed tree must match the CLI's recorded commit. `--companion-userspace` is repeatable, but the initial offline allow-list accepts only `iptsd`. `recommended`, restricted audio, platform firmware, and experimental camera packages are not accepted for on-media inclusion.

The payload is stored under `/sp11/companion`. The embedded
`/sp11/lexr-manifest.json` and the ISO-adjacent
`*.iso.manifest.json` sidecar are byte-identical copies of the only image
inventory; their mandatory `companion_bundle` attribute records every
companion file. The deterministic
source archive includes the strict Windows hand-off collector at
`lexr/tools/collect-sp11-windows-handoff.ps1`. The first path component
is the schema-4 archive root; the archive
never contains
collected device data. The portable receipt inside an IPTSD release verifies
that component's relocatable files and is itself included in the outer
inventory. It is not a second image manifest.

Lexr is provided under `Apache-2.0`. A locally requested companion records
`project_licence: declared`, inventories `LICENSE`, `NOTICE`, and
`THIRD_PARTY_NOTICES.md`, and carries them both in its source archive and its
on-media licence directory.

The repository root is the single legal-document authority for companion
images and CLI releases. A Git-backed companion requires the complete
repository to be clean so those documents match its recorded source revision.
Tag publication likewise fails before GoReleaser if any of the three exact
files is absent, empty, or symbolic. The release job publishes them alongside
the six raw executables, extends the one versioned SHA-256 manifest to cover all
nine payloads, and rejects anything outside the ten-file release set. The
decision and its relationship to the kernel's separate terms are recorded in
[ADR026](docs/adr/adr-026-apache-2.0-project-terms-and-release-notices.md).

After booting the live image, copy the executable from the read-only medium to a writable executable filesystem before using it:

```sh
COMPANION_ROOT=/cdrom/sp11/companion
TOOL=/tmp/lexr

install -m 0755 \
  "$COMPANION_ROOT/bin/linux-arm64/lexr" \
  "$TOOL"

"$TOOL" version
"$TOOL" catalog validate \
  "$COMPANION_ROOT/catalogues/supported-isos.json"
"$TOOL" userspace catalog validate \
  "$COMPANION_ROOT/catalogues/supported-userspace.json"
"$TOOL" doctor userspace
```

The source path uses the schema-4 companion filename; the copy is invoked as
`lexr` through the writable `$TOOL` path.

A non-zero userspace doctor result means support is still missing; it does not
by itself mean the companion is damaged. On Ubuntu media, if IPTSD was
included, first verify its installation plan and then apply it to the live
session:

```sh
IPTSD_ROOT="$COMPANION_ROOT/userspace/iptsd-v1/sp11-iptsd-v1"

"$TOOL" userspace install iptsd --from "$IPTSD_ROOT" --dry-run
sudo "$TOOL" userspace install iptsd --from "$IPTSD_ROOT" --yes
"$TOOL" doctor userspace --feature iptsd
```

For an installed system mounted at `/target`, add `--root /target` to the install and doctor commands.

Do not run that portable IPTSD installer on a Fedora image created with both
Fedora companion flags. Fedora already contains the native, RPM-owned
`/usr/libexec`, systemd, and udev layout; the portable installer would add a
second unowned `/usr/local` copy. On Fedora, verify the carried package with
`rpm -q lexr-sp11-iptsd` and use `doctor userspace --feature iptsd`. The
portable archive remains on the medium as corresponding-source and offline
recovery evidence, not as the normal Fedora installation path.

### Why these are true live-image remasters

Copying kernel packages beside an untouched installer does not change the kernel used by the live environment. The Ubuntu adapter instead unpacks the Casper filesystem, installs the custom runtime packages, rebuilds the initramfs, replaces `/casper/vmlinuz` and `/casper/initrd`, adds the paired device trees, and repacks the filesystem.

The Fedora adapter extracts the EROFS filesystem stored at
`/LiveOS/squashfs.img`, creates a native RPM from the exact verified kernel
payload, and rebuilds the live initramfs with `dracut-live`. It keeps only the
custom `/boot/vmlinuz-*` candidate in the deployable root so Anaconda selects
that kernel during installation, while the untouched Fedora kernel remains in
the outer ISO's troubleshooting menu. After the installed system boots the
custom kernel, its one-shot finalizer restores the package-owned stock image
from the module tree, creates the corresponding BLS fallback, and explicitly
keeps the custom kernel as the default. The adapter then recreates
LZMA-compressed EROFS with SELinux file contexts and extended attributes
intact.

Both source images are hybrid boot media: they contain ISO boot metadata and
an appended GPT EFI System Partition. Each adapter replays that layout while
retaining its distribution-specific media discovery contract. Ubuntu binds
Casper's generated UUID to `.disk/casper-uuid-generic`; Fedora binds
`dracut-live` to the pinned `Fedora-WS-Live-44` volume label and preserves the
ESP's marker-based hand-off to `/boot/grub2/grub.cfg`. Adapter-specific output
validation checks those identities, boot records, kernel, initramfs, module
tree, device trees, package ownership, manifests, and EFI locations before
descriptor-bound no-replace publication writes the manifest and journal first,
then the ISO as the final commit marker.

If publication fails, the CLI reports every recoverable transaction path and does not remove anything through a mutable pathname. Inspect the reported final and hidden staging entries before removing them; the absence of the requested ISO means the output set was not committed.

Directly written hybrid media and a nested ISO stored on an outer filesystem are different strategies. The direct ISO does not use `iso-scan/filename`; that argument belongs to the labelled outer-disk loopback workflow. All live-USB entries keep the temporary aDSP blacklist because enabling the DSP while the live root remains on USB can reset or disconnect the medium. Installed-system entries do not carry that blacklist.

The modified Ubuntu deployable root also registers the exact image and modules packages in dpkg, carries a separate non-Casper initramfs, seeds both model-specific device trees under `/boot`, and installs a bounded refresh helper plus explicit X1E and X1P GRUB entries. Ubuntu's default minimal layered installation is expected to deploy this root, so that path inherits the selected kernel support rather than depending on the live-only USB entry. The optional full-desktop upper layer carries its own package database and is not yet proven to preserve the same hand-off. The structural validator extracts and checks the default minimal-root assets; completing that installation, allowing the installer to run its target bootloader step, and booting the installed system on a Surface Pro 11 remain hardware gates.

The extracted live filesystem stays inside a named Linux Docker volume throughout the remaster. This preserves case-sensitive paths, root ownership, device nodes, and Linux extended attributes even when the host filesystem cannot represent them faithfully. The source and completed artefacts cross the host boundary; the mutable Linux filesystem does not.

Structural validation is a publication gate, not a substitute for booting the media on a Surface Pro 11. Disable Secure Boot before using the unsigned custom kernel, and treat an actual device boot as the final compatibility gate.

Compressed raw disk images use a different partition and boot model. Catalogue entries such as Fedora's `.raw.xz` image will require a separate adapter rather than being passed through the ISO remasterer.

### Prepare local image release assets

`image release prepare` first validates one completed lexr ISO, proves
that its embedded manifest bytes equal the existing adjacent
`*.iso.manifest.json`, and then produces deterministic split zstd parts,
checksums, release notes, and a path-free release manifest in a fresh local
directory. It preserves that one ISO manifest—including its
`companion_bundle` attribute—rather than introducing another image inventory.
The command never uploads files or changes a remote release.

```sh
lexr image release prepare lexr-ubuntu-sp11.iso \
  --repository-root ../lexr-build \
  --release-name <release-name> \
  --out-dir build/release/<release-name> \
  --dry-run

lexr image release validate ../lexr-build/build/release/<release-name>
```

Here `--repository-root` is the containment root that already holds the ISO
and its two adjacent sidecars; it need not be an OE checkout. The relative ISO
and output paths are resolved beneath that root. This example continues the
companion build layout above.

Remove `--dry-run` only after reviewing the source identity and fresh destination. Validation checks the closed release set and reconstructs the complete ISO identity from the ordered parts without publishing it.

## Write the validated image to USB

`image devices` is read-only and lists every whole physical device with the evidence needed to review it, including whether the disk has an active non-mount consumer. It does not present an internal, non-removable, non-USB, read-only, system-backed, in-use, weakly identified, or undersized device as an acceptable target merely because its path was supplied explicitly.

```sh
lexr image devices
lexr image write lexr-ubuntu-sp11.iso \
  --device /dev/diskX \
  --dry-run
```

Replace `/dev/diskX` with the reviewed whole-device path shown on your host. The dry run performs structural ISO validation, hashes the complete source, inspects the target, and prints an exact confirmation phrase without unmounting or writing anything. It is safe to repeat after reconnecting the device.

Run the real operation with elevated privilege and paste the exact phrase from the current plan. The phrase includes both the whole-device path and full source SHA-256; `yes`, a shortened digest, and a phrase from a different plan are rejected.

```sh
sudo lexr image write lexr-ubuntu-sp11.iso \
  --device /dev/diskX \
  --confirm 'ERASE /dev/diskX DEVICE <opaque-fingerprint> AND WRITE SHA256 <full-sha256>'
```

An interactive terminal can omit `--confirm` and type the displayed phrase at the protected prompt. Automation must pass the exact phrase explicitly. Because the phrase contains the opaque fingerprint, a confirmation obtained for a previous USB device is rejected after another device takes over the same `/dev` path. Immediately before mutation, the manager reopens and rehashes the source, re-inspects the target, compares the already-open source descriptor with target mounts, checks privilege, unmounts only approved removable-style target filesystems, and refuses to continue if any mount, host-storage classification, active storage consumer, or identity drift remains. The production raw opener rejects links and ordinary files, proves that ordinary and raw nodes address the same kernel device, opens with `O_NOFOLLOW`, and proves that its descriptor still denotes that inspected device. The manager then writes bounded chunks, flushes them, reads back exactly the source length, verifies the SHA-256, re-inspects once more, and ejects or powers off the target. A failure returns the exact not-started, prepared, writing, written, verifying, or verified receipt state, complete byte counts, and only complete digests; it never claims that writing, verification, or ejection began before the corresponding boundary was crossed.

This writer is distribution-neutral. Its pre-write router accepts the
implemented Lexr Ubuntu Casper and Fedora Live outputs only after dispatching
each image to its adapter-owned structural validator. Future Debian, elementary
OS, Pop!_OS, and raw-image adapters will retain their own image validation and
live-media contracts while reusing the removable-device manager.

## Kernel bundles

A usable kernel bundle contains, at minimum:

- one `linux-image-..._arm64.deb` package;
- one matching `linux-modules-..._arm64.deb` package;
- SHA-256 coverage for every selected package; and
- the X1E OLED and X1P LCD device trees supplied by the same modules package.

The bundle records its release, repository, ABI, version, package digests, and expected device-tree paths. `lexr` derives the ABI and version from package filenames and rejects a bundle if required packages are absent, versions are mixed, or a local file no longer matches its recorded digest. Local inspection and installation default to `--package-set all`: they add the exact ABI-specific and common development-header pair when the emitted bundle declaration includes it. A complete declaration fails closed if either header disappears; an intentional runtime-only download remains a two-package set even when its release checksum file also covers undeployed headers. Partial matching header pairs in legacy manifest-free directories are rejected, unrelated header-package versions are ignored, and `--package-set runtime` explicitly selects only image and modules. Package selection is a closed role set rather than a caller-supplied filename pattern. Headers remain optional for live-image creation.

`kernel build` owns a compiled ARM64 Docker build policy and does not require a repository helper script. By default it builds the [`sp11/integration-7.2.x`](https://github.com/ooaklee/linux_ms_dev_kit-sp11/tree/sp11/integration-7.2.x) branch of the custom kernel; `--git-url` and `--git-branch` select another HTTPS source and branch or tag. The default `--boot-image-mode source` leaves the selected source's Stubble policy unchanged. The explicit `stubble` and `nostubble` modes pass a GNU Make command-line override only to flavour packaging, without editing the managed checkout, and validate the resulting packaged boot image across both runtime packages. Stubble mode requires exactly one `.linux` section, one `.hwids` section, at least one embedded `.dtbauto` section, and an embedded Surface Pro 11 Denali device tree; nostubble mode rejects embedded device trees. The selected mode is retained in build and release provenance. The policy pins the Ubuntu 26.04 ARM64 base image by digest and records the exact fetched revision and tree, its own recipe digest, and an installed-toolchain digest beside the packages. `kernel release download` resolves candidate release assets from the established OE release channel by default and verifies the publisher checksum manifest before writing a local bundle manifest. Its explicit `--repository` option remains available for a compatible alternate release channel.

`kernel release prepare` accepts only that exact closed native build output, one or more corresponding-source archives, explicit licence text, a tag-like release identity, and a fresh output path. Its dry run hashes and validates all inputs without creating a parent or output. A real run revalidates the build, copies through private staging, produces one path-free public manifest and British-English notes, checksums the complete set, validates it, and atomically publishes the new local directory. `kernel release validate` performs the same closed-directory structural checks without contacting a remote service. Neither command publishes, installs, elevates privilege, or makes a hardware-qualified claim. The retired `sp11v3` ABI and separate out-of-tree touchscreen modules are rejected because the maintained kernel carries that stack in-tree.

`kernel preflight` is the read-only installation gate. It inspects the exact Debian package metadata, rejects unexpected packages or mixed ABIs, proves that the explicitly selected fallback ABI is the running and bootable kernel, requires a fresh target ABI, and shows the bounded package, initramfs, and GRUB command sequence. By default it includes every package declared by the bundle for the exact local ABI/version set; pass `--package-set runtime` to omit a complete matching header pair deliberately. The target root and fallback ABI are always explicit. `--running-abi` is accepted only when checking an alternate-root fixture; the live root always uses direct `uname` evidence.

`kernel install` repeats that preflight immediately before mutation, stages immutable package copies, retains the fallback kernel, backs up GRUB, and verifies the installed kernel image, initramfs, module tree, boot entry, both Surface Pro 11 device trees, and both development-header trees when selected. A real install requires effective root privilege and `--yes`; the CLI does not elevate itself, change the default kernel, remove the fallback, reboot, or install historical out-of-tree workarounds. If a mutating command or final verification fails, it attempts a bounded rollback and reports the recovery evidence in its receipt.

Review the exact transaction without privilege before installing it:

```sh
RUNNING_ABI="$(uname -r)"

lexr kernel preflight <kernel-bundle> \
  --root / \
  --fallback-abi "$RUNNING_ABI"

lexr kernel install <kernel-bundle> \
  --root / \
  --fallback-abi "$RUNNING_ABI" \
  --dry-run

sudo lexr kernel install <kernel-bundle> \
  --root / \
  --fallback-abi "$RUNNING_ABI" \
  --yes
```

Both installation commands above use the default `--package-set all`. Add
`--package-set runtime` only when the matching development headers should be
left uninstalled.

A local bundle without an authoritative `SHA256SUMS` file is rejected unless `--allow-unverified` is supplied explicitly. That switch accepts only the locally measured package bytes; it is not publisher verification and should not be used for an unknown bundle.

The build subcommand works from a standalone released CLI as well as an OE checkout. Its private transaction and new output directory must be relative to the selected containment root; the output directory must not already exist. Source data persists in a Docker volume labelled for that exact work boundary, while generated packages cross through a private host transaction. `--reset-source` may clean only that managed volume source tree. The command builds packages but never installs them, elevates privileges, reboots, or publishes a release.

Successful output contains the coherent signed image and modules pair, complete common and ABI-specific headers, `SHA256SUMS`, a normal kernel bundle manifest, and a source-provenance manifest. Review the complete non-mutating plan first:

```sh
lexr kernel build --dry-run
lexr kernel build \
  --repository-root <build-root> \
  --git-url https://github.com/ooaklee/linux_ms_dev_kit-sp11 \
  --git-branch sp11/integration-7.2.x \
  --output-dir build/lexr/kernel-v19
```

Use the explicit Stubble mode only for an SP11 build whose selected source
policy would otherwise emit a raw boot image. Omit the flag to preserve the
branch's policy for other devices:

```sh
lexr kernel build \
  --repository-root <build-root> \
  --git-url https://github.com/ooaklee/linux_ms_dev_kit-sp11 \
  --git-branch <sp11-test-branch> \
  --boot-image-mode stubble \
  --output-dir build/lexr/kernel-sp11-stubble
```

After materialising the exact recorded source revision and its licence text,
review and prepare a local release with unique absent output paths:

```sh
lexr kernel release prepare \
  --build-dir build/lexr/kernel-v19 \
  --output-dir build/release/sp11-qcom-x1e-v19 \
  --release-name sp11-qcom-x1e-v19 \
  --source build/lexr/release-source/linux-v19.tar.xz \
  --licence build/lexr/release-source/LICENSE.kernel.txt \
  --dry-run

lexr kernel release prepare \
  --build-dir build/lexr/kernel-v19 \
  --output-dir build/release/sp11-qcom-x1e-v19 \
  --release-name sp11-qcom-x1e-v19 \
  --source build/lexr/release-source/linux-v19.tar.xz \
  --licence build/lexr/release-source/LICENSE.kernel.txt

lexr kernel release validate build/release/sp11-qcom-x1e-v19
```

Preparation records the supplied source bytes; the operator remains responsible for proving that the archive corresponds to the manifest's exact revision and tree and that its licence evidence is sufficient for redistribution. Follow the OE repository's [kernel release how-to](https://github.com/ooaklee/linux-surface-pro-11-oe/blob/main/docs/how-to/how-to-release-kernel-artifacts.md) for the verified source-materialisation procedure.

## Image catalogue

`supported-isos.json` is intentionally hand-editable. Each entry has a stable ID, user-facing metadata, exact upstream filename, artefact format, HTTPS download and homepage links, support status, adapter, mutability flag, compatibility notes, and verification date. The filename must be a portable basename, must match the final URL path segment exactly, and must use the extension declared by `artifact_kind`. An optional catalogue checksum can use SHA-256 or SHA-512. Image creation currently consumes SHA-256 source digests; use `--source-sha256` when a build must enforce a digest that is not supplied by an implemented catalogue entry.

Validate edits before committing them:

```sh
lexr catalog validate supported-isos.json
go test ./internal/catalog/...
```

Validation is strict and unknown fields are rejected. Semantic problems—including duplicate or malformed IDs, unsupported architectures or formats, insecure URLs, filename/format disagreement, invalid adapter/support combinations, malformed checksums, and invalid dates—are reported together. Both `arm64` and `aarch64` input spellings normalise to `arm64`.

To add discoverable media before an adapter exists, use:

```json
{
  "id": "distribution-release-arm64",
  "name": "Distribution Release ARM64",
  "distribution": "Distribution",
  "release": "Release",
  "filename": "distribution-arm64.iso",
  "architecture": "arm64",
  "artifact_kind": "iso",
  "url": "https://example.org/distribution-arm64.iso",
  "homepage": "https://example.org/downloads/",
  "adapter": "none",
  "support_level": "catalog-only",
  "experimental": true,
  "mutable": false,
  "compatibility_notes": [
    "No lexr image adapter is implemented yet."
  ],
  "last_verified": "2026-08-30"
}
```

Mark an entry `implemented` only when its named adapter can create and validate that artefact format.

## Private Windows hand-offs

Some Surface Pro 11 platform firmware and the Bluetooth public controller address must come from an authorised Windows installation on the same device. They are private device data, not a userspace release and not an ISO companion. Do not add a collected hand-off directory, its manifest, or any of its payloads to an image, release, issue, diagnostic archive, or source checkout.

The canonical Windows collector is `tools/collect-sp11-windows-handoff.ps1` in the CLI source tree and emits one strict directory. It is also present in the companion source archive, so a user can extract that ordinary non-private script from the live medium before running it in Windows. Contract version 3 and collector `3.0.0` use a fresh random salt and a domain-separated SMBIOS UUID binding for same-device application. The raw SMBIOS UUID is never exported. A selected Bluetooth adapter instance identifier remains private in-memory collection evidence and is not exported as either raw text or a digest. Platform firmware is an all-or-absent eleven-file set with fixed destinations, copied-byte digests, and Windows DriverStore provenance; every file must come from its exact compiled original INF basename rather than a mutable `oemN.inf` alias or a filename-only match. Windows Wi-Fi firmware is deliberately excluded because Linux board firmware remains owned by the distribution firmware package.

Contract versions 1 and 2 were unpublished pre-release shapes. The current CLI does not import, list, purge or apply them, and it never silently treats their state as Lexr state. Before upgrading, use the exact predecessor binary which created any stored pre-release entry to complete its reviewed purge, then recollect with collector `3.0.0`. A transferred version 1 or version 2 source directory is not valid version 3 input and must not be reused.

### Collect on Windows

Run collection from an elevated Windows PowerShell 5.1 session. First create one new protected parent on a local fixed NTFS volume. The following locale-independent commands set the exact owner and access rules required by the collector:

```powershell
$privateParent = Join-Path $env:ProgramFiles 'lexr-private'
if ([System.IO.Directory]::Exists($privateParent) -or
    [System.IO.File]::Exists($privateParent)) {
    throw 'Choose a new private parent; do not reset an existing directory.'
}
[void][System.IO.Directory]::CreateDirectory($privateParent)

$administrators = New-Object System.Security.Principal.SecurityIdentifier('S-1-5-32-544')
$localSystem = New-Object System.Security.Principal.SecurityIdentifier('S-1-5-18')
$inheritance = [System.Security.AccessControl.InheritanceFlags]::ContainerInherit -bor `
    [System.Security.AccessControl.InheritanceFlags]::ObjectInherit
$propagation = [System.Security.AccessControl.PropagationFlags]::None
$allow = [System.Security.AccessControl.AccessControlType]::Allow
$fullControl = [System.Security.AccessControl.FileSystemRights]::FullControl

$security = New-Object System.Security.AccessControl.DirectorySecurity
$security.SetOwner($administrators)
$security.SetAccessRuleProtection($true, $false)
[void]$security.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule(
    $administrators, $fullControl, $inheritance, $propagation, $allow)))
[void]$security.AddAccessRule((New-Object System.Security.AccessControl.FileSystemAccessRule(
    $localSystem, $fullControl, $inheritance, $propagation, $allow)))
[System.IO.Directory]::SetAccessControl($privateParent, $security)
```

Use the stock `Program Files` directory as the protected parent's immediate
ancestor. Its default ACL grants unprivileged principals read and execute
access only, while the stock filesystem-root ACL grants create-directory
access on the root and makes its broader Modify entry inherit-only. Do not put
the parent beneath `ProgramData`: its stock Users rule grants write-attribute
and write-extended-attribute access to that ancestor, which the collector
deliberately rejects at this privileged boundary even when the new child has
the exact private ACL.

Choose a new child name for every collection; the requested child must not already exist. Unplug external Bluetooth radios before collecting Bluetooth evidence, then run the collector from the CLI source tree:

```powershell
$handoff = Join-Path $privateParent `
    ('sp11-handoff-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))

& powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
    .\tools\collect-sp11-windows-handoff.ps1 `
    -OutputDirectory $handoff
if ($LASTEXITCODE -ne 0) {
    throw 'Windows hand-off collection failed.'
}
```

The default Bluetooth source is the sole network-adapter `PermanentAddress` whose structured PnP ancestry reaches the exact built-in WCN7850 radio. Add `-UseBTHPORTRegistry` only when you also want the collector to require the sole valid local BTHPORT address to agree exactly with that independently correlated value. The built-in radio and transport identities are `QCA_SHB\UART_H4_HMT` and `ACPI\QCOM0D04`; attached or ambiguous physical radios fail closed.

The parent check walks from the filesystem root without following reparse points, requires trusted ownership, rejects access which could redirect the privileged path, and retains filesystem object identities across writes and publication. Staging receives the same private DACL. Publication is a no-replace same-parent move, and failure cleanup enumerates and removes only checked entries without recursive traversal through a reparse object.

Do not collect directly onto FAT, exFAT, a network share, or an unprotected directory. The implementation can accept a local removable NTFS parent only when the identical ACL, ancestor, and no-reparse policy passes, but collecting on fixed local NTFS and transferring only the completed child gives the clearest boundary. Copy that complete child to a new directory on trusted removable storage after the collector reports success:

```powershell
$transferRoot = 'E:\lexr-private-transfer'
if ([System.IO.Directory]::Exists($transferRoot) -or
    [System.IO.File]::Exists($transferRoot)) {
    throw 'Choose a new empty transfer directory.'
}
[void][System.IO.Directory]::CreateDirectory($transferRoot)
Copy-Item -LiteralPath $handoff -Destination $transferRoot -Recurse -ErrorAction Stop
```

The removable copy is private even when its filesystem cannot preserve Windows ACLs. It is a transfer copy, never the live privileged output transaction. Keep it physically controlled and remove unneeded copies after Linux import has been verified.

### Import and apply on Linux

Copy the completed hand-off directory from the private medium to the Linux system, then import it as the same unprivileged user who will manage it:

```sh
HANDOFF_STORE="${HOME}/.lexr-handoffs"
lexr handoff import <windows-handoff-directory> --store "$HANDOFF_STORE"
lexr handoff list --store "$HANDOFF_STORE"
```

`$HOME` is expanded by the unprivileged shell, so `HANDOFF_STORE` is the absolute path to that user's private store. Keep this same value for every import, inspection, application, and retention command; in particular, the shell expands it before `sudo`, preventing privileged application from selecting root's separate default store.

Import rejects unknown or mis-cased JSON fields, missing or extra files, symbolic links, special files, case-colliding paths, non-canonical mappings, digest or size mismatches, and source mutation during verification. It publishes the exact bytes atomically beneath a mode-`0700` content-addressed store and protects every stored file with mode `0600`. Re-importing identical bytes revalidates and reuses the existing entry. Ordinary and JSON output contain only redacted summary fields; they never contain the Bluetooth address, raw UUID, adapter identifier, salts, or their bindings.

Applying an imported hand-off is a separate, privileged transaction. The command revalidates the stored closed set, proves that the live SMBIOS identity at `--identity-root` matches the device-bound evidence, and prepares changes only beneath the mandatory `--target-root`. The default identity root is `/`; keep it when preparing another mounted root on the same Surface. Use `--feature firmware` or `--feature bluetooth` to select one included feature, or omit the repeatable flag to select every included feature. Firmware application also requires an explicit aDSP policy: `enabled` for an installed system whose root is on internal storage, or `disabled` for a live USB root.

Review the immutable plan as an unprivileged user before applying it. The dry run prints the exact ID-, policy-, target-, and current-state-bound confirmation phrase:

```sh
lexr handoff apply <id> \
  --store "$HANDOFF_STORE" \
  --target-root /target \
  --feature firmware \
  --feature bluetooth \
  --adsp-policy enabled \
  --dry-run

sudo lexr handoff apply <id> \
  --store "$HANDOFF_STORE" \
  --target-root /target \
  --feature firmware \
  --feature bluetooth \
  --adsp-policy enabled \
  --confirm '<exact phrase from the current dry run>'
```

For the running live system, spell the target explicitly as `--target-root /` and select `--adsp-policy disabled` when firmware is included. The transaction installs only the fixed eleven-file platform-firmware set, its Denali GPU link, the selected aDSP policy, and the private Bluetooth runtime integration represented by the imported evidence. It does not copy Windows Wi-Fi firmware, change an unselected feature, expose private values in output, or accept a confirmation generated for another plan.

Bluetooth application records the compiled selector `surface-pro-11-wcn7850-uart`, never a boot-order-dependent numeric index. At service start, the Linux helper scans `/sys/class/bluetooth/hciN/device/of_node/compatible` for the exact NUL-delimited `qcom,wcn7850-bt` token supplied by the Surface Pro 11 UART device-tree node. An external controller cannot acquire authority by appearing as `hci0`; no built-in match times out without issuing an HCI address mutation, and multiple matching candidates fail as ambiguous.

Every started mutation keeps private same-filesystem backups and a durable receipt beneath the target. A failure attempts bounded rollback but deliberately retains the receipt and backups for inspection or recovery. Restore is therefore explicit and uses its own current-state-bound confirmation:

```sh
sudo lexr handoff restore <receipt-id> \
  --target-root /target \
  --dry-run

sudo lexr handoff restore <receipt-id> \
  --target-root /target \
  --confirm '<exact phrase from the current restore dry run>'
```

Do not delete a retained receipt or its private backup directory until application, boot validation, and any necessary restoration have completed. `--json` is available for redacted automation output on both commands.

The current Lexr restore path accepts only schema-2 application receipts. Before
upgrading, use the exact predecessor binary which created a schema-1 receipt to
finish restoration. If restoration must be deferred, retain that exact binary
together with the receipt, backups and target until recovery is complete; a
schema-1 receipt cannot be recreated safely from current state.

Review retention before deleting an entry from the same explicit private store:

```sh
lexr handoff purge <id> --store "$HANDOFF_STORE" --dry-run
lexr handoff purge <id> --store "$HANDOFF_STORE" --confirm 'purge <id>'
```

Purge accepts only the complete content-addressed phrase, revalidates the private
closed set, atomically isolates that exact direct child, revalidates it again,
and removes only the verified files and directories. The current command
accepts only Lexr's version 3 store entries. Use the exact predecessor binary
which created a version 1 or version 2 entry to purge that state before
upgrading; recursive manual deletion is not a supported substitute. Purging a
current stored entry does not remove application receipts or backups from a
target root; recover or deliberately retain those records independently.

Host-independent tests do not replace maintained-hardware qualification. Successful collection on supported Windows, private transfer, same-device Linux import and application, Bluetooth address programming, firmware loading, cold boot, and restoration on the same physical Surface Pro 11 remain release gates. [ADR022](docs/adr/adr-022-privileged-windows-collection-and-controller-authority.md) records the privileged storage and controller-authority decision together with the reviewed Microsoft driver-package evidence.

## Userspace companion

The userspace companion helps answer a separate question from image creation: after installing a custom kernel, which supporting components are present, missing, obsolete, or outside the CLI's redistribution boundary?

`supported-userspace.json` is the human-readable source of audited component metadata. It records support maturity, capability, redistribution policy, evidence, remediation, exact release assets where applicable, and the bounded actions available through the CLI. Action flags are declarations only; the catalogue cannot supply commands or writable paths. The dedicated loader rejects unknown fields and inconsistent combinations.

Review and validate the catalogue with:

```sh
lexr userspace list
lexr userspace show firmware
lexr userspace catalog validate supported-userspace.json
go test ./internal/userspace/catalog/...
```

`userspace status` and `doctor userspace` use the same static inspector. They examine filesystem, package, kernel-compatibility, boot-argument, and ELF dependency state below the selected target root and do not start services, execute target binaries, contact the network, probe live devices, or modify the system. Use `--kernel` to select a Surface kernel ABI explicitly, `--feature` to limit the report, and `--json` for automation. Accepted feature names are `kernel`, `firmware`, `wifi`, `bluetooth`, `audio`, `iptsd`, `g6-pen`, `touchscreen`, `camera`, and `power`.

```sh
lexr doctor userspace
lexr userspace status --feature audio --feature iptsd
lexr userspace status --json
```

With no feature filter, only catalogue-required support blocks readiness. When a supported or experimental feature is selected explicitly, its failed checks also produce a failing exit status so scripts receive a useful result. Diagnostic-only and obsolete checks remain clearly labelled and never make the complete default report claim that those components are required.

An alternate or mounted target root must be trusted and quiescent while it is inspected. The doctor resolves symbolic links and confines its static reads at each check, but its report is a point-in-time diagnosis rather than a sandbox for a concurrently hostile filesystem.

### Pull, build, and install

`userspace pull` accepts one supported component or `recommended`. A pull succeeds only when the remote release contains the exact audited asset set, the checksum manifest matches its release digest, and every installable payload is covered by `SHA256SUMS` with matching release and publisher digests. Verified assets and a bundle manifest are published atomically to the local cache.

The recommended set deliberately contains the supported audio release and pinned `iptsd` integration. The camera package set is experimental and remains an explicit opt-in. Restricted platform firmware is never downloaded by `lexr`; acquire it from an authorised source and use the status report to verify its presence.

```sh
lexr userspace pull recommended
lexr userspace pull camera
lexr userspace build iptsd
lexr userspace build camera
```

Source builds invoke only compiled, component-specific adapters with bounded arguments. Catalogue content is never interpreted as a shell command. The current camera package build requires a native ARM64 Linux host; users on other hosts can still pull and verify the published experimental package set.

Both userspace source-build adapters use compiled Go policy and do not invoke repository scripts. They require the complete OE checkout because the native builders authenticate their tracked component inputs; pass `--repository-root <oe-checkout>` when it cannot be detected from the current directory. Pull, status, doctor, and installation from a downloaded immutable release do not have that checkout requirement. A successful native camera build prints an independent authority SHA-256 for its final receipt. Retain it separately from the directory. Installing a native camera build or prepared local camera release repeats static Git-backed provenance proof and therefore requires both the explicit repository root and the corresponding independently retained authority digest.

Review an install before granting elevated access. A real install requires both effective root privileges and `--yes`; the CLI does not elevate itself. `--root` may select an alternate target filesystem where the component supports it.

```sh
lexr userspace install recommended --from <userspace-cache> --dry-run
sudo lexr userspace install recommended --from <userspace-cache> --yes

lexr userspace install camera \
  --from <native-camera-build-or-local-release> \
  --repository-root <oe-checkout> \
  --camera-authority-sha256 <matching-build-or-release-authority-sha256> \
  --dry-run
sudo lexr userspace install camera \
  --from <native-camera-build-or-local-release> \
  --repository-root <oe-checkout> \
  --camera-authority-sha256 <matching-build-or-release-authority-sha256> \
  --yes
```

The camera installer also retains compatibility with the downloaded, checksum-pinned camera release and does not accept the native-only repository or authority options for that input shape. Native camera input is selected only by its structured build or local-release authority; mixed authority files are rejected. Static validation never executes a supplied package member. The installer verifies every package again while privately staging the confirmed `apt-get` transaction. Installing support never invokes legacy clean-up implicitly. Use the separate `clean` commands to inspect and remove only recognised obsolete workarounds with backups and receipts.

### Prepare local userspace releases

Release commands prepare and validate closed local directories; they never create a Git tag, upload an artefact, or change a remote service. FullIO audio preparation accepts only the reviewed v19c source bytes and an explicit paired kernel tag and ABI. Camera preparation accepts only one validated native camera build, its authenticated inputs, and an explicit paired kernel tag and ABI.

```sh
lexr userspace audio release prepare \
  --source-root <SP11X1e-audio-checkout> \
  --repository-root <oe-checkout> \
  --tag sp11-audio-v19c \
  --kernel-tag <kernel-release-tag> \
  --kernel-abi <kernel-abi> \
  --dry-run

lexr userspace camera release prepare \
  --from <native-camera-build> \
  --repository-root <oe-checkout> \
  --tag <camera-release-tag> \
  --kernel-tag <kernel-release-tag> \
  --kernel-abi <kernel-abi> \
  --build-authority-sha256 <native-build-authority-sha256> \
  --dry-run
```

Remove `--dry-run` only after reviewing the complete plan. Successful camera preparation prints a release authority SHA-256; retain it separately, then pass it to `camera release validate --authority-sha256` against the newly prepared directory. The resulting manifests make provenance and kernel pairing explicit without claiming hardware qualification.

### Inspect the experimental camera

`userspace camera capture` discovers the exact supported IMX681 media route, can validate it without changing the graph, and otherwise captures complete private 3840×2640 packed-RAW10 frames behind bounded transport, content, temporal, and kernel-error gates. It does not claim that the privacy LED or a cold-boot lifecycle passed; those remain manual hardware gates.

```sh
lexr userspace camera capture --dry-run
lexr userspace camera capture --output capture.raw --frames 10
lexr userspace camera render capture.raw preview.png
```

Capture output and rendered previews may contain sensitive imagery. New files are private on Unix hosts; keep them out of source control, releases, issue attachments, and diagnostics unless their contents have been reviewed deliberately.

## Reversible clean-up

Clean-up is deliberately separate from image creation. Start with a read-only scan, then write the exact JSON plan you intend to apply:

```sh
lexr clean scan
lexr clean plan --output lexr-cleanup-plan.json
cat lexr-cleanup-plan.json
sudo lexr clean apply \
  --root / \
  --plan lexr-cleanup-plan.json \
  --yes
```

The scanner considers only a fixed set of known legacy paths. Required content markers must match before a regular file is considered recognised. A known service-enablement link must resolve to the exact retired unit. Other links, unusual files, changed content, and entries created after planning are left for manual review.

Applying a plan requires `--yes`. Before the first original path changes, the CLI writes and flushes a prepared recovery receipt below `/var/lib/lexr/backups`. Each reviewed entry is then atomically moved into a private same-filesystem quarantine, verified again, copied into its durable backup, and removed from quarantine. A completed receipt is published only after every entry succeeds. If the operation is interrupted, `receipt.pending.json` maps any original, quarantine, and backup locations.

Restore a prepared or completed transaction with the receipt path printed by `clean apply` or included in an interruption error:

```sh
sudo lexr clean restore \
  /var/lib/lexr/backups/<transaction>/receipt.json \
  --root / \
  --yes
```

Restoration verifies regular-file digests or exact symbolic-link text and refuses to overwrite locally changed content. Recovery copies remain available after a successful restore.

Current `clean restore` accepts only receipts and quarantine names created in
Lexr's recovery hierarchy. Before upgrading, restore any predecessor clean-up
transaction with the exact binary which created it. If recovery must be
deferred, retain that binary together with its receipt, backups, quarantine
content and target; do not rename those paths into the Lexr hierarchy.

The allow-list currently covers selected system-wide audio routing helpers, in-tree touchscreen configuration hooks, and G6 service enablement. It does not automatically remove arbitrary out-of-tree modules, rebuild contaminated historical initramfs images, delete per-user configuration, or remove unfamiliar UCM data. Those findings require explicit manual diagnosis. A future kernel change can also make a workaround relevant again, so removal remains an operator decision.

## Architecture

The executable and Bubble Tea UI are delivery layers. Feature packages own image catalogue, kernel, image, removable media, private hand-off, userspace, doctor, and clean-up behaviour; orchestration managers compose simpler services. Docker and process execution are isolated behind platform interfaces. Interactive and scriptable image entry points share the same manager and operation plan; other commands use feature-specific dry runs, verified bundle manifests, content-addressed private stores, status reports, or recovery receipts where those records fit their workflow.

Architecture decisions are recorded in [`docs/adr`](docs/adr/).

## Licence and contributions

Lexr is available under the [`Apache-2.0` project licence](LICENSE). The
project attribution is in [`NOTICE`](NOTICE), and the complete audited
dependency inventory and applicable terms are in
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).

If you would like to help, start with the human-sized guide in
[`CONTRIBUTING.md`](CONTRIBUTING.md) and please follow the
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md). The current maintainer is listed in
[`MAINTAINERS`](MAINTAINERS).

## Development

```sh
go fmt ./...
go test -race ./...
go vet ./...
go run ./cmd/lexr-build
```

Do not commit downloaded images, kernel packages, build workspaces, generated
output ISOs, private diagnostics, or captured device data. See
[`CONTRIBUTING.md`](CONTRIBUTING.md) for the full development and privacy
guidance.
