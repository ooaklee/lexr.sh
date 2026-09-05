package popos

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/image/debian"
	"github.com/ooaklee/lexr.sh/internal/image/sp11"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
	userspaceinstall "github.com/ooaklee/lexr.sh/internal/userspace/install"
)

// TestVerifiedPopSourceIntegration exercises the parser and existing native
// SP11 Wi-Fi preparation against locally extracted, checksum-verified media.
// LEXR_POP_SOURCE_AUDIT selects a private audit containing iso/ and
// rootfs-evidence/; no firmware or source ISO is committed as a fixture.
func TestVerifiedPopSourceIntegration(t *testing.T) {
	root := os.Getenv("LEXR_POP_SOURCE_AUDIT")
	if root == "" {
		t.Skip("set LEXR_POP_SOURCE_AUDIT to an extracted, checksum-verified Pop source")
	}
	read := func(relative string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	layout, err := parseSourceLayout(read("iso/boot/grub/grub.cfg"), read("iso/.disk/info"))
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{layout.kernel, layout.initrd, layout.member("filesystem.squashfs"), layout.member("filesystem.manifest-remove")} {
		if info, err := os.Stat(filepath.Join(root, "iso", member)); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatalf("missing source member %s: %v", member, err)
		}
	}
	link, err := os.Readlink(filepath.Join(root, "iso/casper"))
	if err != nil || link != layout.liveDirectory {
		t.Fatalf("Casper alias %q does not match %s: %v", link, layout.liveDirectory, err)
	}
	database := read("rootfs-evidence/usr/lib/firmware/ath12k/WCN7850/hw2.0/board-2.bin")
	selector, board, err := userspaceinstall.SurfaceWiFiBoard(database)
	if err != nil || len(board) == 0 {
		t.Fatalf("source Wi-Fi board selection failed: %v", err)
	}
	t.Logf("live-directory=%s board-selector=%s board-size=%d board-sha256=%s", layout.liveDirectory, selector, len(board), fmt.Sprintf("%x", sha256.Sum256(board)))
}

// TestPopOfflineKernelIntegration exercises the shared package and Wi-Fi
// transactions in a disposable Linux volume containing the actual Pop root.
// Both corpus variables must be explicit; normal test runs need no Docker,
// downloads, private firmware or large generated files.
func TestPopOfflineKernelIntegration(t *testing.T) {
	audit, packages := os.Getenv("LEXR_POP_SOURCE_AUDIT"), os.Getenv("LEXR_TEST_KERNEL_BUNDLE")
	if audit == "" || packages == "" {
		t.Skip("set LEXR_POP_SOURCE_AUDIT and LEXR_TEST_KERNEL_BUNDLE for the offline Pop transaction")
	}
	bundle, err := kernel.DiscoverLocalBundle(packages)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "kernel"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, pkg := range bundle.Packages {
		if !pkg.Verified {
			t.Fatal("integration kernel inputs require verified checksums")
		}
		if err := os.Link(pkg.Path, filepath.Join(workspace, "kernel", pkg.Name)); err != nil {
			t.Fatal(err)
		}
	}
	grub, err := os.ReadFile(filepath.Join(audit, "iso/boot/grub/grub.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.ReadFile(filepath.Join(audit, "iso/.disk/info"))
	if err != nil {
		t.Fatal(err)
	}
	layout, err := parseSourceLayout(grub, info)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(audit, "iso", layout.member("filesystem.squashfs")), filepath.Join(workspace, "source.squashfs")); err != nil {
		t.Fatal(err)
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
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := docker.RemoveWorkVolume(cleanup, volume); err != nil {
			t.Errorf("remove integration volume %s: %v", volume, err)
		}
	})
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
		"unsquashfs", "-no-progress", "-xattrs-exclude", "^trusted\\.", "-d", "/linux-work/rootfs", "/work/source.squashfs"); err != nil {
		t.Fatal(err)
	}
	if err := debian.InstallKernelPackages(ctx, docker, image, workspace, volume, bundle); err != nil {
		t.Fatal(err)
	}
	digest, err := sp11.PrepareWiFiBoard(ctx, docker, image, workspace, volume, "rootfs")
	if err != nil {
		t.Fatal(err)
	}
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
		"depmod", "-a", "-b", "/linux-work/rootfs", bundle.ABI); err != nil {
		t.Fatal(err)
	}
	if err := stageInstalledSupport(workspace); err != nil {
		t.Fatal(err)
	}
	if err := installInstalledSupport(ctx, docker, image, workspace, volume, bundle); err != nil {
		t.Fatal(err)
	}
	t.Logf("registered ABI=%s and prepared Wi-Fi SHA256=%s in the actual Pop root", bundle.ABI, digest)
	if err := buildInitramfs(ctx, docker, image, workspace, volume, bundle.ABI); err != nil {
		t.Fatal(err)
	}
	for _, initramfs := range []struct {
		path string
		live bool
	}{
		{"/work/casper-initrd", true},
		{"/work/installed-initrd", false},
	} {
		listing, err := docker.CaptureInWorkspace(ctx, image, workspace, "lsinitramfs", initramfs.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range []string{"scripts/casper\n", "conf/uuid.conf\n"} {
			if strings.Contains(string(listing), marker) != initramfs.live {
				t.Fatalf("%s has incorrect Casper marker %s", initramfs.path, marker)
			}
		}
		if !strings.Contains(string(listing), "usr/lib/modules/"+bundle.ABI+"/") {
			t.Fatalf("%s lacks the selected kernel modules", initramfs.path)
		}
	}
}

// TestPopSourcePreparationIntegration exercises ISO extraction and the actual
// EFI editing commands against an explicitly supplied source. It retains no
// generated ISO and removes only its own disposable Linux volume.
func TestPopSourcePreparationIntegration(t *testing.T) {
	source := os.Getenv("LEXR_POP_SOURCE_ISO")
	if source == "" {
		t.Skip("set LEXR_POP_SOURCE_ISO to inspect actual source preparation")
	}
	workspace := t.TempDir()
	if err := os.Link(source, filepath.Join(workspace, "source.iso")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := NewRemasterer(nil, os.Stdout)
	image, err := r.Docker.EnsureToolsImage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	volume, err := r.Docker.CreateWorkVolume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := r.Docker.RemoveWorkVolume(cleanup, volume); err != nil {
			t.Error(err)
		}
	})
	layout, err := r.extractSource(ctx, image, workspace, volume)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.prepareEFI(ctx, image, workspace); err != nil {
		t.Fatal(err)
	}
	t.Logf("actual source directory=%s; installer root and direct ARM64 EFI preparation passed", layout.liveDirectory)
}
