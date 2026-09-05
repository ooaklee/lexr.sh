package ubuntu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// liveGPUFirmware contains the distribution-provided firmware requested by
// the X1E Adreno 43050c01 GPU. These names are absent from msm's MODULE_FIRMWARE
// metadata and sit outside the directory copied by ubuntu-x1e-settings.
var liveGPUFirmware = []string{"qcom/gen70500_gmu.bin", "qcom/gen70500_sqe.fw"}

// maximumLiveFirmwareBytes bounds each extracted GPU firmware file.
const maximumLiveFirmwareBytes = 16 << 20

// liveFirmwareHook returns a live-only hook which uses Ubuntu's firmware
// search order, including ABI overrides and compressed distribution files.
// Private Denali firmware is deliberately outside this fixed set.
func liveFirmwareHook() string {
	return `#!/bin/sh
set -e
case "${1:-}" in
prereqs) exit 0 ;;
esac
. /usr/share/initramfs-tools/hook-functions
for firmware in ` + strings.Join(liveGPUFirmware, " ") + `; do
    if ! add_firmware "$firmware"; then
        echo "lexr: source image is missing required live GPU firmware: $firmware" >&2
        exit 1
    fi
done
if ! add_firmware ` + liveWiFiBoard + `; then
    echo 'lexr: prepared SP11 Wi-Fi board data is missing' >&2
    exit 1
fi
`
}

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

// initramfsSections returns unmkinitramfs sections in reverse overlay order,
// so validation observes main before earlyN and early when a path is repeated.
func initramfsSections(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	indices := make(map[int]string)
	for _, entry := range entries {
		name := entry.Name()
		index := 0
		switch {
		case name == "main":
			index = 1 << 20
		case name == "early":
			index = 1
		case strings.HasPrefix(name, "early"):
			index, err = strconv.Atoi(strings.TrimPrefix(name, "early"))
			if err != nil || index < 2 || index >= 1<<20 || name != "early"+strconv.Itoa(index) {
				return nil, fmt.Errorf("unrecognised initramfs section %q", name)
			}
		default:
			return nil, fmt.Errorf("unrecognised initramfs section %q", name)
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("initramfs section %q is not a regular directory", name)
		}
		indices[index] = name
	}
	order := make([]int, 0, len(indices))
	for index := range indices {
		order = append(order, index)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(order)))
	sections := make([]string, 0, len(order))
	for _, index := range order {
		sections = append(sections, indices[index])
	}
	return sections, nil
}

// validateLiveGPUFirmware checks actual, bounded firmware bytes across the
// concatenated CPIO archives, including mkinitramfs's uncompressed early archive.
func validateLiveGPUFirmware(root, abi string) error {
	sections, err := initramfsSections(root)
	if err != nil {
		return err
	}
	for _, firmware := range liveGPUFirmware {
		found := false
		for _, directory := range []string{"updates/" + abi, "updates", abi, ""} {
			for _, suffix := range []string{"", ".xz", ".zst"} {
				for _, section := range sections {
					relative := path.Join(section, "usr/lib/firmware", directory, firmware+suffix)
					data, readErr := readBoundedExtractedFile(root, relative, maximumLiveFirmwareBytes)
					if errors.Is(readErr, os.ErrNotExist) {
						continue
					}
					if readErr != nil {
						return fmt.Errorf("invalid live GPU firmware %s: %w", firmware, readErr)
					}
					if len(data) == 0 {
						return fmt.Errorf("live GPU firmware %s is empty", firmware)
					}
					found = true
					break
				}
			}
		}
		if !found {
			return fmt.Errorf("live initramfs is missing required GPU firmware %s", firmware)
		}
	}
	return nil
}
