package fedora

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/sp11"
	userspaceinstall "github.com/ooaklee/lexr.sh/internal/userspace/install"
)

// validateWiFiBoard rederives the board from the retained distribution database
// without changing the root being validated or trusting a preparation receipt.
func (v *Validator) validateWiFiBoard(ctx context.Context, image, workspace, volume string) imagecontract.ValidationCheck {
	check := imagecontract.ValidationCheck{Name: "source-derived-wifi-board"}
	database, err := sp11.ReadWiFiBoardDatabase(ctx, v.Docker, image, workspace, volume, "rootfs")
	if err != nil {
		check.Details = err.Error()
		return check
	}
	_, expected, err := userspaceinstall.SurfaceWiFiBoard(database)
	if err != nil {
		check.Details = err.Error()
		return check
	}
	const snapshot = `board=/linux-work/rootfs/usr/lib/firmware/ath12k/WCN7850/hw2.0/board.bin
test -f "$board"
test ! -L "$board"
test "$(stat -c %s "$board")" -gt 0
test "$(stat -c %s "$board")" -le 1048576
head -c 1048577 "$board" > /work/validated-wifi-board.bin
chmod a+r /work/validated-wifi-board.bin
`
	if err := v.Docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", snapshot); err != nil {
		check.Details = fmt.Sprintf("snapshot installed Wi-Fi board: %v", err)
		return check
	}
	actual, err := imagecontract.ReadBoundedExtractedFile(workspace, "validated-wifi-board.bin", 1<<20)
	if err != nil {
		check.Details = err.Error()
		return check
	}
	check.Passed = bytes.Equal(expected, actual)
	check.Details = fmt.Sprintf("installed board must match the source database payload SHA-256 %x", sha256.Sum256(expected))
	return check
}
