# Create and write installation media

Lexr turns a supported upstream image into live media with the selected Surface
Pro 11 kernel on a structurally validated boot path. It checks that result
before it can be written to a reviewed removable device. This page covers the
shipped Ubuntu Concept and elementary OS entries, from image creation to a
verified USB write and the physical test which must follow. Fedora contributor
instructions use a custom catalogue while its shipped entries are withdrawn.

> [!CAUTION]
> All implemented adapters remain experimental. `implemented` means Lexr can
> create and structurally validate their output; physical boot and installation
> need separate evidence. The Ubuntu candidate built
> with Lexr `384f2c0` and v23 reached the X1E/OLED live desktop; the tester
> confirmed Wi-Fi connected without a live-session repair. Installation and
> remaining hardware checks still need qualification. A Fedora 44 candidate passed
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
- Fedora Workstation Live 44 requires an explicit custom catalogue and a patch-line-qualified verified Surface kernel bundle: 7.2.0/sp11v19+ or 7.2.2/sp11v1+. Its custom live and installed-system path is limited to X1E/OLED; X1P/LCD has a stock-kernel live troubleshooting entry only and must not be installed from this adapter.
- Structural validation is a publication gate, not a substitute for booting the media on a Surface Pro 11. Disable Secure Boot before using the unsigned custom kernel, and treat an actual device boot as the final compatibility gate.
- Every current catalogue entry still needs complete end-to-end testing. Ubuntu's tested X1E/OLED live desktop and Wi-Fi result does not qualify installation, other models or other kernel versions. The implemented entries remain runnable so contributors can reproduce and improve them.
- The pre-write router accepts only the implemented Lexr Ubuntu Casper, elementary Casper and Fedora Live outputs after their adapter-owned structural validators pass. Compressed raw disk images use a different partition and boot model and need a separate adapter.
- USB planning is read-only. The real write requires elevated privilege and the exact confirmation generated for the current source and device.

## 1. Choose, create and validate an image

### Ubuntu Concept

Physical testing on 2026-09-05 used the dated 2026-03-26 source, Lexr
`384f2c0`, and kernel `7.2.0-jg-0sp11v23-qcom-x1e`. After a complete Lexr USB
write and matching SHA-256 read-back, the tester reached the Surface Pro 11
X1E/OLED live desktop and connected to Wi-Fi without repairing the live
session. Board data was prepared from the source distribution before the
first driver probe. See [issue #41](https://github.com/ooaklee/lexr.sh/issues/41)
for live-boot evidence; installation, installed-system boot and the full
peripheral matrix remain under [issue #16](https://github.com/ooaklee/lexr.sh/issues/16).

The tester also confirmed the installer welcome screen and working pen and
touchscreen input after using the bundled Lexr to install `sp11-iptsd-v2`
from the USB companion. This confirms the desktop guide's IPTSD workflow;
pressure, palm rejection and suspend/resume need separate testing. Audio
configuration and validation remain part of the installed-system workflow.

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

### elementary OS 8.1 ARM64

The `elementary-os-8-1-20260219` entry pins the ARM64 image and publisher checksum.
Its `elementary-casper` adapter remains experimental. On 2026-09-06, the maintainer
confirmed that the image built with Lexr `927d00e` and v23 reached the Surface Pro
11 X1E/OLED desktop from the default GRUB entry. Wi-Fi, browsing a website and the
language/try/install chooser worked. Boot showed the elementary logo and spinner,
then took longer than expected before reaching the desktop; allow time for that
first start. No fixed boot-time guarantee has been measured.

Download the [tested elementary OS 8.1 image](https://github.com/ooaklee/linux-surface-pro-11-oe/releases/tag/sp11-elementary-os-8.1-v23-20260906)
or build it with the command below. The release notes include the latest Lexr
installer and a pinned source-build option until elementary support ships in a
stable release. Lexr v0.3.0 predates this adapter.

The corrected initramfs includes early DSP, graphics and keyboard-hub drivers,
the device-tree panel drivers, and QRTR's socket protocol and remote transport
for Qualcomm service discovery. These are not all implied by the graphics
module's ELF dependencies. Preparation and validation cover each required
driver's dependency membership and bytes against the kernel bundle.

An actual installation, installed custom-kernel/DTB boot, recovery, audio,
pen/touch setup and other peripherals still need testing under
[issue #49](https://github.com/ooaklee/lexr.sh/issues/49). Reaching the installer
chooser does not establish that installation succeeds.

The separate **firmware display diagnostics** GRUB entry disables only the `msm`
graphics module for that boot and requests a text session without Plymouth.
It can help collect early boot messages when graphics startup loses the display;
it does not qualify the accelerated desktop or installed-system boot.

```sh
mkdir -p ../lexr-build
lexr image create \
  --catalog-id elementary-os-8-1-20260219 \
  --kernel-release sp11-qcom-x1e-7.2.0-jg-0sp11v23 \
  --kernel-profile surface-pro-11-x1e-oled \
  --companion-source-dir . \
  --companion-userspace iptsd-v1 \
  --output ../lexr-build/lexr-elementary-sp11.iso
lexr image validate ../lexr-build/lexr-elementary-sp11.iso
```

Run from the Lexr source tree when including its companion, or replace `.` with
your source directory. The v23 tag is the initial SP11 baseline; other compatible
verified bundles can be selected. This adapter currently requires external DTBs
and one declared installed-system profile. Use the matching live menu entry for
your device; X1P/LCD remains unqualified.

The adapter retains elementary's single `/casper/filesystem.squashfs`, Distinst
installer and native GRUB lifecycle. Both normal and recovery GRUB entries use
the selected ABI's staged device tree. The active installed configuration is
`/boot/efi/EFI/ubuntu/grub/grub.cfg`; elementary's patched `update-grub` maintains
it alongside `/boot/grub/grub.cfg`. Automatic elementary installations do not
create Pop's separate recovery partition. Keep the removable live image until
installed-system and recovery boot are verified.

The source lacks the X1E GPU GMU and SQE firmware. Lexr downloads only those two
public files plus their complete licence, notices and provenance from the pinned
upstream Linux firmware revision. Their SHA-256 and sizes are verified before
use and during completed-image validation. The record is available at
`/cdrom/sp11/firmware/provenance.json` and `/usr/share/lexr/firmware`. Initial
creation needs internet access for these small downloads. Wi-Fi board data is
derived from the distribution's existing database before the first driver probe;
private firmware and data from another installed system are never added.

`LEXR_GETTING_STARTED.txt` is installed in the live user's Desktop folder when
the companion is included. It covers the bundled tool, updating to the latest
stable Lexr, Wi-Fi, IPTSD and post-install audio setup. Elementary may expose
that folder through Files rather than desktop icons. The companion and its
source/licences remain under `/usr/share/lexr/elementary-media/sp11/companion`
after installation. Avoid restarting the audio DSP while using a live USB root.

### Fedora Workstation Live 44

Both Fedora entries are temporarily withdrawn from the shipped catalogue while
the boot failure in [issue #17](https://github.com/ooaklee/lexr.sh/issues/17)
remains unresolved. The adapter is retained for contributor investigation.
Save the complete preserved JSON catalogue document from that issue as
`fedora-catalog.json`, review its historical source metadata, and validate it
before selecting the Live ISO. The raw disk entry has no image adapter.
Provide either a local patch-line-qualified bundle or a corresponding verified
release:

```sh
KERNEL_BUNDLE=/path/to/verified-patch-line-kernel-bundle

lexr catalog validate fedora-catalog.json
lexr image create \
  --catalog fedora-catalog.json \
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

The preserved catalogue supplies Fedora's recorded publisher SHA-256. The adapter accepts the
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

This writer is distribution-neutral. Its pre-write router accepts the implemented Lexr Ubuntu Casper, elementary Casper and Fedora Live outputs only after dispatching each image to its adapter-owned structural validator. Future Debian, Pop!_OS, and raw-image adapters will retain their own validation and live-media contracts while reusing the removable-device manager.

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

The Ubuntu and Fedora source images are hybrid boot media with an appended GPT
EFI System Partition; the elementary adapter adds a USB GPT ESP to its optical
source. Ubuntu and elementary bind Casper to their generated UUIDs; Fedora binds
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
