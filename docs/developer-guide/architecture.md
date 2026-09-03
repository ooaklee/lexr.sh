# Architecture

Lexr coordinates image remastering, kernel handling and hardware support, but
those jobs do not share the same risks. Keeping them behind feature boundaries
makes it possible to review a catalogue lookup without also granting authority
to write a disk or change an installed system.

```text
Cobra commands / Bubble Tea wizard
                |
        feature orchestration
                |
  domain packages and durable records
                |
 Docker, process, filesystem and network boundaries
```

## Delivery layers

`cmd/lexr` starts the application. `internal/cli` assembles Cobra commands,
parses input and renders results; `internal/tui` provides the Bubble Tea image
wizard. They call the same feature services rather than implementing a second
version of the workflow. The interactive and scriptable image paths therefore
share one manager and operation plan.

## Feature ownership

| Area | Primary packages | What the boundary owns |
| --- | --- | --- |
| Discovery | `internal/catalog`, `internal/userspace/catalog` | Strict, human-editable metadata and semantic validation |
| Kernel | `internal/kernel` and its build, release and install packages | ABI-bound bundles, native builds, release preparation and recoverable installation |
| Images | `internal/manager`, `internal/image`, `internal/image/ubuntu`, `internal/image/fedora` | Source and kernel orchestration, adapter-owned remastering and validation, and shared descriptor-bound publication |
| Removable media | `internal/media` | Whole-device identity, confirmation, raw writing, full read-back and ejection |
| Private hand-offs | `internal/handoff` | Closed private stores, same-device evidence, application and restoration |
| Userspace | `internal/userspace` plus audio and camera packages | Status, authenticated acquisition, bounded builds, installs and release preparation |
| Diagnosis and recovery | `internal/doctor`, `internal/hardwaredoctor`, `internal/bootdoctor`, `internal/cleanup` | Read-only readiness evidence and reversible legacy clean-up |
| Host boundaries | `internal/platform` and feature-specific adapters | Argument-separated process execution and Docker isolation |

Managers compose smaller services; they do not turn catalogue fields into shell
commands. Docker and external processes sit behind narrow interfaces so tests
can exercise policy without invoking a real container, package manager or raw
device.

## Records carry trust between steps

Lexr does not treat one successful command as permanent authority for the next.
The workflow chooses a record suited to the boundary:

- image and kernel work uses operation plans, manifests, checksums and
  provenance records;
- private Windows material enters a content-addressed store and is revalidated
  before use;
- userspace diagnosis produces a point-in-time status report;
- destructive operations bind confirmation to the current input and target;
  and
- recoverable mutations retain receipts and backups for explicit restoration.

That separation matters. A valid catalogue entry is not executable policy, a
structurally valid image is not hardware qualification, and an imported private
hand-off is not permission to modify a system.

## Architecture decisions

Architecture decision records explain why lasting boundaries exist. Start with
the decision closest to the question you are asking:

| Theme | Decisions |
| --- | --- |
| Project and hardware scope | [ADR023: standalone naming boundary](../adr/adr-023-lexr-standalone-repository-and-compatibility.md), [ADR028: Snapdragon X project scope](../adr/adr-028-snapdragon-x-project-scope.md) |
| Command and data foundations | [ADR001: feature-oriented CLI](../adr/adr-001-feature-oriented-cli.md), [ADR002: image catalogue](../adr/adr-002-validated-image-catalog.md), [ADR003: kernel bundles](../adr/adr-003-version-bound-kernel-bundles.md) |
| Image creation and installation | [ADR004: Ubuntu remaster](../adr/adr-004-ubuntu-hybrid-iso-remaster.md), [ADR007: installed-system hand-off](../adr/adr-007-installed-system-handoff.md), [ADR008: media discovery](../adr/adr-008-adapter-owned-media-discovery.md), [ADR009: offline companion](../adr/adr-009-single-manifest-companion-bundle.md), [ADR012: removable-media writing](../adr/adr-012-identity-bound-removable-media-writing.md), [ADR017: image release preparation](../adr/adr-017-native-image-release-preparation.md), [ADR027: Fedora EROFS remaster and installed hand-off](../adr/adr-027-fedora-erofs-remaster-and-installed-handoff.md) |
| Userspace, privacy and recovery | [ADR005: clean-up](../adr/adr-005-reversible-legacy-cleanup.md), [ADR006: userspace companion](../adr/adr-006-userspace-companion.md), [ADR010: native workflows](../adr/adr-010-native-cli-workflow-migration.md), [ADR011: Windows hand-offs](../adr/adr-011-private-windows-handoff.md), [ADR013: application transactions](../adr/adr-013-private-handoff-application-transactions.md), [ADR014: IPTSD releases](../adr/adr-014-native-iptsd-release-transactions.md), [ADR015: camera packages](../adr/adr-015-native-imx681-package-and-release-contracts.md), [ADR018: camera validation](../adr/adr-018-native-imx681-runtime-validation.md), [ADR019: audio releases](../adr/adr-019-native-audio-release-preparation.md), [ADR020: camera authority](../adr/adr-020-independent-camera-authority-digests.md), [ADR021: original-INF authority](../adr/adr-021-windows-handoff-v2-original-inf-authority.md), [ADR022: privileged collection](../adr/adr-022-privileged-windows-collection-and-controller-authority.md) |
| Releases and repository ownership | [ADR016: kernel release preparation](../adr/adr-016-native-kernel-release-preparation.md), [ADR024: automation ownership](../adr/adr-024-lexr-owned-automation.md), [ADR025: build provenance](../adr/adr-025-submodule-safe-build-provenance.md), [ADR026: project terms](../adr/adr-026-apache-2.0-project-terms-and-release-notices.md) |

Read an ADR as historical reasoning, then check current code and task guides for
the implemented contract. When an agreed change needs a new record, use the
[ADR template](../adr/_adr-XXXX-template.md) and follow the coordination rule in
[`CONTRIBUTING.md`](https://github.com/ooaklee/lexr.sh/blob/main/CONTRIBUTING.md#before-you-start).
