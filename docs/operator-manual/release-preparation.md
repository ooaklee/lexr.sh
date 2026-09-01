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
| FullIO audio | Reviewed FullIO v19c source bytes and an explicit paired kernel tag and ABI | Exact seven-file local audio release | [Prepare an audio release](#prepare-an-audio-release) |
| IMX681 camera | Validated eight-file native camera build, authenticated inputs, independently retained build authority, and explicit paired kernel tag and ABI | Exact eleven-file local camera release plus a separately retained release authority digest | [Prepare a camera release](#prepare-a-camera-release) |

## Prepare an image release

`image release prepare` starts from one completed Lexr ISO. It proves that the manifest bytes embedded in the ISO equal the adjacent `*.iso.manifest.json`, then produces deterministic split zstd parts, checksums, release notes, and a path-free release manifest in a fresh directory.

The ISO's existing manifest remains the single image inventory, including its `companion_bundle` attribute. Preparation does not introduce a second companion authority. The original path-bearing creation journal is not published; the release manifest carries its path-free evidence instead.

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

This is structural evidence, not proof that the image booted on physical hardware. [ADR017](../adr/adr-017-native-image-release-preparation.md) records the image release and recovery contract.

## Prepare an audio release

Audio preparation accepts only the reviewed FullIO v19c source bytes and an explicit paired kernel release tag and ABI. Its seven-file release contains the four reviewed installable audio artefacts, `SHA256SUMS`, deterministic `RELEASE-NOTES.md`, and `audio-release-manifest.json`. The manifest records the pairing and source evidence without a host path or preparation time.

Start with a dry run:

```sh
lexr userspace audio release prepare \
  --source-root <SP11X1e-audio-checkout> \
  --repository-root <oe-checkout> \
  --tag sp11-audio-v19c \
  --kernel-tag <kernel-release-tag> \
  --kernel-abi <kernel-abi> \
  --dry-run
```

Remove `--dry-run` only after reviewing the complete plan, then repeat the source, pairing, and artefact proofs:

```sh
lexr userspace audio release validate \
  <oe-checkout>/build/release/sp11-audio-v19c \
  --repository-root <oe-checkout>
```

The generated checksum set must retain the reviewed FullIO v19c identity; locally generating it does not make different source bytes authoritative. [ADR019](../adr/adr-019-native-audio-release-preparation.md) records the exact seven-file contract.

## Prepare a camera release

Camera preparation accepts one validated native camera build, its authenticated repository inputs, an independently retained build-authority SHA-256, and an explicit paired kernel tag and ABI. It does not execute package payload while proving the transferred build.

The native build is an exact eight-file set: five coherent runtime packages, the original Debian `.changes` and `.buildinfo` records, and the structured build receipt. Preparation adds `SHA256SUMS`, deterministic release notes, and a path-free release manifest to form an eleven-file local release.

Review the preparation plan:

```sh
lexr userspace camera release prepare \
  --from <native-camera-build> \
  --repository-root <oe-checkout> \
  --tag <camera-release-tag> \
  --kernel-tag <kernel-release-tag> \
  --kernel-abi <kernel-abi> \
  --build-authority-sha256 <native-build-authority-sha256> \
  --dry-run
```

After a successful mutating run, Lexr prints a release-authority SHA-256. Retain it independently; a manifest stored beside the packages cannot authenticate itself. Pass that exact digest to validation:

```sh
lexr userspace camera release validate \
  <oe-checkout>/build/lexr/camera/releases/<camera-release-tag> \
  --repository-root <oe-checkout> \
  --authority-sha256 <release-authority-sha256>
```

The release manifest makes package provenance and kernel pairing explicit but does not claim camera transport, privacy indication, image quality, suspend recovery, or any other physical-hardware qualification. [ADR015](../adr/adr-015-native-imx681-package-and-release-contracts.md) defines the package and release set; [ADR020](../adr/adr-020-independent-camera-authority-digests.md) defines the independent authority chain.

## Hand off to publication without widening authority

All four preparation paths stop at a validated local directory. Kernel and other hardware-support assets belong on the established OE channel; Lexr's own `v*` releases contain only the CLI release set. The only current cross-repository publication credential is confined to the hosted OE publication step described in [automation and release channels](automation-and-releases.md).

Do not interpret a successful local receipt as permission to publish. The publication operator still has to select the correct channel, verify the complete remote bytes, and preserve the tag, draft, and hardware-qualification boundaries for that release.
