# Manage Lexr configuration

You should not have to repeat your device model and cache paths for every
command. Save them once, then use flags for the things that change.

## Initialise a hardware profile

List the available hardware and save the profile that matches your device:

```sh
lexr profile list
lexr init x1e80100-microsoft-denali-oled
lexr config check
```

The example selects the Surface Pro 11 X Elite OLED. For the X Plus LCD, use
`x1p64100-microsoft-denali`. A new file contains:

```yaml
version: 1
profile: x1e80100-microsoft-denali-oled
```

Lexr uses that choice for hardware operations. To target another model for one
command without changing the file:

```sh
lexr --profile x1p64100-microsoft-denali image create --dry-run
```

See [hardware profiles](../concepts/hardware-profiles.md) for live detection,
offline targets and current support limits. Build and release commands use
their source or bundle inventories and do not take a workstation profile.

Setting the same profile again needs no confirmation. To replace a different
saved profile, make that choice explicit:

```sh
lexr init x1p64100-microsoft-denali --force
```

`init` keeps unrelated values, comments, file permissions and symlinks.
Formatting may change. Invalid YAML is refused even with `--force`; use
`config edit` to repair it first.

## Check, edit and show

```sh
lexr config edit
lexr config check
lexr config show
```

`edit` opens the primary file with `$VISUAL`, then `$EDITOR`, then `vi`.
It creates a commented template if the file is missing and can open an invalid
file for repair. An executable path may contain spaces; editor arguments are
split on whitespace. Shell expressions inside these variables are not run.

`check` reports the selected paths and validates the files, their merge and
the saved profile. `show` prints merged configured values with source-path
comments. It includes neither command-line overrides nor unspecified defaults.

For example, save a cache location and a preferred diagnostic view:

```yaml
version: 1
profile: x1e80100-microsoft-denali-oled
userspace:
  pull:
    cache_dir: ~/lexr-cache/userspace
doctor:
  userspace:
    feature: [audio, power]
    json: true
```

Then run `lexr doctor userspace`, or use `--json=false` for a readable report.
[How configuration works](../concepts/configuration-binding.md) explains
precedence and the settings that must remain explicit for each operation.

## Choose or layer files

The standard Linux file is `~/.config/lexr/lexr.yml`, or
`$XDG_CONFIG_HOME/lexr/lexr.yml`. The
[configuration reference](../reference/configuration.md#file-location-and-selection)
lists the macOS and Windows locations.

Use `LEXR_CONFIG` to select one alternative file, or repeat `--config` to
combine files in order:

```sh
lexr --config base.yml --config local.yml config check
lexr --config base.yml --config local.yml config show
```

Explicit flags replace the environment selection. The first file is the
primary file that `init` and `config edit` change. Later layers can override
its values. `init` checks the selected overlays before writing and reports
when a later file overrides the profile it saved.

### Merge order and validation

Later scalar values win, including `false`, `0` and empty strings. Maps
merge; lists replace earlier lists, including an empty list. A `null` clears
an earlier value and allows the command default to apply.

Lexr validates each file before merging, so an overlay cannot hide an invalid
field in an earlier file. Selected files must exist, except that an absent
standard file is optional for ordinary commands. Empty paths are errors.
See the [complete merge and path rules](../reference/configuration.md).

## Use your profile with sudo

A privileged process may read root's configuration instead of your own. Pass
the profile explicitly, or select your file by absolute path:

```sh
sudo lexr --profile x1e80100-microsoft-denali-oled userspace install wifi --dry-run
sudo lexr --config /home/alex/.config/lexr/lexr.yml userspace install wifi --dry-run
```

Review the dry run before using the operation's confirmation flag. Consent
flags such as `--yes` are per-run decisions only: there are no `confirm`,
`yes`, `force` or `overwrite` keys in YAML, and 0.5 removed the ones earlier
versions accepted. If you used the old per-command selectors
(`device.variant`, `doctor.boot.device`, `image.create.kernel_profile`,
`kernel.boot.refresh.profile`), delete them from `lexr.yml` and keep the
top-level `profile`.
