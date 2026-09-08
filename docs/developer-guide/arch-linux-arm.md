# Arch Linux ARM implementation status

Arch work is tracked in [issue #52](https://github.com/ooaklee/lexr.sh/issues/52).
The experimental adapter builds a terminal live ISO from a signed, pinned Arch
Linux ARM rootfs and a fixed package set. It uses native pacman registration,
separate mkinitcpio configurations and direct ARM64 GRUB. No desktop is selected.
The X1E/OLED live terminal and Wi-Fi have been tested on a Surface Pro 11.
On 2026-09-08, the maintainer also confirmed the
[guided installer](../operator-manual/arch-linux-arm-install.md#hardware-test-record)
completed and booted the internal ext4 root with v23, working touchscreen input
and Wi-Fi after reconnecting.

## Reuse and distribution boundaries

The [source intake](../operator-manual/arch-linux-arm-source.md) authenticates a
dated AArch64 root filesystem snapshot from Arch Linux ARM's rolling download.
It is not an ISO. Ubuntu and elementary supply useful Surface lessons: preserve
the coherent kernel and DTBs, copy QRTR transport and panel dependencies into
early userspace, stage public GPU and Wi-Fi firmware before probing, and retain
Lexr with its source and notices. Arch needs its own pacman and mkinitcpio
integration; Casper boot arguments and Debian package registration do not apply.

The bootstrap uses the distribution's signed `mkinitcpio-archiso` package for
read-only squashfs plus a writable RAM overlay. Its runtime uses BusyBox and
udev. Lexr supplies separate live and installed mkinitcpio configurations,
without build-host autodetection or forced Surface module loading. Only the
live configuration includes `archiso`. The root archive's original systemd
initramfs configuration cannot run this BusyBox live hook unchanged.

The v23 Wi-Fi driver is `ath12k`, including its PCI transport. Lexr also copies
`ath12k_pci` when another selected bundle exposes that separate module. The
hook explicitly requests the WCN7850 `amss.bin`, `m3.bin`, `board-2.bin` and
derived `board.bin`, plus the two `gen70500` GPU files. During the bootstrap
test, mkinitcpio 41-4's handling of the driver's firmware wildcard copied only
one matching file; module inclusion alone did not carry the complete Wi-Fi
firmware set. Missing required files now fail generation.

## Authenticated package evidence

The following package archives were downloaded from Arch Linux ARM's California
HTTPS mirror and authenticated with its independently pinned signing key before
inspection. Native pacman also verified the three bootstrap packages when
installing them into the isolated test root.

| Package | Version | Archive SHA-256 | Role |
| --- | --- | --- | --- |
| `grub` | `2:2.14-1.1` | `cf8470867afde66260a1e59d32ea186cd3493513c785a18a3b0f8b8aacab269c` | Bootstrap |
| `mkinitcpio-archiso` | `73-1` | `d0d933fd5815c4a6e48d210f5a489e76d5a8391c0c00ea447de1615cc4de42eb` | Bootstrap |
| `linux-firmware-qcom` | `20260810-2` | `5cf7ac0a7150f6674ba2b9d041693a3c12d1ba9fd0b83abf2b2331ea6da543ed` | Bootstrap |
| `archinstall` | `4.4-1` | `c92806ea459cf705e4b06f2fcbc1b3bf3f71ecb6e70b9c0ed0da735538b87a88` | Guided installer |
| `arch-install-scripts` | `31-2` | `9f346dfa37925779f228855ef05742749ffdb0753be4c43ad87b2659576a46de` | Terminal live image |

The complete terminal package set is recorded in
`internal/image/archlinux/packages.lock.json`. Each archive was checked against
its repository SHA-256 and independently verified with the Arch Linux ARM
signing fingerprint before intake. The builder verifies the locked digest,
size and detached signature again. An unavailable pinned version is an error;
a newer package is not substituted. Mirror repository metadata is not treated
as a signed authority. Native package licences remain in the filesystem.

The root is extracted with libarchive ownership, ACL and xattr preservation in
a Linux filesystem. Build chroots have no network or host disks. The coherent
Debian kernel payload is repackaged as `lexr-kernel-sp11` and registered by native
pacman, replacing the generic kernel only inside this disposable build root.
Its scriptlet rebuilds the installed-system initramfs, without changing GRUB or
NVRAM. A cross-ABI upgrade is deliberately unsupported until retention is ready.

The terminal account is `arch`, with tty1 autologin and passwordless sudo.
NetworkManager is enabled; SSH is disabled, stock credentials are removed and
private signing keys, machine IDs and host keys are not shipped. The setup menu
provides networking, inspection, the guide and a guided installation action.

## GRUB and installed-system direction

The inspected Arch Linux ARM GRUB package contains `arm64-efi` modules. The
packaged archinstall 4.4-1 derives its UEFI target from `platform.machine()`,
which gives `aarch64-efi` on this platform. It also lacks Lexr's exact kernel/DTB
and shared-ESP policy. Using that installer unmodified is not the installation
hand-off selected here.

The live GRUB renderer provides explicit X1E/OLED and X1P/LCD entries plus text
and firmware-display diagnostics. Kernel, initramfs and DTB paths contain the
same ABI. A generated image identity binds GRUB's marker and archiso's volume
label. `copytoram=n` retains the medium at `/run/archiso/bootmnt` for the
companion; `checksum=y` requires `airootfs.sha512` to be calculated after the
final squashfs is closed. This checksum detects corruption, not publisher
authenticity. `BOOTAA64.EFI` belongs to a newly created ISO ESP.

The installed boot renderer uses the ext4 root's UUID and actual Surface variant.
`/boot` stays on that root; the FAT ESP is mounted at `/boot/efi`. The external
Python adapter registers Archinstall's `on_pacstrap`, `on_mkinitcpio`,
`on_add_bootloader` and `on_genfstab` callbacks. Kernel and ARM mirror fields
are fixed; graphics use Mesa and the Adreno Vulkan provider. The normal GRUB
menu remains available. No desktop is preselected. The upstream package files,
accounts, profile and network handlers remain unmodified. A module-local
constant adaptation makes the existing formatter assign the ARM64 root type;
it does not replace partitioning or formatting code.

A small compatibility check runs in the Install preview and guided saved-config
path before formatting. It rejects boot layouts the Surface payload cannot use:
non-ext4 root, a separate `/boot`, an ESP elsewhere, encryption, LVM, UKI and
other bootloaders. It does not inspect partition geometry, compare GPT snapshots
or prohibit formatting selected by the user. Archinstall owns those actions
and their confirmation. Alongside-install instructions explain preservation of
the existing ESP and other OS partitions.

The same check validates effective ARM repositories and resolves selected
profile/additional packages, including dependencies and groups, before formatting.
Every later pacstrap request checks package architecture and rejects unsupported
PC platform packages too. Application/network/greeter recipes stay upstream;
their later transactions and network downloads may still fail. External profile
code is rejected before upstream imports it. Custom commands and replacement
scripts/plugins are outside this reviewed flow. See
[ADR035](../adr/adr-035-arch-guided-surface-installation.md) for the pinned API
adaptations and the limits of the package checks.

The package callback removes the local kernel placeholder from repository
requests and retains the ARM signing keyring. The first initramfs callback
installs the digest-verified kernel archive and creates its installed initramfs;
later callbacks rebuild the `lexr-sp11` preset. The local unsigned transaction
uses a temporary repository-free configuration, preserving normal package trust.
The platform helper has no Archinstall imports and consumes the Go-rendered
GRUB templates. Live accounts and login policy are never copied. Networking and
profiles use upstream handlers; users reconnect with NetworkManager after boot.

GRUB uses `arm64-efi`, `EFI/LexrArch` and `--no-nvram`. The installer then uses
`efibootmgr --create-only` and verifies that the original BootOrder and EFI files
are unchanged during that hook. This does not cover formatting selected earlier in
Archinstall. It prints the new entry and an optional one-time BootNext command.
Kernel, initramfs and DTBs remain on the root filesystem. An existing conflicting
Lexr installation is refused rather than overwritten. The first installed boot
has been confirmed on X1E/OLED; repeated boot selection and recovery still need
hardware testing.

The image retains a fixed installer payload manifest, the native kernel package,
public firmware, exact kernel/DTB identities and GRUB templates. The independent
ISO validator reads these as data and compares the retained root copies and
compiled adapter code. The installer rechecks them before target writes and
verifies kernel, DTBs, initramfs, GRUB, firmware variables and fstab before the
upstream completion dialog.

## Validation and remaining work

Unit tests execute the generated hook/configuration with substitutes for
mkinitcpio's build API, reject unsafe identifiers, and check that installed boot
has no live-media arguments. Bootstrap validation additionally runs the real
Arch ARM64 mkinitcpio and GRUB against the coherent v23 payload in an isolated
Linux filesystem. It checks both generated initrds, required module objects and
firmware, live-hook separation, GRUB syntax and the EFI machine type. Container
results do not establish Surface boot or installation success.

A native ARM64 test also runs the pinned guided installation stages against a
fresh ext4 root in an isolated GPT loop image. Real pacstrap, local kernel
installation, mkinitcpio, GRUB, user creation, NetworkManager and fstab generation
complete, with the existing ESP sentinel file and all partition records
preserved. Firmware variables are simulated; the container supplies test udev
records and uses ordinary arch-chroot because its PID 1 is not systemd. Those
test accommodations are not part of the live-image installer.

The independent validator reads a private completed-ISO snapshot with trusted
container tools and never chroots into the inspected image. It checks the actual
GPT/El Torito extent, EFI machine type and embedded GRUB bootstrap, media label,
final squashfs checksum, exact kernel/module/DTB bytes, early dependency closures,
firmware, terminal configuration, native pacman inventory and retained companion.

The maintainer's physical test confirms the internal ext4 root, the running v23
kernel and native `lexr-kernel-sp11` package, with touchscreen input and Wi-Fi.
The [hardware test record](../operator-manual/arch-linux-arm-install.md#hardware-test-record)
binds that result to candidate 4 from `2e0d384`. This is distinct from the
container checks above. Repeated boot selection, returning to the other installed
operating systems, audio, broader desktop behaviour, recovery and X1P/LCD remain
untested after this installation. Cross-ABI kernel updates and rollback remain
separate qualification steps.

The first KDE session used a minimal desktop selection: Konsole, Dolphin and
`plasma-nm` were missing. Installing the requested AArch64 packages restored the
terminal and registered the file manager and network widget. These are documented
as user-selected packages, without adding another desktop recipe to the wrapper.
The same session used `llvmpipe` because the private Surface GPU firmware was
absent; accelerated graphics remain separate from first-boot qualification.

A manual installation guide is a possible second path, tracked with the Arch
work in #52. It must use Arch Linux ARM repositories and the same verified
Surface kernel, DTBs, installed initramfs and separate GRUB entry. The current
platform helper is independent of Archinstall, but a standalone manual workflow
has not been documented or qualified. Generic x86 installation instructions and
an in-place ARM first-boot wizard are not drop-in replacements for this flow.
