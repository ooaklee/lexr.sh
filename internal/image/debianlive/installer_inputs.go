package debianlive

import (
	"context"
	"fmt"
	"github.com/ooaklee/lexr.sh/internal/platform"
	"strings"
)

// inspectedForwardingSHA256 binds the alternate source ARM64 bootstrap before
// every entry point is redirected to the canonical SP11 live menu.
const inspectedForwardingSHA256 = "28e0691d2fe8dfc5bcee30ede680541b67d18109f144b578d1c93ab29a2f9507"

// installerInputs pins the inspected Calamares sequence, filesystem source,
// package-removal policy and GRUB installation contract from the accepted ISO.
var installerInputs = []struct{ path, digest string }{
	{"etc/calamares/settings.conf", "62221cf3b9ddbed42886d5508cab81f7f0035f1bcd1e0468d842bb0fca6b7ab0"},
	{"etc/calamares/modules/bootloader.conf", "f90d1a734d6e3ea4bc84680192b2ee3b3afd02164eb4cfd2e67d3a506ab68d75"},
	{"etc/calamares/modules/unpackfs.conf", "8313d4cd7b0e3c887458216af613bfb44add32c0537e4db009dc99eaba0a227a"},
	{"etc/calamares/modules/packages.conf", "b3be308cfac38e1c45b9348cdfeab17d909d8e00a8aa92a7b6d397b61a7cbcf0"},
	{"usr/share/calamares/helpers/calamares-bootloader-config", "44aed1319a84f6569b1c231054a2e067ccdc0ce0882c66e373ece23654e29009"},
}

// validateInstallerInputs reads regular source files with trusted container-side
// tools. It never runs Calamares or any command supplied by the image.
func validateInstallerInputs(ctx context.Context, docker *platform.Docker, image, workspace, volume string) error {
	const script = `root=/linux-work/rootfs
relative=$1
file="$root/$relative"
test -f "$file"
test ! -L "$file"
test "$(realpath "$file")" = "$file"
sha256sum "$file"
`
	for _, input := range installerInputs {
		output, err := docker.CaptureInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", script, "lexr-debian-installer-audit", input.path)
		if err != nil {
			return fmt.Errorf("inspect Debian installer input %s: %w", input.path, err)
		}
		if !strings.HasPrefix(string(output), input.digest+"  ") {
			return fmt.Errorf("Debian installer input changed: %s", input.path)
		}
	}
	return nil
}
