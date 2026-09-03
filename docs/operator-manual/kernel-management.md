# Kernel management

A kernel image on its own is not a usable or recoverable Surface Pro 11 kernel. Lexr binds the image, modules, optional matching headers, checksums, and both model-specific device trees into one bundle, then keeps inspection, installation, release preparation, and physical boot qualification as separate gates.

This page is for maintainers building or releasing kernels and for operators deliberately installing one on an existing system. Image creation is covered in the [user guide](../user-guide/index.md); the automation which publishes an OE prerelease is covered in [automation and release channels](automation-and-releases.md).

## Start from a complete bundle

At minimum, a usable bundle contains:

- one `linux-image-..._arm64.deb` package;
- one matching `linux-modules-..._arm64.deb` package;
- SHA-256 coverage for every selected package; and
- the X1E OLED and X1P LCD device trees supplied by that modules package.

The bundle records its release, repository, ABI, version, package digests, and expected device-tree paths. Lexr derives the ABI and version from package filenames and rejects absent required packages, mixed versions or ABIs, and package bytes which no longer match their recorded digest.

Local inspection and installation use `--package-set all` by default. When the emitted bundle declares the exact ABI-specific and common development-header pair, both headers join the runtime image and modules. A complete declaration fails closed if either header disappears. `--package-set runtime` deliberately selects only the image and modules; an intentional runtime-only download remains a valid two-package set even if the publisher checksum file also covers headers which were not downloaded.

Legacy directories without a manifest do not weaken this rule: a partial matching header pair is rejected, unrelated header versions are ignored, and callers cannot substitute an arbitrary filename pattern for the closed role set. Headers remain optional for live-image creation. [ADR003](../adr/adr-003-version-bound-kernel-bundles.md) explains the version-bound bundle decision.

## Download a published bundle

`kernel release download` uses the established OE release channel by default. It verifies the publisher's checksum manifest before writing a local bundle manifest. Use its explicit `--repository` option only for a compatible alternate release channel.

A local bundle without an authoritative `SHA256SUMS` is rejected unless you supply `--allow-unverified` explicitly. That option accepts only the package bytes measured locally; it is not publisher verification and must not be used to trust an unknown bundle.

## Build without changing the host

`kernel build` owns a compiled ARM64 Docker build policy and does not depend on a repository helper script. Its default source is the custom kernel's [`sp11/integration-7.2.x` branch](https://github.com/ooaklee/linux_ms_dev_kit-sp11/tree/sp11/integration-7.2.x); `--git-url` and `--git-branch` can select another HTTPS source and branch or tag.

Review the non-mutating plan before starting the long build:

```sh
lexr kernel build --dry-run

lexr kernel build \
  --repository-root <build-root> \
  --git-url https://github.com/ooaklee/linux_ms_dev_kit-sp11 \
  --git-branch sp11/integration-7.2.x \
  --output-dir build/lexr/kernel-v19
```

The private transaction and new output directory must be relative to the selected containment root, and the output directory must not already exist. Source data persists in a Docker volume labelled for that exact work boundary; generated packages cross through a private host transaction. `--reset-source` can clean only that managed volume's source tree.

The policy pins its Ubuntu 26.04 ARM64 base image by digest. Beside the packages it records the exact fetched revision and tree, the compiled recipe digest, and the installed-toolchain digest. Successful output contains the coherent signed image and modules pair, complete common and ABI-specific headers, `SHA256SUMS`, the normal kernel bundle manifest, and a source-provenance manifest.

The build never installs a package, elevates privilege, reboots the host, or publishes a release.

## Choose the boot-image policy deliberately

The default `--boot-image-mode source` preserves the selected source's Stubble policy. Use an explicit override only when the intended package policy differs:

- `stubble` passes a GNU Make command-line override to flavour packaging without editing the managed checkout. It validates the resulting packaged boot image across both runtime packages and requires exactly one `.linux` section, one `.hwids` section, at least one embedded `.dtbauto` section, and an embedded Surface Pro 11 Denali device tree.
- `nostubble` applies the corresponding packaging override, validates the packaged boot image across both runtime packages, and rejects embedded device trees.

The selected mode is retained in build and release provenance. For an SP11 source which would otherwise emit a raw boot image, an explicit Stubble build looks like this:

```sh
lexr kernel build \
  --repository-root <build-root> \
  --git-url https://github.com/ooaklee/linux_ms_dev_kit-sp11 \
  --git-branch <sp11-test-branch> \
  --boot-image-mode stubble \
  --output-dir build/lexr/kernel-sp11-stubble
```

Omit the override when another branch or device should retain its source policy.

## Prove the fallback before installation

`kernel preflight` is the read-only installation gate. It inspects exact Debian package metadata, rejects unexpected packages and mixed ABIs, proves that the explicitly selected fallback ABI is bootable, requires it to match the running ABI by default, classifies the target ABI without changing anything, and prints the bounded package, initramfs, and GRUB sequence. A complete fallback must also have one proven boot-time device-tree path: either an exact required same-ABI `.dtbauto` payload embedded in its AArch64 PE image or one matching same-ABI external DTB referenced consistently by all of its normal and recovery GRUB entries.

The target ABI is classified as `absent-and-eligible`, `already-installed-complete`, or `partial-or-inconsistent`. The classification collects bounded read-only evidence: boot files, module trees, firmware and device trees, development headers, GRUB entries, and the package database states for the exact target-ABI packages. A target that is not absent blocks installation with the full evidence plus safe next steps scoped to the target ABI only; the running and fallback ABI are never proposed for removal, and nothing is purged or repaired automatically. Pass `--overwrite` to explicitly replace an existing complete or partial installation; Lexr then prints a warning that an unsafe overwrite could break the system or prevent returning to the desktop on reboot, and rejects `--overwrite` when the target ABI matches the running ABI. If an overwritten installation fails and rolls back, the rollback removes the whole target installation rather than restoring the previous one, so review the evidence before overwriting a working target. `--yes` alone never bypasses this gate.

The target root and fallback ABI are always explicit. `--running-abi` is accepted only for an alternate-root fixture; inspection of the live root always uses direct `uname` evidence.

Review both preflight and the install dry run without privilege:

```sh
RUNNING_ABI="$(uname -r)"

lexr kernel preflight <kernel-bundle> \
  --root / \
  --fallback-abi "$RUNNING_ABI"

lexr kernel install <kernel-bundle> \
  --root / \
  --fallback-abi "$RUNNING_ABI" \
  --dry-run
```

Both commands use `--package-set all` by default. Add `--package-set runtime` only when you deliberately want to leave the complete matching development-header pair uninstalled.

When the running kernel is the broken candidate and an older installed kernel
is the known-good recovery path, pass that older ABI with `--force` to both
commands. The override bypasses only the running-versus-fallback equality
check. Lexr still requires the selected fallback's kernel image, initramfs,
System.map, configuration, populated module tree, `modules.dep`, and exactly one
non-recovery GRUB entry, and repeats those checks during installation. The plan
and receipt record the override and print a warning naming both ABIs:

```sh
KNOWN_GOOD_ABI="<installed-known-good-abi>"

lexr kernel preflight <kernel-bundle> \
  --root / \
  --fallback-abi "$KNOWN_GOOD_ABI" \
  --force

lexr kernel install <kernel-bundle> \
  --root / \
  --fallback-abi "$KNOWN_GOOD_ABI" \
  --force \
  --dry-run
```

Do not use `--force` to name an untested or incomplete kernel. It authorises a
different fallback selection; it does not make that selection bootable.

## Install with an explicit recovery path

A real installation requires effective root privilege and `--yes`; Lexr never elevates itself:

```sh
sudo lexr kernel install <kernel-bundle> \
  --root / \
  --fallback-abi "$RUNNING_ABI" \
  --yes
```

For the known-good non-running fallback path, keep the same ABI and `--force`
from the reviewed dry run in the confirmed command; omitting either deliberately
returns to the default exact-match guard.

Immediately before mutation, `kernel install` repeats preflight. It stages immutable package copies, retains the fallback kernel, backs up GRUB, and verifies the installed kernel image, initramfs, module tree, boot entry, both packaged Surface Pro 11 device trees, and both development-header trees when headers were selected. It separately proves the boot-time device-tree path with the same embedded-or-external rule used for the fallback. Packaged firmware-tree DTBs without either an exact embedded payload or a matching GRUB binding are not boot evidence and cannot produce a successful receipt or reboot hand-off. If the package maintainer scripts skipped the target ABI's initramfs image, Lexr regenerates it explicitly with the trusted `update-initramfs` generator before verification, and a repair that still fails triggers the same bounded rollback as any other verification failure.

Human output labels these facts separately as `packaged device trees verified` and `boot device-tree mode`. JSON receipts carry the digest, delivery mode, and number of matching normal and recovery GRUB entries under `installed.device_tree_boot`; preflight records the same evidence under `fallback.device_tree_boot`.

Lexr does not change the default kernel, remove the fallback, reboot, or install historical out-of-tree workarounds. If mutation or final verification fails, it attempts a bounded rollback and reports the recovery evidence in its receipt. Keep that receipt and the fallback available until the new kernel has passed the required device boot and hardware checks.

## Prepare a kernel release locally

`kernel release prepare` accepts only the exact closed output from `kernel build`, one or more corresponding-source archives, explicit licence text, a tag-like release identity, and a fresh output path. The historical `6.12.0-jg-0sp11v3-qcom-x1e` ABI and separate out-of-tree touchscreen modules are rejected because the maintained kernel carries that stack in-tree. Generation numbers are scoped to each kernel patch line, so a later in-tree kernel may legitimately use `sp11v3`.

A dry run hashes and validates every input without creating a parent or output directory. A real run repeats validation, copies through private staging, creates one path-free public manifest and British-English notes, checksums the complete closed set, validates it, and atomically publishes the new local directory. An existing or raced destination is never replaced. `kernel release validate` repeats the closed-directory structural checks without contacting a remote service.

Neither command publishes, installs, elevates privilege, or claims hardware qualification. Review and prepare with unique, absent output paths:

```sh
lexr kernel release prepare \
  --build-dir build/lexr/kernel-7.2.2-v1 \
  --output-dir build/release/sp11-qcom-x1e-7.2.2-jg-0sp11v1 \
  --release-name sp11-qcom-x1e-7.2.2-jg-0sp11v1 \
  --source build/lexr/release-source/linux-7.2.2-v1.tar.xz \
  --licence build/lexr/release-source/LICENSE.kernel.txt \
  --dry-run

lexr kernel release prepare \
  --build-dir build/lexr/kernel-7.2.2-v1 \
  --output-dir build/release/sp11-qcom-x1e-7.2.2-jg-0sp11v1 \
  --release-name sp11-qcom-x1e-7.2.2-jg-0sp11v1 \
  --source build/lexr/release-source/linux-7.2.2-v1.tar.xz \
  --licence build/lexr/release-source/LICENSE.kernel.txt

lexr kernel release validate build/release/sp11-qcom-x1e-7.2.2-jg-0sp11v1
```

Preparation records the supplied source bytes. The operator still has to prove that each archive corresponds to the build manifest's exact revision and tree, and that the licence evidence is sufficient for redistribution. Follow the OE repository's [kernel release procedure](https://github.com/ooaklee/linux-surface-pro-11-oe/blob/main/docs/how-to/how-to-release-kernel-artifacts.md) for verified source materialisation. [ADR016](../adr/adr-016-native-kernel-release-preparation.md) records the closed release contract.

Remote publication is a later, separately authorised step. See [automation and release channels](automation-and-releases.md) before enabling it.
