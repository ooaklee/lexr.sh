---
id: adrs-adr042
title: "ADR042: Ordered Stubble inventory and explicit support claims"
description: Preserve complete embedded inventories without treating shared optional selectors as qualified hardware support.
---

## Status

Proposed on 2026-10-21. Software qualification is in progress; this decision
does not record a hardware boot result.

This decision partially supersedes the flat selector-uniqueness interpretation
of [ADR029](adr-029-self-contained-kernel-dtb-delivery.md). Its complete payload,
package, provenance and exact-ABI lifecycle requirements remain. Installation
ownership remains governed by [ADR040](adr-040-installation-scoped-grub-ownership.md).

## Context

A real multi-device Stubble image can contain different optional payloads with
shared HWIDs, or variants with the same primary compatible string. A flat
selector-to-device bijection rejects such an image even when the selected
profile has an unambiguous payload. Removing overlapping records would hide
the actual image inventory and its provenance.

Stubble's [version-9 matcher](https://github.com/ubuntu/stubble/blob/6dee731dd7d20b2d8658b5385d2774955252bb73/chid.c)
evaluates the running board's CHIDs in priority order, then scans the encoded
device records. Its [PE chooser](https://github.com/ubuntu/stubble/blob/6dee731dd7d20b2d8658b5385d2774955252bb73/pe.c)
uses the first matching embedded payload. UUID sorting is not a specificity
ranking. A public source reference is context for the model, not proof that a
particular binary was reproduced from that source or that hardware booted.

The local manifest reader also had a 64 KiB ceiling while release manifests
already allowed 1 MiB. A complete inventory with detailed provenance can exceed
the smaller limit without exceeding any structural count or field bound.

## Decision

### Preserve schema 2; explicitly introduce schema 3

Schema-2 manifests retain their globally unique selection-route requirement.
Readers accept them without rewriting their meaning. Schema 3 requires an
explicit `stubble-inventory-order-v1` observation in selection provenance;
relabeling one schema as the other is rejected. Older clients may reject
schema 3 and must be upgraded before consuming it.

The observation records the whole image digest, exact virtual `.hwids` payload
digest, physical `.dtbauto` device order, and ordered type-1 HWID records. The
observer validates a bounded AArch64 PE32+ EFI image, actual FDT root compatible
strings, unique payload attribution, and the bounded HWID record/string
encoding. Firmware records and the table terminator are structurally checked;
the complete table digest also covers bytes not used as device-tree routes.

The existing inventory and raw selector/provenance records remain complete.
Device-sorted inventory and physical image order are separate facts. Neither
order may be inferred from a filesystem walk or overwritten by canonical
device sorting.

### Separate optional observations from supported-device claims

The first policy permits shared routes only among optional inventory entries.
Every required device retains globally unique routes and must not be shadowed
by an earlier payload with the same primary compatible. Missing or inconsistent
order, raw-record disagreement, unsupported policy, or an unsupported machdb
combination fails closed.

This is intentionally conservative. A future policy could qualify a required
device with shared lower-specificity routes using additional board-context
evidence. This policy does not invent that evidence or silently assume that
every shared HWID is harmless.

All optional payloads, including shadowed variants, remain byte-verified.
Schema-3 `BootPlatforms` exposes only explicit required-device claims; it does
not promote other observed payloads into tested or supported platforms. No
unrelated DTB becomes a fallback for the selected device.

### Apply the same contract to producer and installer

For an overlapping embedded inventory, the producer retains the exact inspected
image as temporary transaction evidence and derives the observation from those
bytes before publication. This is not an additional Debian package or a change
to the compiled kernel.

The installer owns copies of the reviewed observation and complete inventory.
Before reporting successful installation it re-observes the installed image,
payload order, compatible strings and encoded HWID records. Any disagreement
uses the existing rollback path. Manifest-only assertions cannot establish that
check. Existing initramfs, fallback, module, header and GRUB checks remain.

Reconstructing metadata for saved packages is new qualification evidence, not
a replacement for a failed original build receipt or missing original tool
measurements. Hardware tests remain a separate gate.

### Use one bounded manifest ceiling

Local and release readers share a 1 MiB manifest limit. Producer/composition
checks use that same constant. Regular-file, symlink, identity, digest,
unknown-field, trailing-data and semantic checks remain in force. Exceeding
the bound is still an error; neither record removal nor a force flag is a
substitute for a complete contract.

## Consequences

- Complete multi-device images can be represented without weakening required
  device claims or discarding source contributions.
- Software fixtures must cover optional overlaps, required shadowing, changed
  encoded order, malformed PE/FDT/HWID data and both schema versions.
- Resource-bound tests cover full inventories, the exact limit and an
  over-limit input; image/table observations are rechecked at installation.
- Supporting required devices with overlapping routes, machdb ordering, or a
  different selection algorithm needs a separately reviewed policy.
