# Requirements by workflow

A Lexr executable is available for six host targets, but that does not make
every workflow portable to all six. Check the job you intend to run before
choosing a host or granting privilege.

## Executable targets

CLI releases provide raw executables for Linux, macOS and Windows on AMD64 and
ARM64. The embedded image and userspace catalogues make catalogue-driven
commands self-contained, but host integrations still fail closed when their
required operating-system boundary is unavailable.

## Workflow requirements

| Workflow | Host and tools | Storage or network | Privilege |
| --- | --- | --- | --- |
| Run version, help or catalogue commands | Matching release executable | None after download | Regular user |
| Build Lexr from source | Go 1.26 or newer | Network for the initial clone and Go modules | Regular user |
| Create or validate the current Ubuntu image | Linux or macOS; Docker CLI, running daemon and Linux ARM64 container support | At least 24 GiB free workspace; the readiness check fails below it. Network is needed only for remote image or kernel acquisition | Regular user when inputs are readable |
| Add an offline companion | Image requirements plus Go for `--companion-source-dir` | Output and workspace must remain outside the clean source tree | Regular user |
| Discover or write removable media | Linux: `lsblk`, `umount`, `udisksctl`; macOS: `diskutil`, `plutil` | A removable whole device large enough for the image | Discovery and dry run use a regular user; the real raw write needs elevation |
| Prepare an image release | Linux or macOS plus host `zstd` | Space for the ISO, split parts and fresh release directory | Regular user |
| Build a kernel | Docker with Linux ARM64 execution | The managed build volume requires 40 GiB | Regular user with Docker access |
| Install a kernel | Debian or Ubuntu target with `apt-get`, `dpkg`, `dpkg-deb`, `update-initramfs` and `chroot` as required | Complete bundle plus a proven bootable fallback ABI | Preflight and dry run use a regular user; installation needs effective root and `--yes` |
| Inspect or pull userspace support | A readable live or mounted Linux target | Network only for pull | Regular user |
| Install userspace support | Supported Linux target and its package tools | Verified component directory | Effective root and `--yes` for a real install |
| Build camera packages | Native ARM64 Linux | 20 GiB working space by default and the authenticated OE checkout | Regular user for the build |
| Capture experimental camera frames | Linux with `media-ctl`, `v4l2-ctl`, `journalctl` or `dmesg`, `uname` and `fuser` | Private space for raw frames and previews | Depends on access to the media devices and logs |
| Collect a Windows hand-off | Elevated Windows PowerShell 5.1 and the separate collector from tagged source or an offline companion | New protected fixed-NTFS parent and a physically controlled transfer medium | Elevated collection session |
| Import and apply a hand-off | Linux for the target workflow; keep the same explicit private store path | Private hand-off directory and target filesystem | Import/list use the managing user; apply and restore need elevation and exact confirmation. A restore preview normally needs elevation because application creates its receipt directory as private root-owned state |
| Scan and plan clean-up | Supported Linux target | Space for plans and later recovery records | Regular user |
| Apply or restore clean-up | Supported Linux target | Private same-filesystem quarantine, backups and receipt | Effective root and `--yes` |

The dedicated CI kernel runner has a wider operational envelope than a local
kernel command: the workflow requires a self-hosted Linux x86-64 or ARM64 host,
at least 64 GiB free in Docker storage and at least 8 GiB free in its Actions
workspace. See [automation and release channels](../operator-manual/automation-and-releases.md).

## Network access

Lexr needs network access only when it must acquire something remotely: an
upstream image, kernel release, userspace release, source repository or Go
module. Supplying already downloaded inputs does not silently grant permission
to refresh them.

## Privilege model

Lexr never elevates itself. Image creation and validation, catalogues,
downloads, builds, diagnostics, hand-off import and readable dry runs normally
run as your regular user. Elevate only the operation which must write a raw
device or real system root.

Different mutations use different consent models. Kernel and userspace installs
and clean-up use a reviewed dry run plus `--yes`; removable-media and hand-off
operations bind an exact confirmation phrase to current inputs, state and
target. Do not replace one model with another in scripts.
