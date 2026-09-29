//go:build linux

package bootidentity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// TestNativeResolverChecksDirectoryAndMetadata binds a Linux directory stat,
// mountinfo record and persistent identifier without requiring privileged mounts.
func TestNativeResolverChecksDirectoryAndMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "boot"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := statUnix(root)
	if err != nil {
		t.Fatal(err)
	}
	major, minor := int(unix.Major(uint64(st.Dev))), int(unix.Minor(uint64(st.Dev)))
	uuidDir, partDir := makeByDir(t, t.TempDir(), "fixture-root", "fixture-part", [2]int{major, minor})
	oldMount, oldUUID, oldPart := mountInfoPath, uuidMetaDir, partUUIDMetaDir
	mountInfoPath, uuidMetaDir, partUUIDMetaDir = filepath.Join(t.TempDir(), "mountinfo"), uuidDir, partDir
	t.Cleanup(func() { mountInfoPath, uuidMetaDir, partUUIDMetaDir = oldMount, oldUUID, oldPart })
	record := fmt.Sprintf("20 1 %d:%d / %s rw - ext4 /dev/fixture rw\n", major, minor, root)
	if err := os.WriteFile(mountInfoPath, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := Resolve(context.Background(), root)
	if err != nil || id.Root.UUID != "fixture-root" || id.Boot != id.Root {
		t.Fatalf("mounted view: %+v %v", id, err)
	}
	namespace := "21 20 0:4 mnt:[12345] /run/snapd/ns/example.mnt rw - nsfs nsfs rw\n"
	if err := os.WriteFile(mountInfoPath, []byte(record+namespace), 0o600); err != nil {
		t.Fatal(err)
	}
	withNamespace, err := Resolve(context.Background(), root)
	if err != nil || withNamespace != id {
		t.Fatalf("unrelated namespace changed mounted identity: %v", err)
	}
	// A namespace appearing over the selected boot directory must not be
	// ignored in favour of the apparently usable root-local boot view.
	covering := fmt.Sprintf("21 20 0:4 mnt:[12345] %s/boot rw - nsfs nsfs rw\n", root)
	if err := os.WriteFile(mountInfoPath, []byte(record+covering), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(context.Background(), root); err == nil {
		t.Fatal("namespace over selected boot accepted")
	}
	wrong := fmt.Sprintf("20 1 %d:%d / %s rw - ext4 /dev/fixture rw\n", major, minor+1, root)
	if err := os.WriteFile(mountInfoPath, []byte(wrong), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(context.Background(), root); err == nil {
		t.Fatal("directory/mountinfo device mismatch accepted")
	}
}

// TestNativeStatRequiresBlockDevice rejects ordinary files as identity proof.
func TestNativeStatRequiresBlockDevice(t *testing.T) {
	file := filepath.Join(t.TempDir(), "regular")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := statDeviceFn(file); err == nil {
		t.Fatal("ordinary file accepted as block device")
	}
}
