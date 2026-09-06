package debianlive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestDebianGRUBSupportIntegration installs the real pinned packages into a
// disposable copy of the inspected Debian root, tests normal/recovery DTBs and
// package diversion lifecycle, and checks deterministic complete archive bytes.
// Hardware probes are simulated only for GRUB entry generation; this is not an
// installation or physical-boot test.
func TestDebianGRUBSupportIntegration(t *testing.T) {
	source := os.Getenv("LEXR_DEBIAN_TEST_SOURCE_ISO")
	reference := os.Getenv("LEXR_DEBIAN_TEST_AUDIT_VOLUME")
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" || source == "" || reference == "" {
		t.Skip("set Debian source ISO and read-only audit-volume integration inputs")
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]+$`).MatchString(reference) {
		t.Fatal("unsafe audit volume")
	}
	build := filepath.Join("..", "..", "..", "build")
	if err := os.MkdirAll(build, 0755); err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(build, ".lexr-debian-support-test-")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(workspace); err != nil {
			t.Error(err)
		}
	})
	if err := os.Link(source, filepath.Join(workspace, "source.iso")); err != nil {
		t.Fatal(err)
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
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := docker.RemoveWorkVolume(ctx, volume); err != nil {
			t.Error(err)
		}
	})
	clone := exec.CommandContext(t.Context(), "docker", "run", "--rm", "--network", "none", "--mount", "type=volume,source="+reference+",target=/reference,readonly", "--mount", "type=volume,source="+volume+",target=/linux-work", image, "cp", "-a", "/reference/rootfs", "/linux-work/rootfs")
	if output, err := clone.CombinedOutput(); err != nil {
		t.Fatalf("copy isolated Debian fixture: %v\n%s", err, output)
	}
	if err := prepareInstalledPackages(t.Context(), docker, image, workspace, volume); err != nil {
		t.Fatal(err)
	}
	original, err := recordFile(filepath.Join(workspace, grubSupportDirectory, grubSupportDebName), grubSupportDebName)
	if err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(workspace, "second")
	if err := os.Mkdir(second, 0755); err != nil {
		t.Fatal(err)
	}
	if err := buildSupportPackage(t.Context(), docker, image, second, volume); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := recordFile(filepath.Join(second, grubSupportDirectory, grubSupportDebName), grubSupportDebName)
	if err != nil {
		t.Fatal(err)
	}
	if original != rebuilt {
		t.Fatalf("support package was not deterministic: %v versus %v", original, rebuilt)
	}
	if err := docker.RunInWorkspaceVolume(t.Context(), image, workspace, volume, "bash", "-ceu", installedGRUBTestScript, "lexr-debian-grub-tests", grubSupportDebName, inspectedLinuxGeneratorSHA256); err != nil {
		t.Fatal(err)
	}
}

// installedGRUBTestScript exercises native Debian generation with data-only
// device-probe stubs and real dpkg install/reinstall/remove/purge operations.
const installedGRUBTestScript = `set -o pipefail
root=/linux-work/rootfs
archive=/work/sp11/debian-grub/$1
native_digest=$2
# Offline roots have not run systemd-sysusers; preserve the same source file
# around the remaining isolated dpkg lifecycle probes.
mv "$root/var/lib/dpkg/statoverride" /linux-work/test-statoverride
trap 'mv -f /linux-work/test-statoverride "$root/var/lib/dpkg/statoverride"' EXIT
: > "$root/var/lib/dpkg/statoverride"
abi=7.2.0-jg-0sp11v24-qcom-x1e
cp "$root/usr/share/lexr/debian-grub/10_linux" /linux-work/native-original
mkdir -p "$root/tmp/lexr-probe" "$root/dev/disk/by-uuid" "$root/var/lib/lexr/kernel-boot/$abi"
printf root > "$root/dev/disk/by-uuid/ROOT-UUID"
printf kernel > "$root/boot/vmlinuz-$abi"
printf initrd > "$root/boot/initrd.img-$abi"
printf dtb > "$root/boot/dtb-$abi"
cat > "$root/tmp/lexr-probe/probe" <<'PROBE'
#!/bin/sh
case "$*" in
 *fs_uuid*) echo BOOT-UUID ;;
 *partmap*) echo gpt ;;
 *abstraction*|*hints_string*|*cryptodisk_uuid*) ;;
 *device*) echo /dev/test-boot ;;
 *drive*) echo '(hd0,gpt2)' ;;
 *fs*) echo ext2 ;;
 *) exit 1 ;;
esac
PROBE
cat > "$root/tmp/lexr-probe/relpath" <<'RELPATH'
#!/bin/sh
if [ "$LEXR_TEST_SEPARATE_BOOT" = 1 ]; then echo ''; else echo "$1"; fi
RELPATH
chmod 0755 "$root/tmp/lexr-probe/probe" "$root/tmp/lexr-probe/relpath"
for split in 0 1; do
 chroot "$root" env pkgdatadir=/usr/share/grub grub_probe=/tmp/lexr-probe/probe grub_mkrelpath=/tmp/lexr-probe/relpath LEXR_TEST_SEPARATE_BOOT="$split" GRUB_DEVICE=/dev/test-root GRUB_DEVICE_BOOT=/dev/test-boot GRUB_DEVICE_UUID=ROOT-UUID GRUB_FS=ext2 GRUB_DISTRIBUTOR=Debian GRUB_DISABLE_RECOVERY=false /etc/grub.d/10_linux > "/work/grub-$split.cfg"
 python3 - "$split" "$abi" "/work/grub-$split.cfg" <<'CHECK'
import pathlib,sys
split,abi,path=sys.argv[1:]
prefix='' if split=='1' else '/boot'
text=pathlib.Path(path).read_text()
entries=[e for e in text.split('menuentry ')[1:] if '\n\t' in e and 'vmlinuz-' in e]
custom=[e for e in entries if '/vmlinuz-'+abi in e]
if len(custom)!=3: raise SystemExit('missing custom default, advanced or recovery entry')
for entry in custom:
 for expected in ['linux\t'+prefix+'/vmlinuz-'+abi+' root=UUID=ROOT-UUID', 'devicetree '+prefix+'/dtb-'+abi, 'initrd\t'+prefix+'/initrd.img-'+abi]:
  if expected not in entry: raise SystemExit('incorrect native entry: '+expected+'\n'+entry)
stock=[e for e in entries if '/vmlinuz-6.10.6-arm64' in e]
if not stock: raise SystemExit('stock kernel entries were lost')
if any('devicetree ' in e for e in stock): raise SystemExit('stock kernel was given an absent DTB')
CHECK
done
# An absent registered DTB must fail instead of publishing an incomplete entry.
mv "$root/boot/dtb-$abi" "$root/boot/dtb-$abi.saved"
if chroot "$root" env pkgdatadir=/usr/share/grub grub_probe=/tmp/lexr-probe/probe grub_mkrelpath=/tmp/lexr-probe/relpath LEXR_TEST_SEPARATE_BOOT=0 GRUB_DEVICE=/dev/test-root GRUB_DEVICE_BOOT=/dev/test-boot GRUB_DEVICE_UUID=ROOT-UUID GRUB_FS=ext2 /etc/grub.d/10_linux >/work/grub-missing.cfg; then exit 1; fi
mv "$root/boot/dtb-$abi.saved" "$root/boot/dtb-$abi"
# Reinstall/upgrade keeps the original native bytes and the package exemption.
DEBIAN_FRONTEND=noninteractive dpkg --root="$root" --install "$archive"
cmp /linux-work/native-original "$root/usr/share/lexr/debian-grub/10_linux"
test "$(chroot "$root" dpkg-divert --listpackage /etc/grub.d/10_linux)" = lexr-debian-grub-support
dpkg --root="$root" --remove lexr-debian-grub-support
cmp /linux-work/native-original "$root/etc/grub.d/10_linux"
test -z "$(chroot "$root" dpkg-divert --listpackage /etc/grub.d/10_linux)"
dpkg --root="$root" --purge lexr-debian-grub-support
cmp /linux-work/native-original "$root/etc/grub.d/10_linux"
# Abort a first install after registering the diversion. dpkg must call the
# new package's abort-install cleanup and restore the untouched native file.
broken=/linux-work/grub-support-failed-install
dpkg-deb -R "$archive" "$broken"
printf '\nexit 42\n' >> "$broken/DEBIAN/preinst"
dpkg-deb --build "$broken" /linux-work/grub-support-failed-install.deb
if dpkg --root="$root" --install /linux-work/grub-support-failed-install.deb; then exit 1; fi
cmp /linux-work/native-original "$root/etc/grub.d/10_linux"
test -z "$(chroot "$root" dpkg-divert --listpackage /etc/grub.d/10_linux)"
DEBIAN_FRONTEND=noninteractive dpkg --root="$root" --install "$archive"
# Abort an upgrade after its preinst. The previous package's wrapper and
# diversion must stay installed and usable.
sed -i 's/^Version:.*/Version: 0.1~lexr2/' "$broken/DEBIAN/control"
dpkg-deb --build "$broken" /linux-work/grub-support-failed-upgrade.deb
cp "$root/etc/grub.d/10_linux" /linux-work/wrapper-original
if dpkg --root="$root" --install /linux-work/grub-support-failed-upgrade.deb; then exit 1; fi
cmp /linux-work/wrapper-original "$root/etc/grub.d/10_linux"
cmp /linux-work/native-original "$root/usr/share/lexr/debian-grub/10_linux"
test "$(chroot "$root" dpkg-divert --listpackage /etc/grub.d/10_linux)" = lexr-debian-grub-support
test "$(dpkg-query --admindir="$root/var/lib/dpkg" -W -f='${Status}|${Version}' lexr-debian-grub-support)" = 'install ok installed|0.1~lexr1'
# Simulate a native package update with compatible new comment bytes. dpkg must
# route it to the diverted destination and retain our active wrapper.
fake=/linux-work/grub-common-update
mkdir -p "$fake/DEBIAN" "$fake/etc/grub.d"
printf 'Package: grub-common\nVersion: 2.12-5+lexrtest\nArchitecture: arm64\nMaintainer: Test <test@example.invalid>\nDescription: disposable package-upgrade fixture\n' > "$fake/DEBIAN/control"
printf '/etc/grub.d/10_linux\n' > "$fake/DEBIAN/conffiles"
cat /linux-work/native-original > "$fake/etc/grub.d/10_linux"
printf '\n# compatible package-update fixture\n' >> "$fake/etc/grub.d/10_linux"
chmod 0755 "$fake/etc/grub.d/10_linux"
dpkg-deb --build "$fake" /linux-work/grub-common-update.deb
dpkg --root="$root" --force-confnew --install /linux-work/grub-common-update.deb
cmp "$fake/etc/grub.d/10_linux" "$root/usr/share/lexr/debian-grub/10_linux"
grep -q /usr/bin/python3 "$root/etc/grub.d/10_linux"
# The runtime adapter accepts this compatible generator update.
chroot "$root" env pkgdatadir=/usr/share/grub grub_probe=/tmp/lexr-probe/probe grub_mkrelpath=/tmp/lexr-probe/relpath LEXR_TEST_SEPARATE_BOOT=0 GRUB_DEVICE=/dev/test-root GRUB_DEVICE_BOOT=/dev/test-boot GRUB_DEVICE_UUID=ROOT-UUID GRUB_FS=ext2 /usr/bin/python3 -c 'import runpy; m=runpy.run_path("/usr/share/lexr/debian-grub/grub_generator.py"); m["transform_generator"](open("/usr/share/lexr/debian-grub/10_linux").read())'
printf 'PASS: Debian offline dependencies, native entry DTBs, deterministic package and diversion lifecycle\n'
`
