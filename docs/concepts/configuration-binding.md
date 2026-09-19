# How configuration works

Lexr reads optional YAML configuration from `lexr.yml` and uses it to supply
defaults for command flags before a command runs. This page explains which
settings reach which flags, what YAML can never grant, and how precedence
works. For file selection and merge rules, see
[Manage Lexr configuration](../user-guide/configuration.md); for the complete
schema, see the [configuration reference](../reference/configuration.md).

## How settings bind to flags

Ordinary schema keys bind their corresponding command flags through the
command path. The YAML namespaces use the command names; flag names use
underscores in place of hyphens. For example:

```yaml
version: 1
image:
  create:
    output: /mnt/lexr/lexr-sp11.iso
userspace:
  pull:
    cache_dir: /mnt/lexr/caches/userspace
```

supplies defaults for `lexr image create --output` and
`lexr userspace pull --cache-dir`. The binding happens once, after flag
parsing and before command preflight.

Hardware selection is deliberately outside this binding: the schema has no
per-command hardware keys. Only the top-level `profile` exists, and it is
resolved by the [shared profile resolver](hardware-profiles.md) rather than
per-command defaults, so one consistent rule governs every hardware-aware
command.

## Precedence

For a setting a command consumes, the order is:

1. Explicit command-line flag — wins even when its value is `false`, `0`,
   empty or an empty list.
2. Existing environment override, where the command defines one.
3. Merged configuration value, bound as described above.
4. Config-directory default, where defined.
5. Built-in workflow default.

A YAML `null` clears an earlier layer's value and lets command defaults apply.
Lists replace earlier lists; they are never appended during a merge.

## What YAML can never grant

Consent and safety overrides cannot come from a file, and the schema does not
accept keys for them. There are no `confirm`, `yes`, `force`, `overwrite`,
`allow_unverified` or `image.write.device` settings in YAML; strict validation
rejects them. The flags `--confirm`, `--yes`, `--force`, `--overwrite`,
`--allow-unverified` are always per-run
decisions, and `image write --device`, the whole-disk target, is always the
reviewed device for that run.

Confirmation phrases are an additional reason: `--confirm` flags on
`image write` and the hand-off commands require exact review-bound strings
that a YAML boolean cannot supply.

## Host directories

The cache and workspace directories under the configuration home stay lazy:
they are created on demand when a workflow needs them, and loading
configuration or showing help does not create them. A YAML host-directory
path left empty uses the default for that flag, while an explicitly supplied
empty flag retains its command's own interpretation. See the
[configuration reference](../reference/configuration.md#host-directory-defaults)
for the defaults table.

## Inspecting what you have

`lexr config show` prints the merged configured keys with their source paths.
It shows configuration only — not command-line overrides, and not the defaults
a command would apply. `lexr config check` validates the selected files, their
merge, and the saved profile against the
[profile registry](hardware-profiles.md). Neither command mutates anything.

## Build and release scope

Build and release commands still consume ordinary non-consent configuration
defaults, but they ignore any saved hardware profile and reject an explicit
`--profile`; see [hardware profiles](hardware-profiles.md#build-and-release-commands).
