package debianlive

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	lexr "github.com/ooaklee/lexr.sh"
	"github.com/ooaklee/lexr.sh/internal/platform"
	"io/fs"
	"os"
	"path/filepath"
)

// inspectedLinuxGeneratorSHA256 pins the source's native generator at intake
// and output validation; compatible future native updates retain the wrapper.
const inspectedLinuxGeneratorSHA256 = "86f79b1e03cdfe2d38f0628603deaa28ab0b5c457be78e91b44e5b8b6cb56be7"

// grubSupportVersion identifies the Debian-only installed GRUB integration.
const grubSupportVersion = "0.1~lexr1"

// supportPackageName identifies the package owning the generator diversion.
const supportPackageName = "lexr-debian-grub-support"

// divertedGeneratorPath retains the untouched, package-owned native generator.
const divertedGeneratorPath = "/usr/share/lexr/debian-grub/10_linux"

// grubSupportDirectory is the complete retained support package directory.
const grubSupportDirectory = "sp11/debian-grub"

// grubSupportDebName names the deterministic all-architecture support archive.
const grubSupportDebName = supportPackageName + "_" + grubSupportVersion + "_all.deb"

// pinnedPackage records inspected source-pool dependency bytes and metadata.
type pinnedPackage struct {
	ISOPath, File, Package, Version, Arch, SHA256 string
}

// grubDependencies supplies all six offline packages used by Calamares's
// grub-efi install, including the unsigned EFI binary dependency.
var grubDependencies = []pinnedPackage{
	{
		ISOPath: "/pool/main/g/grub2/grub2-common_2.12-5_arm64.deb",
		File:    "grub2-common_2.12-5_arm64.deb", Package: "grub2-common",
		Version: "2.12-5", Arch: "arm64",
		SHA256: "7e2da95b620d06d1b25fb25a42f0d8f6cdde4352f9b84264762e017f78d6ea20",
	},
	{
		ISOPath: "/pool/main/g/grub2/grub-efi-arm64-bin_2.12-5_arm64.deb",
		File:    "grub-efi-arm64-bin_2.12-5_arm64.deb", Package: "grub-efi-arm64-bin",
		Version: "2.12-5", Arch: "arm64",
		SHA256: "1791c029b65c3926cbfca426b09dcc0bd0a09574c74c88d75ae3a5afc372f572",
	},
	{
		ISOPath: "/pool/main/g/grub2/grub-efi-arm64_2.12-5_arm64.deb",
		File:    "grub-efi-arm64_2.12-5_arm64.deb", Package: "grub-efi-arm64",
		Version: "2.12-5", Arch: "arm64",
		SHA256: "2f1ddc189cbc5db8d235f30e24222a0710e0ded3e9f1342345091e8affbdb851",
	},
	{
		ISOPath: "/pool/main/e/efibootmgr/efibootmgr_18-2_arm64.deb",
		File:    "efibootmgr_18-2_arm64.deb", Package: "efibootmgr",
		Version: "18-2", Arch: "arm64",
		SHA256: "1a3e8436a68cae4eb075f441c9cad212b05303e5f7002fdbd0b3920523f3a516",
	},
	{
		ISOPath: "/pool/main/g/grub2/grub-efi-arm64-unsigned_2.12-5_arm64.deb",
		File:    "grub-efi-arm64-unsigned_2.12-5_arm64.deb", Package: "grub-efi-arm64-unsigned", Version: "2.12-5", Arch: "arm64",
		SHA256: "74ca1f8e432b6bd2acc97e67f9b9e296c157072732e788681fb70951ef3924dc",
	},
	{
		ISOPath: "/pool/main/g/grub2/grub-efi_2.12-5_arm64.deb",
		File:    "grub-efi_2.12-5_arm64.deb", Package: "grub-efi", Version: "2.12-5", Arch: "arm64",
		SHA256: "cc4d5a45f99619961d559a652322084761f58ccd8fc971dfb3257627fb40593d",
	},
}

// installedGrubDefaults adds SP11 arguments without replacing native GRUB policy.
const installedGrubDefaults = `# Surface Pro 11 arguments for installed Debian kernels.
GRUB_CMDLINE_LINUX_DEFAULT="${GRUB_CMDLINE_LINUX_DEFAULT} clk_ignore_unused pd_ignore_unused arm64.nopauth systemd.tpm2_wait=0"
GRUB_TIMEOUT_STYLE=menu
GRUB_TIMEOUT=15
`

// grubWrapperScript is owned by this package at the diverted generator path.
const grubWrapperScript = `#!/bin/sh
exec /usr/bin/python3 /usr/share/lexr/debian-grub/grub_generator.py "$@"
`

// grubGenerator is Lexr's source-bearing runtime generator integration.
//
//go:embed grub_generator.py
var grubGenerator []byte

// grubPreinst registers the diversion before dpkg unpacks our exempt wrapper.
// No file from Debian's native generator is shipped by this support package.
const grubPreinst = `#!/bin/sh
set -e
case "$1" in
 install|upgrade)
  owner=$(dpkg-divert --listpackage /etc/grub.d/10_linux)
  case "$owner" in
   lexr-debian-grub-support) ;;
   "")
    test -f /etc/grub.d/10_linux
    test ! -L /etc/grub.d/10_linux
    install -d -m 0755 /usr/share/lexr/debian-grub
    dpkg-divert --package lexr-debian-grub-support --add --rename --divert /usr/share/lexr/debian-grub/10_linux /etc/grub.d/10_linux
    ;;
   *) echo "Another package owns the Debian GRUB diversion: $owner" >&2; exit 1 ;;
  esac
  ;;
esac
`

// grubPostrm restores the native generator after normal removal or a failed
// first install. An upgrade or aborted upgrade keeps the existing diversion.
const grubPostrm = `#!/bin/sh
set -e
case "$1" in
 remove|abort-install)
  owner=$(dpkg-divert --listpackage /etc/grub.d/10_linux)
  if [ "$owner" = lexr-debian-grub-support ]; then
   test ! -e /etc/grub.d/10_linux
   test ! -L /etc/grub.d/10_linux
   dpkg-divert --package lexr-debian-grub-support --remove --rename --divert /usr/share/lexr/debian-grub/10_linux /etc/grub.d/10_linux
  fi
  ;;
esac
`

// supportControl defines package metadata without selecting a kernel version.
const supportControl = `Package: lexr-debian-grub-support
Version: ` + grubSupportVersion + `
Architecture: all
Maintainer: Leon Silcott <leon@boasi.io>
Section: admin
Priority: optional
Depends: grub-common, grub2-common, python3
Description: Surface device-tree integration for Debian GRUB
 Preserve native GRUB entry generation with exact-kernel device trees.
`

// supportPackagePayload returns the complete package-owned payload, including
// the repository's authoritative project terms and notice.
func supportPackagePayload() (map[string][]byte, error) {
	licence, err := fs.ReadFile(lexr.ProjectTermsFS(), "LICENSE")
	if err != nil {
		return nil, err
	}
	notice, err := fs.ReadFile(lexr.ProjectTermsFS(), "NOTICE")
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		"etc/grub.d/10_linux":                              []byte(grubWrapperScript),
		"usr/share/lexr/debian-grub/grub_generator.py":     grubGenerator,
		"usr/share/doc/lexr-debian-grub-support/copyright": licence,
		"usr/share/doc/lexr-debian-grub-support/NOTICE":    notice,
	}, nil
}

// buildSupportPackage builds deterministically in the isolated Linux volume.
// Only the finished archive is retained, not private package-build intermediates.
func buildSupportPackage(ctx context.Context, docker *platform.Docker, image, workspace, volume string) error {
	payload, err := supportPackagePayload()
	if err != nil {
		return err
	}
	payload["DEBIAN/control"] = []byte(supportControl)
	payload["DEBIAN/preinst"] = []byte(grubPreinst)
	payload["DEBIAN/postrm"] = []byte(grubPostrm)
	stage := filepath.Join(workspace, "debian-grub-package")
	for path, contents := range payload {
		destination := filepath.Join(stage, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(destination, contents, 0644); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(workspace, grubSupportDirectory), 0755); err != nil {
		return err
	}
	const script = `set -o pipefail
pkg=$(mktemp -d /linux-work/debian-grub-package.XXXXXX)
trap 'rm -rf -- "$pkg"' EXIT
cp -a /work/debian-grub-package/. "$pkg/"
chmod 0755 "$pkg/etc/grub.d/10_linux" "$pkg/DEBIAN/preinst" "$pkg/DEBIAN/postrm"
find "$pkg" -print0 | xargs -0 touch --date=@0
SOURCE_DATE_EPOCH=0 dpkg-deb --root-owner-group --uniform-compression -Zxz --build "$pkg" "/work/sp11/debian-grub/$1"
chmod 0644 "/work/sp11/debian-grub/$1"
`
	return docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", script, "lexr-debian-support", grubSupportDebName)
}

// verifySupportArchive compares the entire archive to the manifest's identity;
// its expanded files and maintainer scripts are checked separately in the root.
func verifySupportArchive(workspace string, recordPath string, digest string, size int64) error {
	record, err := recordFile(filepath.Join(workspace, filepath.FromSlash(recordPath)), recordPath)
	if err != nil {
		return err
	}
	if record.SHA256 != digest || record.Size != size {
		return fmt.Errorf("Debian support archive differs from its recorded identity")
	}
	return nil
}

// digestHex formats SHA-256 identities for source and installed payload checks.
func digestHex(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
