package sp11

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestModuleClosureAcceptsBuiltinsAndDependencies keeps compatible kernels
// independent of the v23 baseline and normalises repeated shared dependencies.
func TestModuleClosureAcceptsBuiltinsAndDependencies(t *testing.T) {
	const root, abi = "/inspection", "7.3.0-test-qcom-x1e"
	dependency := "insmod " + root + "/lib/modules/" + abi + "/kernel/drivers/remoteproc/qcom_common.ko.zst \n"
	output := dependency + "builtin qcom-q6v5-pas\n" + dependency +
		"insmod " + root + "/lib/modules/" + abi + "/kernel/drivers/gpu/drm/msm/msm.ko.xz\n"
	got, err := moduleClosure(output, root, abi)
	want := []string{"builtin qcom_q6v5_pas", "insmod kernel/drivers/gpu/drm/msm/msm.ko", "insmod kernel/drivers/remoteproc/qcom_common.ko"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("closure=%v error=%v", got, err)
	}
}

// TestModuleClosureIgnoresIndependentBranchOrder covers depmod's different
// valid traversal orders for a complete package and an initramfs subset.
func TestModuleClosureIgnoresIndependentBranchOrder(t *testing.T) {
	const root, abi = "/inspection", "7.3.0-test-qcom-x1e"
	prefix := "insmod " + root + "/lib/modules/" + abi + "/kernel/"
	first, err := moduleClosure(prefix+"drm_exec.ko\n"+prefix+"gpu-sched.ko\n"+prefix+"msm.ko\n", root, abi)
	if err != nil {
		t.Fatal(err)
	}
	second, err := moduleClosure(prefix+"gpu-sched.ko\n"+prefix+"drm_exec.ko\n"+prefix+"msm.ko\n", root, abi)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("equivalent dependency sets differ: %v, %v, error=%v", first, second, err)
	}
}

// TestModuleClosureRejectsForeignPathsAndCommands ensures resolver output is
// data only and cannot redirect comparisons outside the selected kernel tree.
func TestModuleClosureRejectsForeignPathsAndCommands(t *testing.T) {
	const root, abi = "/inspection", "7.3.0-test-qcom-x1e"
	prefix := "insmod " + root + "/lib/modules/" + abi + "/"
	for _, output := range []string{
		"", "install /bin/true", "builtin ../../outside", "builtin module.name",
		"insmod /elsewhere/module.ko", "insmod /inspection/lib/modules/old/kernel/module.ko",
		prefix + "../../module.ko", prefix + "kernel/../module.ko", prefix + "kernel/module.ko force=1",
		prefix + "kernel/module.txt", prefix + "kernel//module.ko",
		prefix + "kernel/module.ko\n" + prefix + "kernel/module.ko.zst\n",
	} {
		if _, err := moduleClosure(output, root, abi); err == nil {
			t.Fatalf("accepted invalid closure %q", output)
		}
	}
}

// TestEarlyModuleClosureIntegration exercises trusted kmod and actual kernel
// objects, including absent DSP/dependencies and corrupt concatenated archives.
// Supply a local redistributable linux-modules Debian package; no download or
// host module load is performed, and all mutations stay in a disposable volume.
func TestEarlyModuleClosureIntegration(t *testing.T) {
	archive := os.Getenv("LEXR_ELEMENTARY_MODULE_TEST_DEB")
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" || archive == "" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 and LEXR_ELEMENTARY_MODULE_TEST_DEB for kernel-object integration")
	}
	name, prefixed := strings.CutPrefix(filepath.Base(archive), "linux-modules-")
	abi, _, ok := strings.Cut(name, "_")
	if !prefixed || !ok || !SafeKernelABI(abi) {
		t.Fatal("expected a linux-modules-<ABI>_<version>_<arch>.deb fixture")
	}
	// Keep the exchange directory inside the checkout shared with Docker;
	// macOS system temporary directories may be outside its file sharing roots.
	// Generated package data belongs under build so concurrent source-quality
	// checks do not mistake the fixture for repository source.
	build := filepath.Join("..", "..", "..", "build")
	if err := os.MkdirAll(build, 0o755); err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(build, ".lexr-module-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(workspace); err != nil {
			t.Errorf("remove module test workspace: %v", err)
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
		t.Fatalf("copy kernel fixture: %v %v", copyErr, closeErr)
	}
	docker := platform.NewDocker(nil)
	image, err := docker.EnsureToolsImage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	volume, err := docker.CreateWorkVolume(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := docker.RemoveWorkVolume(context.Background(), volume); err != nil {
			t.Errorf("remove module test volume: %v", err)
		}
	})
	const extract = `abi=$1
root=/linux-work/package-linux-modules-$abi
test "$(dpkg-deb -f /work/modules.deb Package)" = "linux-modules-$abi"
dpkg-deb -x /work/modules.deb "$root"
ln -s usr/lib "$root/lib"
depmod -C /dev/null -b "$root" "$abi"
`
	if err := docker.RunInWorkspaceVolume(t.Context(), image, workspace, volume, "bash", "-ceu", extract, "module-fixture", abi); err != nil {
		t.Fatal(err)
	}
	for _, testcase := range []struct {
		name, mutation string
		valid          bool
	}{
		{"complete split archives", "", true},
		{"fresh subset indices", `ln -s usr/lib /linux-work/live-initrd/early2/lib; rm "$live/modules.order"; depmod -C /dev/null -b /linux-work/live-initrd/early2 "$abi"`, true},
		{"decompressed Debian modules", decompressedModulesFixture, true},
		{"xz representation", `zstd -dcq "$live/$dependency" | xz > "$live/${dependency%.zst}.xz"
rm "$live/$dependency"
ln -s usr/lib /linux-work/live-initrd/early2/lib
rm "$live/modules.order"
depmod -C /dev/null -b /linux-work/live-initrd/early2 "$abi"`, true},
		{"corrupt decompressed dependency", decompressedModulesFixture + `printf broken > "$live/${dependency%.zst}"`, false},
		{"ambiguous compressed and plain module", `zstd -dcq "$live/$dependency" > "$live/${dependency%.zst}"`, false},
		{"stale plain early copy", decompressedModulesFixture + `file="$live/${dependency%.zst}"; cp --parents "${file#/linux-work/live-initrd/early2/}" /linux-work/live-initrd/main/; printf stale > "$file"`, false},
		{"missing DSP", `rm "$live/$dsp"`, false},
		{"missing OLED panel with complete msm dependencies", `rm "$live/$oled"`, false},
		{"missing LCD panel module or builtin index", `if [ "$lcd" = "(builtin)" ]; then
sed -i '\|kernel/drivers/gpu/drm/panel/panel-edp.ko|d' "$live/modules.builtin"
ln -s usr/lib /linux-work/live-initrd/early2/lib
rm "$live/modules.order"
depmod -C /dev/null -b /linux-work/live-initrd/early2 "$abi"
else
rm "$live/$lcd"
fi`, false},
		{"missing QRTR socket protocol", `rm "$live/$qrtr"`, false},
		{"missing QRTR remote transport", `rm "$live/$transport"`, false},
		{"missing in-kernel domain mapper", `rm "$live/$mapper"`, false},
		{"missing transitive dependency", `rm "$live/$dependency"`, false},
		{"corrupt dependency", `file="$live/$dependency"; rm "$file"; printf broken > "$file"`, false},
		{"stale early override", `file="$live/$dsp"; cp --parents "${file#/linux-work/live-initrd/early2/}" /linux-work/live-initrd/main/; rm "$file"; printf stale > "$file"`, false},
		{"installed DSP missing", `rm "$installed/$dsp"`, false},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			const setup = `set -o pipefail
abi=$1
root=/linux-work/package-linux-modules-$abi
rm -rf /linux-work/live-initrd /linux-work/installed-initrd /linux-work/live-module-view /linux-work/installed-module-view
shift
for kind in live installed; do
    destination=/linux-work/$kind-initrd/early2/usr/lib/modules/$abi
    mkdir -p "$destination" /linux-work/$kind-initrd/early /linux-work/$kind-initrd/main
    cp -a "$root/usr/lib/modules/$abi"/modules.* "$destination/"
    for module in "$@"; do
        modprobe -d "$root" -S "$abi" -C /dev/null --ignore-install --show-depends "$module"
    done > /linux-work/dependencies
    while read -r action file remainder; do
        [ "$action" = insmod ] || continue
        relative=${file#"$root/lib/modules/$abi/"}
        (cd "$root/usr/lib/modules/$abi" && cp --parents "$relative" "$destination/")
    done < /linux-work/dependencies
done
live=/linux-work/live-initrd/early2/usr/lib/modules/$abi
installed=/linux-work/installed-initrd/early2/usr/lib/modules/$abi
dsp=$(modinfo -b "$root" -k "$abi" -F filename qcom_q6v5_pas)
dsp=${dsp#"$root/lib/modules/$abi/"}
dependency=$(modinfo -b "$root" -k "$abi" -F filename mdt_loader)
dependency=${dependency#"$root/lib/modules/$abi/"}
module_relative() {
    file=$(modinfo -b "$root" -k "$abi" -F filename "$1")
    printf '%s\n' "${file#"$root/lib/modules/$abi/"}"
}
oled=$(module_relative panel_samsung_atna33xc20)
lcd=$(module_relative panel_edp)
qrtr=$(module_relative qrtr)
transport=$(module_relative qrtr_smd)
mapper=$(module_relative qcom_pd_mapper)
test -f "$live/$dsp" && test -f "$live/$dependency"
cd /linux-work/live-initrd/early2
`
			args := append([]string{"bash", "-ceu", setup + testcase.mutation, "module-fixture", abi}, earlyModules...)
			if err := docker.RunInWorkspaceVolume(t.Context(), image, workspace, volume, args...); err != nil {
				t.Fatal(err)
			}
			err := ValidateEarlyModules(t.Context(), docker, image, workspace, volume, abi)
			if (err == nil) != testcase.valid {
				t.Fatalf("validation error=%v want valid=%v", err, testcase.valid)
			}
		})
	}
}

// decompressedModulesFixture models Debian's real mkinitramfs conversion of
// package zstd modules to plain ELF objects followed by a fresh dependency index.
const decompressedModulesFixture = `for kind in live installed; do
    tree=/linux-work/$kind-initrd/early2
    find "$tree/usr/lib/modules/$abi" -name '*.ko.zst' -exec zstd -dq --rm '{}' \;
    ln -s usr/lib "$tree/lib"
    rm "$tree/usr/lib/modules/$abi/modules.order"
    depmod -C /dev/null -b "$tree" "$abi"
done
`
