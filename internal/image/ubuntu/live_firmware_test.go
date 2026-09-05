package ubuntu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// writeLiveFirmwareFixture creates one extracted initramfs member.
func writeLiveFirmwareFixture(t *testing.T, root, relative string, data []byte) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLiveGPUFirmwareValidation covers real split-CPIO layouts and rejects the
// missing-firmware layout observed in the v23 Ubuntu Concept live initramfs.
func TestLiveGPUFirmwareValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		gmu  string
		sqe  string
	}{
		{name: "compressed early", gmu: "early/usr/lib/firmware/qcom/gen70500_gmu.bin.zst", sqe: "early/usr/lib/firmware/qcom/gen70500_sqe.fw.zst"},
		{name: "raw main", gmu: "main/usr/lib/firmware/qcom/gen70500_gmu.bin", sqe: "main/usr/lib/firmware/qcom/gen70500_sqe.fw"},
		{name: "split and updates", gmu: "early2/usr/lib/firmware/updates/qcom/gen70500_gmu.bin.xz", sqe: "main/usr/lib/firmware/updates/" + installedTestABI + "/qcom/gen70500_sqe.fw.zst"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeLiveFirmwareFixture(t, root, test.gmu, []byte("GMU firmware"))
			writeLiveFirmwareFixture(t, root, test.sqe, []byte("SQE firmware"))
			if err := validateLiveGPUFirmware(root, installedTestABI); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(root, test.gmu)); err != nil {
				t.Fatal(err)
			}
			if err := validateLiveGPUFirmware(root, installedTestABI); err == nil || !strings.Contains(err.Error(), "gen70500_gmu.bin") {
				t.Fatalf("missing GMU error = %v", err)
			}
		})
	}
}

// TestLiveGPUFirmwareRejectsStockOnlyAndEmptyOverride proves unrelated older
// GPU firmware and earlier CPIO members cannot make an incomplete image pass.
func TestLiveGPUFirmwareRejectsStockOnlyAndEmptyOverride(t *testing.T) {
	root := t.TempDir()
	writeLiveFirmwareFixture(t, root, "early/usr/lib/firmware/qcom/a660_gmu.bin.zst", []byte("older firmware"))
	writeLiveFirmwareFixture(t, root, "early/usr/lib/firmware/qcom/a660_sqe.fw.zst", []byte("older firmware"))
	if err := validateLiveGPUFirmware(root, installedTestABI); err == nil {
		t.Fatal("stock-only initramfs passed GPU firmware validation")
	}
	for _, firmware := range liveGPUFirmware {
		writeLiveFirmwareFixture(t, root, "early/usr/lib/firmware/"+firmware, []byte("firmware"))
	}
	writeLiveFirmwareFixture(t, root, "main/usr/lib/firmware/"+liveGPUFirmware[0], nil)
	if err := validateLiveGPUFirmware(root, installedTestABI); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty main override error = %v", err)
	}
}

// TestLiveGPUFirmwareRejectsSymlink proves firmware validation cannot follow
// an initramfs member outside its private extraction directory.
func TestLiveGPUFirmwareRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	relative := "main/usr/lib/firmware/" + liveGPUFirmware[0]
	writeLiveFirmwareFixture(t, root, relative, []byte("firmware"))
	filename := filepath.Join(root, relative)
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), filename); err != nil {
		t.Fatal(err)
	}
	if err := validateLiveGPUFirmware(root, installedTestABI); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink firmware error = %v", err)
	}
}

// TestLiveFirmwareHookIntegration exercises Ubuntu's actual add_firmware
// helper with compressed files, its prereqs protocol, and a missing source.
func TestLiveFirmwareHookIntegration(t *testing.T) {
	if os.Getenv("LEXR_DOCKER_INTEGRATION") != "1" {
		t.Skip("set LEXR_DOCKER_INTEGRATION=1 to exercise the Docker daemon")
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hook"), []byte(liveFirmwareHook()), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	docker := platform.NewDocker(nil)
	image, err := docker.EnsureToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const script = `
/work/hook prereqs
mkdir -p /lib/firmware/qcom
printf 'GMU test firmware' | zstd -q -o /lib/firmware/qcom/gen70500_gmu.bin.zst
printf 'SQE test firmware' | xz > /lib/firmware/qcom/gen70500_sqe.fw.xz
export version=fixture-abi verbose=n DESTDIR=/work/result/main
mkdir -p "$DESTDIR/usr/lib"
ln -s usr/lib "$DESTDIR/lib"
/work/hook
cmp /lib/firmware/qcom/gen70500_gmu.bin.zst "$DESTDIR/usr/lib/firmware/qcom/gen70500_gmu.bin.zst"
cmp /lib/firmware/qcom/gen70500_sqe.fw.xz "$DESTDIR/usr/lib/firmware/qcom/gen70500_sqe.fw.xz"
rm /lib/firmware/qcom/gen70500_gmu.bin.zst
export DESTDIR=/work/missing
mkdir -p "$DESTDIR/usr/lib"
ln -s usr/lib "$DESTDIR/lib"
if /work/hook; then
    echo 'missing firmware unexpectedly succeeded' >&2
    exit 1
fi
`
	if err := docker.RunInWorkspace(ctx, image, workspace, "bash", "-ceu", script); err != nil {
		t.Fatal(err)
	}
	if err := validateLiveGPUFirmware(filepath.Join(workspace, "result"), "fixture-abi"); err != nil {
		t.Fatal(err)
	}
}
