package ubuntu

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// liveGettingStarted is the maintained command guide copied to each live user's
// desktop through Casper's skeleton directory, and retained after installation.
//
//go:embed LEXR_GETTING_STARTED.txt
var liveGettingStarted string

// installGettingStarted stages the companion guide in the deployable root before
// Casper assembles the live session. Images without a companion omit this guide.
func installGettingStarted(ctx context.Context, docker *platform.Docker, image, workspace, volume string, companionIncluded bool) error {
	if !companionIncluded {
		return nil
	}
	if err := os.WriteFile(filepath.Join(workspace, "LEXR_GETTING_STARTED.txt"), []byte(liveGettingStarted), 0o644); err != nil {
		return err
	}
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
		"install", "-D", "-m", "0644", "/work/LEXR_GETTING_STARTED.txt", "/linux-work/rootfs/etc/skel/Desktop/LEXR_GETTING_STARTED.txt"); err != nil {
		return fmt.Errorf("install live desktop getting-started guide: %w", err)
	}
	return nil
}
