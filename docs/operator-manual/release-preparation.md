# Release preparation

Preparing release assets is not the same act as publishing them. Lexr's preparation commands turn already reviewed local inputs into fresh, closed directories which another operator or workflow can validate. They do not create a Git tag, upload an artefact, change a remote service, install anything, or elevate privilege.

Use this page to choose and run the correct local preparation path. Kernel builders should also read [kernel management](kernel-management.md); remote release operators should read [automation and release channels](automation-and-releases.md).

## Use the same safe sequence for every release family

1. Select the exact source set and a fresh destination which does not exist.
2. Run the preparation command with `--dry-run` and review the source identities, release name, kernel pairing, authority digest, and destination which apply to that release family.
3. Remove `--dry-run` only when the plan is correct. Preparation repeats its input checks and publishes through a private transaction rather than merging into an existing directory.
4. Run the matching `release validate` command against the completed directory.
5. Retain receipts and any independently printed authority digest separately from the directory they authenticate.
6. Treat remote publication and physical-hardware testing as later, separately authorised gates.

If preparation fails, do not assemble a release by copying part of its staging data. The supported preparers use fresh no-replace destinations; inspect the reported result, keep the source evidence, and rerun only with a reviewed absent destination. A structurally valid release still does not claim that its contents passed a Surface Pro 11 hardware test.

## Choose the release family

| Release | Authoritative input | Local result | Where to continue |
| --- | --- | --- | --- |
| Installation image | One completed Lexr ISO and its exact adjacent manifest and creation journal | Deterministic split zstd parts, the copied image manifest, path-free release manifest, notes, and checksums | [Prepare an image release](#prepare-an-image-release) |
| Kernel | Exact closed native kernel build, corresponding-source archives, and explicit licence text | Closed, path-free kernel release directory | [Prepare a kernel release](kernel-management.md#prepare-a-kernel-release-locally) |
| FullIO audio | Reviewed FullIO v19c source bytes and an explicit paired kernel tag and ABI | Exact eight-file local audio release | [Prepare an audio release](#prepare-an-audio-release) |
| IMX681 camera | Validated nine-file native camera build, authenticated inputs, independently retained build authority, and explicit paired kernel tag and ABI | Exact twelve-file local camera release plus a separately retained release authority digest | [Prepare a camera release](#prepare-a-camera-release) |

## Prepare an image release

`image release prepare` starts from one completed Lexr ISO. It proves that the manifest bytes embedded in the ISO equal the adjacent `*.iso.manifest.json`, then produces deterministic split zstd parts, checksums, release notes, and a path-free release manifest in a fresh directory.

Ubuntu, elementary OS, Fedora and Arch Linux ARM use this same release path.
Each image must retain its own producer's complete creation journal; another
distribution's journal cannot substitute for it. For Arch, build the terminal
ISO from the [pinned rootfs snapshot](arch-linux-arm-source.md) first, then pass
the resulting `.iso` and its adjacent sidecars to release preparation.

The ISO's existing manifest remains the single image inventory, including its `companion_bundle` attribute. Preparation does not introduce a second companion authority. The original path-bearing creation journal is not published; the release manifest carries its path-free evidence instead.

Fedora rc.3 journals placed the ISO volume label and GRUB search marker in the
`bind-live-media` digest map even though those values are not hashes. Preparation
accepts this specific legacy pair only when both values exactly match the
manifest's typed media-discovery evidence. Their public representation remains
in the image contract; the digest-only journal projection omits the pair.
The original journal stays unchanged and its complete file identity remains
in the private preparation plan. Other journal values still require valid
SHA-256 digests. New Fedora builds record these strings only in the manifest.

Review the plan and validate the resulting closed directory:

```sh
lexr image release prepare lexr-ubuntu-sp11.iso \
  --repository-root ../lexr-build \
  --release-name <release-name> \
  --out-dir build/release/<release-name> \
  --dry-run

lexr image release validate ../lexr-build/build/release/<release-name>
```

Here `--repository-root` is the containment root which already holds the ISO and its two adjacent sidecars; it does not need to be an OE checkout. The relative ISO and output paths are resolved beneath that root.

After reviewing the source identity and fresh destination, remove `--dry-run` to create the release. Independent validation checks the exact member and checksum set, verifies every ordered compressed part, and reconstructs the complete ISO digest and size without publishing it. The closed directory contains only the copied image-manifest sidecar, `image-release-manifest.json`, `RELEASE-NOTES.md`, `SHA256SUMS`, and the declared parts.

Set the release title in GitHub's title field. Newly generated `RELEASE-NOTES.md`
starts directly with the introduction so the body does not repeat that title.
It also links to the Windows image download and USB guide; Linux and macOS users
follow the Lexr commands in the notes. Existing prepared rc.3 assets remain valid
with the exact historical `# Surface Pro 11 ARM64 installation image` heading
and original manifest-derived body, without the new Windows footer. Keep those
checksummed assets unchanged; validation does not permit other headings or
edited guidance merely because their checksums were recalculated.

This is structural evidence, not proof that the image booted on physical hardware. [ADR017](../adr/adr-017-native-image-release-preparation.md) records the image release and recovery contract.

## Prepare an audio release

Audio preparation accepts only the reviewed FullIO v19c source bytes and an explicit paired kernel release tag and ABI. Its eight-file release contains the four reviewed installable audio artefacts, the canonical `lexr-component-compatibility.json`, `SHA256SUMS`, deterministic `RELEASE-NOTES.md`, and `audio-release-manifest.json`. The manifest records the pairing and source evidence without a host path or preparation time.

The OE checkout must have a clean HEAD containing the reviewed source declaration. All new preparations require explicit payload evidence. These initial declarations have no recorded OS qualification, so the examples deliberately record `--allow-unverified-compatibility`; review the decision before using it. Hard incompatibility and missing evidence still block. See [component compatibility](../reference/component-compatibility.md).

Start with a dry run:

```sh
lexr userspace audio release prepare \
  --source-root <SP11X1e-audio-checkout> \
  --repository-root <oe-checkout> \
  --tag sp11-audio-v19c-full \
  --kernel-tag <kernel-release-tag> \
  --kernel-abi <kernel-abi> \
  --target-architecture arm64 \
  --target-device-profile x1e80100-microsoft-denali-oled \
  --target-os ubuntu --target-os-version 26.04 \
  --allow-unverified-compatibility \
  --dry-run
```

Remove `--dry-run` only after reviewing the complete plan, then repeat the source, pairing, and artefact proofs:

```sh
lexr userspace audio release validate \
  <oe-checkout>/build/release/sp11-audio-v19c-full \
  --repository-root <oe-checkout>
```

The four payload identities remain pinned. New schema-2 preparation records also bind the canonical declaration and assessment; [ADR041](../adr/adr-041-component-compatibility-manifests.md) defines this migration. The original seven-file release described by [ADR019](../adr/adr-019-native-audio-release-preparation.md) remains valid for legacy validation only.

## Prepare a camera release

Camera preparation accepts one validated native camera build, its authenticated repository inputs, an independently retained build-authority SHA-256, and an explicit paired kernel tag and ABI. It does not execute package payload while proving the transferred build.

The native build is an exact nine-file set: five coherent runtime packages, the original Debian `.changes` and `.buildinfo` records, the structured build receipt and the canonical compatibility manifest. Preparation adds `SHA256SUMS`, deterministic release notes, and a path-free release manifest to form a twelve-file local release.

Review the preparation plan:

```sh
lexr userspace camera release prepare \
  --from <native-camera-build> \
  --repository-root <oe-checkout> \
  --tag sp11-imx681-libcamera-v2 \
  --kernel-tag <kernel-release-tag> \
  --kernel-abi <kernel-abi> \
  --target-architecture arm64 \
  --target-device-profile x1e80100-microsoft-denali-oled \
  --target-os ubuntu --target-os-version 26.04 \
  --allow-unverified-compatibility \
  --build-authority-sha256 <native-build-authority-sha256> \
  --dry-run
```

After a successful mutating run, Lexr prints a release-authority SHA-256. Retain it independently; a manifest stored beside the packages cannot authenticate itself. Pass that exact digest to validation:

```sh
lexr userspace camera release validate \
  <oe-checkout>/build/lexr/camera/releases/sp11-imx681-libcamera-v2 \
  --repository-root <oe-checkout> \
  --authority-sha256 <release-authority-sha256>
```

The release manifest makes package provenance and kernel pairing explicit but does not claim camera transport, privacy indication, image quality, suspend recovery, or any other physical-hardware qualification. [ADR041](../adr/adr-041-component-compatibility-manifests.md) extends the original package and release set from [ADR015](../adr/adr-015-native-imx681-package-and-release-contracts.md) with schema-2 compatibility authority; [ADR020](../adr/adr-020-independent-camera-authority-digests.md) defines the independent authority chain.

## Hand off to publication without widening authority

All four preparation paths stop at a validated local directory. Kernel and other hardware-support assets belong on the established OE channel; Lexr's own `v*` releases contain only the CLI release set. The only current cross-repository publication credential is confined to the hosted OE publication step described in [automation and release channels](automation-and-releases.md).

Do not interpret a successful local receipt as permission to publish. The publication operator still has to select the correct channel, verify the complete remote bytes, and preserve the tag, draft, and hardware-qualification boundaries for that release.
