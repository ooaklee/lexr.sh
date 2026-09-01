# User guide

Use this guide to move from a supported Lexr image to a working Surface Pro 11 system without losing the verification, privacy, or recovery boundaries built into the CLI. Each page focuses on one job, so you can follow the path you need instead of reading the complete command reference first.

## Who this guide is for

These pages are for people creating or using Lexr installation media, bringing authorised device data across from Windows, installing supporting components, or removing recognised legacy workarounds. They also give contributors a task-oriented view of the operator workflows that Lexr must preserve.

Lexr deliberately separates image creation, private Windows hand-offs, userspace support, and clean-up. A successful step in one workflow does not grant another workflow permission to modify the system.

## Choose your task

| If you want to… | Start here |
| --- | --- |
| Create, validate, and write the supported live image | [Create and write installation media](installation-media.md) |
| Put the CLI, source, catalogues, and optional IPTSD support on the image | [Carry the offline companion](offline-companion.md) |
| Collect private firmware and Bluetooth evidence on Windows, then apply it on Linux | [Complete the Windows hand-off](windows-handoff.md) |
| Check, obtain, build, or install supporting components | [Manage userspace support](userspace-support.md) |
| Review and remove recognised obsolete workarounds with a recovery path | [Run reversible clean-up](reversible-cleanup.md) |

If this is your first visit, begin with the [project overview and installation choices](https://github.com/ooaklee/lexr.sh#readme), then return here for the workflow you need.

## A typical route

1. [Create and validate the installation image](installation-media.md).
2. If the live session must work without another download, [include the offline companion](offline-companion.md) during image creation.
3. Write the validated image to a reviewed removable device and boot it on the Surface Pro 11.
4. [Inspect userspace support](userspace-support.md) to see which components are present or missing.
5. When the device needs its authorised Windows firmware or Bluetooth evidence, [complete the private hand-off](windows-handoff.md).
6. Only after reviewing an explicit plan, [remove recognised legacy workarounds](reversible-cleanup.md).

## Know when you are finished

Lexr's structural checks and receipts make each operation reviewable, but host-independent validation is not hardware qualification. Keep receipts and backups until the relevant boot and hardware checks have passed on the same physical Surface Pro 11.

For command discovery at any point, run `lexr <command> --help`. Where a command advertises `--json`, machine-readable output is available for automation.
