# Arch Linux ARM implementation status

Arch work is tracked in [issue #52](https://github.com/ooaklee/lexr.sh/issues/52).
The source catalogue and boot configuration foundation are implemented. The
complete image builder, installer and independent image validator remain in
development; the catalogue entry deliberately rejects `image create`.

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
| `archinstall` | `4.4-1` | `c92806ea459cf705e4b06f2fcbc1b3bf3f71ecb6e70b9c0ed0da735538b87a88` | Inspection only |
| `arch-install-scripts` | `31-2` | `9f346dfa37925779f228855ef05742749ffdb0753be4c43ad87b2659576a46de` | Inspection only |

These records describe the investigated inputs, not a complete package lock for
a distributable desktop image. Additional packages require the same version,
digest, signature and retention evidence. Keep native package licences and
corresponding source obligations with published images; third-party hook code
is supplied by its package, not copied into Lexr's Go source.

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

The initial installed boot renderer is limited to a selected ext4 filesystem
UUID and Surface variant. `/boot` belongs on that root filesystem. The intended
installer will require explicitly selected, revalidated root and ESP identities,
preserve existing partitions and EFI directories, and refuse an occupied target
root or an existing conflicting Lexr bootloader directory. It will not format
the shared ESP or write its removable-media fallback path.

Installed GRUB is intended for `EFI/LexrArch` with target `arm64-efi`. Merely
using `--no-nvram` does not make a new loader reachable. The installer still
needs a tested hand-off: an entry created without changing the existing
BootOrder, followed by an explicit boot selection, or a chainloader entry in
the user's existing GRUB. Surface firmware behaviour must be qualified before
promising either route. Kernels, initrds and DTBs stay off the shared ESP;
space checks must cover the actual staged files on both filesystems.

## Validation and remaining work

Unit tests execute the generated hook/configuration with substitutes for
mkinitcpio's build API, reject unsafe identifiers, and check that installed boot
has no live-media arguments. Bootstrap validation additionally runs the real
Arch ARM64 mkinitcpio and GRUB against the coherent v23 payload in an isolated
Linux filesystem. It checks both generated initrds, required module objects and
firmware, live-hook separation, GRUB syntax and the EFI machine type. Container
results do not establish Surface boot or installation success.

Before making the catalogue entry usable, finish the builder and independent
validator, preserve archive ownership/capabilities, register the kernel with
pacman, implement updates with a retained verified ABI for recovery, assemble
the desktop and companion, and implement the reviewed installation hand-off.
Then create the ISO with Lexr, validate and write/read back the USB with Lexr,
and qualify live boot and installation alongside existing OSes on hardware.
