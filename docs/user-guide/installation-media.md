# Create and write installation media

Lexr turns the supported Ubuntu Concept image into live media that actually boots the selected Surface Pro 11 kernel, then validates the result before it can be written to a reviewed removable device. This page takes you from the shortest supported image command to a verified USB write.

## Audience and context

Follow this workflow when you want a bootable Lexr live image for a Surface Pro 11. If the live session also needs a self-contained CLI, source archive, catalogues, or IPTSD installer, read [Carry the offline companion](offline-companion.md) before creating the image; its image command adds those files without changing the verification boundary described here.

## Prerequisites and limits

- The shortest command selects `ubuntu-concept-resolute-x1e` and the latest candidate kernel release. Those packages become trusted only after their publisher checksums and measured contents pass verification.
- Structural validation is a publication gate, not a substitute for booting the media on a Surface Pro 11. Disable Secure Boot before using the unsigned custom kernel, and treat an actual device boot as the final compatibility gate.
- The current pre-write structural validator accepts only the implemented Lexr Ubuntu Casper output. Compressed raw disk images use a different partition and boot model and need a separate adapter.
- USB planning is read-only. The real write requires elevated privilege and the exact confirmation generated for the current source and device.

## 1. Create and validate the supported image

Run the host checks, create the image, and validate the completed output:

```sh
lexr doctor
lexr image create --output lexr-ubuntu-sp11.iso
lexr image validate lexr-ubuntu-sp11.iso
```

Use `--source` to supply an already downloaded Ubuntu Concept ISO, `--source-sha256` to require a known digest, `--kernel-dir` to use a local kernel bundle, or `--kernel-release` to select a tagged release. Cache and temporary-workspace locations can also be overridden.

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

## Why this is a true live-image remaster

Copying kernel packages beside an untouched installer does not change the kernel used by the live environment. The Ubuntu adapter instead unpacks the Casper filesystem, installs the custom runtime packages, rebuilds the initramfs, replaces `/casper/vmlinuz` and `/casper/initrd`, adds the paired device trees, and repacks the filesystem.

The source image is hybrid boot media: it contains ISO boot metadata and an appended GPT EFI System Partition. The adapter replays that layout and installs direct GRUB in both the ISO filesystem and appended EFI partition. Ubuntu's initramfs generation creates a Casper media UUID, so the adapter writes that same value to `.disk/casper-uuid-generic` and records the discovery contract in the image manifest. Output validation checks exact UUID agreement, Casper's default boot and live-layer declarations, the boot records, kernel, initramfs, module tree, device trees, manifests, and both EFI locations before descriptor-bound no-replace publication writes the manifest and journal first, then the ISO as the final commit marker.

If publication fails, the CLI reports every recoverable transaction path and does not remove anything through a mutable pathname. Inspect the reported final and hidden staging entries before removing them; the absence of the requested ISO means the output set was not committed.

Directly written hybrid media and a nested ISO stored on an outer filesystem are different strategies. The direct ISO does not use `iso-scan/filename`; that argument belongs to the labelled outer-disk loopback workflow. Both live and installed boot paths apply `soundwire_qcom.sp11_feedback_active_offset2_zero=1` for the validated FullIO audio behaviour. Live-USB entries also apply `modprobe.blacklist=qcom_q6v5_pas` because enabling the DSP while the live root remains on USB can reset or disconnect the medium. Installed-system entries must not carry that USB-only blacklist.

The modified deployable root also registers the exact image and modules packages in dpkg, carries a separate non-Casper initramfs, seeds both model-specific device trees under `/boot`, and installs a bounded refresh helper plus explicit X1E and X1P GRUB entries. Ubuntu's default minimal layered installation is expected to deploy this root, so that path inherits the selected kernel support rather than depending on the live-only USB entry. The optional full-desktop upper layer carries its own package database and is not yet proven to preserve the same hand-off. The structural validator extracts and checks the default minimal-root assets; completing that installation, allowing the installer to run its target bootloader step, and booting the installed system on a Surface Pro 11 remain hardware gates.

The extracted live filesystem stays inside a named Linux Docker volume throughout the remaster. This preserves case-sensitive paths, root ownership, device nodes, and Linux extended attributes even when the host filesystem cannot represent them faithfully. The source and completed artefacts cross the host boundary; the mutable Linux filesystem does not.

## 2. Review the USB target

`image devices` is read-only and lists every whole physical device with the evidence needed to review it, including whether the disk has an active non-mount consumer. It does not present an internal, non-removable, non-USB, read-only, system-backed, in-use, weakly identified, or undersized device as an acceptable target merely because its path was supplied explicitly.

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

This writer is distribution-neutral, but the current pre-write structural validator accepts only the implemented Lexr Ubuntu Casper output. Future Fedora, Debian, elementary OS, Pop!_OS, and raw-image adapters will retain their own image validation and live-media contracts while reusing the removable-device manager.

## Success and next steps

The write is complete only after Lexr has written the source length, read it back, verified its SHA-256, and reported the final device state. Boot the media on the target Surface Pro 11 to complete the hardware compatibility gate.

Once booted, either [use the offline companion](offline-companion.md), [inspect userspace support](userspace-support.md), or return to the [user-guide index](index.md).
