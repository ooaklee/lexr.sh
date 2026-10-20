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
	const expectedRecipe = "fa788880ee6cb31e5fd78528a7121a9c22da5fd9c6a4630da70ea3c35c48f828"
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
		"meson", "ninja-build", "pkgconf", "protobuf-c-compiler", "protobuf-compiler",
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
