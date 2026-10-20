// Package builderimage owns the dependency-only native sensor builder image.
// Source compilation and hardware access are outside this image producer.
package builderimage

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	// BaseImage is the pinned public Ubuntu 26.04 base also used by the camera
	// builder. It is not an identity for a completed sensor dependency image.
	BaseImage = "docker.io/library/ubuntu@sha256:61b65dc6bddff5e68c552f22126fe77496395f956ff2e983e05d8a52efd63e55"
	// PackageInventoryPath records all installed package versions after APT.
	PackageInventoryPath = "/usr/share/lexr/sensors-builder/packages.txt"
	// ToolchainInventoryPath records the observed tools and development APIs.
	ToolchainInventoryPath = "/usr/share/lexr/sensors-builder/toolchain.txt"
)

// DependencyScript is the complete compiled dependency-preparation policy,
// supplied as a command argument to a fresh owned container with no source,
// context, secrets or host bind mounts. The producer must bound execution and
// verify terminal success before committing its image. NoPull must prohibit
// both base acquisition and this network-backed preparation before execution.
//
// APT uses the pinned base's distribution repositories and authentication.
// Package versions are observed, not locked: this is not bit-reproducibility
// or permission to run source compilation in the network-enabled container.
const DependencyScript = `#!/bin/bash
set -euo pipefail
umask 022
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
export DEBIAN_FRONTEND=noninteractive
export LANG=C.UTF-8
export LC_ALL=C.UTF-8
export TZ=UTC
export PYTHONDONTWRITEBYTECODE=1

. /etc/os-release
test "$ID" = ubuntu
test "$VERSION_ID" = 26.04
test "$(dpkg --print-architecture)" = arm64
case "$(uname -m)" in aarch64|arm64) ;; *) exit 1 ;; esac

# This checks the Docker container's root filesystem, not host free space.
# Reject malformed output and low space before invoking APT; never delete caches.
available_bytes="$(df --output=avail -B1 / | tail -n1)"
available_bytes="${available_bytes#"${available_bytes%%[![:space:]]*}"}"
available_bytes="${available_bytes%"${available_bytes##*[![:space:]]}"}"
case "$available_bytes" in
  ''|*[!0-9]*) echo 'invalid dependency image free-space result' >&2; exit 1 ;;
esac
if ! test "$available_bytes" -ge 6442450944; then
  printf 'dependency image requires at least 6442450944 free bytes (6 GiB); observed %s\n' "$available_bytes" >&2
  exit 1
fi

# Dependency installation must not activate a service in the builder.
test ! -L /usr/sbin/policy-rc.d
printf '#!/bin/sh\nexit 101\n' >/usr/sbin/policy-rc.d
chmod 0755 /usr/sbin/policy-rc.d

apt_options=(
  -o Acquire::Retries=2
  -o Acquire::http::Timeout=30
  -o Acquire::https::Timeout=30
)
apt-get "${apt_options[@]}" -o APT::Update::Error-Mode=any update
apt-get "${apt_options[@]}" install -y --no-install-recommends \
  binutils build-essential ca-certificates dbus-daemon gcc-14 \
  gir1.2-umockdev-1.0 libglib2.0-dev libgudev-1.0-dev libpolkit-gobject-1-dev \
  libprotobuf-c-dev libqrtr-glib-dev libudev-dev libxml2-utils linux-libc-dev \
  meson ninja-build pkgconf protobuf-c-compiler protobuf-compiler \
  python3 python3-dbus python3-dbusmock python3-gi python3-psutil \
  systemd-dev ubuntu-keyring umockdev
apt-get clean

test ! -L /usr/share/lexr
test ! -e /usr/share/lexr/sensors-builder
test ! -L /usr/share/lexr/sensors-builder
install -d -m 0755 /usr/share/lexr/sensors-builder
dpkg-query -W -f='${binary:Package}=${Version}\n' | \
  LC_ALL=C sort >/usr/share/lexr/sensors-builder/packages.txt
{
  printf '%s\n' 'lexr-sensors-builder-toolchain-v1'
  printf 'os_id=%s\nos_version=%s\n' "$ID" "$VERSION_ID"
  printf 'architecture=%s\n' "$(dpkg --print-architecture)"
  printf '[gcc-version]\n'
  gcc-14 -dumpfullversion
  printf '[gcc-target]\n'
  gcc-14 -dumpmachine
  printf '[gcc-details]\n'
  gcc-14 --version
  printf '[linker]\n'
  ld --version
  printf '[readelf]\n'
  readelf --version
  printf '[meson]\n'
  meson --version
  printf '[ninja]\n'
  ninja --version
  printf '[python]\n'
  python3 --version
  printf '[pkgconf]\n'
  pkg-config --version
  printf '[protoc]\n'
  protoc --version
  printf '[protoc-c]\n'
  protoc-c --version
  printf '[development-api-versions:glib-2.0,gio-2.0,gudev-1.0,polkit-gobject-1,qrtr-glib,libprotobuf-c]\n'
  pkg-config --modversion glib-2.0 gio-2.0 gudev-1.0 \
    polkit-gobject-1 qrtr-glib libprotobuf-c
} >/usr/share/lexr/sensors-builder/toolchain.txt
test -s /usr/share/lexr/sensors-builder/packages.txt
test -s /usr/share/lexr/sensors-builder/toolchain.txt
chmod 0444 /usr/share/lexr/sensors-builder/packages.txt \
  /usr/share/lexr/sensors-builder/toolchain.txt
`

// RecipeSHA256 identifies exactly the embedded command-argument script bytes.
// The base reference and observed resulting image ID are separate provenance.
func RecipeSHA256() string {
	digest := sha256.Sum256([]byte(DependencyScript))
	return hex.EncodeToString(digest[:])
}
