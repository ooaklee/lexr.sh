package ubuntu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ooaklee/lexr.sh/internal/image/sp11"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// liveGPUFirmware retains the shared SP11 firmware set used by Ubuntu tests.
var liveGPUFirmware = sp11.LiveGPUFirmware

// liveFirmwareHook shares the initramfs-tools firmware hook with Pop!_OS.
func liveFirmwareHook() string { return sp11.LiveFirmwareHook() }

// installLiveFirmwareHook adds firmware only to the assembled Casper root;
// the deployable base and the package-owned installed boot policy are separate.
func installLiveFirmwareHook(ctx context.Context, docker *platform.Docker, image, workspace, volume string) error {
	const name = "lexr-sp11-live-firmware"
	directory := filepath.Join(workspace, "live-support")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("stage live firmware hook: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte(liveFirmwareHook()), 0o644); err != nil {
		return fmt.Errorf("stage live firmware hook: %w", err)
	}
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
		"install", "-D", "-m", "0755", "/work/live-support/"+name,
		"/linux-work/initramfs-root/etc/initramfs-tools/hooks/"+name); err != nil {
		return fmt.Errorf("install live firmware hook: %w", err)
	}
	return nil
}

// initramfsSections shares the concatenated CPIO overlay ordering.
func initramfsSections(root string) ([]string, error) { return sp11.InitramfsSections(root) }

// validateLiveGPUFirmware checks the shared SP11 firmware requirements.
func validateLiveGPUFirmware(root, abi string) error { return sp11.ValidateLiveGPUFirmware(root, abi) }
