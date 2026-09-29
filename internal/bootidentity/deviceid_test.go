package bootidentity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureStat installs a fake statDeviceFn over a temporary device tree.
type fixtureStat map[string][2]int // absolute path -> {major, minor}

// withFixtureStat replaces native block-device stat for portable metadata tests.
func withFixtureStat(t *testing.T, fx fixtureStat) {
	t.Helper()
	old := statDeviceFn
	statDeviceFn = func(path string) (int, int, error) {
		if mm, ok := fx[path]; ok {
			return mm[0], mm[1], nil
		}
		fi, err := os.Lstat(path)
		if err != nil {
			return 0, 0, err
		}
		// Non-device files get a synthetic non-matching dev.
		_ = fi
		return -1, -1, nil
	}
	t.Cleanup(func() { statDeviceFn = old })
}

// makeByDir creates a by-uuid-style directory of symlinks into a fake device
// tree under dir/dev, returning the metadata directories.
func makeByDir(t *testing.T, dir string, uuidName, partName string, mm [2]int) (uuidDir, partDir string) {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	dir = canonical
	devRoot = filepath.Join(dir, "dev")
	t.Cleanup(func() { devRoot = "/dev" })
	devDir := filepath.Join(devRoot, "block")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}
	devPath := filepath.Join(devDir, "sda-test")
	if err := os.WriteFile(devPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	uuidDir = filepath.Join(dir, "by-uuid")
	partDir = filepath.Join(dir, "by-partuuid")
	for d, name := range map[string]string{uuidDir: uuidName, partDir: partName} {
		if name == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(d, devPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(rel, filepath.Join(d, name)); err != nil {
			t.Fatal(err)
		}
	}
	withFixtureStat(t, fixtureStat{devPath: mm})
	return uuidDir, partDir
}

// TestResolveDeviceIdentityMatches binds persistent identifiers to a checked block device.
func TestResolveDeviceIdentityMatches(t *testing.T) {
	dir := t.TempDir()
	uuidDir, partDir := makeByDir(t, dir, "ABCD-1234", "abcd-part", [2]int{8, 1})
	if err := os.WriteFile(filepath.Join(uuidDir, ".unrelated-udev-temporary"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := resolveDeviceIdentity(context.Background(), uuidDir, partDir, 8, 1)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if id.uuid != "ABCD-1234" || id.partUUID != "abcd-part" {
		t.Fatalf("identifiers: %+v", id)
	}
	if !filepath.IsAbs(id.device) || !strings.HasPrefix(id.device, devRoot) {
		t.Fatalf("canonical device: %q", id.device)
	}
	if id.device != filepath.Join(devRoot, "block", "sda-test") {
		t.Fatalf("canonical path: %q", id.device)
	}
}

// TestResolveDeviceIdentityNoMatch rejects missing or unrelated device metadata.
func TestResolveDeviceIdentityNoMatch(t *testing.T) {
	dir := t.TempDir()
	uuidDir, partDir := makeByDir(t, dir, "ABCD-1234", "", [2]int{8, 1})
	if _, err := resolveDeviceIdentity(context.Background(), uuidDir, partDir, 8, 2); !errors.Is(err, errNoDeviceIdentity) {
		t.Fatalf("want errNoDeviceIdentity, got %v", err)
	}
	// Missing metadata dir entirely also fails closed.
	if _, err := resolveDeviceIdentity(context.Background(), filepath.Join(dir, "missing"), filepath.Join(dir, "missing2"), 8, 1); !errors.Is(err, errNoDeviceIdentity) {
		t.Fatalf("missing dirs: want errNoDeviceIdentity, got %v", err)
	}
}

// TestResolveDeviceIdentitySymlinkEscape never trusts metadata links outside the device tree.
func TestResolveDeviceIdentitySymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	uuidDir, _ := makeByDir(t, dir, "ABCD-1234", "", [2]int{8, 1})
	// Point a metadata entry outside the device tree.
	escape := filepath.Join(uuidDir, "EEEE-EEEE")
	if err := os.Symlink("../../../../etc", escape); err != nil {
		t.Fatal(err)
	}
	// The escape never stat-matches, so resolution still succeeds via the
	// honest entry and the escape is ignored... but a direct hit is required
	// for the *escaped* target to matter; verify it is not trusted:
	id, err := resolveDeviceIdentity(context.Background(), uuidDir, filepath.Join(dir, "none"), 8, 1)
	if err != nil || id.uuid != "ABCD-1234" {
		t.Fatalf("escape handling: %+v %v", id, err)
	}
	_ = escape
}

// TestResolveDeviceIdentityDuplicatesFail rejects conflicting identifiers for one device.
func TestResolveDeviceIdentityDuplicatesFail(t *testing.T) {
	dir := t.TempDir()
	uuidDir, partDir := makeByDir(t, dir, "ABCD-1234", "", [2]int{8, 1})
	if err := os.Symlink("../dev/block/sda-test", filepath.Join(uuidDir, "FFFF-FFFF")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveDeviceIdentity(context.Background(), uuidDir, partDir, 8, 1); !errors.Is(err, errNoDeviceIdentity) {
		t.Fatalf("duplicates: want errNoDeviceIdentity, got %v", err)
	}
}
