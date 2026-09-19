# Hardware profiles

Surface Pro 11 machines look similar but boot differently. The Snapdragon X
Elite model with an OLED display and the Snapdragon X Plus model with an LCD
display need different device trees and boot handling. A command that changes
boot files, installs a kernel or builds offline media must know which device it
is targeting before it starts. Lexr calls that choice a **hardware profile**,
and every hardware-aware command resolves it through one shared mechanism.

This page explains how profiles are identified, selected and checked. For the
exact command syntax, see the [command reference](../reference/command-reference.md);
for configuration files, see [Manage Lexr configuration](../user-guide/configuration.md).

## The profile registry

Lexr keeps one reviewed registry of supported hardware. A profile is an
identity, not a qualification claim: it says which device a workflow targets,
not that every workflow has passed physical testing on that device.

| Profile ID | Hardware | Bundle alias |
| --- | --- | --- |
| `x1e80100-microsoft-denali-oled` | Surface Pro 11 — Snapdragon X Elite (OLED) | `surface-pro-11-x1e-oled` |
| `x1p64100-microsoft-denali` | Surface Pro 11 — Snapdragon X Plus (LCD) | `surface-pro-11-x1p-lcd` |

The profile ID is the canonical value saved by `lexr init`. The bundle alias is
the stable platform identifier used in kernel bundles and boot registries, and
is also accepted wherever a profile ID is. The short boot-doctor variants
(`x1e-oled`, `x1p-lcd`) are no longer accepted selectors; use the profile ID.

Run `lexr profile list` (or `lexr profile list --json` for automation) to see
the profiles your executable supports. The command reads no configuration, so
it also works when your saved profile is invalid and needs repair.

## Save a profile once

Persist your device in the primary configuration file:

```sh
lexr init x1e80100-microsoft-denali-oled
```

`lexr init` validates the value against the registry, stores the canonical ID
as the top-level `profile` key, and adds `version: 1` if the file is new.
Replacing a different non-empty profile requires `--force`. See
[Manage Lexr configuration](../user-guide/configuration.md) for the file
handling details.

## How a command selects your profile

For ordinary commands, the selection order is:

1. An explicit global `--profile <id|alias|auto>` flag.
2. The saved top-level `profile` in your merged configuration.

This is the only selection mechanism: Lexr has no per-command hardware flags
and no per-command profile keys in the configuration file.

An explicit flag always wins, even over a saved profile. A supplied profile is
validated against the registry for ordinary commands; hardware-targeted
commands additionally check kernel or image inventory where relevant.

`--profile auto` bypasses the saved choice. For the hardware-targeted live
commands listed below, it attempts exact local device-tree detection at root
`/`. It cannot select an offline destination. Detection reads the bounded
`compatible` tokens under the selected root and accepts only one unambiguous
match. A processor name alone never establishes the model. When detection
cannot identify exactly one supported profile, the command fails and asks you
to choose explicitly.

### When a profile is required

Offline destinations have no physical machine to inspect, so they need an
explicit or saved profile:

- `lexr image create`, `lexr wizard` and the no-argument interactive wizard.

Hardware-targeted live commands resolve a profile through the same shared
resolver, preserving each domain's runtime and identity checks, and reject a
selected profile that contradicts proven target evidence:

- `doctor boot`, `doctor hardware`, `doctor userspace`;
- `userspace status`, `userspace install`, `userspace camera capture`;
- `kernel preflight`, `kernel install`, `kernel boot refresh`;
- `clean scan`, `clean plan`, `clean apply`;
- `handoff apply`.

On a live root these commands can auto-detect the device when no profile was
supplied or saved. An alternate or mounted root always needs an explicit or
saved profile, even if it contains a device-tree snapshot. Readable evidence
is checked for contradictions; it does not choose the offline destination.

### When no profile is needed

Generic work has no hardware target, so no profile is required or invented:
catalogue commands, host readiness checks (`lexr doctor`), file inspections
such as `image validate` or `kernel inspect`, `lexr version`, the configuration
commands, and receipt restoration.

### Build and release commands

`kernel build`, `userspace build` and every release subtree (`kernel release …`,
`image release …`, `userspace audio/camera release …`) ignore any saved
hardware profile and **reject** an explicit `--profile`. Their platform scope
comes from the source tree and bundle inventories, never from your
workstation's saved choice.

## Sudo runs do not see your profile

Your saved profile lives in your user configuration directory. Under `sudo`,
`lexr` runs as root and reads root's separate configuration, so the profile
you saved for your own account is not visible. For privileged
hardware-targeted commands, either pass the profile explicitly:

```sh
sudo lexr --profile x1e80100-microsoft-denali-oled userspace install wifi --yes
```

or select your own configuration file by absolute path:

```sh
sudo lexr --config /home/alex/.config/lexr/lexr.yml userspace install wifi --yes
```

A live-root command without either form tries device-tree detection. If the
evidence is missing, unreadable or unrecognised, it asks you to select a profile.

## Migrating from Lexr 0.4

Lexr 0.5 removed the per-command hardware selectors. If you used them, move to
one top-level `profile` (or the global `--profile`):

- Replace `lexr doctor boot --device <variant>` with
  `lexr init <profile-id>` or `lexr --profile <profile-id> doctor boot …`.
- Replace `lexr image create --kernel-profile <platform-id>`
  with the global `--profile` or the saved top-level `profile`.
- Delete the removed configuration keys `device.variant`,
  `doctor.boot.device`, `image.create.kernel_profile` and
  `kernel.boot.refresh.profile`; strict validation now rejects them. Keep only
  the top-level `profile`.

`image write --device` is unchanged: it is the reviewed **whole-disk target**
for a USB write, a required explicit storage selection. A hardware profile
checks the image's declared boot target; it never selects the disk to erase.

`kernel boot refresh --profile` uses the global flag. You can place it
before or after the command, or omit it when your configuration selects a
profile.

## Profile selection and boot support

Kernel preflight and installation use the selected profile to check both the
fallback and the new kernel. A GRUB title or `-qcom-x1e` ABI suffix does not
identify the hardware. A historical shared `sp11-denali.dtb` is checked against
the selected model's installed DTB bytes for that exact ABI. Installation
preserves that verified fallback, retires recognised competing hooks and
refreshes the new ABI for the same profile. Follow the
[installation guide](../user-guide/install-released-kernel-and-userspace.md)
for one sequence that covers both X Plus LCD and X Elite OLED.

For an external-DTB image, Lexr selects the profile's declared device tree and
records that choice in the image. An embedded Stubble bundle keeps its complete
boot-time selection, after Lexr verifies that it includes the requested model.
An optional packaged DTB alone does not establish that the image selects it at boot.

Fedora custom-kernel media remains limited to X Elite OLED. Its X Plus LCD
entry is a stock-kernel troubleshooting path. Choosing the LCD profile does
not enable the custom-kernel path or imply new hardware qualification.
