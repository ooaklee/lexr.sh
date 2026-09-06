# Pin an Arch Linux ARM source

The Arch Linux ARM entry records an authenticated root filesystem snapshot.
It is currently **catalogue-only**: `image create` does not yet turn it into
bootable installation media. Follow [issue #52](https://github.com/ooaklee/lexr.sh/issues/52)
for the live-image adapter and GRUB installation alongside existing OSes.

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
hardware test. GRUB, a live filesystem discovery
mechanism, desktop/installer packages and the custom-kernel installation
lifecycle still need to be prepared and tested. The kernel alone does not
supply firmware files.

The [Arch implementation status](../developer-guide/arch-linux-arm.md) records
the signed GRUB/live-hook package audit and the separate boot configurations
being tested with v23. Those bootstrap checks do not enable image creation.

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

Keep the versions, signatures and digests of packages added through pacman in
the future image build's provenance. A fixed rootfs with an unconstrained
`pacman -Syu` does not describe a reproducible final package set.

See [catalogue maintenance](../developer-guide/catalogues.md) for schema and
support-state rules. This source audit establishes no Surface live boot,
installation or recovery qualification.
