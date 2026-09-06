package debianlive

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// liveGettingStarted supplies Debian's maintained live and installed-system guide.
//
//go:embed LEXR_GETTING_STARTED.txt
var liveGettingStarted string

// installGettingStarted makes the companion commands available through the
// skeleton copied into the live user's home and retained after installation.
func installGettingStarted(ctx context.Context, docker *platform.Docker, image, workspace, volume string, included bool) error {
	if !included {
		return nil
	}
	if err := os.WriteFile(filepath.Join(workspace, "LEXR_GETTING_STARTED.txt"), []byte(liveGettingStarted), 0o644); err != nil {
		return err
	}
	return docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
		"install", "-D", "-m", "0644", "/work/LEXR_GETTING_STARTED.txt", "/linux-work/rootfs/etc/skel/Desktop/LEXR_GETTING_STARTED.txt")
}
