package fedora

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestKernelRPMInstallIntegration optionally converts an exact verified local
// runtime bundle and installs it through the production Fedora RPM boundary.
// The supplied root volume is never created or removed here and requires an
// explicit acknowledgement that this destructive integration test may mutate it.
func TestKernelRPMInstallIntegration(t *testing.T) {
	kernelDirectory := strings.TrimSpace(os.Getenv("LEXR_TEST_KERNEL_DIR"))
	volume := strings.TrimSpace(os.Getenv("LEXR_TEST_FEDORA_ROOT_VOLUME"))
	mutationConsent := os.Getenv("LEXR_TEST_MUTATE_FEDORA_ROOT_VOLUME")
	if kernelDirectory == "" && volume == "" && mutationConsent == "" {
		t.Skip("set LEXR_TEST_KERNEL_DIR and LEXR_TEST_FEDORA_ROOT_VOLUME, then confirm mutation with LEXR_TEST_MUTATE_FEDORA_ROOT_VOLUME=1")
	}
	if kernelDirectory == "" || volume == "" {
		t.Fatal("LEXR_TEST_KERNEL_DIR and LEXR_TEST_FEDORA_ROOT_VOLUME are both required")
	}
	if mutationConsent != "1" {
		t.Fatal("set LEXR_TEST_MUTATE_FEDORA_ROOT_VOLUME=1 to confirm the supplied disposable root volume may be modified")
	}
	if !filepath.IsAbs(kernelDirectory) {
		t.Fatal("LEXR_TEST_KERNEL_DIR must be an absolute path")
	}

	bundle, err := kernel.DiscoverLocalBundle(kernelDirectory)
	if err != nil {
		t.Fatalf("discover exact local runtime kernel bundle: %v", err)
	}
	if err := validateBundlePaths(bundle); err != nil {
		t.Fatalf("validate Fedora kernel bundle boundary: %v", err)
	}
	modulesPackage, ok := bundle.Package(kernel.RoleModules)
	if !ok {
		t.Fatal("verified local runtime bundle has no modules package")
	}

	buildRoot := filepath.Clean(filepath.Join("..", "..", "..", "build"))
	if err := os.MkdirAll(buildRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(buildRoot, ".fedora-kernel-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	if err := stageBundle(bundle, workspace); err != nil {
		t.Fatalf("stage verified runtime kernel bundle: %v", err)
	}
	if err := stageFedoraSupport(workspace, bundle); err != nil {
		t.Fatalf("stage compiled Fedora kernel support: %v", err)
	}

	docker := platform.NewDocker(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := docker.Check(ctx); err != nil {
		t.Fatal(err)
	}
	image, err := docker.EnsureFedoraToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := installKernelRPM(ctx, docker, image, workspace, volume, bundle); err != nil {
		t.Fatal(err)
	}

	output, err := docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", `root=/linux-work/rootfs
abi=$1
rpm_version=$2
modules_archive=$3
rpm_path=/work/lexr-kernel-sp11.aarch64.rpm
expected=/linux-work/kernel-rpm-integration-expected
deb_expected=/linux-work/kernel-rpm-integration-deb

rpm -K --nosignature "$rpm_path"
[ "$(rpm -qp --qf '%{NAME}:%{VERSION}:%{ARCH}' "$rpm_path")" = "lexr-kernel-sp11:$rpm_version:aarch64" ]
[ "$(rpm --root "$root" -q --qf '%{NAME}:%{VERSION}:%{ARCH}' lexr-kernel-sp11)" = "lexr-kernel-sp11:$rpm_version:aarch64" ]
rpm --root "$root" -q --qf '%{NAME}\n' --whatprovides kernel-uname-r | grep -Fx lexr-kernel-sp11

rm -rf -- "$expected" "$deb_expected"
mkdir -p "$expected" "$deb_expected"
rpm2cpio "$rpm_path" > /linux-work/kernel-rpm-integration.cpio
(cd "$expected" && cpio -idm --quiet < /linux-work/kernel-rpm-integration.cpio)
dpkg-deb --extract "$modules_archive" "$deb_expected"

rpm -qpl "$rpm_path" | LC_ALL=C sort > /linux-work/kernel-rpm-integration-package-files.txt
rpm --root "$root" -ql lexr-kernel-sp11 | LC_ALL=C sort > /linux-work/kernel-rpm-integration-root-files.txt
diff -u /linux-work/kernel-rpm-integration-package-files.txt /linux-work/kernel-rpm-integration-root-files.txt

# One RPM-owned set comparison above proves ownership without spawning one RPM
# process per module. Hash the complete immutable regular-file subset in two
# bounded batches; depmod's package-owned generated modules.* indexes are the
# only deliberately mutable files.
(cd "$expected" && find . -type f ! -path "./usr/lib/modules/$abi/modules.*" -print0 | LC_ALL=C sort -z) \
  > /linux-work/kernel-rpm-integration-immutable-paths.bin
(cd "$expected" && xargs -0 sha256sum < /linux-work/kernel-rpm-integration-immutable-paths.bin) \
  > /linux-work/kernel-rpm-integration-expected.sha256
(cd "$root" && xargs -0 sha256sum < /linux-work/kernel-rpm-integration-immutable-paths.bin) \
  > /linux-work/kernel-rpm-integration-root.sha256
cmp /linux-work/kernel-rpm-integration-expected.sha256 /linux-work/kernel-rpm-integration-root.sha256

(cd "$expected" && find . -type l -printf '%p\t%l\n' | LC_ALL=C sort) \
  > /linux-work/kernel-rpm-integration-expected-links.txt
while IFS=$'\t' read -r relative target; do
  test -L "$root/$relative"
  test "$(readlink "$root/$relative")" = "$target"
done < /linux-work/kernel-rpm-integration-expected-links.txt

cmp "$deb_expected/boot/vmlinuz-$abi" "$expected/boot/vmlinuz-$abi"
cmp "$deb_expected/boot/vmlinuz-$abi" "$root/boot/vmlinuz-$abi"
cmp "$root/boot/vmlinuz-$abi" "$root/usr/lib/modules/$abi/vmlinuz-dtbloader.efi"
[ "$(find "$root/boot" -maxdepth 1 -type f -name 'vmlinuz-*' -printf '%f\n')" = "vmlinuz-$abi" ]
test -s "$root/usr/lib/modules/$abi/modules.dep"
grep -Fx 'layout=other' "$root/etc/kernel/install.conf"
grep -Fx "$abi" "$root/usr/lib/lexr/sp11/kernel-abi"
printf 'package=lexr-kernel-sp11:%s:aarch64 abi=%s\n' "$rpm_version" "$abi"`,
		"validate-fedora-kernel-rpm-install", bundle.ABI, rpmVersion(bundle.Version),
		"/work/kernel/"+modulesPackage.Name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "package=lexr-kernel-sp11:") || !strings.Contains(string(output), "abi="+bundle.ABI) {
		t.Fatalf("kernel RPM integration output lacks exact package identity:\n%s", output)
	}
}
