# Command reference

Use this page to find the right command family and its exact shape, then run
`lexr <command> --help` against your installed version for its complete flags.
Only commands which advertise `--dry-run` or `--json` support those options.
For how hardware profiles are selected across these commands, see
[hardware profiles](../concepts/hardware-profiles.md); for configuration keys,
see the [configuration reference](configuration.md).

Running `lexr` with no arguments in an interactive terminal opens the image
wizard. A non-interactive invocation prints help. The wizard covers image
creation; it is not an alternate interface for every command family.

The command shape is `lexr [global flags] <command> [arguments and flags]`.

## Global flags

- `--profile <id|alias|auto>` selects the hardware profile for the command.
  It wins over a saved profile; `auto` bypasses the saved choice and attempts
  exact local device-tree detection. Build and release commands reject it.
- `--config <path>` selects configuration files. Repeat it in merge order:
  later scalars override earlier values, maps merge recursively, and lists
  replace earlier lists. Each file is validated before merging. Explicit flags
  replace the file selection from `LEXR_CONFIG` or the standard location.
- `--catalog <path>` selects an image catalogue override instead of the
  embedded image catalogue.
- `--userspace-catalog <path>` selects a userspace catalogue override instead
  of the embedded component catalogue.
- `lexr -v` and `lexr --version` print the executable version. This is a root
  flag; `-v` does not enable verbose logging.

```sh
lexr --config base.yml --config local.yml config show
lexr --catalog ./supported-isos.json catalog list
```

## Profiles, version and completion

```text
lexr profile list [--json]
lexr init <profile> [--force]
lexr version
lexr completion <bash|fish|powershell|zsh>
```

`profile list` shows the supported hardware identities without reading
configuration. `init` validates the value against the registry, stores the
canonical ID as the primary file's top-level `profile`, and adds `version: 1`
if absent; replacing a different non-empty profile requires `--force`.

## Configuration

```text
lexr config check
lexr config edit
lexr config show
```

Repeatable `--config` flags select files in merge order and replace the default
file set supplied by `LEXR_CONFIG` or the standard config home. `edit` and
`init` target the first file. See
[Manage Lexr configuration](../user-guide/configuration.md) and the
[configuration reference](configuration.md) for strict validation, merge rules
and profile overwrite confirmation.

## Diagnostics

```text
lexr doctor
lexr doctor boot
lexr doctor hardware [wifi|bluetooth|audio|touchscreen ...]
lexr doctor userspace
```

Bare `doctor` checks whether the current host is ready for image creation. The
subcommands answer questions about an installed or live Surface Pro 11 system:
`boot` inspects static kernel boot and device-tree evidence without changing
boot files, `hardware` reports live hardware state, and `userspace` reports
installed support using the same checks as `userspace status`. The boot,
hardware and userspace variants resolve a
[hardware profile](../concepts/hardware-profiles.md); follow the
[userspace guide](../user-guide/userspace-support.md) for interpretation and
[kernel management](../operator-manual/kernel-management.md) for boot
diagnostics. The former `doctor boot --device` flag was removed in 0.5; use
the global `--profile` or the saved top-level `profile`.

## Image catalogue and media

```text
lexr catalog list
lexr catalog show <id>
lexr catalog validate [path]

lexr image create --output <iso> [--profile <profile-id>]
lexr image validate <iso>
lexr image devices
lexr image write <iso> --device <whole-device> --dry-run
```

Offline media needs an explicit or saved profile; see
[hardware profiles](../concepts/hardware-profiles.md). The former
`image create --kernel-profile` flag was removed in 0.5; use the global
`--profile` or the saved top-level `profile`. `image write --device` is the
reviewed whole-disk target and is never satisfied by a hardware profile.

Begin with the [installation-media guide](../user-guide/installation-media.md)
before using a whole-device write or assuming a catalogue entry has an
implemented adapter. In catalogue output, `implemented` means the adapter can
create and structurally validate the image; inspect `Experimental` and `Notes`
before treating that output as a hardware test candidate.

For local release packaging, `image release prepare <iso>` prepares split
compressed assets, and `image release validate <release-directory>` checks the
result. Release commands reject `--profile`; their platform scope comes from
the bundle inventory. See the
[release preparation guide](../operator-manual/release-preparation.md).

## Kernel commands

```text
lexr kernel release list
lexr kernel release download [ref]
lexr kernel release prepare --help
lexr kernel release validate <release-directory>
lexr kernel inspect <directory>
lexr kernel boot refresh --root <path> --abi <abi> [--profile <profile|auto>]
lexr kernel preflight <bundle-directory> --root <path> --fallback-abi <abi> [--force]
lexr kernel install <bundle-directory> --root <path> --fallback-abi <abi> --dry-run [--force]
lexr kernel install <bundle-directory> --root <path> --fallback-abi <abi> --yes [--force]
lexr kernel build
```

Kernel inspection and preflight are separate from installation. Protected
boot files can require `sudo` even for read-only preflight or an install dry
run; pass the same profile or an absolute configuration path through `sudo`.
Preflight reports any exact-ABI fallback DTB copy and recognised boot-hook
retirement planned for the confirmed installation. Preflight,
install and boot refresh resolve a hardware profile and check the bundle's
declared platform inventory; `kernel build` and the release subtree ignore any
saved profile and reject an explicit `--profile`. Read
[kernel management](../operator-manual/kernel-management.md) before granting
privilege or changing a bootable system. `kernel boot register-arch` adds an
installed Surface Arch loader to an existing GRUB menu; see
[Arch installation](../operator-manual/arch-linux-arm-install.md).

## Private hand-off commands

```text
HANDOFF_STORE="${HOME}/.lexr-handoffs"
lexr handoff import <directory> --store "$HANDOFF_STORE"
lexr handoff list --store "$HANDOFF_STORE"
lexr handoff apply <id> --store "$HANDOFF_STORE" --target-root <path> --dry-run
sudo lexr handoff restore <receipt-id> --target-root <path> --dry-run
lexr handoff purge <id> --store "$HANDOFF_STORE" --dry-run
```

The explicit store path must remain the same across import, inspection,
application and retention. `handoff apply` resolves a hardware profile against
its same-device identity evidence. Because a `sudo` run reads root's separate
configuration, pass `--profile` or an absolute `--config` path for privileged
profile-dependent commands. Follow the
[Windows hand-off guide](../user-guide/windows-handoff.md) for private
collection, exact confirmations and recovery-version limits.

## Userspace commands

```text
lexr userspace list
lexr userspace show <component>
lexr userspace catalog validate [path]
lexr userspace status
lexr userspace pull <component|recommended>
lexr userspace build <iptsd|camera>
lexr userspace install <component|recommended> [--from <directory>]
lexr userspace audio release prepare --help
lexr userspace audio release validate <release-directory>
lexr userspace camera capture --dry-run
lexr userspace camera render <capture.raw> <preview.png>
lexr userspace camera release prepare --help
lexr userspace camera release validate <release-directory> --authority-sha256 <sha256>
```

Status is read-only; pull, build, install, release and camera workflows have
different input and host boundaries. `userspace status`, `install` and
`camera capture` resolve a hardware profile. Use
[userspace support](../user-guide/userspace-support.md) or
[release preparation](../operator-manual/release-preparation.md) for the
complete task.

## Reversible clean-up

```text
lexr clean scan [--feature <feature>]
lexr clean plan [--feature <feature>] --output lexr-cleanup-plan.json
lexr clean apply --plan lexr-cleanup-plan.json --yes
lexr clean restore /var/lib/lexr/backups/<transaction>/receipt.json --yes
```

Clean-up considers only recognised legacy state and deliberately keeps recovery
evidence. Scan, plan and apply resolve a hardware profile against the selected
target root. Read the [clean-up guide](../user-guide/reversible-cleanup.md)
before applying a plan.

## The image wizard

Use `wizard` to choose and create an installation image interactively. It
requires terminal input, covers image creation only, and resolves a hardware
profile for the offline target. For scripts, use `image create`.
