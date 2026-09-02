# Install a released kernel and userspace support

Use this guide to install one exact Surface Pro 11 kernel release, retain the
running kernel as a recovery path, and add the audited recommended userspace
support. [Install Lexr on the device](../getting-started/install.md) before you
begin.

## Check the target and Lexr version

This workflow supports an installed Debian or Ubuntu target with `apt-get`,
`dpkg`, `dpkg-deb`, `update-initramfs`, and the normal GRUB package hooks. Read
the notes for the installed Lexr release because documentation on `main` can
describe behaviour which has not reached that executable.

Resolve the executable once so the read-only checks and privileged operations
use the same reviewed binary:

```sh
LEXR="$(command -v lexr)"
"$LEXR" version
```

The catalogue embedded in that version selects the audited userspace releases,
so record this output with the kernel tag used below.

!!! warning
    Released project kernels remain experimental and unsigned. Back up
    important data, keep a separate bootable recovery device, disable Secure
    Boot, and boot the existing Surface kernel successfully before treating it
    as the fallback.

The fallback must be the currently running, complete `-qcom-x1e` ABI, distinct
from the target ABI, with a usable module tree and GRUB entry. Lexr also
requires the target ABI to be fresh: existing kernel, module, header, or GRUB
state for that ABI makes preflight fail closed. See the
[requirements reference](../reference/requirements.md) for the complete host
and privilege boundary.

## 1. Download an exact kernel release

List the non-draft releases which contain a candidate image and modules pair,
then copy one exact tag into `KERNEL_REF`. An exact tag is reproducible;
`latest` can resolve to a different release later.

Choose a bundle path which has not held another download:

```sh
"$LEXR" kernel release list

KERNEL_REF="<exact-tag-shown-by-the-list>"
KERNEL_BUNDLE="$PWD/kernel-bundle"
RUNNING_ABI="$(uname -r)"

"$LEXR" kernel release download "$KERNEL_REF" \
  --headers \
  --output-dir "$KERNEL_BUNDLE"
```

Replace the angle-bracket placeholder with the tag exactly as listed. The
download uses the OE release channel by default, verifies its checksum
manifest, and records a local bundle manifest. `--headers` selects both
matching development-header packages; omit it for a runtime-only image and
modules installation.

## 2. Preflight the installation

Run the read-only preflight and dry run as your regular user:

```sh
"$LEXR" kernel preflight "$KERNEL_BUNDLE" \
  --root / \
  --fallback-abi "$RUNNING_ABI"

"$LEXR" kernel install "$KERNEL_BUNDLE" \
  --root / \
  --fallback-abi "$RUNNING_ABI" \
  --dry-run
```

Review the target root, new ABI, package set, initramfs and GRUB operations,
and retained fallback. These checks do not modify the system and do not prove
that either kernel passes physical hardware qualification.

If the running kernel is the broken candidate and another installed kernel is
your known-good fallback, replace `$RUNNING_ABI` with that exact installed ABI
and add `--force` to both preflight and install. Lexr will warn that the ABIs do
not match, record the override in JSON, and continue only after proving that
the selected fallback has its complete boot files, module tree, `modules.dep`,
and exactly one non-recovery GRUB entry. The flag does not bypass those checks.

Do not pass `--allow-unverified` to preflight or install to bypass a missing or
invalid checksum manifest for a published release.

## 3. Install the kernel

Install only after the complete dry run is acceptable. Lexr never elevates
itself, so grant privilege only to the confirmed operation:

```sh
sudo "$LEXR" kernel install "$KERNEL_BUNDLE" \
  --root / \
  --fallback-abi "$RUNNING_ABI" \
  --yes
```

If the reviewed dry run used a known-good non-running fallback, use that same
ABI and add `--force` to this confirmed command as well.

The real installation repeats preflight immediately before mutation, retains
the fallback, backs up GRUB, and verifies the installed package and boot state.
Keep the printed receipt. A failure triggers a bounded rollback attempt, but
the receipt may still require recovery action if rollback cannot finish.

Lexr does not reboot or explicitly select the default kernel. Package hooks
regenerate the normal GRUB configuration. When you are ready to test, reboot
through the normal system controls and select the new ABI deliberately. Keep
the fallback installed until the new kernel has passed the required boot and
hardware checks. The [kernel-management guide](../operator-manual/kernel-management.md)
describes the bundle and recovery contract in more detail.

## 4. Check userspace support

Inspect the complete system, then focus on the supported audio and IPTSD
features:

```sh
"$LEXR" doctor userspace

"$LEXR" userspace status \
  --feature audio \
  --feature iptsd
```

For automation, request JSON and inspect both its contents and the command's
exit status:

```sh
"$LEXR" userspace status --json
```

These commands share a static, point-in-time inspector. They do not run
services, probe physical hardware, contact the network, or modify the target.
An unfiltered report fails only for catalogue-required support. Explicitly
selected supported or experimental features also affect the exit status;
diagnostic-only and obsolete checks do not.

When more than one Surface ABI is installed and the active kernel pairing
matters, select it explicitly with `--kernel "$(uname -r)"`.

## 5. Pull and install the recommended userspace releases

`recommended` resolves to the supported audio and IPTSD releases in the
catalogue embedded in the recorded Lexr version. It does not include platform
firmware, Bluetooth evidence, or camera support. Use a fresh cache root:

```sh
USERSPACE_CACHE="$PWD/lexr-userspace"

"$LEXR" userspace pull recommended \
  --cache-dir "$USERSPACE_CACHE"

"$LEXR" userspace install recommended \
  --from "$USERSPACE_CACHE" \
  --dry-run

sudo "$LEXR" userspace install recommended \
  --from "$USERSPACE_CACHE" \
  --yes
```

The dry run verifies both component bundles before mutation. The real install
applies components sequentially rather than as one cross-component atomic
transaction, so keep every receipt and follow any partial-result or reboot
instruction printed by Lexr. Userspace installation does not remove recognised
legacy workarounds implicitly.

After installation, and after rebooting when requested, check the active
kernel pairing again:

```sh
ACTIVE_ABI="$(uname -r)"
"$LEXR" doctor userspace --kernel "$ACTIVE_ABI"
```

Confirm that `uname -r` reports the ABI you intended to boot. A passing static
report is not a substitute for testing audio, touch, pen, suspend, and other
required hardware on the same device.

## 6. Keep restricted and experimental support separate

Restricted platform firmware and the Bluetooth public-address evidence cannot
be pulled from the OE release channel. Acquire those only through the
[private same-device Windows hand-off](windows-handoff.md). Hand-off contents
are private device data and must not be committed, attached to issues,
published in releases, included in images, or placed in ordinary support
reports.

Camera support is experimental and is never part of `recommended`. Opt in to
its separate verified download only when you intend to follow the camera
qualification path:

```sh
"$LEXR" userspace pull camera \
  --cache-dir "$USERSPACE_CACHE"
```

Pulling a camera release does not install it. Follow
[Manage userspace support](userspace-support.md) for its separate installation
authority and compatibility requirements.

## Recover and continue

If the new kernel does not boot or fails its device checks, select the retained
fallback ABI from GRUB. Keep the kernel and userspace receipts, bundle, cache,
and recovery device until the system has passed the intended qualification.

For component-specific status semantics, native builds, camera installation,
and recognised legacy workarounds, continue with
[Manage userspace support](userspace-support.md). For lower-level bundle and
rollback details, use [Kernel management](../operator-manual/kernel-management.md).
