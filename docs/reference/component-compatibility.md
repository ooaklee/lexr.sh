# Component compatibility

New userspace releases carry an authenticated
`lexr-component-compatibility.json`. It records the Lexr releases, operating
systems, architecture, device profile and scoped kernel generations assessed
for that exact component release. See
[ADR040](../adr/adr-040-component-compatibility-manifests.md) for the authority
and migration contract.

## Read the decision

| JSON status | Meaning | Installation |
| --- | --- | --- |
| `tested` | One complete rule matches recorded evidence | Allowed |
| `unverified` | Hard bounds match, but some evidence is absent or older | Dedicated override required |
| `incompatible` | A known hard bound or complete target rule does not match | Blocked |
| `unavailable` | Required identity or supported metadata is missing or malformed | Blocked |

A tested-through version is not an upper compatibility limit. Hard ceilings
use `maximum_exclusive`. Lexr compares SemVer prereleases normally and ignores
runtime build metadata for precedence. Development, dirty and noncanonical
build identities remain unverified. Kernel generations have meaning only
inside the same patch line, platform flavour and registered scope.

The target's exact `/etc/os-release` `ID` and `VERSION_ID` select the OS rule.
A derivative does not inherit Ubuntu compatibility through `ID_LIKE`.

`device_profiles` uses the canonical IDs from `lexr profile list`:
`x1e80100-microsoft-denali-oled` for X Elite OLED and
`x1p64100-microsoft-denali` for X Plus LCD. The audio, pen and camera
declarations include both IDs. Supply these IDs to `--device-profile` and
`--target-device-profile`; kernel platform aliases are not compatibility IDs.
Device detection and image creation record the same canonical identities.

## Inspect and install offline

For a new manifest-aware release, inspect the selected target and an already
verified local bundle. Supply missing target evidence explicitly:

```sh
lexr userspace status --root /mnt/target --feature iptsd \
  --from /path/to/verified-bundle --architecture arm64 \
  --device-profile x1e80100-microsoft-denali-oled \
  --kernel 7.2.0-jg-0sp11v19-qcom-x1e --json

lexr userspace install iptsd --root /mnt/target \
  --from /path/to/verified-bundle --architecture arm64 \
  --device-profile x1e80100-microsoft-denali-oled \
  --kernel 7.2.0-jg-0sp11v19-qcom-x1e --dry-run --json
```

These values are examples; choose the actual target. Lexr reads the selected
root's OS identity, never the host's identity. `doctor userspace` shares the
same read-only evaluator. Successful manifest-aware installs retain the
manifest and assessment under the target's fixed
`/var/lib/lexr/userspace/<component>/` diagnostic directory.

If the decision is `unverified`, review its reasons before explicitly adding
`--allow-unverified-compatibility`. The flag records that choice in the plan
and receipt. It does not override missing identity or a hard incompatibility.
`--yes` only confirms the filesystem change. Retain the transaction receipt
and component bundle when diagnosing or recovering an installation.

## Existing releases and new packaging

The exact pinned audio v19c, IPTSD v2 and camera v1 bundles retain their compiled
legacy installation and diagnostic paths. Do not add a manifest to a historic
release or modify its receipt. New source declarations describe future
packaging of existing payloads; they do not retroactively change an older
Lexr release's behaviour.

For new releases, the source declaration, checksum coverage, catalogue digest
and compiled payload policy must agree. A pull verifies that agreement without
comparing the download computer, so you can prepare another target. An image
companion evaluates the extracted image's OS and selected device and kernel.

A successful software check establishes structural behaviour. It does not
qualify speakers, pen input, cameras, suspend or installation on physical
hardware. Host command availability is also separate: a compatible component
does not make a Linux-only install command available on another host OS.

## Build and prepare a new release

Build and release commands authenticate the fixed source declaration under
`userspace/compatibility/<component>/` from a clean OE repository HEAD. Lexr
also pins its exact bytes independently. Editing the declaration requires a
reviewed pin update in Lexr; an editable receipt cannot substitute for that
review. Current declarations cover the next packaging identities
`sp11-audio-v19c-full`, `sp11-iptsd-v3` and `sp11-imx681-libcamera-v2`.

Declare `--target-architecture`, `--target-device-profile`, `--target-os` and
`--target-os-version`. Builds also require `--target-kernel`; release
preparation uses the existing `--kernel-abi` pairing. Camera and IPTSD build
payloads must match the compiled ARM64 Ubuntu 26.04 builder. The invoking
host's OS never supplies payload compatibility. Lexr obtains the producer
version from its own build identity, so a development binary requires the
same dedicated override as other unverified evidence.

Initial declarations leave `tested_versions` empty. Preparing new packaging
therefore requires a deliberate `--allow-unverified-compatibility` even when
using previously shipped payload bytes. This records an experiment, not a
new OS or hardware qualification for either profile. If qualification later
differs between profiles, record separate complete target rules so evidence
for one device does not qualify the other.

Audio release schema 2 includes the declaration among eight files. Camera
build schema 2 contains nine files and camera release schema 2 contains twelve;
both bind the declaration, full payload tuple and override to the independent
receipt digest. Release preparation rejects a tuple different from the build.
Current native camera validation requires schema 2; existing exact catalogue
camera downloads retain their legacy path. See the
[release preparation commands](../operator-manual/release-preparation.md).

IPTSD builds retain their existing closed `stage/` payload and add the canonical
declaration, `iptsd-build-compatibility.json`, and an outer `SHA256SUMS` beside
it. The record binds the payload checksum manifest and producer decision.
These are build records; they do not turn a local source build into an
installable release. A future downloadable IPTSD package still needs its
reviewed catalogue and compiled payload pins before Lexr will consume it.

Image manifests use schema 6 to retain optional compatibility assessments.
The image workflow reads the extracted target's OS and combines it with the
explicit profile, architecture and selected kernel. When evidence is
unverified, `image create --allow-unverified-compatibility` records the
exception; the general `--yes` flag does not provide it.

Existing schema-5 images remain readable only with their legacy companion
records; compatibility assessments require schema 6. Unknown schemas still
fail closed.
