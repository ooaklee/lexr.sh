package fedora

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/image/sp11"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestInitramfsValidatorRealKmodIntegration exercises the production validator
// with actual package modules and kmod indexes. Only archive unpacking and the
// dracut inventory listing are substituted; this does not test dracut generation.
// No modules are loaded, and every mutation stays in an owned disposable volume.
func TestInitramfsValidatorRealKmodIntegration(t *testing.T) {
	archive := os.Getenv("LEXR_ELEMENTARY_MODULE_TEST_DEB")
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" || archive == "" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 and LEXR_ELEMENTARY_MODULE_TEST_DEB for real-kmod integration")
	}
	name, prefixed := strings.CutPrefix(filepath.Base(archive), "linux-modules-")
	abi, _, ok := strings.Cut(name, "_")
	if !prefixed || !ok || !sp11.SafeKernelABI(abi) {
		t.Fatal("expected a linux-modules-<ABI>_<version>_<arch>.deb fixture")
	}

	// Worktrees may be outside Docker Desktop's shared paths. Allow an explicit
	// shared parent, otherwise use the checkout's build directory.
	build := os.Getenv("LEXR_TEST_FEDORA_KMOD_WORKSPACE")
	if build == "" {
		build = filepath.Join("..", "..", "..", "build")
	}
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(build, ".fedora-real-kmod-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(workspace); err != nil {
			t.Errorf("remove real-kmod workspace: %v", err)
		}
	})
	source, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := os.Create(filepath.Join(workspace, "modules.deb"))
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("copy module package: %v %v", copyErr, closeErr)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	docker := platform.NewDocker(nil)
	image, err := docker.EnsureToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	volume, err := docker.CreateWorkVolume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := docker.RemoveWorkVolume(cleanupContext, volume); err != nil {
			t.Errorf("remove real-kmod volume: %v", err)
		}
	})
	const extract = `set -o pipefail
abi=$1
shift
root=/linux-work/rootfs
test "$(dpkg-deb -f /work/modules.deb Package)" = "linux-modules-$abi"
test "$(dpkg-deb -f /work/modules.deb Architecture)" = arm64
dpkg-deb -x /work/modules.deb "$root"
ln -s usr/lib "$root/lib"
depmod -C /dev/null -b "$root" "$abi"
sha256sum "$root/usr/lib/modules/$abi"/modules.* > /linux-work/expected-module-metadata.sha256
mkdir -p "$root/tmp"
for firmware in "$@"; do
    file="$root/usr/lib/firmware/$firmware"
    mkdir -p "${file%/*}"
    printf 'real-kmod fixture firmware\n' > "$file"
done
`
	args := append([]string{"bash", "-ceu", extract, "fedora-real-kmod-extract", abi}, earlyFirmware()...)
	if output, err := docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume, args...); err != nil {
		t.Fatalf("prepare real module package: %v\n%s", err, output)
	}

	const setup = `set -o pipefail
abi=$1
root=/linux-work/rootfs
fixture=/linux-work/archive-fixture
modules="$fixture/usr/lib/modules/$abi"
rm -rf "$fixture"
mkdir -p "$fixture"
# Hard-link immutable objects so each case need not duplicate a full kernel tree.
# Mutations below unlink or replace files; they never write through shared links.
cp -al "$root/usr" "$fixture/usr"
ln -s usr/lib "$fixture/lib"
# depmod must only be able to rewrite the fixture's metadata, even if its
# implementation writes an index in place instead of replacing it atomically.
for metadata in "$modules"/modules.*; do
    [ -f "$metadata" ] || continue
    cp "$metadata" "$metadata.private"
    mv "$metadata.private" "$metadata"
done
module_relative() {
    local file
    file=$(modinfo -b "$root" -k "$abi" -F filename "$1")
    case "$file" in
        "$root/lib/modules/$abi/"*) printf '%s\n' "${file#"$root/lib/modules/$abi/"}" ;;
        "$root/usr/lib/modules/$abi/"*) printf '%s\n' "${file#"$root/usr/lib/modules/$abi/"}" ;;
        *) echo "fixture requires loadable $1" >&2; return 1 ;;
    esac
}
`
	const unpack = `chroot() {
    local root=$1
    shift
    case "$1" in
        mktemp) mktemp -d "$root/tmp/lexr-initrd-check.XXXXXX" | sed "s|^$root||" ;;
        /usr/bin/lsinitrd) printf 'dmsquash-live\n' ;;
        /usr/bin/bash) cp -al /linux-work/archive-fixture/. "$root$5/" ;;
        *) echo "unexpected fixture chroot call: $*" >&2; return 1 ;;
    esac
}
`
	for _, testcase := range []struct {
		name, mutation, failure string
	}{
		{name: "complete module lookup"},
		{"missing SSAM UART", `rm "$modules/$(module_relative qcom_geni_serial)"`, "qcom_geni_serial"},
		{"missing SSAM registry", `rm "$modules/$(module_relative surface_aggregator_registry)"`, "surface_aggregator_registry"},
		{"missing UCSI service", `rm "$modules/$(module_relative ucsi_glink)"`, "ucsi_glink"},
		{"missing UCSI protocol", `rm "$modules/$(module_relative typec_ucsi)"`, "typec_ucsi"},
		{"missing Type-C retimer", `rm "$modules/$(module_relative ps883x)"`, "ps883x"},
		{"missing dependency binary index", `rm "$modules/modules.dep.bin"`, "modules.dep.bin"},
		{"corrupt dependency binary index", `rm "$modules/modules.dep.bin"; printf broken > "$modules/modules.dep.bin"`, "initramfs cannot resolve required driver"},
		{"missing builtin binary index", `rm "$modules/modules.builtin.bin"`, "modules.builtin.bin"},
		{"loadable UART falsely indexed as builtin", `relative=$(module_relative qcom_geni_serial)
rm "$modules/$relative"
cp "$modules/modules.builtin" "$modules/modules.builtin.new"
relative=${relative%.zst}
printf '%s\n' "${relative%.xz}" >> "$modules/modules.builtin.new"
mv "$modules/modules.builtin.new" "$modules/modules.builtin"
depmod -C /dev/null -b "$fixture" "$abi"`, "initramfs module closure differs for qcom_geni_serial"},
	} {
		passed := t.Run(testcase.name, func(t *testing.T) {
			if output, err := docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume,
				"bash", "-ceu", setup+testcase.mutation+"\nsha256sum -c /linux-work/expected-module-metadata.sha256 >/dev/null\n", "fedora-real-kmod-fixture", abi); err != nil {
				t.Fatalf("prepare archive mutation: %v\n%s", err, output)
			}
			output, err := docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume,
				"bash", "-ceu", unpack+initramfsValidationScript(), "fedora-real-kmod-check", abi)
			diagnostic := string(output)
			if err != nil {
				// ExecRunner reports captured stderr in its returned error.
				diagnostic += "\n" + err.Error()
			}
			if testcase.failure == "" {
				if err != nil {
					t.Fatalf("complete fixture must pass: %v\n%s", err, output)
				}
			} else if err == nil || !strings.Contains(diagnostic, testcase.failure) {
				t.Fatalf("expected rejection containing %q, got error=%v\n%s", testcase.failure, err, output)
			}
		})
		if testcase.failure == "" && !passed {
			t.Fatal("real-kmod baseline failed; negative cases would not be meaningful")
		}
	}
}
