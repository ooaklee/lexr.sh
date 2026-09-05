# Manage userspace support

The userspace companion answers the question that image creation cannot: after installing a custom kernel, which supporting components are present, missing, obsolete, or outside the CLI's redistribution boundary? Use it to inspect first, then pull, build, or install only the support you have selected.

## Audience and context

This page is for operators checking firmware, audio, pen, touchscreen, camera, power, and related support on a live or installed system. It also covers contributors preparing local userspace releases. Userspace installation never removes legacy workarounds implicitly; [reversible clean-up](reversible-cleanup.md) remains a separate reviewed transaction.

[`supported-userspace.json`](https://github.com/ooaklee/lexr.sh/blob/main/supported-userspace.json) is the human-readable source of audited component metadata. It records support maturity, capability, redistribution policy, evidence, remediation, exact release assets where applicable, and the bounded actions available through the CLI. Action flags are declarations only; the catalogue cannot supply commands or writable paths. The dedicated loader rejects unknown fields and inconsistent combinations.

## 1. Review the catalogue

Review and validate the catalogue with:

```sh
lexr userspace list
lexr userspace show firmware
lexr userspace catalog validate supported-userspace.json
```

The validation command expects a catalogue file from a source checkout or an
offline companion. Ordinary list and show commands use the catalogue embedded
in the executable.

## 2. Inspect current support

`userspace status` and `doctor userspace` use the same static inspector. They examine filesystem, package, kernel-compatibility, boot-argument, and ELF dependency state below the selected target root and do not start services, execute target binaries, contact the network, probe live devices, or modify the system. Use `--kernel` to select a Surface kernel ABI explicitly, `--feature` to limit the report, and `--json` for automation. Accepted feature names are `kernel`, `firmware`, `wifi`, `bluetooth`, `audio`, `iptsd`, `g6-pen`, `touchscreen`, `camera`, and `power`.

```sh
lexr doctor userspace
lexr userspace status --feature audio --feature iptsd
lexr userspace status --json
```

With no feature filter, only catalogue-required support blocks readiness. When a supported or experimental feature is selected explicitly, its failed checks also produce a failing exit status so scripts receive a useful result. Diagnostic-only and obsolete checks remain clearly labelled and never make the complete default report claim that those components are required.

SP11 generation numbers belong to a kernel patch line; they are not one global
counter. The existing userspace evidence covers the `7.2.0` line through
`sp11v19`, while the `7.2.2` line starts again at `sp11v1`. Until that newer
kernel and userspace pairing has been qualified, the status commands preserve
its exact ABI and report a warning. They do not reject it for being numerically
below `sp11v19`, or pretend that `sp11v1` means `sp11v20`.

An alternate or mounted target root must be trusted and quiescent while it is inspected. The doctor resolves symbolic links and confines its static reads at each check, but its report is a point-in-time diagnosis rather than a sandbox for a concurrently hostile filesystem.

## 3. Pull or build selected components

`userspace pull` accepts one supported component or `recommended`. A pull succeeds only when the remote release contains the exact audited asset set, the checksum manifest matches its release digest, and every installable payload is covered by `SHA256SUMS` with matching release and publisher digests. Verified assets and a bundle manifest are published atomically to the local cache.

The recommended set deliberately contains the supported audio release and pinned `iptsd` integration. The camera package set is experimental and remains an explicit opt-in. Restricted platform firmware is never downloaded by `lexr`; acquire it from an authorised source and use the status report to verify its presence.

IPTSD activation retires the legacy `g6-pen.service` when present. If its
cleanup command fails, Lexr checks systemd's unit state: an absent, inactive
unit with no unit-file state is already clean. An existing unit, failed or
incomplete inspection, or timeout still produces an activation error.

```sh
lexr userspace pull recommended
lexr userspace pull camera
lexr userspace build iptsd
lexr userspace build camera
```

Source builds invoke only compiled, component-specific adapters with bounded arguments. Catalogue content is never interpreted as a shell command. The current camera package build requires a native ARM64 Linux host; users on other hosts can still pull and verify the published experimental package set.

Both userspace source-build adapters use compiled Go policy and do not invoke repository scripts. They require the complete OE checkout because the native builders authenticate their tracked component inputs; pass `--repository-root <oe-checkout>` when it cannot be detected from the current directory. Pull, status, doctor, and installation from a downloaded immutable release do not have that checkout requirement. A successful native camera build prints an independent authority SHA-256 for its final receipt. Retain it separately from the directory. Installing a native camera build or prepared local camera release repeats static Git-backed provenance proof and therefore requires both the explicit repository root and the corresponding independently retained authority digest.

## 4. Review and install

### Wi-Fi from distribution firmware

When `lexr doctor hardware wifi` reports a board-data failure, derive the
qualified Surface Pro 11 fallback from the selected root's existing
`linux-firmware` package:

```sh
lexr userspace install wifi --dry-run
sudo lexr userspace install wifi --yes
```

This workflow needs no `--from` release, Windows files, previous installation,
or network download. It parses a bounded `board-2.bin` or decompresses its
`.zst` variant with the installed `zstd` tool. It prefers the exact Surface
entry when present; otherwise it extracts the single entry qualified by the
OE board fixup. It preserves `board-2.bin`, backs up an existing `board.bin`,
and records source and derived digests in a private receipt. Repeating the
command with matching data leaves the file in place.

Prepare the board data before booting the installed system:

```sh
lexr userspace install wifi --dry-run
sudo lexr userspace install wifi --yes
```

Reboot the installed system afterwards, then run `lexr doctor hardware wifi`.
Ubuntu image creation now performs the same derivation before building the live
and installed initramfs, so the radio can find its board data on its first probe.
The distribution's `board-2.bin` retains priority, including any native SP11
entry; the image also carries the selected calibration bytes as `board.bin`.

The optional `--activate` restart is restricted to an initialised X1E/OLED radio
using the legacy `ath12k_pci` driver. Reloading the split `ath12k_wifi7_pci`
driver after missing board data caused an MHI/QMI kernel crash during SP11
testing, so Lexr refuses that layout until reload is separately qualified.
Eligibility depends on hardware and driver state, without a kernel-version
allow-list. Unknown layouts, built-in modules and failed initial probes cannot
be restarted. Do not retry a failed activation; save diagnostics and reboot.

For a mounted installed root, use `--root /target` without `--activate`, then
boot that system. A non-persistent live session loses its changes at reboot;
use a rebuilt ISO with board data already included. No Wi-Fi setup command
restarts NetworkManager, the DSP or USB.

### Released audio, IPTSD and camera support

Review an install before granting elevated access. A real install requires both effective root privileges and `--yes`; the CLI does not elevate itself. `--root` may select an alternate target filesystem where the component supports it.

```sh
lexr userspace install recommended --from <userspace-cache> --dry-run
sudo lexr userspace install recommended --from <userspace-cache> --yes

lexr userspace install camera \
  --from <native-camera-build-or-local-release> \
  --repository-root <oe-checkout> \
  --camera-authority-sha256 <matching-build-or-release-authority-sha256> \
  --dry-run
sudo lexr userspace install camera \
  --from <native-camera-build-or-local-release> \
  --repository-root <oe-checkout> \
  --camera-authority-sha256 <matching-build-or-release-authority-sha256> \
  --yes
```

The camera installer also retains compatibility with the downloaded, checksum-pinned camera release and does not accept the native-only repository or authority options for that input shape. Native camera input is selected only by its structured build or local-release authority; mixed authority files are rejected. Static validation never executes a supplied package member. The installer verifies every package again while privately staging the confirmed `apt-get` transaction. Installing support never invokes legacy clean-up implicitly. Use the separate `clean` commands to inspect and remove only recognised obsolete workarounds with backups and receipts.

## 5. Prepare a release as a maintainer

Local audio and camera release preparation has separate source, kernel-pairing
and authority requirements. If you are assembling assets for publication, move
to the operator manual's [release-preparation guide](../operator-manual/release-preparation.md)
rather than treating an installed component as a releasable input.

## 6. Inspect the experimental camera

`userspace camera capture` discovers the exact supported IMX681 media route, can validate it without changing the graph, and otherwise captures complete private 3840×2640 packed-RAW10 frames behind bounded transport, content, temporal, and kernel-error gates. It does not claim that the privacy LED or a cold-boot lifecycle passed; those remain manual hardware gates.

```sh
lexr userspace camera capture --dry-run
lexr userspace camera capture --output capture.raw --frames 10
lexr userspace camera render capture.raw preview.png
```

Capture output and rendered previews may contain sensitive imagery. New files are private on Unix hosts; keep them out of source control, releases, issue attachments, and diagnostics unless their contents have been reviewed deliberately.

## Success and next steps

Use the status or doctor exit result that matches the scope you selected: the unfiltered report blocks only on catalogue-required support, while an explicitly selected supported or experimental feature also fails when its checks fail. Treat camera output as private and hardware qualification as a separate manual gate.

If the report finds recognised obsolete workarounds, continue with [reversible clean-up](reversible-cleanup.md). For restricted platform firmware and Bluetooth evidence, use the [private Windows hand-off](windows-handoff.md). For a live session, see the [offline companion](offline-companion.md).
