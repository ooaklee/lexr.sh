// Package builderimage owns the dependency-only native sensor builder image.
// Source compilation and hardware access are outside this image producer.
package builderimage

import (
	"crypto/sha256"
	"encoding/hex"
)

// The dependency base and inventory paths are fixed inputs to image preparation.
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
  meson ninja-build passwd pkgconf protobuf-c-compiler protobuf-compiler \
  python3 python3-dbus python3-dbusmock python3-gi python3-psutil \
  systemd-dev ubuntu-keyring umockdev
apt-get clean

# The offline source phase keeps this numeric identity, but its private D-Bus
# fixtures also need a resolvable account. Register it in this fresh image only;
# its runtime home is created later in the exclusively owned work volume.
test -f /usr/sbin/nologin
test -x /usr/sbin/nologin
test ! -e /work/job/home
test ! -L /work/job/home
# LEXR_SENSOR_ACCOUNT_BEGIN
python3 - <<'LEXR_SENSOR_ACCOUNT_UNUSED'
import grp
import pwd

for lookup, key in ((pwd.getpwnam, 'lexr-sensor-build'),
                    (pwd.getpwuid, 65532),
                    (grp.getgrnam, 'lexr-sensor-build'),
                    (grp.getgrgid, 65532)):
    try:
        lookup(key)
    except KeyError:
        continue
    raise SystemExit('sensor build account name or numeric ID is already occupied')
LEXR_SENSOR_ACCOUNT_UNUSED
groupadd --system --gid 65532 --key SYS_GID_MAX=65532 lexr-sensor-build
useradd --system --uid 65532 --gid 65532 --no-user-group \
  --no-create-home --no-log-init --home-dir /work/job/home \
  --shell /usr/sbin/nologin --comment 'Lexr sensor source builder' \
  --password '!' --key SYS_UID_MAX=65532 lexr-sensor-build
python3 - <<'LEXR_SENSOR_ACCOUNT_VERIFY'
import grp
import pwd

user = pwd.getpwnam('lexr-sensor-build')
group = grp.getgrnam('lexr-sensor-build')
assert user == pwd.getpwuid(65532)
assert (user.pw_name, user.pw_passwd, user.pw_uid, user.pw_gid,
        user.pw_gecos, user.pw_dir, user.pw_shell) == (
    'lexr-sensor-build', 'x', 65532, 65532, 'Lexr sensor source builder',
    '/work/job/home', '/usr/sbin/nologin')
assert group == grp.getgrgid(65532)
assert (group.gr_name, group.gr_passwd, group.gr_gid, group.gr_mem) == (
    'lexr-sensor-build', 'x', 65532, [])
LEXR_SENSOR_ACCOUNT_VERIFY
sensor_passwd_name="$(getent passwd lexr-sensor-build)"
sensor_passwd_id="$(getent passwd 65532)"
sensor_group_name="$(getent group lexr-sensor-build)"
sensor_group_id="$(getent group 65532)"
test "$sensor_passwd_name" = "$sensor_passwd_id"
test "$sensor_group_name" = "$sensor_group_id"
sensor_password_status="$(passwd --status lexr-sensor-build)"
read -r sensor_status_name sensor_password_lock sensor_status_rest <<<"$sensor_password_status"
test "$sensor_status_name" = lexr-sensor-build
test "$sensor_password_lock" = L
# LEXR_SENSOR_ACCOUNT_END
test ! -e /work/job/home
test ! -L /work/job/home

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
  printf '[build-identity]\n%s\n%s\npassword_status=%s\n' \
    "$sensor_passwd_name" "$sensor_group_name" "$sensor_password_lock"
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
