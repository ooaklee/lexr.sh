# Get started with Lexr

This path takes you from a downloaded CLI to a validated Surface Pro 11 image.
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
explicitly and paired with a v19-or-newer verified kernel bundle.

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

## After the first boot

Use [userspace support](../user-guide/userspace-support.md) to see which hardware
components are ready or missing. If the medium carries the optional CLI and
IPTSD payload, follow the [offline companion guide](../user-guide/offline-companion.md)
instead.
