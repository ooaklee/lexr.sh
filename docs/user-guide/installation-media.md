# Create and write installation media

Lexr turns a supported upstream image into live media with the selected Surface
Pro 11 kernel on a structurally validated boot path. It checks that result
before it can be written to a reviewed removable device. This page covers the
implemented Ubuntu Concept and Fedora Workstation Live adapters, from image
creation to a verified USB write and the physical test which must follow.

> [!CAUTION]
> Both implemented adapters remain experimental. `implemented` means Lexr can
> create and structurally validate their output; it does not mean that the image
> has completed a physical boot or installation. A Fedora 44 candidate passed
> structural validation and USB read-back, but a Surface Pro 11 boot reached the
> emergency path. Removing `quiet` exposed early text before the display remained
> black for hours. Reproduction and diagnosis continue in
> [issue #17](https://github.com/ooaklee/lexr.sh/issues/17); Ubuntu's separate
> end-to-end qualification is tracked in
> [issue #16](https://github.com/ooaklee/lexr.sh/issues/16).

## Audience and context

Follow this workflow when you want to build a candidate live image and put it on
a USB device for Surface Pro 11 testing. If the live session also needs a
self-contained CLI, source archive, catalogues, or IPTSD support, read
[Carry the offline companion](offline-companion.md) before creating the image;
its image command adds those files without changing the verification boundary
described here.

## Prerequisites and limits

- The shortest command selects `ubuntu-concept-resolute-x1e` and the latest candidate kernel release. Those packages become trusted only after their publisher checksums and measured contents pass verification.
- Fedora Workstation Live 44 must be selected explicitly and requires a patch-line-qualified verified Surface kernel bundle: 7.2.0/sp11v19+ or 7.2.2/sp11v1+. Its custom live and installed-system path is limited to X1E/OLED; X1P/LCD has a stock-kernel live troubleshooting entry only and must not be installed from this adapter.
- Structural validation is a publication gate, not a substitute for booting the media on a Surface Pro 11. Disable Secure Boot before using the unsigned custom kernel, and treat an actual device boot as the final compatibility gate.
- Every current catalogue entry still needs complete end-to-end testing, especially to confirm its image structure and bootability. The implemented entries remain runnable so contributors can reproduce and improve them.
- The pre-write router accepts only the implemented Lexr Ubuntu Casper and Fedora Live outputs after their adapter-owned structural validators pass. Compressed raw disk images use a different partition and boot model and need a separate adapter.
- USB planning is read-only. The real write requires elevated privilege and the exact confirmation generated for the current source and device.

## 1. Choose, create and validate an image

### Ubuntu Concept

Run the host checks, create the image, and validate the completed output:

```sh
lexr doctor
lexr image create --output lexr-ubuntu-sp11.iso
lexr image validate lexr-ubuntu-sp11.iso
```

Use `--source` to supply an already downloaded Ubuntu Concept ISO, `--source-sha256` to require a known digest, `--kernel-dir` to use a local kernel bundle, or `--kernel-release` to select a tagged release. Cache and temporary-workspace locations can also be overridden.

If the selected kernel bundle reports `external-required` DTB delivery, also
pass `--kernel-profile <platform-id>`. Offline media has no physical machine
identity during creation, so Lexr requires one explicit declared profile (for
example, `surface-pro-11-x1e-oled`) and records that projection in the image
manifest. Embedded Stubble bundles select their matching DTB at boot and reject
this option.

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

Select Fedora's implemented ARM64 Live ISO explicitly and provide either a
local patch-line-qualified bundle or a corresponding verified release:

```sh
KERNEL_BUNDLE=/path/to/verified-patch-line-kernel-bundle

lexr image create \
  --catalog-id fedora-workstation-live-44 \
  --kernel-dir "$KERNEL_BUNDLE" \
  --output lexr-fedora-44-sp11-7.2.2-v1.iso

lexr image validate lexr-fedora-44-sp11-7.2.2-v1.iso
```

To use a published bundle instead, set `KERNEL_RELEASE` to its exact tag and
replace the `--kernel-dir` line with `--kernel-release "$KERNEL_RELEASE"`.
Without either flag, Lexr selects the latest candidate release; the Fedora
adapter still rejects an unknown patch line, a generation below that line's
floor, or an incomplete bundle.

The catalogue supplies Fedora's publisher SHA-256. The adapter accepts the
explicit 7.2.0/sp11v19 and 7.2.2/sp11v1 lines, applies each line's generation
floor, and rejects unknown, mixed, or incomplete ABIs. Secure Boot must be
disabled for the unsigned custom Stubble kernel. X1P custom Stubble auto-DTB selection and
installed-system hand-off are not supported; use its explicit-DTB stock entry
for live investigation only. Physical USB boot, an X1E installation,
installed-system boot, pen/touch, audio, and suspend/resume remain hardware
qualification gates.

The known Fedora physical result is not a successful boot: the candidate reached
the emergency path, and removing `quiet` revealed early console output before a
persistent black screen. Preserve the generated manifest and journal when
reporting a reproduction; the follow-up work must find the first failing boot
boundary rather than treating structural validation as proof of bootability.

## 2. Review the USB target

`image devices` is read-only and lists every whole physical device with the evidence needed to review it, including whether the disk has an active non-mount consumer. It does not present an internal, non-removable, non-USB, read-only, system-backed, in-use, weakly identified, or undersized device as an acceptable target merely because its path was supplied explicitly.

The commands below use the Ubuntu filename from the shortest example. Replace
it with `lexr-fedora-44-sp11-7.2.2-v1.iso` when writing the Fedora output.

```sh
lexr image devices
lexr image write lexr-ubuntu-sp11.iso \
  --device /dev/diskX \
  --dry-run
```

Replace `/dev/diskX` with the reviewed whole-device path shown on your host. The dry run performs structural ISO validation, hashes the complete source, inspects the target, and prints an exact confirmation phrase without unmounting or writing anything. It is safe to repeat after reconnecting the device.

## 3. Write and verify the image

Run the real operation with elevated privilege and paste the exact phrase from the current plan. The phrase includes both the whole-device path and full source SHA-256; `yes`, a shortened digest, and a phrase from a different plan are rejected.

```sh
sudo lexr image write lexr-ubuntu-sp11.iso \
  --device /dev/diskX \
  --confirm 'ERASE /dev/diskX DEVICE <opaque-fingerprint> AND WRITE SHA256 <full-sha256>'
```

An interactive terminal can omit `--confirm` and type the displayed phrase at the protected prompt. Automation must pass the exact phrase explicitly. Because the phrase contains the opaque fingerprint, a confirmation obtained for a previous USB device is rejected after another device takes over the same `/dev` path. Immediately before mutation, the manager reopens and rehashes the source, re-inspects the target, compares the already-open source descriptor with target mounts, checks privilege, unmounts only approved removable-style target filesystems, and refuses to continue if any mount, host-storage classification, active storage consumer, or identity drift remains. The production raw opener rejects links and ordinary files, proves that ordinary and raw nodes address the same kernel device, opens with `O_NOFOLLOW`, and proves that its descriptor still denotes that inspected device. The manager then writes bounded chunks, flushes them, reads back exactly the source length, verifies the SHA-256, re-inspects once more, and ejects or powers off the target. A failure returns the exact not-started, prepared, writing, written, verifying, or verified receipt state, complete byte counts, and only complete digests; it never claims that writing, verification, or ejection began before the corresponding boundary was crossed.

This writer is distribution-neutral. Its pre-write router accepts the implemented Lexr Ubuntu Casper and Fedora Live outputs only after dispatching each image to its adapter-owned structural validator. Future Debian, elementary OS, Pop!_OS, and raw-image adapters will retain their own validation and live-media contracts while reusing the removable-device manager.

## Why Lexr remasters the live root

Dropping kernel packages beside an installer can look sufficient, but the live
environment would still boot its original kernel and initramfs. Each
implemented adapter changes the distribution's deployable root and rebuilds
the initramfs for the exact selected ABI.

### Ubuntu path

The Ubuntu adapter unpacks Casper, installs the custom runtime packages,
rebuilds the initramfs, replaces `/casper/vmlinuz` and `/casper/initrd`, adds
both model-specific device trees, and repacks the filesystem. Its default
minimal installation path carries that same kernel contract. The optional
full-desktop upper layer has its own package database and is not yet proven to
preserve the hand-off.

Every Surface live entry passes the Casper, clock, and power-domain
parameters directly to the kernel. These model-specific entries
do not depend on a firmware processor-name match. Validation rejects a menu
that places the required arguments only in a variable or a comment.

Ubuntu leaves `qcom_q6v5_pas` available for Type-C USB. With v23, the driver
can attach to the ADSP firmware already started by UEFI when the full Denali
firmware is absent. Blacklisting this driver prevented USB media discovery
on the tested X1E/OLED Surface; removing it restored the live desktop.
Validation rejects that blacklist on Ubuntu live entries. X1P/LCD remains
unqualified, and desktop boot alone does not validate the installer.

The adapter preserves Ubuntu's original `.disk/info` bytes, including the
release and quoted codename used by Desktop Bootstrap. Replacing that identity
with an unquoted Lexr label caused the installer to fail before its welcome
page. Source and output validation reject malformed product metadata; Lexr's
kernel identity and provenance remain under `/sp11`. The corrected metadata
has reached the welcome page on the tested X1E/OLED live session; an actual
installation and installed boot still require separate validation.

The live initramfs also includes the distribution's X1E Adreno GMU and SQE
firmware. The custom GPU driver can request these files before Casper mounts
the live filesystem, even when its module metadata does not list them.
Creation fails if the source lacks either file, and validation checks their
non-empty contents across the initramfs archives. This does not supply the
private Denali firmware handled by the [Windows hand-off](windows-handoff.md),
or establish hardware bootability. Ubuntu live-boot qualification remains
tracked in [issue #41](https://github.com/ooaklee/lexr.sh/issues/41).

### Fedora path

The Fedora adapter extracts the LZMA-compressed EROFS root at
`/LiveOS/squashfs.img`, turns the verified Debian kernel payload into the
Lexr-built `lexr-kernel-sp11` RPM, generates an exact-ABI `dracut-live`
initramfs, and recreates EROFS with SELinux labels and extended attributes
intact. Anaconda sees only the custom `/boot/vmlinuz-*` candidate. The
installed-system contract includes a one-shot finalizer which restores the
package-owned stock image and its BLS fallback after the first non-live X1E
boot while keeping the custom kernel as the default.

[ADR027](../adr/adr-027-fedora-erofs-remaster-and-installed-handoff.md) records
the complete EROFS, RPM, Anaconda, boot-policy, and validation decision.

### Shared boot and publication safeguards

Both source images are hybrid boot media with an appended GPT EFI System
Partition. Ubuntu binds Casper to its generated UUID; Fedora binds
`dracut-live` to the pinned `Fedora-WS-Live-44` volume label and preserves the
ESP marker which hands off to `/boot/grub2/grub.cfg`. Validation checks those
identities together with the boot records, kernel, initramfs, module tree,
device trees, package ownership, manifests, and EFI locations before the
manifest and journal are published and the ISO becomes the final commit marker.

The Fedora live policy still blacklists `qcom_q6v5_pas`; its hardware
qualification is separate from the Ubuntu fix above. Installed systems must
not retain a live-only DSP blacklist. Ubuntu omits the retired
`soundwire_qcom.sp11_feedback_active_offset2_zero=1` parameter.
A directly written hybrid ISO does not use `iso-scan/filename`, which belongs
to a labelled outer-disk loopback workflow.

The custom kernel does not replace device firmware or audio userspace.
The distributable ISO carries no private Denali firmware or restricted FullIO
audio payload. The native [Wi-Fi userspace command](userspace-support.md#wi-fi-from-distribution-firmware)
derives its board fallback from the distribution's existing Linux firmware.
Full audio needs the [same-device Windows hand-off](windows-handoff.md) and
[userspace setup](userspace-support.md). Importing full ADSP firmware can
restart the DSP and disconnect Type-C USB; perform that hand-off on the
installed system rather than while its live root depends on USB.

The mutable live filesystem remains inside a named Linux Docker volume during
the build, preserving ownership, device nodes, case-sensitive paths, SELinux
labels, and other extended attributes. If publication fails, Lexr reports the
recoverable transaction paths without deleting through a mutable pathname; an
absent requested ISO means the output set was not committed.

## Success and next steps

The write is complete only after Lexr has written the source length, read it back, verified its SHA-256, and reported the final device state. Boot the media on the target Surface Pro 11 to complete the hardware compatibility gate.

Once booted, either [use the offline companion](offline-companion.md), [inspect userspace support](userspace-support.md), or return to the [user-guide index](index.md).
