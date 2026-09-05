// Package sp11 shares firmware preparation for Surface Pro 11 live images.
package sp11

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/platform"
	userspaceinstall "github.com/ooaklee/lexr.sh/internal/userspace/install"
)

// WiFiBoard is the derived board data required before the WCN7850 first probe.
const WiFiBoard = "ath12k/WCN7850/hw2.0/board.bin"

// PrepareWiFiBoard uses the native userspace parser with the source image's
// distribution database. Both the deployable base and the effective live root
// must select identical bytes. No installed machine or downloaded helper is used.
func PrepareWiFiBoard(ctx context.Context, docker *platform.Docker, image, workspace, volume, root string) (string, error) {
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
	database, err := imagecontract.ReadBoundedExtractedFile(workspace, root+"-board-2.bin", 16<<20)
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
