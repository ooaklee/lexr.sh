package sp11

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
)

// LiveGPUFirmware contains the distribution-provided firmware requested by
// the X1E Adreno 43050c01 GPU. These names are absent from msm's MODULE_FIRMWARE
// metadata and sit outside the directory copied by ubuntu-x1e-settings.
var LiveGPUFirmware = []string{"qcom/gen70500_gmu.bin", "qcom/gen70500_sqe.fw"}

// maximumLiveFirmwareBytes bounds each extracted GPU firmware file.
const maximumLiveFirmwareBytes = 16 << 20

// LiveFirmwareHook returns a live-only hook which uses initramfs-tools' firmware
// search order, including ABI overrides and compressed distribution files.
// Private Denali firmware is deliberately outside this fixed set.
func LiveFirmwareHook() string {
	return `#!/bin/sh
set -e
case "${1:-}" in
prereqs) exit 0 ;;
esac
. /usr/share/initramfs-tools/hook-functions
for firmware in ` + strings.Join(LiveGPUFirmware, " ") + `; do
    if ! add_firmware "$firmware"; then
        echo "lexr: source image is missing required live GPU firmware: $firmware" >&2
        exit 1
    fi
done
if ! add_firmware ` + WiFiBoard + `; then
    echo 'lexr: prepared SP11 Wi-Fi board data is missing' >&2
    exit 1
fi
`
}

// InitramfsSections returns unmkinitramfs sections in reverse overlay order,
// so validation observes main before earlyN and early when a path is repeated.
func InitramfsSections(root string) ([]string, error) {
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

// ValidateLiveGPUFirmware checks actual, bounded firmware bytes across the
// concatenated CPIO archives, including mkinitramfs's uncompressed early archive.
func ValidateLiveGPUFirmware(root, abi string) error {
	sections, err := InitramfsSections(root)
	if err != nil {
		return err
	}
	for _, firmware := range LiveGPUFirmware {
		found := false
		for _, directory := range []string{"updates/" + abi, "updates", abi, ""} {
			for _, suffix := range []string{"", ".xz", ".zst"} {
				for _, section := range sections {
					relative := path.Join(section, "usr/lib/firmware", directory, firmware+suffix)
					data, readErr := imagecontract.ReadBoundedExtractedFile(root, relative, maximumLiveFirmwareBytes)
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
