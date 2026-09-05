package ubuntu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestValidateWiFiBoardData rejects missing, empty or shadowed boot firmware
// even when a different CPIO section contains the expected calibration bytes.
func TestValidateWiFiBoardData(t *testing.T) {
	for _, name := range []string{"valid", "missing", "empty", "shadowed", "symlink"} {
		t.Run(name, func(t *testing.T) {
			root, initrd := t.TempDir(), t.TempDir()
			board := []byte("synthetic calibration data")
			member := "usr/lib/firmware/" + liveWiFiBoard
			writeLiveFirmwareFixture(t, root, member, board)
			writeLiveFirmwareFixture(t, initrd, "early/"+member, board)
			switch name {
			case "missing":
				if err := os.Remove(filepath.Join(initrd, "early", member)); err != nil {
					t.Fatal(err)
				}
			case "empty":
				writeLiveFirmwareFixture(t, root, member, nil)
			case "shadowed":
				writeLiveFirmwareFixture(t, initrd, "main/"+member, []byte("wrong board"))
			case "symlink":
				filename := filepath.Join(initrd, "early", member)
				if err := os.Remove(filename); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, member), filename); err != nil {
					t.Fatal(err)
				}
			}
			if err := validateWiFiBoardData(initrd, root); (err == nil) != (name == "valid") {
				t.Fatalf("validation error=%v", err)
			}
		})
	}
}

// imageWiFiDatabase creates a synthetic, aligned API-2 record. No real firmware
// is embedded in tests or downloaded by the integration fixture.
func imageWiFiDatabase(payload []byte) []byte {
	tlv := func(kind uint32, data []byte) []byte {
		result := make([]byte, 8+(len(data)+3)&^3)
		binary.LittleEndian.PutUint32(result, kind)
		binary.LittleEndian.PutUint32(result[4:], uint32(len(data)))
		copy(result[8:], data)
		return result
	}
	name := "bus=pci,vendor=17cb,device=1107,subsystem-vendor=17cb,subsystem-device=3378,qmi-chip-id=2,qmi-board-id=255"
	header := make([]byte, 20)
	copy(header, "QCA-ATH12K-BOARD\x00")
	return append(header, tlv(0, append(tlv(0, []byte(name)), tlv(1, payload)...))...)
}

// TestPrepareWiFiBoardIntegration exercises compressed source firmware through
// Docker, the shared Go parser and actual image filesystem publication.
func TestPrepareWiFiBoardIntegration(t *testing.T) {
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 to exercise the Docker daemon")
	}
	ctx := context.Background()
	docker := platform.NewDocker(nil)
	image, err := docker.EnsureToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	volume, err := docker.CreateWorkVolume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = docker.RemoveWorkVolume(context.Background(), volume) })
	workspace := t.TempDir()
	payload := []byte("synthetic firmware prepared before probe")
	if err := os.WriteFile(filepath.Join(workspace, "database"), imageWiFiDatabase(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	const setup = `for root in rootfs initramfs-root; do
directory=/linux-work/$root/usr/lib/firmware/ath12k/WCN7850/hw2.0
mkdir -p "$directory"
zstd -q -c /work/database > "$directory/board-2.bin.zst"
done
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", setup); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"rootfs", "initramfs-root"} {
		digest, err := prepareWiFiBoard(ctx, docker, image, workspace, volume, root)
		if err != nil || digest != fmt.Sprintf("%x", sha256.Sum256(payload)) {
			t.Fatalf("%s digest=%s error=%v", root, digest, err)
		}
		actual, err := os.ReadFile(filepath.Join(workspace, root+"-board.bin"))
		if err != nil || !bytes.Equal(actual, payload) {
			t.Fatalf("derived bytes=%q error=%v", actual, err)
		}
	}
	const verify = `for root in rootfs initramfs-root; do
directory=/linux-work/$root/usr/lib/firmware/ath12k/WCN7850/hw2.0
cmp /work/$root-board.bin "$directory/board.bin"
zstd -qdc "$directory/board-2.bin.zst" | cmp /work/database -
test -s /linux-work/$root/usr/share/lexr/wifi-board.json
done
rm /linux-work/rootfs/usr/lib/firmware/ath12k/WCN7850/hw2.0/board.bin
ln -s /work/do-not-change /linux-work/rootfs/usr/lib/firmware/ath12k/WCN7850/hw2.0/board.bin
printf 'unchanged' > /work/do-not-change
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", verify); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareWiFiBoard(ctx, docker, image, workspace, volume, "rootfs"); err == nil {
		t.Fatal("symlink firmware destination was accepted")
	}
	if data, _ := os.ReadFile(filepath.Join(workspace, "do-not-change")); string(data) != "unchanged" {
		t.Fatal("symlink destination was modified")
	}
}

// TestWiFiUpperLayerRejectionIntegration checks actual SquashFS listings so
// firmware and symlink overrides cannot silently hide the prepared base data.
func TestWiFiUpperLayerRejectionIntegration(t *testing.T) {
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 to exercise the Docker daemon")
	}
	ctx := context.Background()
	docker := platform.NewDocker(nil)
	image, err := docker.EnsureToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	const empty = `mkdir -p /tmp/layer/usr/share
mksquashfs /tmp/layer /work/minimal.standard.squashfs -noappend -no-progress -quiet
cp /work/minimal.standard.squashfs /work/minimal.standard.live.squashfs
`
	if err := docker.RunInWorkspace(ctx, image, workspace, "bash", "-ceu", empty); err != nil {
		t.Fatal(err)
	}
	if err := rejectWiFiLayerOverrides(ctx, docker, image, workspace); err != nil {
		t.Fatalf("non-conflicting layers rejected: %v", err)
	}
	for _, kind := range []string{"file", "symlink"} {
		const conflict = `mkdir -p /tmp/layer/usr/lib/firmware/ath12k/WCN7850
if [ "$1" = symlink ]; then
    ln -s elsewhere /tmp/layer/usr/lib/firmware/ath12k/WCN7850/hw2.0
else
    mkdir /tmp/layer/usr/lib/firmware/ath12k/WCN7850/hw2.0
    printf 'wrong board' > /tmp/layer/usr/lib/firmware/ath12k/WCN7850/hw2.0/board.bin
fi
mksquashfs /tmp/layer /work/minimal.standard.live.squashfs -noappend -no-progress -quiet
`
		if err := docker.RunInWorkspace(ctx, image, workspace, "bash", "-ceu", conflict, "lexr-layer-fixture", kind); err != nil {
			t.Fatal(err)
		}
		if err := rejectWiFiLayerOverrides(ctx, docker, image, workspace); err == nil {
			t.Fatalf("%s firmware override accepted", kind)
		}
	}
}
