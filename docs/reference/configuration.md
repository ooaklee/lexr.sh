# Configuration reference

Save the values you reuse in `lexr.yml`. Command-line flags override those
values for one invocation. Start with [Manage configuration](../user-guide/configuration.md)
for a walkthrough, or [hardware profiles](../concepts/hardware-profiles.md)
to choose your device.

## File location and selection

| Host | Standard file |
| --- | --- |
| Linux | `~/.config/lexr/lexr.yml`, or `$XDG_CONFIG_HOME/lexr/lexr.yml` |
| macOS | `~/Library/Application Support/lexr/lexr.yml` |
| Windows | `%AppData%\lexr\lexr.yml` |

These locations follow Go's `os.UserConfigDir`. Without `--config`, a
non-empty `LEXR_CONFIG` selects one file instead. Explicit `--config`
flags replace that selection and ignore `LEXR_CONFIG`:

```sh
lexr --config base.yml --config local.yml config check
lexr config show --config base.yml,local.yml
```

Repeat the flag in merge order, or use a comma-separated list. For a literal
comma in a filename, use CSV quoting inside the shell argument:
`--config '"a,b.yml"'`. Empty paths, including `--config=`, are errors.

Only a missing standard file is optional for ordinary commands. Explicitly
selected files must exist. A whitespace-only `LEXR_CONFIG` selects the
standard path but makes it required. `config check` and `config show`
always require their selected files. `profile list` and help remain available
without loading configuration.

The first selected file is the **primary file**. `config edit` opens it;
`init` sets its profile. Later layers may override that choice. Selecting a
different file does not move the standard cache or workspace home.

## Merge rules and validation

Later files take precedence:

- Scalars replace earlier values, including `false`, `0` and `""`.
- Maps merge recursively; absent keys keep their earlier values.
- Lists replace the earlier list, including an empty `[]`.
- `null` clears an earlier value, allowing the command default to apply.

Each file may omit `version`. If present, it must be the integer `1`.
Unknown fields, duplicate keys, incompatible types, additional YAML documents
and unsupported versions are rejected before merging. An overlay cannot hide
an invalid earlier file. Anchors and merge keys resolve within each file;
empty files provide no overrides.

`config check` validates the files and the saved profile. `config show`
prints merged configured keys and absolute source paths, with leading home
paths expanded. It does not add workflow defaults or command-line overrides.

## Precedence and command mapping

For ordinary settings, explicit flags override merged configuration, which
overrides the command's defaults. Existing workflow-specific environment
fallbacks remain where documented. `LEXR_CONFIG` selects files; it does not
set individual flags.

Namespaces follow command names. Flag names replace hyphens with underscores:
`image.create.kernel_release` supplies `lexr image create --kernel-release`.
The hyphenated command `register-arch` keeps its spelling as a namespace.
Boolean flags accept an explicit false override, such as `--json=false`;
a command-line list replaces its configured list.

`profile` is resolved separately from command defaults: explicit global
`--profile` (including `auto`), then the saved top-level `profile`. This is
the only selection mechanism; per-command hardware selectors were removed in
0.5 and strict validation rejects their keys. Hardware commands can detect the
live target when no profile is selected; offline media and alternate roots
require a profile. Build and release commands ignore the saved profile and
reject `--profile`.

### Per-run decisions

The schema has no consent or safety-bypass keys. `yes`, `confirm`, `force`,
`overwrite`, `allow_unverified` and `image.write.device` were removed in 0.5;
strict validation rejects them. Supply the corresponding flag after reviewing
that operation, and select the whole disk explicitly for each write. Exact
confirmation phrases remain command-line or interactive inputs.

## Path semantics

A leading `~` or `~/` expands to your home. Shell variables and command
substitution are never evaluated. Relative host paths resolve from the current
working directory, not from the configuration file's directory.

Repository-relative paths keep their literal values for the owning workflow:
`kernel.build.work_dir`, `kernel.build.output_dir`,
`userspace.build.output_dir`, `image.release.prepare.out_dir` and
`userspace.camera.release.prepare.output_dir`. The profile ID is also literal.

## Host-directory defaults

The standard configuration home is the directory containing the standard file
listed above, regardless of which YAML files you select.

| Setting | Default beneath configuration home |
| --- | --- |
| `image.create.cache_dir` | `caches/image` |
| `image.create.workspace_dir` | `builds/image` |
| `doctor.workspace` | `builds/doctor` |
| `kernel.release.download.output_dir` | `caches/kernel` |
| `userspace.pull.cache_dir` | `caches/userspace` |
| `wizard.cache_dir` | `caches/wizard` |

Userspace installation also searches the configured pull cache when you omit
`--from`. Both wizard entry points use `image.create.workspace_dir`.

Defaults are resolved on demand. Help and configuration loading create no cache
directories; running a command, including a dry run, may create its empty
default directories. An empty YAML host-directory path selects the default;
an explicitly empty flag is passed to the workflow. Configured custom
directories are not created by the resolver.

If the operating system cannot supply a configuration home, existing fallback
locations remain: image and userspace caches use their workflow defaults;
doctor uses `./builds/doctor`, kernel downloads use `./kernel-bundle`, and the
image workspace remains unset for its temporary-workspace allocator.
Filesystem errors while creating an otherwise resolved home are returned.

## Top-level settings

| Key | Type | Meaning |
| --- | --- | --- |
| `version` | integer | Optional on read; new files use `1` |
| `profile` | string | Canonical hardware ID, bundle alias, or `auto`; no default model |

## Command settings

All keys below are optional. Omitted values use the command's current defaults,
which you can inspect with `lexr <command> --help`. A Go zero value is not
necessarily a workflow default. Positional inputs such as a bundle directory,
component name or receipt ID remain command-line arguments.

### global

| Key | Type | Use |
| --- | --- | --- |
| `global.catalog` | string | `--catalog` |
| `global.userspace_catalog` | string | `--userspace-catalog` |

### catalog

| Key | Type | Use |
| --- | --- | --- |
| `catalog.list.json` | boolean | `--json` |
| `catalog.show.json` | boolean | `--json` |
| `catalog.validate.json` | boolean | `--json` |

### clean

| Key | Type | Use |
| --- | --- | --- |
| `clean.scan.root` | string | `--root` |
| `clean.scan.user_home` | string | `--user-home` |
| `clean.scan.feature` | list of strings | `--feature` |
| `clean.scan.json` | boolean | `--json` |
| `clean.plan.root` | string | `--root` |
| `clean.plan.user_home` | string | `--user-home` |
| `clean.plan.feature` | list of strings | `--feature` |
| `clean.plan.json` | boolean | `--json` |
| `clean.plan.output` | string | `--output` |
| `clean.apply.root` | string | `--root` |
| `clean.apply.user_home` | string | `--user-home` |
| `clean.apply.json` | boolean | `--json` |
| `clean.apply.plan` | string | `--plan` |
| `clean.restore.root` | string | `--root` |
| `clean.restore.user_home` | string | `--user-home` |
| `clean.restore.json` | boolean | `--json` |

### doctor

| Key | Type | Use |
| --- | --- | --- |
| `doctor.workspace` | string | `--workspace` |
| `doctor.json` | boolean | `--json` |
| `doctor.boot.root` | string | `--root` |
| `doctor.boot.target_abi` | string | `--target-abi` |
| `doctor.boot.fallback_abi` | string | `--fallback-abi` |
| `doctor.boot.json` | boolean | `--json` |
| `doctor.hardware.root` | string | `--root` |
| `doctor.hardware.json` | boolean | `--json` |
| `doctor.userspace.root` | string | `--root` |
| `doctor.userspace.user_home` | string | `--user-home` |
| `doctor.userspace.kernel` | string | `--kernel` |
| `doctor.userspace.feature` | list of strings | `--feature` |
| `doctor.userspace.json` | boolean | `--json` |

### handoff

| Key | Type | Use |
| --- | --- | --- |
| `handoff.restore.target_root` | string | `--target-root` |
| `handoff.restore.dry_run` | boolean | `--dry-run` |
| `handoff.restore.json` | boolean | `--json` |
| `handoff.apply.store` | string | `--store` |
| `handoff.apply.identity_root` | string | `--identity-root` |
| `handoff.apply.target_root` | string | `--target-root` |
| `handoff.apply.feature` | list of strings | `--feature` |
| `handoff.apply.adsp_policy` | string | `--adsp-policy` |
| `handoff.apply.dry_run` | boolean | `--dry-run` |
| `handoff.apply.json` | boolean | `--json` |
| `handoff.import.store` | string | `--store` |
| `handoff.import.json` | boolean | `--json` |
| `handoff.list.store` | string | `--store` |
| `handoff.list.json` | boolean | `--json` |
| `handoff.purge.store` | string | `--store` |
| `handoff.purge.dry_run` | boolean | `--dry-run` |
| `handoff.purge.json` | boolean | `--json` |

### image

| Key | Type | Use |
| --- | --- | --- |
| `image.devices.json` | boolean | `--json` |
| `image.write.dry_run` | boolean | `--dry-run` |
| `image.write.json` | boolean | `--json` |
| `image.create.catalog_id` | string | `--catalog-id` |
| `image.create.source` | string | `--source` |
| `image.create.source_sha256` | string | `--source-sha256` |
| `image.create.refresh_source` | boolean | `--refresh-source` |
| `image.create.kernel_dir` | string | `--kernel-dir` |
| `image.create.kernel_repository` | string | `--kernel-repository` |
| `image.create.kernel_release` | string | `--kernel-release` |
| `image.create.cache_dir` | string | `--cache-dir` |
| `image.create.workspace_dir` | string | `--workspace-dir` |
| `image.create.companion_source_dir` | string | `--companion-source-dir` |
| `image.create.companion_userspace` | list of strings | `--companion-userspace` |
| `image.create.output` | string | `--output` |
| `image.create.keep_workspace` | boolean | `--keep-workspace` |
| `image.create.dry_run` | boolean | `--dry-run` |
| `image.create.json` | boolean | `--json` |
| `image.validate.json` | boolean | `--json` |
| `image.release.prepare.repository_root` | string | `--repository-root` |
| `image.release.prepare.release_name` | string | `--release-name` |
| `image.release.prepare.out_dir` | string | `--out-dir` |
| `image.release.prepare.part_size_bytes` | integer | `--part-size-bytes` |
| `image.release.prepare.dry_run` | boolean | `--dry-run` |
| `image.release.prepare.json` | boolean | `--json` |
| `image.release.validate.json` | boolean | `--json` |

### kernel

| Key | Type | Use |
| --- | --- | --- |
| `kernel.boot.refresh.root` | string | `--root` |
| `kernel.boot.refresh.abi` | string | `--abi` |
| `kernel.boot.register-arch.arch_root` | string | `--arch-root` |
| `kernel.boot.register-arch.grub_directory` | string | `--grub-directory` |
| `kernel.boot.register-arch.esp` | string | `--esp` |
| `kernel.boot.register-arch.dry_run` | boolean | `--dry-run` |
| `kernel.release.prepare.build_dir` | string | `--build-dir` |
| `kernel.release.prepare.output_dir` | string | `--output-dir` |
| `kernel.release.prepare.release_name` | string | `--release-name` |
| `kernel.release.prepare.source` | list of strings | `--source` |
| `kernel.release.prepare.licence` | list of strings | `--licence` |
| `kernel.release.prepare.dry_run` | boolean | `--dry-run` |
| `kernel.release.prepare.json` | boolean | `--json` |
| `kernel.release.validate.json` | boolean | `--json` |
| `kernel.release.list.repository` | string | `--repository` |
| `kernel.release.list.limit` | integer | `--limit` |
| `kernel.release.list.json` | boolean | `--json` |
| `kernel.release.download.repository` | string | `--repository` |
| `kernel.release.download.output_dir` | string | `--output-dir` |
| `kernel.release.download.headers` | boolean | `--headers` |
| `kernel.release.download.json` | boolean | `--json` |
| `kernel.inspect.package_set` | string | `--package-set` |
| `kernel.inspect.json` | boolean | `--json` |
| `kernel.preflight.root` | string | `--root` |
| `kernel.preflight.fallback_abi` | string | `--fallback-abi` |
| `kernel.preflight.running_abi` | string | `--running-abi` |
| `kernel.preflight.package_set` | string | `--package-set` |
| `kernel.preflight.json` | boolean | `--json` |
| `kernel.install.root` | string | `--root` |
| `kernel.install.fallback_abi` | string | `--fallback-abi` |
| `kernel.install.running_abi` | string | `--running-abi` |
| `kernel.install.package_set` | string | `--package-set` |
| `kernel.install.json` | boolean | `--json` |
| `kernel.install.dry_run` | boolean | `--dry-run` |
| `kernel.build.repository_root` | string | `--repository-root` |
| `kernel.build.git_url` | string | `--git-url` |
| `kernel.build.git_branch` | string | `--git-branch` |
| `kernel.build.boot_image_mode` | string | `--boot-image-mode` |
| `kernel.build.work_dir` | string | `--work-dir` |
| `kernel.build.output_dir` | string | `--output-dir` |
| `kernel.build.jobs` | integer | `--jobs` |
| `kernel.build.reset_source` | boolean | `--reset-source` |
| `kernel.build.skip_clean` | boolean | `--skip-clean` |
| `kernel.build.dry_run` | boolean | `--dry-run` |

### userspace

| Key | Type | Use |
| --- | --- | --- |
| `userspace.list.json` | boolean | `--json` |
| `userspace.show.json` | boolean | `--json` |
| `userspace.status.root` | string | `--root` |
| `userspace.status.user_home` | string | `--user-home` |
| `userspace.status.kernel` | string | `--kernel` |
| `userspace.status.feature` | list of strings | `--feature` |
| `userspace.status.json` | boolean | `--json` |
| `userspace.pull.cache_dir` | string | `--cache-dir` |
| `userspace.pull.json` | boolean | `--json` |
| `userspace.build.repository_root` | string | `--repository-root` |
| `userspace.build.output_dir` | string | `--output-dir` |
| `userspace.build.image` | string | `--image` |
| `userspace.build.work_volume` | string | `--work-volume` |
| `userspace.build.jobs` | integer | `--jobs` |
| `userspace.build.minimum_free_gib` | integer | `--minimum-free-gib` |
| `userspace.build.no_pull` | boolean | `--no-pull` |
| `userspace.build.dry_run` | boolean | `--dry-run` |
| `userspace.build.json` | boolean | `--json` |
| `userspace.install.from` | string | `--from` |
| `userspace.install.repository_root` | string | `--repository-root` |
| `userspace.install.camera_authority_sha256` | string | `--camera-authority-sha256` |
| `userspace.install.root` | string | `--root` |
| `userspace.install.dry_run` | boolean | `--dry-run` |
| `userspace.install.activate` | boolean | `--activate` |
| `userspace.install.json` | boolean | `--json` |
| `userspace.audio.release.prepare.repository_root` | string | `--repository-root` |
| `userspace.audio.release.prepare.source_root` | string | `--source-root` |
| `userspace.audio.release.prepare.tag` | string | `--tag` |
| `userspace.audio.release.prepare.kernel_tag` | string | `--kernel-tag` |
| `userspace.audio.release.prepare.kernel_abi` | string | `--kernel-abi` |
| `userspace.audio.release.prepare.dry_run` | boolean | `--dry-run` |
| `userspace.audio.release.prepare.json` | boolean | `--json` |
| `userspace.audio.release.validate.repository_root` | string | `--repository-root` |
| `userspace.audio.release.validate.json` | boolean | `--json` |
| `userspace.camera.capture.frames` | integer | `--frames` |
| `userspace.camera.capture.output` | string | `--output` |
| `userspace.camera.capture.expected_release` | string | `--expected-release` |
| `userspace.camera.capture.dry_run` | boolean | `--dry-run` |
| `userspace.camera.capture.json` | boolean | `--json` |
| `userspace.camera.render.frame` | integer | `--frame` |
| `userspace.camera.render.bayer_order` | string | `--bayer-order` |
| `userspace.camera.render.linear` | boolean | `--linear` |
| `userspace.camera.render.json` | boolean | `--json` |
| `userspace.camera.release.prepare.repository_root` | string | `--repository-root` |
| `userspace.camera.release.prepare.from` | string | `--from` |
| `userspace.camera.release.prepare.output_dir` | string | `--output-dir` |
| `userspace.camera.release.prepare.tag` | string | `--tag` |
| `userspace.camera.release.prepare.kernel_tag` | string | `--kernel-tag` |
| `userspace.camera.release.prepare.kernel_abi` | string | `--kernel-abi` |
| `userspace.camera.release.prepare.build_authority_sha256` | string | `--build-authority-sha256` |
| `userspace.camera.release.prepare.dry_run` | boolean | `--dry-run` |
| `userspace.camera.release.prepare.json` | boolean | `--json` |
| `userspace.camera.release.validate.repository_root` | string | `--repository-root` |
| `userspace.camera.release.validate.authority_sha256` | string | `--authority-sha256` |
| `userspace.camera.release.validate.json` | boolean | `--json` |

### wizard

| Key | Type | Use |
| --- | --- | --- |
| `wizard.output` | string | `--output` |
| `wizard.source` | string | `--source` |
| `wizard.source_sha256` | string | `--source-sha256` |
| `wizard.kernel_dir` | string | `--kernel-dir` |
| `wizard.kernel_release` | string | `--kernel-release` |
| `wizard.cache_dir` | string | `--cache-dir` |

## Example

Save a target and a few useful defaults, then override only what changes:

```yaml
version: 1
profile: x1e80100-microsoft-denali-oled
image:
  create:
    kernel_release: latest
    output: lexr-ubuntu-sp11.iso
userspace:
  pull:
    cache_dir: ~/lexr-cache/userspace
doctor:
  userspace:
    feature: [audio, power]
    json: true
```

```sh
lexr config check
lexr image create --dry-run
lexr doctor userspace --json=false
lexr --profile x1p64100-microsoft-denali image create --dry-run
```

Run `lexr config edit` to change the primary file. See
[configuration management](../user-guide/configuration.md) for layering,
profile replacement and privileged commands.
