# Install a released kernel and userspace support

Use this guide to install one exact Surface Pro 11 kernel release, retain the
running kernel as a recovery path, and add the audited recommended userspace
support. [Install Lexr on the device](../getting-started/install.md) before you
begin.

## Choose the hardware target

Choose your device once. The commands below carry that same choice through
preflight, installation and the final boot check:

| Your Surface Pro 11 | Hardware profile |
| --- | --- |
| Snapdragon X Plus, LCD | `x1p64100-microsoft-denali` |
| Snapdragon X Elite, OLED | `x1e80100-microsoft-denali-oled` |

This example selects LCD. Change the assignment to the OLED ID for that model:

```sh
lexr profile list
HARDWARE_PROFILE="x1p64100-microsoft-denali"
lexr init "$HARDWARE_PROFILE"
```

`lexr init` saves the choice for later commands. The privileged examples also
pass it explicitly because `sudo` reads root's configuration. An absolute
`--config` path is another way to use your saved settings under sudo. See
[hardware profiles](../concepts/hardware-profiles.md) for detection and offline
targets.

## Check the target and Lexr version

This page describes the 0.5.0 workflow on an installed Debian or Ubuntu target with `apt-get`,
`dpkg`, `dpkg-deb`, `update-initramfs`, and the normal GRUB package hooks. Read
the notes for the installed Lexr release because documentation on `main` can
describe behaviour which has not reached that executable.

Check your installed Lexr version:

```sh
lexr version
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

The `-qcom-x1e` suffix names the shared kernel package family. It does **not**
mean your machine is OLED. Lexr checks the selected model's device-tree bytes
for both the fallback and the new kernel.

## 1. Download an exact kernel release

List the non-draft releases which contain a candidate image and modules pair,
then copy one exact tag into `KERNEL_REF`. An exact tag is reproducible;
`latest` can resolve to a different release later.

Choose a bundle path which has not held another download:

```sh
lexr kernel release list

KERNEL_REF="<exact-tag-shown-by-the-list>"
KERNEL_BUNDLE="$PWD/kernel-bundle"
RUNNING_ABI="$(uname -r)"

lexr kernel release download "$KERNEL_REF" \
  --headers \
  --output-dir "$KERNEL_BUNDLE"
```

Replace the angle-bracket placeholder with the tag exactly as listed. The
download uses the OE release channel by default, verifies its checksum
manifest, and records a local bundle manifest. `--headers` selects both
matching development-header packages; omit it for a runtime-only image and
modules installation.

## 2. Preflight the installation

Kernel images can be readable only by root. Use sudo for these read-only
checks and retain your hardware choice explicitly; `sudo -i` is unnecessary:

```sh
sudo lexr --profile "$HARDWARE_PROFILE" kernel preflight "$KERNEL_BUNDLE" \
  --root / \
  --fallback-abi "$RUNNING_ABI"

sudo lexr --profile "$HARDWARE_PROFILE" kernel install "$KERNEL_BUNDLE" \
  --root / \
  --fallback-abi "$RUNNING_ABI" \
  --dry-run
```

Review the target root, new ABI, package set, initramfs and GRUB operations,
retained fallback, and the fallback's reported boot device-tree mode. A valid
mode is either an exact same-ABI DTB embedded in the kernel image or a matching
external DTB referenced consistently by its GRUB entries. These checks do not
modify the system and do not prove that either kernel passes physical hardware
qualification.

For an older installation that boots through `/boot/sp11-denali.dtb`, Lexr
first proves that those bytes match the selected model's installed fallback
DTB. The plan then shows the exact-ABI copy it will preserve for stock GRUB,
any recognised competing hooks it will back up and retire, and the new
kernel's explicit profile refresh. Neither command above applies those changes.

An unrecognised executable at either retired hook path, a conflicting
exact-ABI DTB or a wrong-model fallback stops the operation before package
installation. Follow the named error and review that evidence; choosing a
profile does not bypass verification.

If the running kernel is the broken candidate and another installed kernel is
your known-good fallback, replace `$RUNNING_ABI` with that exact installed ABI
and add `--force` to both preflight and install. Lexr will warn that the ABIs do
not match, record the override in JSON, and continue only after proving that
the selected fallback has its complete boot files, module tree, `modules.dep`,
and exactly one non-recovery GRUB entry. The flag does not bypass those checks.

Do not pass `--allow-unverified` to preflight or install to bypass a missing or
invalid checksum manifest for a published release.

## 3. Install the kernel

Install only after the complete dry run is acceptable. Add `--yes` to authorise
the changes using the same profile, bundle and fallback:

```sh
sudo lexr --profile "$HARDWARE_PROFILE" kernel install "$KERNEL_BUNDLE" \
  --root / \
  --fallback-abi "$RUNNING_ABI" \
  --yes
```

If the reviewed dry run used a known-good non-running fallback, use that same
ABI and add `--force` to this confirmed command as well.

The real installation repeats preflight immediately before mutation, retains
the fallback, backs up GRUB, and verifies the installed package and boot state.
For external-DTB kernels, it calls the bundle's boot-support helper with your
chosen profile after installation. You do not need to install that helper
separately or run a manual boot refresh as part of the normal workflow.
The receipt distinguishes the number of packaged DTBs from the verified
boot-time mode. Packaged DTBs alone are insufficient: Lexr requires an exact
same-ABI embedded DTB or a matching external GRUB binding before reporting
`reboot required: true`. Keep the printed receipt. A failure triggers a bounded
rollback attempt, but the receipt may still require recovery action if rollback
cannot finish.

Recognised retired `zzzz-surface-pro-11-dtb` hooks are backed up through Lexr's
reversible cleanup mechanism before package scripts run. The receipt records
their backup location. Failed installations attempt to restore them after
package rollback; changed local files are never overwritten during recovery.
The verified fallback DTB copy remains available even if installing the new
kernel fails. Unknown hooks are left for explicit review.

After installation, inspect the generated boot evidence before rebooting.
Keep the same hardware choice and copy the exact target ABI from the
installation plan or receipt:

```sh
TARGET_ABI="<exact-target-abi>"

sudo lexr --profile "$HARDWARE_PROFILE" doctor boot \
  --root / \
  --target-abi "$TARGET_ABI" \
  --fallback-abi "$RUNNING_ABI"
```

Add `--json` for one stable machine-readable report. The command resolves the
effective GRUB default and `saved_entry`, reports only recognised kernel,
initramfs, and device-tree path tokens, identifies stale entries, and applies
the same embedded-or-external boot requirements as installation while
narrowing accepted DTB evidence to the requested or detected device: either
an exact same-ABI `.dtbauto` payload embedded in the AArch64 PE image or one
matching external GRUB `devicetree` binding. It also reports
the presence of the retired `sp11-grub-inject-dtb` helper and matching kernel
hooks as attribution evidence, but never executes them. A missing or mismatched
DTB on the effective default or an explicitly selected target or fallback ABI
makes the report not ready after the complete output has been written. Drift
on another entry is a warning.

For an alternate mounted root, an explicit or saved profile is required. On
the live root, an unset profile can be detected from exact device-tree
evidence. This static command never runs `update-grub`, changes a
default, rewrites a DTB, elevates privileges, or proves physical bootability.

### Audit embedded and external boot-DTB usage per GRUB entry

The JSON report audits DTB bindings one GRUB entry at a time. Each element of
`entries` carries the entry's canonical `abi` (when the `vmlinuz-*` basename is
unambiguous), any recognised `devicetree` path tokens, the exact
`boot_dtb_sha256` and `installed_dtb_sha256` digests, the `dtb_matches`
comparison, and additive `device_tree_boot` mode, digest, and entry-count
evidence when verification succeeds. An embedded binding has no external path
token; Lexr does not invent one. An ABI-level consistency check rejects normal
and recovery entries which mix delivery modes or digests. Entries whose
`devicetree` token is the legacy shared
`/boot/sp11-denali.dtb` path are the ones the retired OpenEmbedded helper
bound; compare each such entry's `boot_dtb_sha256` against the
`installed_dtb_sha256` values of other entries to see which installed ABI the
shared bytes actually belong to. The top-level `dtb_attribution` object
attributes only the effective default entry's boot DTB and includes its
verified delivery mode, so use `entries` for a
host-wide audit. Kernels without a GRUB stanza, and entries whose kernel
identity is not one canonical qcom ABI, have no digest comparison; the
`legacy_hooks.retired_helper` field reports whether the retired helper is still
present.

Diagnosis is read-only. `doctor boot` reports the retired helper and hooks
without executing or changing them. The confirmed installation described
above can retire recognised hooks and preserve an already verified fallback
at `/boot/dtb-<abi>`. It cannot repair a fallback whose current boot bytes
already belong to another ABI or hardware model; use a verified recovery
kernel before attempting an upgrade.

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
lexr doctor userspace

lexr userspace status \
  --feature audio \
  --feature iptsd
```

For automation, request JSON and inspect both its contents and the command's
exit status:

```sh
lexr userspace status --json
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

lexr userspace pull recommended \
  --cache-dir "$USERSPACE_CACHE"

lexr userspace install recommended \
  --from "$USERSPACE_CACHE" \
  --dry-run

sudo lexr --profile "$HARDWARE_PROFILE" userspace install recommended \
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
lexr doctor userspace --kernel "$ACTIVE_ABI"
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
lexr userspace pull camera \
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
