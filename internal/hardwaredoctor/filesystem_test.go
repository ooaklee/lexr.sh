package hardwaredoctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestOSFileSystemEnforcesFileAndDirectoryBounds verifies host reads fail closed.
func TestOSFileSystemEnforcesFileAndDirectoryBounds(t *testing.T) {
	root := t.TempDir()
	procDirectory := filepath.Join(root, "proc")
	if err := os.MkdirAll(procDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDirectory, "value"), []byte("bounded-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDirectory, "other"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	filesystem, err := NewOSFileSystem(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.ReadFile(context.Background(), "/proc/value", 4); !errors.Is(err, ErrReadLimit) {
		t.Fatalf("ReadFile() error = %v, want read limit", err)
	}
	content, err := filesystem.ReadFile(context.Background(), "/proc/value", 32)
	if err != nil || string(content) != "bounded-content" {
		t.Fatalf("ReadFile() = %q, %v", content, err)
	}
	if _, err := filesystem.ReadDir(context.Background(), "/proc", 1); !errors.Is(err, ErrReadLimit) {
		t.Fatalf("ReadDir() error = %v, want read limit", err)
	}
	entries, err := filesystem.ReadDir(context.Background(), "/proc", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "other" || entries[1].Name != "value" {
		t.Fatalf("ReadDir() = %#v", entries)
	}
}

// TestOSFileSystemRejectsUnsafePaths verifies logical traversal never escapes root.
func TestOSFileSystemRejectsUnsafePaths(t *testing.T) {
	filesystem, err := NewOSFileSystem(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, unsafePath := range []string{"relative", "/proc/../secret", "/proc/../../secret", "/proc/\x00secret"} {
		if _, err := filesystem.ReadFile(context.Background(), unsafePath, 32); err == nil {
			t.Errorf("ReadFile(%q) unexpectedly succeeded", unsafePath)
		}
	}
}

// TestOSFileSystemReadsLinksAndFollowedMetadata verifies the two explicit link operations.
func TestOSFileSystemReadsLinksAndFollowedMetadata(t *testing.T) {
	root := t.TempDir()
	targetDirectory := filepath.Join(root, "sys", "target")
	linkDirectory := filepath.Join(root, "sys", "class")
	if err := os.MkdirAll(targetDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(linkDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "target"), filepath.Join(linkDirectory, "device")); err != nil {
		t.Fatal(err)
	}
	filesystem, err := NewOSFileSystem(root)
	if err != nil {
		t.Fatal(err)
	}
	target, err := filesystem.ReadLink(context.Background(), "/sys/class/device")
	if err != nil || target != filepath.Join("..", "target") {
		t.Fatalf("ReadLink() = %q, %v", target, err)
	}
	info, err := filesystem.Stat(context.Background(), "/sys/class/device")
	if err != nil || info.Kind != PathDirectory {
		t.Fatalf("Stat() = %#v, %v", info, err)
	}
}

// TestOSFileSystemRejectsEscapingSymlink verifies alternate roots cannot reach host data.
func TestOSFileSystemRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private"), []byte("must not be read"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sys"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "sys", "escape")); err != nil {
		t.Fatal(err)
	}
	filesystem, err := NewOSFileSystem(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.ReadFile(context.Background(), "/sys/escape/private", 128); err == nil {
		t.Fatal("ReadFile() followed a symbolic link outside the diagnostic root")
	}
}

// TestPlatformInspectionAvoidsAbsoluteProcDeviceTreeLink models the live Linux
// alias while proving canonical sysfs identity remains available in a root.
func TestPlatformInspectionAvoidsAbsoluteProcDeviceTreeLink(t *testing.T) {
	root := t.TempDir()
	canonical := filepath.Join(root, "sys/firmware/devicetree/base")
	if err := os.MkdirAll(canonical, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "model"), []byte("Microsoft Surface Pro 11th Edition (OLED)\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(canonical, "compatible"), []byte("microsoft,denali\x00qcom,x1e80100\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "proc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/sys/firmware/devicetree/base", filepath.Join(root, "proc/device-tree")); err != nil {
		t.Fatal(err)
	}
	filesystem, err := NewOSFileSystem(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.ReadFile(context.Background(), "/proc/device-tree/model", maximumIdentityBytes); err == nil {
		t.Fatal("descriptor-rooted read followed an absolute proc device-tree link")
	}
	doctor := &Doctor{filesystem: filesystem}
	check, matched := doctor.inspectPlatform(context.Background())
	if !matched || check.State != StatePass {
		t.Fatalf("canonical platform inspection = %#v, matched %t", check, matched)
	}
}

// TestSafeLeafRejectsControlAndTraversalNames verifies generated sysfs paths stay bounded.
func TestSafeLeafRejectsControlAndTraversalNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", "a\\b", "private\nname"} {
		if safeLeaf(name) {
			t.Errorf("safeLeaf(%q) = true", name)
		}
	}
	if !safeLeaf("rfkill123") {
		t.Fatal("safeLeaf(rfkill123) = false")
	}
}
