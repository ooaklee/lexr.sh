---
id: adrs-adr029
title: "ADR029: Self-contained kernel device-tree delivery"
description: Architecture decision for proving embedded device trees or provisioning exact-ABI external device trees through one Debian package lifecycle.
---

## Status

Accepted on 2026-09-04.

Implementation is tracked in
[#39](https://github.com/ooaklee/lexr.sh/issues/39). Structural implementation
does not constitute physical qualification of a generated kernel.

## Context

The kernel source policy selected for a build and the device-tree delivery
found in its output are different facts. A `source` request preserves the
kernel tree's packaging choice and can therefore produce either a Stubble PE
image with embedded `.dtbauto` sections or a raw image which requires an
external device tree. Recording only the request cannot tell an installer how
to make that kernel bootable.

Existing kernel packages place device trees beneath
`/usr/lib/firmware/<abi>/device-tree`, but that package-side presence is not a
boot binding. A raw image also needs the selected device tree materialised in
an exact-ABI `/boot` path and referenced by that ABI's bootloader entries.
Stubble has the opposite authority: its embedded payload must remain the
device tree for that build rather than being shadowed by an independently
selected host copy.

The native installer already rejects a target when neither delivery path is
proven. That fail-closed behaviour exposed an ownership gap: package-manager
installation and Ubuntu image creation do not yet share one generic owner for
external device-tree materialisation and removal.

## Decision

### Separate requested policy from effective delivery

Build, bundle, release and image evidence will record both:

```text
requested_boot_image_mode: source | stubble | nostubble
effective_dtb_delivery: embedded | external-required
```

Every build will inspect the generated image, including a `source` build, and
derive effective delivery from the artefact. An explicit `stubble` request
which produces a raw image and an explicit `nostubble` request which produces
embedded `.dtbauto` sections will fail. Downstream selection will use effective
delivery; it will not infer it from the request.

### Treat a Stubble image as a closed inventory

A valid embedded image will contain at least one `.dtbauto` section. For the
selected build profile, every required device tree must have exactly one
packaged copy and exactly one byte-identical embedded copy. Every embedded
device tree must in turn be attributable to one declared packaged output and
one declared selection route. Missing, duplicate, stale, unrecognised,
undeclared or selection-unattributable payloads will be rejected.

The deterministic inventory will be sorted independently of archive and PE
section discovery order. Each item will record its profile or platform
identifier, package and package-relative path, basename, size, SHA-256 digest,
root compatible strings, required state, exact `.dtbauto` match count and
typed selection metadata. Provenance will also identify the Stubble stub,
ukify, device-tree selection helper, HWID input and other inputs used to form
the image. That identity will include the build-container digest, installed
package names and versions, and digests of the effective executables and input
data rather than only an opaque aggregate package-set hash. Public evidence
will use digests and bounded metadata rather than host paths or identifiers
collected from a physical machine.

Multiple embedded device trees form a selectable compatibility set, not an
interchangeable fallback pool. Selection may use an exact generated SMBIOS
HWID, equality with the first root compatible value of a firmware device tree,
or an explicitly recorded machine-database mapping to that same primary
compatible. In the absence of a match, Stubble retains the
firmware-provided device tree; Lexr will not treat an unrelated embedded
payload as a substitute for a missing required device tree. A generic fallback
is valid only when the selected build profile declares and validates its
compatibility route.

This is structural attribution, not a hardware claim. A compatible string or
HWID registry record demonstrates how an artefact may be selected; it does not
prove that a particular machine exposes that identity, that firmware chooses
the expected path, or that the machine boots and operates correctly.

### Package generic external boot support

An `external-required` bundle contains one architecture-independent Debian
boot-support package, published as
`lexr-kernel-boot-support_<version>_all.deb`. The package owns a reusable
exact-ABI implementation, kernel lifecycle hooks and a declarative platform
registry under generic names, including:

```text
/usr/libexec/lexr/kernel-boot-refresh
/etc/kernel/postinst.d/05-lexr-kernel-boot
/etc/kernel/postrm.d/05-lexr-kernel-boot
/usr/lib/lexr/kernel-platforms/<platform-id>/
```

The public operation requires an exact ABI and derives the corresponding image
path rather than accepting a second, potentially inconsistent value:

```sh
lexr kernel boot refresh \
  --root / \
  --abi <exact-abi> \
  --profile auto
```

An explicit `<platform-id>` may replace `auto`. Automatic selection is limited
to declared, safely detected identities. The API does not name a particular
device, choose the numerically newest kernel, or write a shared DTB.

For one exact ABI, the package lifecycle selects only a permitted device tree
from that ABI's package contents, copies it atomically to
`/boot/dtbs/<abi>/<vendor>/<name>.dtb`, compares package-side and boot-side
digests, and writes a digest-identical regular `/boot/dtb-<abi>` compatibility
copy. Supported Debian and Ubuntu `10_linux` generators discover that
exact-ABI name and bind their simple, normal and recovery entries to it. Lexr
regenerates GRUB only after both files are durable and resolves and
digest-compares the actual kernel, initramfs and DTB tokens in every entry which
names the exact image. It does not install a competing GRUB generator.

One target root may select exactly one external platform for an ABI. A second
selection fails without changing the first. A physical host can therefore
install a bundle which carries several declared platforms because local
identity selects one of them. An offline image with no hardware identity must
either narrow the external bundle to one required platform explicitly with
`image create --kernel-profile <platform-id>` or use Stubble for multi-device
selection at boot. This projection changes only the deployment applicability
flag in a derived image inventory. Every source package and device-tree record,
byte identity, digest and selector is retained, but the derived inventory is
not represented as the unchanged source manifest.

Reinstallation and package-trigger ordering are idempotent. Removal deletes
only digest-authenticated files and state owned for the removed ABI, leaving
independently verified fallbacks intact. Removing the shared support package
is refused while any owned exact-ABI kernel image remains installed.

The post-removal hook consumes the package manager's lifecycle action and
retains state during same-ABI upgrade, failed-upgrade and abort processing.
Only `remove` or `purge` invokes exact-ABI cleanup.

### Require an exact-ABI trigger handshake

External delivery depends on a mechanically verified handshake between the
generated kernel image package and Lexr's boot-support package. For the exact
generated ABI:

- `lexr-kernel-boot-support` declares
  `interest-noawait linux-update-<abi>` and treats its `triggered` post-install
  action as mandatory final reconciliation;
- the generated `linux-image` control archive declares
  `interest linux-update-<abi>`;
- the image post-install script writes the exact ABI's
  `/etc/kernel/postinst.d` invocation beneath `/usr/lib/linux/triggers/<abi>`,
  activates `linux-update-<abi>`, and executes and removes that deferred command
  when called for the trigger; and
- the image post-removal script passes `DEB_MAINT_PARAMS` into
  `/etc/kernel/postrm.d`, allowing Lexr's hook to distinguish removal from an
  upgrade or abort path.

The build derives `<abi>` from the one packaged kernel image, opens the finished
Debian control archive and verifies this structure before publishing any
bundle. Inspecting the kernel tree's packaging templates is insufficient: a
downstream packaging change, substitution failure or generated-package defect
could otherwise leave the source contract looking correct while the shipped
package no longer activates Lexr's consumer. A missing or mismatched producer,
consumer, trigger name, deferred execution path or lifecycle propagation fails
the build.

The refresh operation remains idempotent because package order can cause both
the kernel hook and the boot-support package's triggered post-install action to
reconcile the same ABI. Success means the final trigger transaction converges
on one digest-verified binding; it does not depend on which package happened to
be unpacked first.

### Define the direct dpkg boundary

A complete bundle is directly installable on a prepared supported Debian
or Ubuntu host with:

```sh
sudo dpkg -i ./*.deb
```

This promise applies to the complete manifest-declared package set, not an
image and modules subset copied out of it. `dpkg -i` does not obtain missing
dependencies, so the host must already satisfy the bundle's declared
dependencies and provide a `10_linux` generator with per-version `dtb-<abi>`
lookup. Exact `linux-update-<abi>` trigger handling makes the final refresh
independent of support, image and modules package order. A deferred missing
package DTB is permitted during initial configuration but fails when the final
trigger runs; missing hardware identity remains deferrable only for an offline
image whose orchestrator supplies one explicit narrowed profile.

The build inspects the finished `linux-image` control archive and rejects it
unless its maintainer scripts own and activate the exact `linux-update-<abi>`
trigger. Source-tree packaging templates alone are not accepted as evidence of
the direct-install lifecycle.

An embedded bundle will not require the external boot-support package for its
boot binding. An external-required bundle will be incomplete without that
package. Both forms remain subject to final exact-ABI image, modules,
initramfs, device-tree and bootloader verification.

### Share one lifecycle implementation

The Debian boot-support package owns external device-tree copying, bootloader
integration and symmetric removal. `kernel install` installs
that package, orchestrates it, verifies its result and records or rolls back the
package-owned state; it does not carry a second copier or GRUB editor. Ubuntu
image creation consumes the same package and exact-ABI operation.

Kernel inspection, build and release validation, preflight, installation,
`doctor` and image validation use one effective-delivery and device-tree
inventory model. Their text and JSON may describe different evidence
boundaries, but must not derive conflicting answers from the same artefacts.

### Preserve guarded installation and recovery

Preflight remains read-only. A real Lexr installation still requires effective
root privilege, explicit confirmation and immediate revalidation. Target and
fallback ABIs will be verified independently before and after mutation.

The installer backs up the bounded GRUB state it can restore byte-for-byte. If
package, initramfs, per-ABI device-tree or GRUB verification fails, it attempts
bounded rollback, purges the failed target ABI, preserves the shared support
package and retains a recovery receipt. A direct package-manager
installation has the package lifecycle guarantees above, but does not gain
Lexr's separate preflight, fallback proof, confirmation, receipt or
transaction-level rollback. Operators who need those protections should use
`kernel install`.

## Migration and rollback

Ubuntu image support moves from its generated device-specific refresh script to
the generic boot-support package in one bounded migration. The image builder
installs and validates the package-owned path, then removes only positively
identified predecessor hooks. It does not leave both implementations active.
An external installed image is accepted only when its bundle declares one
required profile; multi-profile installed images require embedded delivery.

Existing embedded bundles remain embedded and do not acquire a second external
authority. Existing raw installations are not silently adopted: migration
must identify their exact ABI, verify package-side and boot-side bytes, retain
the current fallback and record every state item brought under package
ownership. Ambiguous shared DTBs or unrecognised hooks fail closed.

Rollback restores the byte-exact pre-transaction GRUB configuration and
removes the failed target ABI. It does not recreate a previously overwritten
target package whose archive was not retained. Package removal is symmetric
but is not a substitute for transaction rollback after a partially completed
migration.

## Consequences

- `source` remains a useful build request without being mistaken for a delivery
  result.
- Stubble validation can support multiple embedded device trees without
  accepting an open-ended or partially attributable set.
- Raw bundles gain one package-owned route shared by direct `dpkg`, guarded
  native installation and Ubuntu image creation.
- Every external file and lifecycle action is scoped to one exact ABI, so one
  kernel generation cannot silently replace another generation's device tree.
- Manifests and receipts become more detailed and require a schema migration;
  older evidence cannot be relabelled as the new contract without rebuilding
  or explicit bounded migration.
- Structural validation does not establish firmware behaviour, Secure Boot
  compatibility, peripheral operation or physical hardware qualification.

## Alternatives rejected

Accepting any image with one `.dtbauto` section was rejected because it cannot
prove the complete device set claimed by a profile. Always copying an external
DTB was rejected because it creates a competing authority for Stubble.
Provisioning only inside `kernel install` was rejected because it leaves direct
package installation incomplete and duplicates lifecycle policy. Embedding
device-specific scripts in every kernel image package was rejected because it
does not provide a generic, auditable platform boundary.

## Related decisions

- [ADR003](adr-003-version-bound-kernel-bundles.md) binds kernel artefacts to an
  exact ABI.
- [ADR007](adr-007-installed-system-handoff.md) records the original Ubuntu
  installed-system hand-off; this decision supersedes its device-specific
  external-DTB lifecycle while retaining its broader hand-off boundary.
- [ADR016](adr-016-native-kernel-release-preparation.md) defines closed kernel
  release directories.
- [ADR028](adr-028-snapdragon-x-project-scope.md) separates project scope from
  device-specific qualification.
