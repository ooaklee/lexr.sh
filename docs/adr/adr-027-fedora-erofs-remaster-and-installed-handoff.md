---
id: adrs-adr027
title: "ADR027: Fedora EROFS remaster and installed-system hand-off"
description: Architecture decision for Fedora Live remastering, Stubble boot integration, and the Anaconda kernel hand-off.
---

## Status

Accepted on 2026-08-31.

## Context

Fedora Workstation Live 44 for ARM64 is a directly writable hybrid ISO. Its
live root is LZMA-compressed EROFS at `/LiveOS/squashfs.img`, despite that
historical filename, and its boot layout includes GPT metadata, an appended EFI
System Partition, and marker-based GRUB indirection. Replacing only the visible
kernel file would leave the live initramfs, filesystem package database, and
installed-system hand-off inconsistent.

The Surface Pro 11 kernel is distributed as a verified Debian image/modules
pair. Fedora and Anaconda need native RPM ownership and Fedora kernel
capabilities rather than an unpacked foreign package. Anaconda's live payload
copies the selected root and discovers installable kernels through
`/boot/vmlinuz-*`; it then calls `kernel-install` using the matching module-tree
boot image.

The custom boot image is a Stubble PE executable with embedded kernel and X1E
auto-DTB data. Its `.osrel` section makes systemd classify it as a unified
kernel image even though Fedora must keep the initramfs separate. X1P Stubble
auto-DTB identity is not yet qualified. The unsigned image also cannot satisfy
Secure Boot.

Fedora live media needs `dracut-live` and a temporary
`qcom_q6v5_pas` blacklist to prevent the USB-backed root from being disrupted.
That blacklist is harmful after installation to internal storage, where the
audio DSP is expected to run, and Anaconda can retain a live-session denylist.

The optional `sp11-iptsd-v1` companion is a source-required release. Its
verified archive contains the complete pinned upstream source, Meson fallback
sources, licences, provenance, and the maintained Surface lifecycle templates.
Copying its Ubuntu-built portable executables into Fedora would bypass native
package ownership and could leave a duplicate `/usr/local` installation.

## Decision

The `fedora-live` adapter will accept the pinned Fedora Workstation Live 44 ISO
and a verified image/modules bundle from an explicitly qualified patch line:
7.2.0/sp11v19+ or 7.2.2/sp11v1+. It will preserve the
source hybrid GPT, El Torito, and appended-ESP layout through `xorriso` boot
replay. The ISO and appended ESP will retain Fedora's marker-based GRUB stub,
while `/boot/grub2/grub.cfg` will own the custom X1E entry and stock
kernel/initramfs fallback entries that explicitly load the manifest-bound X1E
or X1P Surface device tree.

The adapter will extract the EROFS live root into a Linux-native Docker volume,
apply Fedora's SELinux file contexts, and recreate it with LZMA compression and
extended attributes intact. Its tool image pins the Fedora base digest and the
exact `erofs-utils` release whose `--path=/` traversal avoids the known packed
fragment prepass failure. The pinned ISO volume label will remain the
`dracut-live` discovery authority.

The adapter will build one native `lexr-kernel-sp11` RPM from the exact
digest-verified Debian payload. That RPM will own the custom boot image, module
tree, device trees, and bounded installed-system integration. It will provide
`kernel`, unversioned `kernel-uname-r`, `kernel-core-uname-r`, and
`kernel-modules-core-uname-r`, plus `installonlypkg(kernel)`, so Fedora's
package and kernel lifecycle recognise it. RPM 6 cannot encode the kernel's
multi-hyphen uname as a valid versioned EVR. Exact ABI identity is therefore
enforced by owned paths, module vermagic, payload digests, and the image
manifest rather than by a lossy versioned capability.

When, and only when, image creation requests both the companion source and
`--companion-userspace iptsd`, the adapter will securely extract and revalidate
the compiled-trust-bound `sp11-iptsd-v1` archive. It will rebuild IPTSD and all
Meson fallbacks in the pinned Fedora AArch64 tool image, generate exactly one
binary `lexr-sp11-iptsd` RPM and one corresponding source RPM, and require the
generic `iptsd` and `g6-pen` packages to be absent rather than silently removing
them. The binary RPM will be injected into the offline live root without
running live-service scriptlets, but with RPM dependency checking against the
deployable root;
its udev rule will enumerate the digitiser when the remastered system boots.
The package owns Fedora-native `/usr/libexec`, systemd, udev, configuration,
documentation, and licence paths. Its future install/upgrade scriptlet reloads
rules and retriggers only already-bound `045E:0C80` or `045E:0C83` hidraw
parents. The binary and source RPMs, plus the original complete companion
release, will all remain independently digest- and size-bound on the ISO.

The deployable root will expose only the custom `/boot/vmlinuz-<abi>` candidate
to Anaconda. The corresponding
`/usr/lib/modules/<abi>/vmlinuz-dtbloader.efi` will contain the same Stubble
bytes, with `/usr/lib/modules/<abi>/vmlinuz` pointing to it. The stock Fedora
kernel and initramfs will remain digest- and size-bound by the embedded
manifest. Validation requires the fallback PE's `.linux` and `.uname` sections
to match the RPM-owned module-side PE and requires its initramfs to contain
`dmsquash-live` plus that exact ABI's modules. The stock boot set remains on the
outer ISO as a troubleshooting fallback rather than competing in Anaconda's
installed-kernel selection. Once the X1E installed system has booted the custom
kernel, the non-live finalizer will restore every
package-owned stock `/boot/vmlinuz-*` from its module-side PE, explicitly
rebuild an installed-system initramfs for that stock ABI before adding its
normal BLS entry, copy the exact X1E DTB to `/boot/dtb-<stock-abi>`, require
that BLS entry to name it, and retain the custom kernel as the saved default.

`/etc/kernel/install.conf` will set `layout=other`. This overrides the false UKI
classification caused by Stubble's `.osrel` section and selects Fedora's
`20-grub.install` path, which generates the normal BLS entry around the
separate exact-ABI initramfs already produced by Anaconda or the RPM scriptlet.
The live initramfs will be non-host-only, include `dmsquash-live`, and contain
the custom modules.

Every live GRUB entry will carry both `modprobe.blacklist=qcom_q6v5_pas` and
`rd.driver.blacklist=qcom_q6v5_pas`. Installed GRUB defaults will omit both.
A one-shot service, gated to a non-live boot, will remove Anaconda's live
denylist, remove either blacklist argument from the selected kernel,
regenerate dependency and initramfs state, restore the installed stock
fallback, and reset the custom kernel as the default. Secure Boot must remain
disabled for the unsigned custom kernel.

The structural validator will inspect each boundary independently:

- the outer boot media: hybrid records, both ESP views, the AArch64 shim and
  GRUB binary, GRUB's FDT command support, the search marker, and the pinned
  volume label;
- the remastered root and kernel hand-off: EROFS compression, SELinux
  attributes, manifest-bound native RPM bytes, RPM ownership and provides,
  exact Anaconda-visible kernel paths, the exact X1E Stubble `.dtbauto`
  payload, both stock external-DTB live entries, `dracut-live` contents, and
  the live-versus-installed blacklist split;
- an included native IPTSD package: both RPM digests, source-archive inclusion
  in the source RPM, binary ownership and byte identity in the live root,
  AArch64 runtime linkage, service and udev-rule paths, rendered device IDs,
  and the bounded retrigger scriptlet; and
- an image without the source-bearing companion: absence of the IPTSD RPMs,
  package-database entry, and fixed native files.

X1P custom Stubble and installed-system hand-off will not be claimed until
qualified; its explicit-DTB stock path is live-only.

## Consequences

- Fedora Live output is a true remaster whose live kernel, initramfs, EROFS
  root, package database, and installed-system hand-off agree on one custom
  ABI.
- Fedora's native RPM, BLS, `kernel-install`, and dracut lifecycle can manage
  the installed kernel without treating Debian package metadata as Fedora
  state.
- A Fedora image built with the exact IPTSD companion has one native,
  Anaconda-carried pen-integration owner and complete on-media corresponding
  source. Its portable companion installer must not be run on Fedora because
  that would create a second unowned `/usr/local` layout.
- Third-party kmod packages that demand a versioned
  `kernel-uname-r = <exact-uname>` cannot resolve this multi-hyphen ABI through
  RPM 6; changing the upstream uname format is outside this adapter's scope.
- Preserving the source hybrid layout retains direct USB bootability while the
  adapter controls only the outer GRUB policy and remastered payload.
- The live-only DSP blacklist is absent from installed policy and is removed
  from state Anaconda may carry across the installation boundary.
- X1E/OLED custom Stubble identity and installed hand-off are structurally
  validated. X1P/LCD has an explicit-DTB stock live path for investigation,
  but installation is unsupported until the custom first-boot hand-off is
  qualified.
- Structural success does not prove a real Surface Pro 11 can boot the USB,
  complete Anaconda installation, boot the installed system, or operate every
  peripheral. Those physical tests remain release gates, as does disabling
  Secure Boot.

## References and prior art

The adapter design was checked against the earlier
[`fedora-surface-pro-11`](https://github.com/rjindael/fedora-surface-pro-11/tree/main)
bring-up, Fedora's
[`Snapdragon WoA Laptop Install`](https://fedoraproject.org/wiki/Snapdragon_WoA_Laptop_Install)
notes, the initial
[Fedora 40 Snapdragon X Elite discussion](https://discussion.fedoraproject.org/t/will-fedora-40-run-on-asus-vivobook-s15-with-snapdragon-x-elite-processor-on-arm-architecture/134772/2),
and the Fedora 42 bring-up reports at
[post 4](https://discussion.fedoraproject.org/t/snapdragon-x-elite-fedora-42-system-bring-up-and-looking-for-collaborators-or-sigs/153631/4)
and
[post 48](https://discussion.fedoraproject.org/t/snapdragon-x-elite-fedora-42-system-bring-up-and-looking-for-collaborators-or-sigs/153631/48).
Those sources informed the boot-argument and recovery baseline; the implemented
EROFS, RPM, Anaconda, and Stubble contracts are validated against the pinned
Fedora 44 media and the selected kernel bundle rather than copied as an
unverified recipe.
