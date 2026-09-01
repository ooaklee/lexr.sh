# Project and support boundaries

Lexr coordinates several kinds of input and output without pretending they all
belong to one release. These boundaries make it clear what you are downloading,
what the CLI generates, and which evidence must remain private.

## What the Lexr release contains

The [`ooaklee/lexr.sh` releases](https://github.com/ooaklee/lexr.sh/releases)
contain six raw CLI executables, `LICENSE`, `NOTICE`,
`THIRD_PARTY_NOTICES.md`, and one SHA-256 manifest covering those nine payloads.
They do not include an ISO, kernel packages, firmware, drivers, catalogues, the
PowerShell collector, userspace bundles or other project documentation.

```text
lexr-v<version>-darwin-amd64
lexr-v<version>-darwin-arm64
lexr-v<version>-linux-amd64
lexr-v<version>-linux-arm64
lexr-v<version>-windows-amd64.exe
lexr-v<version>-windows-arm64.exe
LICENSE
NOTICE
THIRD_PARTY_NOTICES.md
lexr-v<version>.sha256sums
```

Kernel and hardware-support releases remain in the established
[`ooaklee/linux-surface-pro-11-oe` channel](https://github.com/ooaklee/linux-surface-pro-11-oe/releases).
That is the default source used by image creation, kernel release commands and
the userspace catalogue. Existing OE locations are external provenance, not
retired Lexr branding.

## What image creation produces

Lexr builds a new experimental image from a supported upstream image and a
version-bound kernel bundle. The output is not a prebuilt download from the CLI
release. Restricted platform firmware is not downloaded or redistributed; it
must come from an authorised Windows installation on the same device through
the private hand-off workflow.

Only `ubuntu-concept-resolute-x1e` currently has an implemented image adapter.
Debian, elementary OS, Fedora and Pop!_OS entries are `catalog-only`: they are
discoverable metadata, not buildable promises. Generated Ubuntu media includes
X1E OLED and X1P LCD device trees and boot entries, but X1P hardware
qualification remains pending.

Structural image, package and release validation proves the recorded bytes and
layout. It does not replace a boot, device or lifecycle test on a physical
Surface Pro 11.

## What must stay private

Windows hand-offs contain device-bound evidence and firmware. Camera captures,
raw diagnostics, adapter identifiers and hardware logs may also identify a
person or device. They do not belong in images, release assets, issues or source
control. Public commands expose only the redacted summaries promised by their
workflow.

## Current names and compatibility

Lexr.sh is the project and repository; `lexr` is the command. Schema-4 media
uses `/sp11/lexr-manifest.json`, `/sp11/companion/bin/linux-arm64/lexr`, and
`lexr_<version>_source.tar.gz`. Private hand-offs use `.lexr-handoffs` and the
`lexr.windows-handoff` wire domain. Installed integration uses `/etc/lexr`,
`/usr/libexec/lexr`, `/usr/lib/lexr`, `/var/lib/lexr`, and `lexr-sp11-*` units
and hooks. Kernel and userspace bundle, provenance and release filenames use
the `lexr-*.json` form.

[ADR023](../adr/adr-023-lexr-standalone-repository-and-compatibility.md) records
the completed standalone naming boundary. Compatibility exceptions which
require an exact predecessor binary—such as old hand-off stores or recovery
receipts—are documented beside their restore or purge actions, not hidden in
the naming history.

## Where to go next

- [Install Lexr](../getting-started/install.md) for the CLI release boundary.
- [Installation media](../user-guide/installation-media.md) for the generated
  image and hardware gates.
- [Windows hand-offs](../user-guide/windows-handoff.md) for restricted firmware
  and private evidence.
- [Automation and releases](../operator-manual/automation-and-releases.md) for
  channel ownership and remote publication.
