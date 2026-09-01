# Operator manual

Lexr keeps high-impact work deliberately explicit: kernels are built and installed from closed bundles, release assets are prepared locally before publication, and the one cross-repository credential is confined to its publication job. This manual brings those maintainer workflows together without mixing them into the everyday path for using Lexr.

## Who this manual is for

Use these pages if you maintain Lexr automation, build or install an experimental kernel, assemble a release directory, or review the authority boundaries around publication.

If you want to install Lexr, create installation media, complete a Windows hand-off, or manage userspace support on your own device, return to the [documentation home](../index.md) or follow the [user guide](../user-guide/index.md). Those pages keep ordinary operator tasks separate from repository and release maintenance.

## Choose the job in front of you

| If you need to… | Start here |
| --- | --- |
| Understand which repository owns each workflow, provide the kernel runner, or publish through the correct release channel | [Automation and release channels](automation-and-releases.md) |
| Download, build, inspect, install, recover, or package a kernel bundle | [Kernel management](kernel-management.md) |
| Turn an image, kernel build, audio source set, or camera build into a closed local release directory | [Release preparation](release-preparation.md) |

## Keep the boundaries visible

The dry run, mutating operation, local validation, remote publication, and physical-hardware test are separate gates. Passing one does not grant authority for the next. In particular:

- release preparation does not create a tag, upload files, or change a remote service;
- structural validation does not qualify an image, kernel, audio stack, or camera on physical hardware;
- commands which need elevated access require you to invoke them that way; Lexr never elevates itself; and
- receipts, manifests, checksums, independently retained authority digests, and fallback kernels remain useful recovery evidence after a command finishes.

The linked architecture decisions explain why these contracts exist. Treat this manual as the task-oriented procedure and the ADRs as the decision record behind it.
