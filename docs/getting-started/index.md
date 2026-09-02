# Get started with Lexr

This path takes you from a downloaded CLI to a structurally validated,
experimental Snapdragon X image candidate using Lexr's current Surface Pro 11
workflow.
Nothing is written to a USB device until you inspect a separate dry run and
provide the exact confirmation Lexr prints.

## 1. Install the CLI

Follow [Install Lexr](install.md) to choose a stable or prerelease executable,
verify its SHA-256 digest, and add it to your `PATH`. You can also build from
source with the project builder.

Confirm the result before continuing:

```sh
lexr version
```

## 2. Check the host

Image creation currently needs Docker with a running daemon and Linux ARM64
container support, at least 24 GiB of free workspace storage, and network access
for any image or kernel downloads.

```sh
lexr doctor
```

The doctor is a preliminary readiness check. It confirms the Docker CLI and
daemon are reachable and checks workspace capacity; the actual image workflow
is still the final proof that the host can run its ARM64 tooling. See the
[requirements reference](../reference/requirements.md) if you plan to write a
USB device, build a camera component or prepare a release.

## 3. Create and validate an image

```sh
lexr image create --output lexr-ubuntu-sp11.iso
lexr image validate lexr-ubuntu-sp11.iso
```

The convenient path above uses the default Ubuntu catalogue entry and kernel
release. Canonical does not publish a checksum beside the dated Ubuntu
snapshot, so the [installation-media guide](../user-guide/installation-media.md)
also shows the stronger local-source path with an explicitly recorded digest.
That guide separately covers Fedora Workstation Live 44, which must be selected
explicitly and paired with a patch-line-qualified verified kernel bundle. The
accepted floors are 7.2.0/sp11v19 and 7.2.2/sp11v1. Both catalogue entries are
`implemented` because their adapters run, and `experimental` because structural
validation does not yet establish physical bootability. The known Fedora
candidate reached an emergency boot path and then a persistent black screen on
Surface Pro 11. Follow [the Ubuntu qualification](https://github.com/ooaklee/lexr.sh/issues/16)
or [the Fedora boot investigation](https://github.com/ooaklee/lexr.sh/issues/17)
before interpreting a structurally valid image as supported hardware.

## 4. Review the USB write

List candidate whole devices, then ask Lexr for a plan. Neither command writes
to the device.

```sh
lexr image devices
lexr image write lexr-ubuntu-sp11.iso --device <whole-device> --dry-run
```

Stop and check the device identity, size and image digest in that plan. Continue
with [Write and verify the image](../user-guide/installation-media.md#3-write-and-verify-the-image)
only when you have a recovery device and are certain the selected disk can be
overwritten.

## After a successful first boot

Use [userspace support](../user-guide/userspace-support.md) to see which hardware
components are ready or missing. If the medium carries the optional CLI and
IPTSD payload, follow the [offline companion guide](../user-guide/offline-companion.md)
instead.
