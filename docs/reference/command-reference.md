# Command reference

Use this page to find the right command family, then run
`lexr <command> --help` against your installed version for its complete flags.
Only commands which advertise `--dry-run` or `--json` support those options.

Running `lexr` with no arguments in an interactive terminal opens the image
wizard. A non-interactive invocation prints help. The wizard covers image
creation; it is not an alternate interface for every command family.

## General and diagnostic commands

```text
lexr version
lexr wizard
lexr doctor
lexr doctor userspace
lexr doctor hardware
lexr completion <bash|fish|powershell|zsh>
```

`doctor` checks whether the current host is ready for image creation. The
userspace and hardware variants answer different questions about an installed
or live Surface Pro 11 system; follow the [userspace guide](../user-guide/userspace-support.md)
for interpretation.

## Image catalogue and media

```text
lexr catalog list
lexr catalog show <id>
lexr catalog validate [path]

lexr image create --output <iso>
lexr image validate <iso>
lexr image devices
lexr image write <iso> --device <whole-device> --dry-run
lexr image release prepare <iso> --dry-run
lexr image release validate <release-directory>
```

Begin with the [installation-media guide](../user-guide/installation-media.md)
before using a whole-device write or assuming a catalogue entry has an
implemented adapter. In catalogue output, `implemented` means the adapter can
create and structurally validate the image; inspect `Experimental` and `Notes`
before treating that output as a hardware test candidate.

## Kernel commands

```text
lexr kernel release list
lexr kernel release download [ref]
lexr kernel release prepare --help
lexr kernel release validate <release-directory>
lexr kernel inspect <directory>
lexr kernel preflight <bundle-directory> --root <path> --fallback-abi <abi>
lexr kernel install <bundle-directory> --root <path> --fallback-abi <abi> --dry-run
lexr kernel build
```

Kernel inspection and preflight are separate from installation. Read
[kernel management](../operator-manual/kernel-management.md) before granting
privilege or changing a bootable system.

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
application and retention. Follow the [Windows hand-off guide](../user-guide/windows-handoff.md)
for private collection, exact confirmations and recovery-version limits.

## Userspace commands

```text
lexr userspace list
lexr userspace show <component>
lexr userspace catalog validate [path]
lexr userspace status
lexr userspace pull <component|recommended>
lexr userspace build <iptsd|camera>
lexr userspace install <component|recommended> --from <directory>
lexr userspace audio release prepare --help
lexr userspace audio release validate <release-directory>
lexr userspace camera capture --dry-run
lexr userspace camera render <capture.raw> <preview.png>
lexr userspace camera release prepare --help
lexr userspace camera release validate <release-directory> --authority-sha256 <sha256>
```

Status is read-only; pull, build, install, release and camera workflows have
different input and host boundaries. Use [userspace support](../user-guide/userspace-support.md)
or [release preparation](../operator-manual/release-preparation.md) for the
complete task.

## Reversible clean-up

```text
lexr clean scan
lexr clean plan --output lexr-cleanup-plan.json
lexr clean apply --plan lexr-cleanup-plan.json --yes
lexr clean restore /var/lib/lexr/backups/<transaction>/receipt.json --yes
```

Clean-up considers only recognised legacy state and deliberately keeps recovery
evidence. Read the [clean-up guide](../user-guide/reversible-cleanup.md) before
applying a plan.
