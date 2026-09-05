package ubuntu

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ooaklee/lexr.sh/internal/image/sp11"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// liveWiFiBoard is prepared before the radio's first probe. Reloading the split
// ath12k driver after a missing-board failure caused an MHI/QMI kernel Oops.
const liveWiFiBoard = sp11.WiFiBoard

// prepareWiFiBoard applies shared SP11 firmware preparation to one Ubuntu
// root; the adapter separately verifies that upper layers cannot replace it.
func prepareWiFiBoard(ctx context.Context, docker *platform.Docker, image, workspace, volume, root string) (string, error) {
	return sp11.PrepareWiFiBoard(ctx, docker, image, workspace, volume, root)
}

// rejectWiFiLayerOverrides prevents an unchanged upper SquashFS from hiding
// the prepared base firmware at boot or after a desktop installation. Even
// whiteouts of an ancestor are rejected; the adapter must support such a source
// explicitly rather than silently building a different live firmware stack.
func rejectWiFiLayerOverrides(ctx context.Context, docker *platform.Docker, image, workspace string) error {
	const script = `set -o pipefail
for layer in minimal.standard.squashfs minimal.standard.live.squashfs; do
    unsquashfs -lln "/work/$layer" | awk '
    $1 ~ /^[-cl]/ {
        start=index($0, "squashfs-root/")
        if (!start) next
        p=substr($0, start + length("squashfs-root/"))
        sub(/ -> .*/, "", p)
        if (p == "usr" || p == "usr/lib" || p == "usr/lib/firmware" || p == "usr/lib/firmware/ath12k" || p == "usr/lib/firmware/ath12k/WCN7850" || p == "usr/lib/firmware/ath12k/WCN7850/hw2.0" || p ~ /^usr\/lib\/firmware\/ath12k\/WCN7850\/hw2\.0\// || p ~ /^lib\/firmware\/ath12k\/WCN7850/) bad=1
    }
    END { if (bad) { print "Upper Casper layer overrides WCN7850 firmware; source layout requires explicit support" > "/dev/stderr"; exit 1 } }'
done
`
	return docker.RunInWorkspace(ctx, image, workspace, "bash", "-ceu", script, "lexr-wifi-layers")
}

// validateWiFiBoardData proves the effective initramfs and deployable base
// contain identical bounded calibration bytes, including early CPIO sections.
func validateWiFiBoardData(initrd, root string) error {
	board, err := readBoundedExtractedFile(root, "usr/lib/firmware/"+liveWiFiBoard, 256<<10)
	if err != nil {
		return fmt.Errorf("read deployable Wi-Fi board: %w", err)
	}
	if len(board) == 0 {
		return errors.New("deployable Wi-Fi board is empty")
	}
	sections, err := initramfsSections(initrd)
	if err != nil {
		return err
	}
	for _, section := range sections {
		live, err := readBoundedExtractedFile(initrd, filepath.ToSlash(filepath.Join(section, "usr/lib/firmware", liveWiFiBoard)), 256<<10)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(live, board) {
			return errors.New("live initramfs and deployable Wi-Fi board data differ")
		}
		return nil
	}
	return errors.New("live initramfs lacks prepared SP11 Wi-Fi board data")
}
