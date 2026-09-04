# Changelog

Notable changes to Lexr.sh and its `lexr` command are documented here. The project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Before `1.0.0`, each release containing new features advances the minor version, while a release containing fixes only advances the patch version.

## Changelog entry template

### Types of changes

- `Added` for new features.
- `Changed` for changes in existing functionality.
- `Deprecated` for soon-to-be removed features.
- `Removed` for now removed features.
- `Fixed` for any bug fixes.
- `Security` in case of vulnerabilities.

Copy this template above the most recent release. Replace `x.y.z` with the planned
version, leave `Unreleased` in place while work is ongoing, and replace it with
the release date in `YYYY-MM-DD` format when publishing. Delete any subsections
that do not apply.

```markdown
## [x.y.z] - Unreleased

### Added

- tbc

### Changed

- tbc

### Deprecated

- tbc

### Removed

- tbc

### Fixed

- tbc

### Security

- tbc
```

---

## [0.3.0] - Unreleased

### Added

- Added a complete kernel device-tree delivery contract. Kernel bundles now
  record requested boot-image policy separately from effective embedded or
  external delivery and carry a deterministic, attributable device-tree
  inventory with Stubble and ukify selection provenance.
- Added the generic `lexr-kernel-boot-support` Debian package for raw kernel
  images. Its exact-ABI lifecycle hooks materialise the packaged DTB under
  `/boot/dtbs/<abi>/`, maintain the stock-GRUB-compatible `/boot/dtb-<abi>`
  binding, verify digests and converge regardless of Debian package order.
- Added `lexr kernel boot refresh --root <root> --abi <abi> --profile <profile>`
  for explicit, bounded refresh of an external device-tree boot binding.
- Added `image create --kernel-profile <platform-id>` so offline images made
  from multi-platform raw bundles record one explicit external-DTB deployment
  profile. The image contains a derived deployment inventory which retains all
  source package and DTB identities, bytes, digests and selectors while
  changing only which existing profile is required on that image.

### Changed

- Stubble validation now treats every embedded DTB as part of a closed,
  attributable compatibility set. Every DTB required by the selected build
  profile must appear exactly once; extra embedded DTBs must map to packaged
  output and explicit HWID, compatible-string or machine-database selection
  evidence. Embedded DTBs are never treated as arbitrary board fallbacks.
- Guarded installation, direct complete-bundle `dpkg` installation and Ubuntu
  image creation now use the same exact-ABI boot-support lifecycle. External
  delivery uses stock GRUB generation, permits one selected platform per root
  and ABI, and fails closed instead of reporting an unbound firmware-tree DTB
  as bootable.
- Kernel build, release, local discovery and image manifests now preserve the
  effective delivery mode, boot-support package and device-tree provenance as
  one validated schema contract.

### Fixed

- Fixed Ubuntu ISO customization trying to generate installed-system GRUB and
  initramfs state inside an offline chroot with no destination device or
  mounted pseudo-filesystems. Installed initramfs creation now uses the
  isolated ARM64 tools image's trusted Dracut implementation in non-hostonly
  sysroot mode with atomic publication, while image validation proves
  exact-ABI stock-GRUB generation readiness and leaves concrete `grub.cfg`
  generation to Ubuntu's mounted installation target.
- Fixed live hardware diagnostics and boot-support platform auto-selection
  relying on the absolute `/proc/device-tree` alias. Canonical sysfs identity
  is now preferred, with a contained proc fallback that cannot escape an
  alternate target root.
- Fixed `kernel install --json` output during real installs and
  rollback by routing package-manager, maintainer-hook and initramfs progress
  to the diagnostic stream instead of prefixing the JSON receipt on stdout.
- Fixed kernel delivery inspection rejecting normal generic ARM64 packages
  whose packaged device-tree inventory exceeds 1,024 files. Lexr now streams
  that inventory under independent member, path, per-file and aggregate byte
  bounds, retaining only declared external device trees or exact embedded
  matches instead of materialising every unrelated board file.
- Fixed raw kernel bundles being buildable but not independently consumable by
  Lexr or direct `sudo dpkg -i ./*.deb` because no package owned external-DTB
  provisioning. Failed provisioning and guarded installation now use bounded
  rollback while retaining independently verified fallback kernels.

## [0.2.0] - Unreleased

### Added

- Added partial-target detection for kernel delivery. `kernel preflight` and
  `kernel install` now classify an already used target ABI as
  `already-installed-complete` or `partial-or-inconsistent`, record bounded
  read-only evidence (boot files, module trees, firmware, device trees,
  headers, GRUB entries, and package database states), and explain safe,
  target-ABI-scoped next steps instead of failing on the first mismatch.
- Added `--overwrite` to `kernel preflight` and `kernel install` for
  explicitly replacing an existing complete or partial target installation.
  The flag prints a warning that an unsafe overwrite could break the system
  or prevent returning to the desktop on reboot; `--yes` alone never bypasses
  the fresh-target gate.

### Fixed

- Fixed `doctor boot` rejecting a valid Stubble kernel solely because its GRUB
  entry had no external `devicetree` directive. Static diagnosis now reuses
  the installer's embedded-or-external verifier while narrowing accepted DTB
  evidence to the requested or detected device. It reports the boot mode and
  digest in text and JSON and checks consistent delivery across normal and
  recovery entries. Title-based submenu defaults generated by Ubuntu are now
  resolved alongside numeric paths. Kernel bundle, release, preflight, and
  install output now distinguish package or build evidence from an installed
  boot binding consistently.
- Fixed kernel installation reporting success when package contents included
  the required device trees but the installed kernel had no boot-time device
  tree. Final target and fallback verification now require either an exact
  same-ABI embedded `.dtbauto` payload or a consistent external GRUB binding;
  an unbound target fails closed and uses the existing bounded rollback. Human
  and JSON receipts now distinguish packaged DTBs from verified boot binding.
- Fixed kernel preflight falsely reporting ambiguity whenever both Surface Pro
  11 device-tree variants were installed. ABI-stamped GRUB device-tree
  verification now attributes the physical hardware variant by digest before
  comparison, so coexisting OLED and LCD variants remain accepted while a boot
  device tree matching no installed variant still fails closed.
- Fixed native kernel installation accepting a GRUB entry whose legacy shared
  boot DTB belongs to another installed kernel patch line. Verification now
  compares any referenced boot-side DTB with the same-ABI firmware DTB without
  rewriting either file, and `lexr doctor boot` reports bounded GRUB, digest,
  default-selection, and retired-hook evidence read-only.
- Fixed Linux removable-media discovery aborting when one whole-disk LUN
  reports zero capacity. A zero-capacity whole disk is now skipped so
  independent valid devices remain visible in `lexr image devices`, while
  explicitly inspecting or writing to the zero-capacity path still fails
  closed.
- Fixed Darwin removable-media discovery aborting when a mounted volume is
  backed by a disk image through a synthesised APFS container (for example
  CoreSimulator runtime volumes). Such mounts are now skipped so independent
  physical devices remain visible in `lexr image devices`, while mounts that
  cannot be classified at all still fail closed.

### Added

- Added a version-matched installation section to generated CLI release notes.
  Each release now leads with the latest main-branch `install.sh`, an exact
  `--version` release selection, and a tagged installation guide before listing
  changes. The selected binary remains verified against that release's checksum
  manifest.
- Added an explicit `--force` override to kernel preflight and installation for
  retaining a known-good, fully verified fallback ABI that differs from the
  currently running kernel. The default exact-match guard remains in place;
  forced plans and receipts record and print the ABI mismatch warning.
- Added a POSIX `install.sh` one-command installer that detects OS and
  architecture, resolves the latest (or a pinned) version from GitHub releases,
  verifies the SHA-256 checksum against the versioned manifest, and installs
  the `lexr` binary to `/usr/local/bin` when writable or `~/.local/bin`
  otherwise, with idempotent PATH editing and `--version`, `--binary`,
  `--no-modify-path`, `--help`, and `LEXR_INSTALL_DIR`/`LEXR_OS`/`LEXR_ARCH`
  overrides.
- Added an experimental Fedora Workstation Live 44 ARM64 image adapter which
  preserves the source hybrid boot layout, turns a verified patch-line-qualified
  Surface kernel into a Lexr-built Fedora RPM, generates an exact-ABI
  `dracut-live` initramfs, and structurally validates the live boot and Anaconda
  installed-system hand-off contract before publication. Stock live fallbacks
  load manifest-bound X1E/X1P device trees explicitly; installed-system support
  is scoped to X1E until the X1P custom boot hand-off is qualified.
- Added optional Fedora-native IPTSD integration for the exact source-bearing
  `sp11-iptsd-v2` companion release. The adapter renders the exact OE-owned RPM
  template from authenticated source provenance, rebuilds the pinned sources
  and fallbacks as binary and source RPMs, installs the binary package into the
  Anaconda-carried live root, and independently validates both RPMs, their
  source template and their owned runtime layout. Images that omit the IPTSD
  companion remain unchanged.
- Added an explicit, build-and-release-provenance-recorded SP11 Stubble mode with packaged PE-section and Denali device-tree validation while retaining source-owned boot-image policy by default.
- Added closed `all` and `runtime` local kernel package-set selection. Inspection and installation now select every exact ABI/version-matched package from the emitted bundle declaration by default, include a coherent development-header pair when declared, fail if a complete declaration loses either header, preserve intentional runtime-only downloads, and verify both selected header trees after installation; `--package-set runtime` deliberately retains the image-and-modules-only path.

### Changed

- Taught `doctor` and `userspace status` to keep each SP11 generation in its
  kernel patch line. A `7.2.2/sp11v1` build is now reported honestly as newer
  and awaiting userspace qualification, instead of being failed against the
  older `7.2.0/sp11v19` evidence or quietly treated as `sp11v20`.
- Restored the established `sp11-qcom-x1e-<package-version>` kernel release
  tags, including `sp11-qcom-x1e-7.2.2-jg-0sp11v1`, and bound both publication
  stages to the version recorded by the verified package bundle.
- Made cross-repository IPTSD CI select a safely named, same-named branch from
  the canonical OE repository for branch pushes and same-repository pull
  requests, falling back to OE `main` only when that branch does not exist.
- Routed image validation, removable-media writing, and release preparation
  through the adapter declared by each generated manifest, and made Ubuntu and
  Fedora share the descriptor-anchored, no-replace output transaction.
- Clarified the image catalogue's capability and maturity signals. Ubuntu
  Concept and Fedora Workstation Live remain runnable through their implemented
  adapters, but stay experimental and now state that image structure and
  physical bootability still need complete end-to-end testing. A Fedora 44
  candidate passed structural validation and USB read-back but reached the
  emergency boot path followed by a persistent black screen on Surface Pro 11;
  Ubuntu qualification is tracked in
  [#16](https://github.com/ooaklee/lexr.sh/issues/16) and the Fedora failure in
  [#17](https://github.com/ooaklee/lexr.sh/issues/17).
- Advanced the trust-pinned IPTSD release to `sp11-iptsd-v2`. Its two-asset
  contract carries the complete Fedora package source beside the established
  payload while retaining the `iptsd-v1` component and internal archive root
  for compatible operator paths. Lexr verifies the tag, filenames, sizes, and
  hashes rather than relying on GitHub's repository-wide release locking.

### Fixed

- Made `kernel install` detect a staged installation that completed without the
  target ABI's `/boot/initrd.img-<abi>` image and explicitly regenerate it with
  the trusted `update-initramfs` generator before boot verification, so a
  package whose maintainer scripts skipped the image can no longer reach the
  reboot hand-off without an initramfs. The conditional create command is
  disclosed in preflight plans and dry-run output, and a failed repair triggers
  the existing bounded rollback.
- Scoped the retired out-of-tree touchscreen release guard to its historical
  `6.12.0-jg-0sp11v3-qcom-x1e` ABI and module assets, allowing later patch
  lines to use their own valid `sp11v3` generation for in-tree kernels.
- Made the kernel release workflow request Stubble explicitly, so the PE and
  embedded device-tree contract it validates is also recorded accurately in
  public build provenance.
- Made draft-first kernel publication create and verify its release tag
  explicitly, matching GitHub's draft semantics before remote byte validation.
- Allowed `kernel build --reset-source` to move a managed shallow checkout
  between refs without mistaking the retained shallow tip for a local commit,
  while preserving the non-reset build guard.
- Made native kernel builds package the common and ABI-specific headers as a complete pair alongside the signed image and modules.
- Emitted the installed-toolchain provenance digest without a trailing newline so strict bundle publication can validate it.
- Preserved package-postinst Surface device-tree injection by avoiding a redundant final GRUB regeneration after native kernel installation.
- Removed Lexr's duplicate Fedora IPTSD spec. Fedora image creation now fails
  before RPM tooling when the companion lacks the exact reviewed OE template,
  and image validation re-derives the expected raw and rendered spec from that
  same manifest-bound archive.

## [0.1.0] - 2026-08-31

### Added

- A standalone [Lexr.sh repository](https://github.com/ooaklee/lexr.sh) containing the complete feature history previously maintained beneath the OE repository's CLI directory, including the release workflow history.
- Feature-oriented Go CLI foundation with shared image orchestration, deterministic image plans, and execution journals.
- Strict, human-readable ARM64 installation-media catalogue schema v2 with explicit upstream filenames, versioned links, publisher checksums where available, and a dedicated validation package.
- GitHub kernel release discovery and checksum-verified bundle downloads.
- Version-bound kernel bundle validation for image, modules, ABI, version, package digests, and Surface Pro 11 device trees.
- Experimental Ubuntu Concept Casper remastering and structural output validation.
- Docker-isolated ARM64 image tooling.
- Host readiness checks plus reviewed, reversible detection and removal of selected obsolete workarounds.
- Strict clean-up plan files, crash-oriented atomic quarantine, durable prepared and completed receipts, and verified restoration without overwriting changed content.
- Bubble Tea wizard and scriptable command groups for catalogue, kernel, image, doctor, clean-up, and userspace workflows.
- Strict, human-readable userspace component catalogue with dedicated validation and action-capability checks.
- Shared, non-mutating `userspace status` and `doctor userspace` diagnostics for required, supported, experimental, diagnostic-only, and obsolete support.
- Catalogue-bound kernel compatibility, FullIO boot-argument, static AArch64 ELF dependency, package-state, and legacy-component diagnostics.
- Exact userspace release downloads that require an audited remote asset set and agreement between release digests and publisher checksums.
- Bounded source-build workflows for the pinned `iptsd` integration and experimental IMX681 libcamera package set.
- Explicit userspace installation with dry runs, re-verification, root-only mutation, and a deliberate recommended set containing audio and `iptsd`.
- A named Linux Docker volume for remaster filesystems so device nodes, ownership, case-sensitive paths, and extended attributes survive on every supported host.
- Deterministic installed-system hand-off with exact dpkg registration, a non-Casper initramfs, paired versioned device trees, explicit X1E/X1P GRUB entries, and bounded kernel lifecycle hooks.
- Repository-wide documentation quality gates covering all Go declarations, including tests, and British-English comments and public prose.
- Cross-platform GoReleaser configuration for Linux, macOS and Windows on AMD64 and ARM64.
- Adapter-owned live-media discovery metadata with exact Casper initramfs and ISO UUID synchronisation.
- A manifest-tracked on-media companion containing a static Linux ARM64 CLI, its corresponding source archive, validated catalogues, and optionally the redistribution-eligible IPTSD release.
- Closed-set companion validation that rehashes every extracted file and rejects absent, extra, malformed, or incorrectly permissioned payloads.
- Native local release preparation and closed-set validation for split image assets, kernel packages and source evidence, FullIO audio, and coherent camera packages.
- Native IMX681 route discovery, private packed-RAW10 capture validation, and deterministic PNG inspection rendering.
- Strict private Windows hand-off v2 collection, import, same-device application, restoration, and confirmed retention controls for platform firmware and Bluetooth evidence.
- Policy-bound original-INF provenance for every Windows firmware payload, with collector self-tests, Pester coverage, and companion-source delivery of the canonical collector.
- Protected local-NTFS Windows collection transactions with exact private ACLs, retained no-follow object identities, no-replace publication, and bounded cleanup which never traverses a reparse object.
- Physical Bluetooth controller authority which binds Windows evidence to the built-in WCN7850 radio and selects the matching Linux device-tree controller independently of its `hciN` index.
- Standalone quality gates that enforce documented Go declarations, British-English public prose, current Lexr entry points, and protection for private diagnostic output.
- Lexr-owned IPTSD integration and manually dispatched kernel build workflows which require no private credential in the OE repository.
- Draft-first OE kernel prerelease publication with complete remote asset comparison and native validation before promotion.
- A dedicated, repository-scoped OE release token confined to the hosted publication step, after independent validation of self-hosted build output.
- An exact Lexr release allow-list for six raw Linux, macOS and Windows executables, three legal documents, and one versioned checksum manifest, with no hardware-support or unrelated documentation payloads.
- A Go-native source builder which injects explicit Lexr version, revision and build-time metadata without relying on ambiguous automatic VCS discovery.
- Explicit `Apache-2.0` project terms, a project notice, and an audited third-party inventory covering every linked release dependency, the Go runtime, and embedded Unicode data.
- Human contribution and conduct guides plus a single-maintainer record for people who want to report a problem or help the project.
- Structured bug and feature issue forms, a contribution-aware pull-request template, and grouped weekly dependency updates for Go modules and GitHub Actions.

### Changed

- Renamed the predecessor package and executable to Lexr.sh and `lexr`
  respectively.
- Moved the tool to the root of its standalone repository and changed source-checkout, build and contribution guidance accordingly.
- Completed the Lexr rename across schema-4 on-media paths, private state paths,
  wire domains, system integration paths, and bundle filenames while retaining
  OE release references as external provenance.
- Removed the unsafe live-USB aDSP menu entry; every live entry now retains the USB-protection blacklist while installed systems remain unaffected.
- Advanced the image manifest to schema v4 so `companion_bundle` remains explicit while every media identity is Lexr-only.
- Advanced the Windows hand-off to schema v3 and collector `3.0.0`, and advanced private application receipts to schema v2, so mixed-identity pre-release state fails closed.
- Made verified userspace release receipts relocatable so an offline bundle remains installable after being copied to installation media.
- Retired the legacy root script directory, shell tests, helper tools, and their obsolete workflow after recording each native owner or intentional scope boundary.
- Introduced exact original-INF authority and removed an opaque adapter digest
  in the unpublished Windows hand-off v2 shape; v3 retains those policies, and
  v1 or v2 store entries must be purged with their exact creating binary before
  recollection.
- Made `PermanentAddress` authoritative only when structured PnP ancestry reaches the exact built-in radio; optional BTHPORT evidence must corroborate that same value, and external or ambiguous Windows radios fail closed.
- Kept the established OE repository as the publication and discovery channel for kernel and hardware-support releases while reserving Lexr releases for the CLI itself.
- Routed heavyweight kernel compilation only to a dedicated `lexr-kernel` self-hosted Linux runner with explicit architecture, Docker, and free-space gates.
- Replaced multi-file CLI release archives with directly downloadable Lexr executables, three accompanying legal documents, and one checksum manifest covering all nine payload files.

### Fixed

- Corrected Ubuntu live-media discovery so a regenerated Casper initramfs cannot be published with a stale ISO UUID marker.
- Restored offline installation compatibility with the immutable `sp11-iptsd-v1` archive while retaining exact operational-file identities and rejecting unreviewed documentation variants.
- Kept legacy diagnostic captures ignored after retiring their generators, and made repository-root legal documents authoritative for CLI publication, accompanying release assets, and each distributable companion inventory.
- Prevented a Git-submodule build from reporting the containing OE repository's revision as Lexr provenance.
