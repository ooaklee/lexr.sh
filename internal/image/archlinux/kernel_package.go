package archlinux

import (
	"fmt"
	"regexp"
	"strings"
)

// pacmanPkgverCharacter matches characters that cannot be carried into the
// pkgver portion of a pacman version: hyphens are forbidden there because
// pacman splits full versions on the final hyphen (pkgver-pkgrel), so every
// disallowed character, hyphen included, collapses to an underscore.
var pacmanPkgverCharacter = regexp.MustCompile(`[^0-9A-Za-z._+]+`)

// kernelVersionPattern bounds Debian-style kernel versions before any shell
// rendering. Colons are accepted only because Debian epochs carry them; the
// pacman-safe mapping in PackageVersion removes them deterministically.
var kernelVersionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+~:_-]{0,127}$`)

// kernelPackagePkgrel identifies the initial native package revision. A reviewed
// installed-system upgrade and recovery lifecycle is separate from this builder.
const kernelPackagePkgrel = "1"

// kernelPackageName is the pacman identity registered in the Arch ARM root.
const kernelPackageName = "lexr-kernel-sp11"

// kernelPackageRetainedPath is the in-root location where the generated
// package archive is retained for ISO publication. The path is chroot-root
// relative: the file lives at ROOT/work/lexr-kernel-sp11.pkg.tar.gz.
const kernelPackageRetainedPath = "/work/lexr-kernel-sp11.pkg.tar.gz"

// KernelPackageIdentity binds the payload ABI and bundle version used to
// render the native pacman package metadata.
type KernelPackageIdentity struct {
	// ABI is the exact kernel ABI directory name shared by image and modules.
	ABI string
	// Version is the Debian package version shared by the bundle's packages.
	Version string
}

// Validate rejects unsafe kernel identities before rendering executable
// syntax. It mirrors LiveBoot.Validate: the ABI reuses the package ABI
// pattern, and the version is bounded printable Debian-style text.
func (identity KernelPackageIdentity) Validate() error {
	if !abiPattern.MatchString(identity.ABI) {
		return fmt.Errorf("Arch kernel package requires a safe kernel ABI")
	}
	if !kernelVersionPattern.MatchString(identity.Version) {
		return fmt.Errorf("Arch kernel package requires a safe kernel version")
	}
	return nil
}

// PackagePkgver returns the pacman pkgver portion for the bundle version.
// Per PKGBUILD(5) the pkgver may contain letters, digits, periods,
// underscores, and plus signs but never a hyphen; Debian epochs, tildes,
// colons, and hyphens therefore collapse to underscores and an emptied
// result falls back to 1.
func (identity KernelPackageIdentity) PackagePkgver() (string, error) {
	if err := identity.Validate(); err != nil {
		return "", err
	}
	value := strings.Trim(pacmanPkgverCharacter.ReplaceAllString(identity.Version, "_"), "._")
	if value == "" {
		return "1", nil
	}
	return value, nil
}

// PackageVersion returns the full pacman version pkgver-pkgrel as it must
// appear in .PKGINFO's pkgver field and in dependency comparisons. The
// pkgver portion never contains a hyphen (see PackagePkgver); the single
// separating hyphen is the pkgrel delimiter pacman splits on.
func (identity KernelPackageIdentity) PackageVersion() (string, error) {
	pkgver, err := identity.PackagePkgver()
	if err != nil {
		return "", err
	}
	return pkgver + "-" + kernelPackagePkgrel, nil
}

// kernelPackageStagedPaths lists the exact payload-relative paths the build
// script stages into the package. System.map and config are optional in the
// payload; every listed path is package-owned and confined to the ABI's boot,
// modules, and firmware directories. The script stages the same set.
func kernelPackageStagedPaths(abi string) ([]string, error) {
	if !abiPattern.MatchString(abi) {
		return nil, fmt.Errorf("Arch kernel package staging requires a safe kernel ABI")
	}
	return []string{
		"boot/vmlinuz-" + abi,
		"boot/System.map-" + abi,
		"boot/config-" + abi,
		"usr/lib/modules/" + abi,
		"usr/lib/firmware/" + abi,
	}, nil
}

// kernelPackageBuildScriptText is the constant packager text. It embeds no
// live ABI, version, root, or payload values; every caller-supplied value
// arrives through argv and is re-validated in shell before use.
const kernelPackageBuildScriptText = `# Lexr Arch native kernel packager. Invoked as:
#   bash -ceu SCRIPT lexr-arch-kernel ROOT PAYLOAD ABI VERSION
# All caller values arrive via argv; none are interpolated into this text.
set -euo pipefail

die() {
    printf 'lexr-arch-kernel: %s\n' "$*" >&2
    exit 1
}

[ "$#" -eq 4 ] || die "expected exactly ROOT PAYLOAD ABI VERSION"
root=$1
payload=$2
abi=$3
version=$4

abi_pattern='^[a-z0-9][a-z0-9.+-]{0,126}$'
version_pattern='^[0-9A-Za-z][0-9A-Za-z.+~:_-]{0,127}$'
case $root in
    /*) ;;
    *) die "ROOT must be an absolute path" ;;
esac
case $payload in
    /*) ;;
    *) die "PAYLOAD must be an absolute path" ;;
esac
[[ $abi =~ $abi_pattern ]] || die "rejecting unsafe kernel ABI"
[[ $version =~ $version_pattern ]] || die "rejecting unsafe kernel version"
[ -d "$root" ] || die "ROOT is not a directory: $root"
[ -d "$payload" ] || die "PAYLOAD is not a directory: $payload"

modules_dir="$payload/usr/lib/modules/$abi"
firmware_dir="$payload/usr/lib/firmware/$abi"
image_file="$payload/boot/vmlinuz-$abi"
[ -d "$modules_dir" ] && [ ! -L "$modules_dir" ] || die "missing or symlinked modules directory for the requested ABI"
[ -d "$firmware_dir" ] && [ ! -L "$firmware_dir" ] || die "missing or symlinked firmware directory for the requested ABI"
[ -f "$image_file" ] && [ ! -L "$image_file" ] || die "missing or symlinked kernel image for the requested ABI"

stage=$(mktemp -d) || die "cannot create a staging directory"
conf=""
cleanup() {
    rm -rf -- "$stage"
    if [ -n "$conf" ]; then rm -f -- "$conf"; fi
}
trap cleanup EXIT

pkgroot="$stage/pkgroot"
mkdir -p -- "$pkgroot/boot" "$pkgroot/usr/lib/modules" "$pkgroot/usr/lib/firmware"

# cp -R -p copies image bytes, modes, and timestamps verbatim and never
# dereferences symlinks, so module hashes stay exactly as they were
# digest-verified in the payload.
cp -R -p -- "$image_file" "$pkgroot/boot/"
for auxiliary in "System.map-$abi" "config-$abi"; do
    if [ -e "$payload/boot/$auxiliary" ] && [ ! -L "$payload/boot/$auxiliary" ]; then
        cp -R -p -- "$payload/boot/$auxiliary" "$pkgroot/boot/"
    fi
done
cp -R -p -- "$modules_dir" "$pkgroot/usr/lib/modules/"
cp -R -p -- "$firmware_dir" "$pkgroot/usr/lib/firmware/"

# PKGBUILD(5): the pkgver portion may carry [0-9A-Za-z._+] but never a
# hyphen, because pacman splits full versions on the final hyphen. Debian
# epochs, tildes, and hyphens collapse to underscores; the pkgrel is fixed
# at 1 and .PKGINFO carries the combined pkgver-pkgrel in its pkgver field.
pkgver=$(printf '%s\n' "$version" | sed -E 's/[^0-9A-Za-z._+]+/_/g; s/^[._]+//; s/[._]+$//')
[ -n "$pkgver" ] || pkgver=1
case $pkgver in
    *-*) die "internal error: pkgver portion must not contain a hyphen: $pkgver" ;;
esac
pkgrel=1
pkgfullver="$pkgver-$pkgrel"

{
    printf 'pkgname = %s\n' 'lexr-kernel-sp11'
    printf 'pkgver = %s\n' "$pkgfullver"
    printf 'pkgdesc = %s\n' 'Surface Pro 11 kernel payload repackaged as a native pacman package'
    printf 'url = %s\n' 'https://lexr.sh'
    printf 'arch = %s\n' 'aarch64'
    printf 'license = %s\n' 'GPL-2.0-only'
    printf 'provides = %s\n' 'linux-aarch64'
    printf 'conflict = %s\n' 'linux-aarch64'
    printf 'builddate = %s\n' "$(date -u +%s)"
    printf 'packager = %s\n' 'Lexr'
} >"$pkgroot/.PKGINFO"

{
    printf '%s\n' '#!/usr/bin/env bash' 'set -euo pipefail' ''
    # The ABI is charset-validated above, so this assignment is injection-free.
    printf 'lexr_abi=%s\n' "'$abi'"
    cat <<'LEXR_SCRIPTLET'

# Rebuild exactly the installed-system initramfs. The live-media initcpio
# configuration is never referenced here; the caller separately stages
# etc/lexr/mkinitcpio-installed.conf and etc/initcpio/install/lexr_sp11, and
# this package only depends on their presence on the target at install time.
lexr_refresh_boot() {
    /usr/bin/depmod -a "$lexr_abi"
    if [ -r /proc/self/mountinfo ]; then
        /usr/bin/mkinitcpio -c /etc/lexr/mkinitcpio-installed.conf \
            -g "/boot/initramfs-$lexr_abi.img" -k "$lexr_abi"
    else
        # /proc is unavailable while assembling an offline root, so the
        # initramfs rebuild is deferred to the explicit separate builder call.
        printf 'lexr-kernel-sp11: /proc not present; deferring initramfs rebuild for %s\n' "$lexr_abi" >&2
    fi
}

post_install() {
    lexr_refresh_boot
}

post_upgrade() {
    lexr_refresh_boot
}
LEXR_SCRIPTLET
} >"$pkgroot/.INSTALL"

bsdtar --format=mtree --options='!all,use-set,type,uid,gid,mode,time,size,sha256' \
    -cf "$stage/.MTREE" -C "$pkgroot" .PKGINFO .INSTALL boot usr
mv -- "$stage/.MTREE" "$pkgroot/.MTREE"

pkgfile="$stage/lexr-kernel-sp11-$pkgver-$pkgrel-aarch64.pkg.tar.gz"
bsdtar -czf "$pkgfile" --numeric-owner -C "$pkgroot" .PKGINFO .MTREE .INSTALL boot usr

# Retain the generated archive inside the disposable root for ISO
# publication and install from that in-root copy.
mkdir -p -- "$root/work" "$root/tmp"
cp -- "$pkgfile" "$root/work/lexr-kernel-sp11.pkg.tar.gz"

# Transaction-scoped pacman configuration. The persistent pacman.conf keeps
# LocalFileSigLevel = Required so upstream signed packages stay verified;
# this confined copy is used only for the single -U transaction below, relaxes
# signature checking only for the unsigned digest-verified local archive, and
# configures no repositories, so nothing else can flow through it.
conf="$root/tmp/lexr-pacman-local.conf"
cat >"$conf" <<'LEXR_CONF'
[options]
RootDir     = /
DBPath      = /var/lib/pacman/
CacheDir    = /var/cache/pacman/pkg/
Architecture = aarch64
SigLevel = Never
LocalFileSigLevel = Never
LEXR_CONF

# Every pacman invocation runs inside the root via chroot against the real
# ROOT/usr/bin/pacman binary; pacman is never executed on the build host.
run_pacman() {
    chroot "$root" /usr/bin/pacman --config /tmp/lexr-pacman-local.conf "$@"
}

# linux-aarch64 conflicts with this package. -Rdd skips dependency checks
# because dependents reference the kernel by name and this transaction
# reinstates the replacement immediately afterwards. The removal only ever
# touches the disposable root through the chrooted pacman.
if run_pacman -Qi linux-aarch64 >/dev/null 2>&1; then
    run_pacman -Rdd --noconfirm linux-aarch64
fi

# Same-ABI live package only: an installed lexr-kernel-sp11 carrying a
# different ABI is a refused upgrade path. Retaining an old ABI alongside a
# new one is out of scope; failing loudly protects the known-good install.
if run_pacman -Q lexr-kernel-sp11 >/dev/null 2>&1; then
    installed_abi=$(run_pacman -Ql lexr-kernel-sp11 2>/dev/null \
        | sed -n 's|^lexr-kernel-sp11 /usr/lib/modules/\([^/][^/]*\)/$|\1|p')
    if [ -n "$installed_abi" ] && [ "$installed_abi" != "$abi" ]; then
        die "installed lexr-kernel-sp11 uses ABI $installed_abi; refusing to replace it with $abi"
    fi
fi

# Vendor hooks shipped inside the root under /usr/share/libalpm/hooks may
# run during this transaction; an empty --hookdir would not disable them,
# so no such claim is made and nothing here relies on hook suppression.
# libalpm's stock mkinitcpio and depmod hooks match the stock linux package
# name, which this package is not, and provides/conflicts never trigger
# hooks. The .INSTALL scriptlet itself performs the required depmod and
# initramfs work, and scriptlets always run.
run_pacman --noconfirm -U /work/lexr-kernel-sp11.pkg.tar.gz

printf 'lexr-arch-kernel: registered %s %s (ABI %s) in %s\n' 'lexr-kernel-sp11' "$pkgfullver" "$abi" "$root"
`

// KernelPackageBuildScript returns the constant Bash packager for the
// digest-verified kernel payload. It must be invoked as
//
//	bash -ceu SCRIPT lexr-arch-kernel ROOT PAYLOAD ABI VERSION
//
// with the Arch root and payload supplied as absolute paths. The ROOT must
// already have /proc and /dev present for the chrooted pacman and must carry
// the real pacman binary at ROOT/usr/bin/pacman. Packaging choice: makepkg
// refuses to run as root, and a signed-source repack would re-execute
// upstream build tooling against a payload that is already digest-verified.
// Building the package archive directly with bsdtar around a valid .PKGINFO,
// .INSTALL, and .MTREE keeps the result deterministic, preserves the
// verified bytes and symlinks exactly, and still yields a valid native
// pacman package that genuine pacman -U registers in the real database. The
// script performs no bootloader, NVRAM, or partition work.
func KernelPackageBuildScript() string {
	return kernelPackageBuildScriptText
}
