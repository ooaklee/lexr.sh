# Carry the offline companion

An offline companion keeps the Linux ARM64 Lexr CLI, its matching maintained source, validated catalogues, and optionally the audited IPTSD release on the live image. It gives the booted session a verifiable local toolchain path when another download is unavailable or undesirable.

## Audience and context

Use this option while creating installation media when you expect to diagnose userspace support or install pen and touchscreen support from the live session. This page covers only the companion payload; follow [Create and write installation media](installation-media.md) for the base remaster, validation, USB safety, and hardware compatibility gates.

## Prerequisites and limits

- `--companion-source-dir` must identify a complete `lexr` source tree and requires a working host Go toolchain.
- Keep the image output, its sidecars, and any explicit `--workspace-dir` outside that source tree.
- The source is snapshotted before its binary and archive are built, and a clean Git-backed tree must match the CLI's recorded commit.
- `--companion-userspace` is repeatable, but the initial offline allow-list accepts only `iptsd`. `recommended`, restricted audio, platform firmware, and experimental camera packages are not accepted for on-media inclusion.

## 1. Add the companion during image creation

An image can carry the exact Linux ARM64 CLI, a deterministic archive of the corresponding maintained source, and validated copies of both catalogues. Add the audited IPTSD release when offline pen and touchscreen installation is useful:

```sh
mkdir -p ../lexr-build
lexr image create \
  --source resolute-desktop-arm64+x1e-20260326.iso \
  --source-sha256 <sha256> \
  --kernel-release <release-tag> \
  --companion-source-dir . \
  --companion-userspace iptsd \
  --output ../lexr-build/lexr-ubuntu-sp11.iso
```

The example keeps its output outside the source tree and requests the only userspace component currently accepted for on-media inclusion, as required by the limits above.

## 2. Know what the image carries

The payload is stored under `/sp11/companion`. The embedded `/sp11/lexr-manifest.json` and the ISO-adjacent `*.iso.manifest.json` sidecar are byte-identical copies of the only image inventory; their mandatory `companion_bundle` attribute records every companion file. The deterministic source archive includes the strict Windows hand-off collector at `lexr/tools/collect-sp11-windows-handoff.ps1`. The first path component is the schema-4 archive root; the archive never contains collected device data. The portable receipt inside an IPTSD release verifies that component's relocatable files and is itself included in the outer inventory. It is not a second image manifest.

Lexr is provided under `Apache-2.0`. A locally requested companion records `project_licence: declared`, inventories `LICENSE`, `NOTICE`, and `THIRD_PARTY_NOTICES.md`, and carries them both in its source archive and its on-media licence directory.

The repository root is the single legal-document authority for companion images and CLI releases. A Git-backed companion requires the complete repository to be clean so those documents match its recorded source revision. The [project-boundaries reference](../reference/project-boundaries.md#what-the-lexr-release-contains) owns the exact CLI release set; [ADR026](../adr/adr-026-apache-2.0-project-terms-and-release-notices.md) records its relationship to the kernel's separate terms.

## 3. Start Lexr from the live medium

After booting the live image, copy the executable from the read-only medium to a writable executable filesystem before using it:

```sh
COMPANION_ROOT=/cdrom/sp11/companion
TOOL=/tmp/lexr

install -m 0755 \
  "$COMPANION_ROOT/bin/linux-arm64/lexr" \
  "$TOOL"

"$TOOL" version
"$TOOL" catalog validate \
  "$COMPANION_ROOT/catalogues/supported-isos.json"
"$TOOL" userspace catalog validate \
  "$COMPANION_ROOT/catalogues/supported-userspace.json"
"$TOOL" doctor userspace
```

The source path uses the schema-4 companion filename; the copy is invoked as `lexr` through the writable `$TOOL` path.

A non-zero userspace doctor result means support is still missing; it does not by itself mean the companion is damaged.

## 4. Install the included IPTSD support

If IPTSD was included, first verify its installation plan and then apply it to the live session:

```sh
IPTSD_ROOT="$COMPANION_ROOT/userspace/iptsd-v1/sp11-iptsd-v1"

"$TOOL" userspace install iptsd --from "$IPTSD_ROOT" --dry-run
sudo "$TOOL" userspace install iptsd --from "$IPTSD_ROOT" --yes
"$TOOL" doctor userspace --feature iptsd
```

For an installed system mounted at `/target`, add `--root /target` to the install and doctor commands.

## Success and next steps

The companion is usable when the copied `$TOOL` reports its version, both on-media catalogues validate, and the userspace doctor gives the expected point-in-time result. If IPTSD was included, review the dry run before installation and check that feature again afterwards.

Continue with [userspace support](userspace-support.md), [the private Windows hand-off](windows-handoff.md), or return to [installation media](installation-media.md).
