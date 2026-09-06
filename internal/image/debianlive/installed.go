package debianlive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// extractGRUBDependencies retrieves the inspected source-pool packages from
// either the source ISO or the finished ISO, then checks complete archive bytes.
func extractGRUBDependencies(ctx context.Context, docker *platform.Docker, image, workspace, isoName string) error {
	if isoName != "source.iso" && isoName != "image.iso" {
		return fmt.Errorf("unsupported Debian dependency input")
	}
	if err := os.MkdirAll(filepath.Join(workspace, "debian-grub-dependencies"), 0755); err != nil {
		return err
	}
	args := []string{"xorriso", "-osirrox", "on", "-indev", "/work/" + isoName}
	for _, dependency := range grubDependencies {
		args = append(args, "-extract", dependency.ISOPath, "/work/debian-grub-dependencies/"+dependency.File)
	}
	if err := docker.RunInWorkspaceAsHostUser(ctx, image, workspace, args...); err != nil {
		return err
	}
	for _, dependency := range grubDependencies {
		path := "debian-grub-dependencies/" + dependency.File
		data, err := imagecontract.ReadBoundedExtractedFile(workspace, path, 16<<20)
		if err != nil {
			return err
		}
		if digestHex(data) != dependency.SHA256 {
			return fmt.Errorf("Debian GRUB dependency digest changed: %s", dependency.File)
		}
	}
	return nil
}

// prepareInstalledPackages installs the full pinned GRUB dependency closure
// offline before the shared custom-kernel transaction needs grub2-common.
func prepareInstalledPackages(ctx context.Context, docker *platform.Docker, image, workspace, volume string) error {
	if err := extractGRUBDependencies(ctx, docker, image, workspace, "source.iso"); err != nil {
		return err
	}
	if err := buildSupportPackage(ctx, docker, image, workspace, volume); err != nil {
		return err
	}
	const script = `root=/linux-work/rootfs
# This image-build root has no mounted target EFI filesystem or EFI variables.
test ! -e "$root/sys/firmware/efi/efivars"
test ! -e "$root/boot/grub/arm64-efi/core.efi"
test "$(sha256sum "$root/etc/grub.d/10_linux" | cut -d' ' -f1)" = "$1"
shift
# The source's statoverride names live-session accounts not yet created by
# systemd-sysusers. Preserve the exact bytes around this offline transaction,
# just as the shared Debian kernel package transaction does.
backup=$(mktemp -d /linux-work/grub-dpkg-state.XXXXXX)
restore_state() {
 status=$?
 trap - EXIT HUP INT TERM
 if [ -e "$backup/statoverride" ]; then
  mv -f "$backup/statoverride" "$root/var/lib/dpkg/statoverride"
 else
  rm -f "$root/var/lib/dpkg/statoverride"
 fi
 rmdir "$backup"
 exit "$status"
}
trap restore_state EXIT HUP INT TERM
if [ -e "$root/var/lib/dpkg/statoverride" ]; then
 mv "$root/var/lib/dpkg/statoverride" "$backup/statoverride"
fi
: > "$root/var/lib/dpkg/statoverride"
DEBIAN_FRONTEND=noninteractive dpkg --root="$root" --install "$@"
`
	args := []string{"bash", "-ceu", script, "lexr-debian-offline-grub", inspectedLinuxGeneratorSHA256}
	for _, dependency := range grubDependencies {
		args = append(args, "/work/debian-grub-dependencies/"+dependency.File)
	}
	args = append(args, "/work/"+grubSupportDirectory+"/"+grubSupportDebName)
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, args...); err != nil {
		return fmt.Errorf("install offline Debian GRUB support: %w", err)
	}
	return nil
}

// requiredInstalledPackages names the explicitly retained installed boot chain.
func requiredInstalledPackages(abi string) []string {
	packages := []string{"grub-common", "initramfs-tools", "initramfs-tools-core", "python3", supportPackageName, "lexr-kernel-boot-support", "linux-image-" + abi, "linux-modules-" + abi}
	for _, dependency := range grubDependencies {
		packages = append(packages, dependency.Package)
	}
	return packages
}

// installInstalledSupport stages one package-declared exact-ABI DTB and prevents
// Calamares autoremove from stripping the installed boot chain.
func installInstalledSupport(ctx context.Context, docker *platform.Docker, image, workspace, volume string, bundle kernel.Bundle) error {
	if !kernelABIPattern.MatchString(bundle.ABI) || bundle.EffectiveDTBDelivery != kernel.DTBDeliveryExternalRequired {
		return fmt.Errorf("Debian installed support requires a safe exact ABI and external DTBs")
	}
	profile := ""
	for _, tree := range bundle.DeviceTrees {
		if tree.Required {
			if profile != "" {
				return fmt.Errorf("ambiguous Debian installed profile")
			}
			profile = tree.Device
		}
	}
	if profile == "" {
		return fmt.Errorf("missing Debian installed profile")
	}
	if err := os.WriteFile(filepath.Join(workspace, "installed-grub-defaults"), []byte(installedGrubDefaults), 0644); err != nil {
		return err
	}
	const script = `root=/linux-work/rootfs
abi=$1
profile=$2
shift 2
install -D -m 0644 /work/installed-grub-defaults "$root/etc/default/grub.d/99-surface-pro-11.cfg"
chroot "$root" /usr/libexec/lexr/kernel-boot-refresh refresh --root / --abi "$abi" --image "/boot/vmlinuz-$abi" --platform "$profile" --defer-grub
chroot "$root" apt-mark manual "$@"
`
	args := []string{"bash", "-ceu", script, "lexr-debian-installed", bundle.ABI, profile}
	args = append(args, requiredInstalledPackages(bundle.ABI)...)
	return docker.RunInWorkspaceVolume(ctx, image, workspace, volume, args...)
}

// validateInstalledGRUB reads package ownership, archive payloads, diversion
// records and APT state with trusted tools; it never executes image programs.
func validateInstalledGRUB(ctx context.Context, docker *platform.Docker, image, workspace, volume, abi string) error {
	if !kernelABIPattern.MatchString(abi) {
		return fmt.Errorf("invalid Debian installed ABI")
	}
	// The complete .deb must equal a deterministic rebuild from this binary's
	// embedded source and terms, including control scripts and extra-file checks.
	expectedWorkspace, err := os.MkdirTemp(workspace, "expected-debian-support-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(expectedWorkspace)
	if err := buildSupportPackage(ctx, docker, image, expectedWorkspace, volume); err != nil {
		return err
	}
	packagePath := grubSupportDirectory + "/" + grubSupportDebName
	expected, err := recordFile(filepath.Join(expectedWorkspace, packagePath), packagePath)
	if err != nil {
		return err
	}
	if err := verifySupportArchive(workspace, packagePath, expected.SHA256, expected.Size); err != nil {
		return err
	}
	if err := extractGRUBDependencies(ctx, docker, image, workspace, "image.iso"); err != nil {
		return err
	}
	const script = `set -o pipefail
root=/linux-work/rootfs
archive=$1
package=$2
version=$3
architecture=$4
kind=$5
test "$(dpkg-deb -f "$archive" Package)" = "$package"
test "$(dpkg-deb -f "$archive" Version)" = "$version"
test "$(dpkg-deb -f "$archive" Architecture)" = "$architecture"
test "$(dpkg-query --admindir="$root/var/lib/dpkg" -W -f='${Status}|${Version}|${Architecture}' "$package")" = "install ok installed|$version|$architecture"
unpacked="/linux-work/inspect-debian-$package"
dpkg-deb -x "$archive" "$unpacked"
# Dependency binaries and modules must match their pinned source packages.
# The support package's complete payload is compared, including project terms.
while IFS= read -r -d '' file; do
 relative=${file#"$unpacked/"}
 if [ "$kind" = dependency ]; then
  case "$relative" in usr/sbin/*|usr/bin/*|usr/lib/grub/*) ;; *) continue ;; esac
 fi
 target="$root/$relative"
 test -f "$target"
 test ! -L "$target"
 case "$(realpath "$target")" in "$root/"*) ;; *) exit 1 ;; esac
 cmp "$file" "$target"
done < <(find "$unpacked" -type f -print0)
`
	for _, dependency := range grubDependencies {
		if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", script, "lexr-debian-package-validation", "/work/debian-grub-dependencies/"+dependency.File, dependency.Package, dependency.Version, dependency.Arch, "dependency"); err != nil {
			return err
		}
	}
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", script, "lexr-debian-package-validation", "/work/"+grubSupportDirectory+"/"+grubSupportDebName, supportPackageName, grubSupportVersion, "all", "support"); err != nil {
		return err
	}
	// Rebuild expected data from compiled source, not the ISO's own .deb claims.
	payload, err := supportPackagePayload()
	if err != nil {
		return err
	}
	payload["var/lib/dpkg/info/"+supportPackageName+".preinst"] = []byte(grubPreinst)
	payload["var/lib/dpkg/info/"+supportPackageName+".postrm"] = []byte(grubPostrm)
	const hashFile = `file="/linux-work/rootfs/$1"
test -f "$file"
test ! -L "$file"
test "$(realpath "$file")" = "$file"
sha256sum "$file"
`
	for path, contents := range payload {
		output, err := docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", hashFile, "lexr-debian-owned-file", path)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(output), digestHex(contents)+"  ") {
			return fmt.Errorf("Debian installed support changed at %s", path)
		}
	}
	output, err := docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", hashFile, "lexr-debian-native-grub", strings.TrimPrefix(divertedGeneratorPath, "/"))
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(output), inspectedLinuxGeneratorSHA256+"  ") {
		return fmt.Errorf("native Debian GRUB generator changed")
	}
	const state = `import pathlib, sys
root = pathlib.Path('/linux-work/rootfs')
required = set(sys.argv[1:])
for relative in ['var/lib/dpkg/diversions', 'var/lib/dpkg/status']:
 p = root / relative
 if not p.is_file() or p.is_symlink() or p.resolve() != p or p.stat().st_size > 16777216:
  raise SystemExit('invalid Debian package-state file')
lines = (root/'var/lib/dpkg/diversions').read_text().splitlines()
if len(lines) % 3: raise SystemExit('malformed diversions')
records = list(zip(lines[::3],lines[1::3],lines[2::3]))
expected = ('/etc/grub.d/10_linux','/usr/share/lexr/debian-grub/10_linux','lexr-debian-grub-support')
if [r for r in records if r[0] == expected[0] or r[1] == expected[1] or r[2] == expected[2]] != [expected]:
 raise SystemExit('missing or conflicting Debian GRUB diversion')
p = root/'var/lib/apt/extended_states'
if p.exists() or p.is_symlink():
 if not p.is_file() or p.is_symlink() or p.resolve() != p or p.stat().st_size > 16777216:
  raise SystemExit('invalid APT package state')
 for stanza in p.read_text().split('\n\n'):
  fields = dict(line.split(':',1) for line in stanza.splitlines() if ':' in line)
  if fields.get('Package','').strip() in required and fields.get('Auto-Installed','0').strip() != '0':
   raise SystemExit('required installed package is marked automatic')
for relative in ['etc/grub.d/10_linux', 'usr/sbin/update-grub']:
 p = root/relative
 if not p.is_file() or p.is_symlink() or p.stat().st_mode & 0o022 or not p.stat().st_mode & 0o111:
  raise SystemExit('invalid installed GRUB executable permissions')
`
	args := []string{"python3", "-c", state}
	args = append(args, requiredInstalledPackages(abi)...)
	return docker.RunInWorkspaceVolume(ctx, image, workspace, volume, args...)
}
