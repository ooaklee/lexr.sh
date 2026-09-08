# Pin an Arch Linux ARM source

The Arch Linux ARM entry records an authenticated root filesystem snapshot.
The experimental Arch adapter turns it into a **terminal live ISO** with the
custom Surface kernel, public Wi-Fi firmware, networking tools and optional
Lexr companion. No desktop is preselected. Physical boot qualification and the
reviewed installation flow alongside existing OSes remain tracked in
[issue #52](https://github.com/ooaklee/lexr.sh/issues/52).

## Create and boot the terminal image

Use a local coherent kernel bundle and select the Surface variant explicitly:

```sh
lexr image create \
  --catalog-id arch-linux-arm-aarch64-20260805 \
  --source ./ArchLinuxARM-aarch64-latest.tar.gz \
  --kernel-dir ./kernel-v23 \
  --kernel-profile surface-pro-11-x1e-oled \
  --companion-source-dir ./lexr.sh \
  --output ./lexr-arch-terminal-v23.iso

lexr image validate ./lexr-arch-terminal-v23.iso
```

Docker and the host Go toolchain are needed when building with the companion.
The source tree must match the Lexr binary's clean revision. Image creation
installs a fixed package snapshot in an offline build container and publishes
only after independent validation. Retain the source and package cache if you
need to rebuild after the rolling mirror removes those versions.

Write the validated output using Lexr's [USB workflow](../user-guide/installation-media.md).
Disable Secure Boot for the unsigned custom kernel and external DTB, then
select the GRUB entry matching your X1E/OLED or X1P/LCD device. The local live
account `arch` has passwordless sudo. From the terminal run:

```sh
lexr-arch-setup
```

The menu offers hardware checks, NetworkManager's `nmtui`, package signing key
initialisation, the getting-started guide, the bundled Lexr and guided installation.
Newer builds also include optional registration with an existing GRUB menu.
Exit to the shell
to customise your experience. The guide is also in the live account's home
folder. Changes are in a temporary RAM overlay and disappear after reboot.

For installation alongside existing operating systems, follow the
[Arch installation walkthrough](../user-guide/arch-linux-arm-quickstart.md).
The bundled `sudo archinstall` flow supplies the Surface kernel and ARM64 boot
integration while retaining upstream's disk and account choices. No desktop is
preselected. The [hardware test record](arch-linux-arm-install.md#hardware-test-record)
records the first installed X1E/OLED boot and the remaining checks, including
repeated boot selection and kernel upgrades retaining a previous verified ABI.

## Inspect the accepted snapshot

```sh
lexr catalog show arch-linux-arm-aarch64-20260805
```

The [upstream downloads page](https://archlinuxarm.org/about/downloads) describes
`ArchLinuxARM-aarch64-latest.tar.gz` as the ARMv8 AArch64 multi-platform root
filesystem. The source URL is rolling, so `mutable: true` describes its delivery
location. The dated catalogue ID and SHA-256 describe the fixed bytes accepted
by Lexr; they do not follow future contents of `latest`.

The snapshot accepted on 6 September 2026 has:

| Evidence | Value |
| --- | --- |
| Source timestamp and signature date | 5 August 2026 |
| Size | 829367415 bytes |
| Archive SHA-256 | `42a4eeaa038994ffd31fa173256ef2f0ef511358eeb41b9ea1f8626391b9b319` |
| Detached signature SHA-256 | `0157d8cd6261c85205931c766b754d6d56112b28800666fb64add1de192ebe11` |
| Verified signing fingerprint | `68B3537F39A313B3E574D06777193F152BDBE6A6` |

The archive and its `.sig` were fetched from the official redirect's
[California mirror over HTTPS](https://ca.us.mirror.archlinuxarm.org/os/ArchLinuxARM-aarch64-latest.tar.gz).
The generic `os.archlinuxarm.org` HTTPS hostname failed certificate verification
during intake; certificate checking was retained and the working mirror was
selected explicitly. The fingerprint was checked against the official
[package-signing page](https://archlinuxarm.org/about/package-signing).
The [upstream keyring](https://github.com/archlinuxarm/archlinuxarm-keyring)
used for signature checking had SHA-256
`50a08f82ce3cd524a552da4bfa37f3e04b2c9da468e2fce5783474b58a34c518`.
The maintainer recorded the archive's SHA-256 after verifying the signature;
this is not a publisher-provided SHA-256 sidecar.

The inspected archive identifies itself as `archarm`, includes AArch64 ELF
executables, mkinitcpio 41-4, pacman and 165 installed package records. Its
WCN7850 Wi-Fi firmware database is present, and Lexr's existing SP11 parser
successfully extracted its board record. This is source evidence, not a Wi-Fi
hardware test. The adapter adds GRUB, live filesystem discovery and the locked
terminal packages. The kernel alone does not supply firmware files.

The [Arch implementation status](../developer-guide/arch-linux-arm.md) records
the signed GRUB/live-hook package audit and the separate boot configurations
used with v23, plus the completed-image checks required before publication.

## Verify a retained copy

For this exact accepted snapshot, download it into a separate source directory
and compare the complete bytes with the catalogue pin:

```sh
curl --proto '=https' --proto-redir '=https' -fL \
  https://ca.us.mirror.archlinuxarm.org/os/ArchLinuxARM-aarch64-latest.tar.gz \
  -o ArchLinuxARM-aarch64-latest.tar.gz
printf '%s  %s\n' \
  42a4eeaa038994ffd31fa173256ef2f0ef511358eeb41b9ea1f8626391b9b319 \
  ArchLinuxARM-aarch64-latest.tar.gz | shasum -a 256 -c -
```

If this fails because upstream has replaced `latest`, use a previously retained
copy matching the pin. Do not substitute the new hash into the old entry.
No durable public mirror of this accepted snapshot is promised yet; retaining
the verified bytes is necessary for rebuilding after upstream changes.

## Accept a later snapshot

Download the rootfs and detached signature as a candidate pair. Verify the
signature against the independently checked publisher fingerprint before
extracting or executing the archive. Reject mismatched archive/signature pairs,
unexpected signers and failed signatures. MD5 from the download page is not the
acceptance authority.

Then inspect its architecture, package inventory, firmware and boot layout;
record SHA-256, size, signature evidence and the actual snapshot date in a new
reviewed entry. A valid signature establishes publisher origin, not compatibility
with the current Lexr adapter or proof that a rolling mirror is fresh.

The source resolver separates pinned downloads by digest and preserves their
file format. A refresh downloads into a private candidate directory and only
replaces the selected cache entry after verification succeeds. A failed refresh
returns the failure and retains the older bytes; it does not silently fall back
or update the expected hash. Existing older ISO/raw cache files are copied to
the digest-specific location only when they match the selected pin, allowing
offline reuse. Their original files are retained for older Lexr versions.

The adapter embeds a reviewed package lock with versions, SHA-256, sizes and
detached signatures. It uses native pacman to verify those local signed archives
in the build container. It does not query rolling repositories during assembly.

See [catalogue maintenance](../developer-guide/catalogues.md) for schema and
support-state rules. This source audit establishes no Surface live boot,
installation or recovery qualification.
