---
id: adrs-adr021
title: "ADR021: Windows hand-off v2 original-INF authority"
description: Architecture decision for the strict version 2 Windows hand-off, SMBIOS-only device binding, collector verification, and release packaging.
---

## Status

Accepted on 2026-08-30. Superseded as the current interchange version by
[ADR023](adr-023-lexr-standalone-repository-and-compatibility.md) on
2026-08-31; its original-INF and privacy decisions remain current in version 3.

Terminology, wire names, store paths and archive names in this record have been
normalised to Lexr.sh following
[ADR023](adr-023-lexr-standalone-repository-and-compatibility.md).

## Context

The first private Windows hand-off contract selected each platform firmware payload by its filename across every active signed DriverStore package. Surface Pro 11 Windows evidence shows that an identical filename can occur in more than one original driver package, so filename-only discovery can become ambiguous or select the wrong provenance. A published `oemN.inf` name is also a mutable installation alias rather than stable package authority.

Version 1 additionally exported a salted digest of the selected Bluetooth adapter instance identifier. Linux did not consume that field during application, so retaining it created an opaque privacy-sensitive claim without enforcing an end-to-end property. The only enforced same-device boundary is the salted SMBIOS product UUID binding checked against the application identity root.

The contract is still unpublished, so correcting both issues before a supported release is preferable to preserving a misleading compatibility shape. The Windows collector must also be tested and distributed wherever the Linux CLI release promises this workflow. [ADR022](adr-022-privileged-windows-collection-and-controller-authority.md) separately defines the collector's privileged output transaction and the physical-controller authority required on both Windows and Linux.

## Decision

At acceptance, the hand-off contract moved directly to schema version 2. Collector `2.0.0` was its first canonical producer, and that decoder accepted only version 2 documents. There was no version 1 import, application, or migration claim.

> **Implementation amendment (2026-08-31):** The complete Lexr naming boundary
> advances the active hand-off to schema version 3 and collector `3.0.0`.
> Versions 1 and 2 are unpublished predecessor contracts and are not accepted
> by the current importer, store, purge or application paths. The strict
> original-INF authority, privacy limits and physical-controller selection in
> this decision remain unchanged.

Every platform firmware policy fixes an authoritative canonical lowercase original INF basename in addition to the source filename, payload path, and Linux destination:

- GPU payloads must come from `qcdx8380.inf`.
- aDSP and battery-manager payloads must come from `surfacepro_ext_adsp8380.inf`.
- cDSP payloads must come from `qcnspmcdm_ext_cdsp8380.inf`.

The collector derives the original INF basename from the active structured DriverStore record, canonicalises it to lowercase, and compares it using exact ordinal semantics before searching that package for a source filename. Each included file emits the basename as mandatory `windows_source.original_inf`. The Go validator checks both its canonical form and its exact compiled policy value. The published `oemN.inf` alias remains provenance but is not selection authority.

Version 2 removes `bluetooth_public_address.adapter_instance_id_binding_sha256`. The selected Windows network-adapter instance identifier remains private, in-memory collection evidence used to prove `PermanentAddress` ancestry to the chosen physical radio and to prove that the collector did not persist the raw identifier. It is exported as neither raw text nor a digest. An included Bluetooth section contains only the canonical public address and its compiled provenance type. Under ADR022, collection requires the exact built-in WCN7850 radio and transport, derives `PermanentAddress` only through structured PnP ancestry to that radio, and permits BTHPORT only as an exact corroborating value.

The fresh random salt and domain-separated SMBIOS product UUID binding remain unchanged. Application re-derives this binding from the chosen identity root and treats it as the same-device boundary for both firmware and Bluetooth operations. The CLI does not claim cryptographic correspondence between a Windows adapter instance and a particular Linux controller. Instead, ADR022 gives Linux an independent compiled device-tree selector for the built-in radio and removes numeric HCI enumeration order from the application contract.

The version 2 implementation temporarily retained a maintenance-only path for
an exact pre-release version 1 store entry. Version 3 removes that decoder so
the standalone tree has one product and wire identity. Operators with version
1 or version 2 state must use the exact predecessor binary which created it to
complete a reviewed purge before upgrading, then recollect with `3.0.0`.
Recursive manual deletion is not a supported cut-over step.

Windows CI runs the collector's host-independent self-test under Windows PowerShell and the pinned Pester contract suite under PowerShell 7. The tests pin the complete Go-aligned policy, duplicate-source-name disambiguation, the emitted version 3 manifest shape, built-in radio selection, protected output transactions, file copying, encoding, and binding vector. Go tests pin strict current decoding, mandatory original-INF fields, exact policy validation, rejection of predecessor schemas and the retired adapter-digest field, and device-tree selection which cannot be captured by an external `hci0`.

Every GoReleaser platform archive includes the ordinary non-private collector script alongside the Lexr binary, catalogues, and documentation. Collected hand-off manifests and proprietary payloads remain prohibited from release archives and ISO companion bundles.

> **Implementation amendment (2026-08-31):** ADR024 supersedes the release-
> archive delivery clause above. Lexr Releases now contain only raw Lexr
> platform binaries and their checksum manifest. The collector remains in the
> source tree and manifest-tracked companion source archive, and Windows CI
> continues to gate CLI publication against its contract tests.

## Consequences

- Stable original INF identity, rather than a mutable published alias or duplicate filename, becomes firmware selection authority.
- The exported contract contains no unused opaque adapter-identity digest and makes only the same-device claim that Linux actually enforces.
- Privileged collection storage and physical Bluetooth selection remain explicit policy under ADR022 rather than implicit assumptions inside the interchange schema.
- Operators must purge pre-release version 1 or version 2 state with the exact predecessor binary before upgrading and recollecting; automated migration could incorrectly preserve ambiguous provenance and is deliberately unavailable.
- Windows contract drift blocks CLI releases through the dedicated CI job, while the matching collector remains available from the tagged source and companion source archive.
- Host-independent CI does not establish maintained-hardware success. Collection from a supported Windows installation, transfer, import, same-device application, Bluetooth programming, firmware loading, restoration, and cold-boot behaviour remain explicit Surface Pro 11 release gates.
