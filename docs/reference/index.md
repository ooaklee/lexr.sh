# Reference

Use the reference pages when you need an exact command shape, want to check whether a workflow fits your host, or need to understand which repository and release channel owns an artefact. The task guides remain the better place to follow an operation from start to finish.

## Find the fact you need

| Question | Reference |
| --- | --- |
| Which command or command family should I use? | [Command reference](command-reference.md) |
| Which operating system, tool, storage, network access, or privilege does a task need? | [Requirements by workflow](requirements.md) |
| What does Lexr publish, generate, consume privately, or leave to the OE release channel? | [Project and support boundaries](project-boundaries.md) |

## Authoritative data

The reference explains the current contracts, but the versioned data remains authoritative:

- [`supported-isos.json`](https://github.com/ooaklee/lexr.sh/blob/main/supported-isos.json) records discoverable media and whether an adapter is implemented;
- [`supported-userspace.json`](https://github.com/ooaklee/lexr.sh/blob/main/supported-userspace.json) records component maturity, redistribution policy, compatibility evidence, and supported actions; and
- [architecture decisions](../developer-guide/architecture.md#architecture-decisions)
  record why the safety, compatibility, publication and migration boundaries exist.

Use `lexr <command> --help` for every available flag in the version you are running. Where a command advertises `--json`, that version provides machine-readable output for automation.

## Continue with a workflow

Start with the [getting-started guide](../getting-started/index.md) for installation and a first image, or choose a focused task from the [user guide](../user-guide/index.md).
