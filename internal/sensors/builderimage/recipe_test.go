package builderimage

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestDependencyRecipeIdentity binds the script and base to reviewed compiled
// policy, not a caller-supplied image tag, label, Dockerfile, or source tree.
func TestDependencyRecipeIdentity(t *testing.T) {
	t.Parallel()
	const expectedBase = "docker.io/library/ubuntu@sha256:61b65dc6bddff5e68c552f22126fe77496395f956ff2e983e05d8a52efd63e55"
	if BaseImage != expectedBase {
		t.Fatalf("base = %q, want reviewed immutable reference %q", BaseImage, expectedBase)
	}
	const expectedRecipe = "a69473cf303ef34cb26d04603fc2581e33f5297e510ed69d7916227432165a15"
	if got := RecipeSHA256(); got != expectedRecipe {
		t.Fatalf("recipe = %q, want reviewed script bytes %q", got, expectedRecipe)
	}
	digest := sha256.Sum256([]byte(DependencyScript))
	if got, want := RecipeSHA256(), hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("recipe identity = %q, want script SHA-256 %q", got, want)
	}
	if !strings.HasPrefix(DependencyScript, "#!/bin/bash\nset -euo pipefail\n") ||
		!strings.HasSuffix(DependencyScript, "\n") {
		t.Fatal("script lost strict Bash execution or final newline")
	}
}

// TestDependencyRecipeHasClosedPackageSet guards the only package acquisition
// operation and makes dependency additions explicit reviewable policy changes.
func TestDependencyRecipeHasClosedPackageSet(t *testing.T) {
	t.Parallel()
	const start = `apt-get "${apt_options[@]}" install -y --no-install-recommends `
	if strings.Count(DependencyScript, start) != 1 {
		t.Fatal("expected one fixed no-recommends package installation")
	}
	_, after, _ := strings.Cut(DependencyScript, start)
	packages, _, found := strings.Cut(after, "\napt-get clean\n")
	if !found {
		t.Fatal("could not find the closed package-list boundary")
	}
	got := strings.Fields(strings.ReplaceAll(packages, "\\\n", " "))
	want := []string{
		"binutils", "build-essential", "ca-certificates", "dbus-daemon", "gcc-14",
		"gir1.2-umockdev-1.0", "libglib2.0-dev", "libgudev-1.0-dev", "libpolkit-gobject-1-dev",
		"libprotobuf-c-dev", "libqrtr-glib-dev", "libudev-dev", "libxml2-utils", "linux-libc-dev",
		"meson", "ninja-build", "passwd", "pkgconf", "protobuf-c-compiler", "protobuf-compiler",
		"python3", "python3-dbus", "python3-dbusmock", "python3-gi", "python3-psutil",
		"systemd-dev", "ubuntu-keyring", "umockdev",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("package set = %#v, want %#v", got, want)
	}
	seen := make(map[string]bool, len(got))
	for _, name := range got {
		if seen[name] || !regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]*$`).MatchString(name) {
			t.Fatalf("duplicate, option-like, or nonliteral package %q", name)
		}
		seen[name] = true
	}
}

// TestDependencyRecipeRecordsObservedAuthority requires real package/tool
// observations, while avoiding a misleading fixed dependency-version claim.
func TestDependencyRecipeRecordsObservedAuthority(t *testing.T) {
	t.Parallel()
	if PackageInventoryPath != "/usr/share/lexr/sensors-builder/packages.txt" ||
		ToolchainInventoryPath != "/usr/share/lexr/sensors-builder/toolchain.txt" {
		t.Fatal("inventory output escaped its fixed image-local directory")
	}
	for _, required := range []string{
		`test "$ID" = ubuntu`, `test "$VERSION_ID" = 26.04`,
		`test "$(dpkg --print-architecture)" = arm64`,
		`case "$(uname -m)" in aarch64|arm64) ;; *) exit 1 ;; esac`,
		`available_bytes="$(df --output=avail -B1 / | tail -n1)"`,
		`case "$available_bytes" in`,
		`''|*[!0-9]*) echo 'invalid dependency image free-space result' >&2; exit 1 ;;`,
		`if ! test "$available_bytes" -ge 6442450944; then`,
		`dependency image requires at least 6442450944 free bytes (6 GiB); observed %s`,
		`printf '#!/bin/sh\nexit 101\n' >/usr/sbin/policy-rc.d`,
		`chmod 0755 /usr/sbin/policy-rc.d`,
		`apt-get "${apt_options[@]}" -o APT::Update::Error-Mode=any update`,
		`dpkg-query -W -f='${binary:Package}=${Version}\n'`,
		`LC_ALL=C sort >` + PackageInventoryPath,
		`'lexr-sensors-builder-toolchain-v1'`,
		`gcc-14 -dumpfullversion`, `gcc-14 -dumpmachine`, `gcc-14 --version`,
		`ld --version`, `readelf --version`, `meson --version`, `ninja --version`,
		`python3 --version`, `pkg-config --version`, `protoc --version`, `protoc-c --version`,
		`pkg-config --modversion glib-2.0 gio-2.0 gudev-1.0`,
		`polkit-gobject-1 qrtr-glib libprotobuf-c`,
		`test -s ` + PackageInventoryPath, `test -s ` + ToolchainInventoryPath,
	} {
		if !strings.Contains(DependencyScript, required) {
			t.Errorf("missing fixed dependency policy %q", required)
		}
	}
	policy := strings.Index(DependencyScript, "exit 101")
	apt := strings.Index(DependencyScript, `apt-get "${apt_options[@]}"`)
	if policy < 0 || apt < policy {
		t.Fatal("APT can run before service activation is inhibited")
	}
	space := strings.Index(DependencyScript, `if ! test "$available_bytes" -ge 6442450944; then`)
	if space < 0 || apt < space {
		t.Fatal("APT can run before the container-root free-space gate")
	}
}

// TestDependencyRecipeCannotIngestOrCompileSource rejects source workflows,
// third-party download scripts, relaxed APT authentication, and live sensors.
// These static checks are not evidence that APT or the image was executed.
func TestDependencyRecipeCannotIngestOrCompileSource(t *testing.T) {
	t.Parallel()
	for _, forbidden := range []string{
		"curl ", "wget ", "git ", "pip ", "pip3 ", "add-apt-repository",
		"--allow-unauthenticated", "--allow-insecure", "trusted=yes",
		"meson setup", "meson compile", "meson test", "ninja -C", "ninja install", "make ",
		"gcc-14 -c", "ssccli", "monitor-sensor", "systemctl ", "udevadm ",
		"/source", "/inputs", "/exchange", "/dev/", "/home/", "/Users/",
		"COPY ", "ADD ", "Dockerfile", "PRIVATE KEY", "authorized_keys",
	} {
		if strings.Contains(DependencyScript, forbidden) {
			t.Errorf("dependency-only script contains forbidden operation/input %q", forbidden)
		}
	}
}

// The account exists only in the new dependency image. Its home belongs to
// the later private volume; registration must neither create it nor relax the
// fixed unprivileged source identity.
func TestDependencyAccountPolicyAndInventoryOrdering(t *testing.T) {
	t.Parallel()
	for _, required := range []string{
		`test -f /usr/sbin/nologin`, `test -x /usr/sbin/nologin`,
		`groupadd --system --gid 65532 --key SYS_GID_MAX=65532 lexr-sensor-build`,
		`useradd --system --uid 65532 --gid 65532 --no-user-group`,
		`--no-create-home --no-log-init --home-dir /work/job/home`,
		`--shell /usr/sbin/nologin --comment 'Lexr sensor source builder'`,
		`--password '!' --key SYS_UID_MAX=65532 lexr-sensor-build`,
		`sensor_password_status="$(passwd --status lexr-sensor-build)"`,
		`test "$sensor_status_name" = lexr-sensor-build`,
		`test "$sensor_password_lock" = L`,
		`printf '[build-identity]\n%s\n%s\npassword_status=%s\n'`,
	} {
		if strings.Count(DependencyScript, required) != 1 {
			t.Errorf("fixed account policy missing or duplicated: %q", required)
		}
	}
	var previous = -1
	for _, marker := range []string{
		"apt-get clean\n", "# LEXR_SENSOR_ACCOUNT_BEGIN\n", "groupadd --system", "useradd --system",
		"python3 - <<'LEXR_SENSOR_ACCOUNT_VERIFY'", "sensor_passwd_name=", "sensor_password_status=",
		"# LEXR_SENSOR_ACCOUNT_END\n", "dpkg-query -W", "printf '[build-identity]",
	} {
		i := strings.Index(DependencyScript, marker)
		if i <= previous {
			t.Fatalf("account preparation/verification/inventory order differs at %q", marker)
		}
		previous = i
	}
	for _, marker := range []string{"test ! -e /work/job/home", "test ! -L /work/job/home"} {
		if strings.Count(DependencyScript, marker) != 2 ||
			strings.Index(DependencyScript, marker) > strings.Index(DependencyScript, "# LEXR_SENSOR_ACCOUNT_BEGIN") ||
			strings.LastIndex(DependencyScript, marker) < strings.Index(DependencyScript, "# LEXR_SENSOR_ACCOUNT_END") {
			t.Fatalf("home absence must be checked before and after registration: %q", marker)
		}
	}
	for _, forbidden := range []string{"usermod ", "groupmod ", "--non-unique", "--create-home", "--force", "mkdir /work"} {
		if strings.Contains(DependencyScript, forbidden) {
			t.Errorf("account policy contains forbidden adjustment %q", forbidden)
		}
	}
}
