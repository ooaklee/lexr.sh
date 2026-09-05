package ubuntu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ooaklee/lexr.sh/internal/platform"
	userspaceinstall "github.com/ooaklee/lexr.sh/internal/userspace/install"
)

// liveWiFiBoard is prepared before the radio's first probe. Reloading the split
// ath12k driver after a missing-board failure caused an MHI/QMI kernel Oops.
const liveWiFiBoard = "ath12k/WCN7850/hw2.0/board.bin"

// prepareWiFiBoard uses the native userspace parser with the source image's
// distribution database. Both the deployable base and the effective live root
// must select identical bytes. No installed machine or downloaded helper is used.
func prepareWiFiBoard(ctx context.Context, docker *platform.Docker, image, workspace, volume, root string) (string, error) {
	if root != "rootfs" && root != "initramfs-root" {
		return "", fmt.Errorf("unsupported Wi-Fi preparation root %q", root)
	}
	// Decompress only in the isolated tools container. Fixed limits apply to
	// compressed input, expanded output and zstd's memory and execution time.
	const snapshot = `set -o pipefail
root=/linux-work/$1
firmware="$root/usr/lib/firmware/ath12k/WCN7850/hw2.0"
for component in usr usr/lib usr/lib/firmware usr/lib/firmware/ath12k usr/lib/firmware/ath12k/WCN7850 usr/lib/firmware/ath12k/WCN7850/hw2.0; do
    test -d "$root/$component"
    test ! -L "$root/$component"
done
source="$firmware/board-2.bin"
if [ ! -e "$source" ] && [ ! -L "$source" ]; then source="$source.zst"; fi
test -f "$source"
test ! -L "$source"
size=$(stat -c %s "$source")
test "$size" -gt 0
test "$size" -le 16777216
destination=/work/$1-board-2.bin
case "$source" in
    *.zst) timeout 15 zstd --decompress --stdout --quiet --memory=32MB "$source" | head -c 16777217 > "$destination" ;;
    *) head -c 16777217 "$source" > "$destination" ;;
esac
test "$(stat -c %s "$destination")" -le 16777216
chmod a+r "$destination"
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", snapshot, "lexr-wifi-snapshot", root); err != nil {
		return "", fmt.Errorf("snapshot source image Wi-Fi database: %w", err)
	}
	database, err := readBoundedExtractedFile(workspace, root+"-board-2.bin", 16<<20)
	if err != nil {
		return "", err
	}
	selector, board, err := userspaceinstall.SurfaceWiFiBoard(database)
	if err != nil {
		return "", fmt.Errorf("derive source image SP11 Wi-Fi board: %w", err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(board))
	if err := os.WriteFile(filepath.Join(workspace, root+"-board.bin"), board, 0o644); err != nil {
		return "", err
	}
	receipt, err := json.MarshalIndent(struct {
		Source       string `json:"source"`
		SourceSHA256 string `json:"source_sha256"`
		Selector     string `json:"selector"`
		BoardSHA256  string `json:"board_sha256"`
	}{"source ISO WCN7850 board-2.bin", fmt.Sprintf("%x", sha256.Sum256(database)), selector, digest}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(workspace, root+"-wifi-board.json"), receipt, 0o644); err != nil {
		return "", err
	}
	const publish = `root=/linux-work/$1
target="$root/usr/lib/firmware/ath12k/WCN7850/hw2.0/board.bin"
test ! -L "$target"
if [ -e "$target" ]; then test -f "$target"; fi
install -m 0644 /work/$1-board.bin "$target"
install -D -m 0644 /work/$1-wifi-board.json "$root/usr/share/lexr/wifi-board.json"
cmp /work/$1-board.bin "$target"
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", publish, "lexr-wifi-publish", root); err != nil {
		return "", fmt.Errorf("prepare image Wi-Fi board: %w", err)
	}
	return digest, nil
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
