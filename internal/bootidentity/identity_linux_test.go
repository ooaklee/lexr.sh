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

// TestBtrfsDeviceDivergenceRequiresExactStatxMountProof closes the mismatch exception.
func TestBtrfsDeviceDivergenceRequiresExactStatxMountProof(t *testing.T) {
	st := unix.Stat_t{Dev: unix.Mkdev(0, 38), Ino: 256, Mode: unix.S_IFDIR | 0o755}
	entry := mountEntry{mountID: 20, devMajor: 0, devMinor: 36, fstype: "btrfs"}
	const mask = unix.STATX_TYPE | unix.STATX_MODE | unix.STATX_INO | unix.STATX_MNT_ID
	proof := unix.Statx_t{Mask: mask, Mnt_id: 20, Ino: st.Ino, Mode: uint16(st.Mode), Dev_major: 0, Dev_minor: 38}
	old := statxForMount
	t.Cleanup(func() { statxForMount = old })
	for _, tc := range []struct {
		name string
		edit func(*unix.Statx_t)
		err  error
		want bool
	}{
		{"selected mount", func(*unix.Statx_t) {}, nil, true},
		{"wrong mount", func(x *unix.Statx_t) { x.Mnt_id++ }, nil, false},
		{"missing mount mask", func(x *unix.Statx_t) { x.Mask &^= unix.STATX_MNT_ID }, nil, false},
		{"missing inode mask", func(x *unix.Statx_t) { x.Mask &^= unix.STATX_INO }, nil, false},
		{"wrong inode", func(x *unix.Statx_t) { x.Ino++ }, nil, false},
		{"wrong live device", func(x *unix.Statx_t) { x.Dev_minor++ }, nil, false},
		{"wrong mode", func(x *unix.Statx_t) { x.Mode = unix.S_IFREG | 0o755 }, nil, false},
		{"unsupported syscall", func(*unix.Statx_t) {}, unix.ENOSYS, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := proof
			tc.edit(&value)
			statxForMount = func(dirfd int, path string, flags, requested int, dst *unix.Statx_t) error {
				if dirfd != unix.AT_FDCWD || path != "/selected" || flags != unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT || requested != mask {
					t.Fatal("statx scope widened")
				}
				*dst = value
				return tc.err
			}
			if got := statMatchesSelectedMount("/selected", st, entry); got != tc.want {
				t.Fatalf("mount proof = %t, want %t", got, tc.want)
			}
		})
	}
	statxForMount = func(int, string, int, int, *unix.Statx_t) error { t.Fatal("unexpected statx tolerance"); return nil }
	ext4 := entry
	ext4.fstype = "ext4"
	if statMatchesSelectedMount("/selected", st, ext4) {
		t.Fatal("non-btrfs mismatch accepted")
	}
	ext4.devMinor = 38
	if !statMatchesSelectedMount("/selected", st, ext4) {
		t.Fatal("ordinary exact-match path regressed")
	}
	st.Mode = unix.S_IFLNK | 0o755
	if statMatchesSelectedMount("/selected", st, ext4) {
		t.Fatal("symlink accepted")
	}
}

// These fixtures use real temporary directories and injected kernel evidence,
// not privileged mounts, the btrfs tool or a real Btrfs filesystem.
func TestNativeBtrfsResolverRechecksDefaultAndUnmountedBoot(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		splitBoot, flipDefault bool
		wantError              bool
	}{
		{"stable explicit proof", false, false, false},
		{"unmounted boot subvolume", true, false, true},
		{"default changed during resolution", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "boot"), 0o755); err != nil {
				t.Fatal(err)
			}
			uuidDir, partDir := makeByDir(t, t.TempDir(), "fixture-root", "fixture-part", [2]int{8, 1})
			oldMount, oldUUID, oldPart := mountInfoPath, uuidMetaDir, partUUIDMetaDir
			oldStat, oldStatx, oldDefault, oldDevice := statForIdentity, statxForMount, defaultBtrfsRoot, statDeviceFn
			t.Cleanup(func() {
				mountInfoPath, uuidMetaDir, partUUIDMetaDir = oldMount, oldUUID, oldPart
				statForIdentity, statxForMount, defaultBtrfsRoot, statDeviceFn = oldStat, oldStatx, oldDefault, oldDevice
			})
			mountInfoPath, uuidMetaDir, partUUIDMetaDir = filepath.Join(t.TempDir(), "mountinfo"), uuidDir, partDir
			if err := os.WriteFile(mountInfoPath, []byte(fmt.Sprintf("20 1 0:36 / %s rw - btrfs /dev/fixture rw\n", root)), 0o600); err != nil {
				t.Fatal(err)
			}
			statDeviceFn = func(path string) (int, int, error) {
				if path == "/dev/fixture" {
					return 8, 1, nil
				}
				return oldDevice(path)
			}
			statForIdentity = func(path string) (unix.Stat_t, error) {
				st, err := statUnix(path)
				st.Dev = unix.Mkdev(0, 38)
				if tc.splitBoot && path == filepath.Join(root, "boot") {
					st.Dev = unix.Mkdev(0, 39)
				}
				return st, err
			}
			statxForMount = func(_ int, path string, _ int, mask int, dst *unix.Statx_t) error {
				st, err := statForIdentity(path)
				*dst = unix.Statx_t{Mask: uint32(mask), Mnt_id: 20, Ino: st.Ino, Mode: uint16(st.Mode), Dev_major: unix.Major(st.Dev), Dev_minor: unix.Minor(st.Dev)}
				return err
			}
			attestations := 0
			defaultBtrfsRoot = func(context.Context, string, unix.Stat_t) bool {
				attestations++
				return !tc.flipDefault || attestations == 1
			}
			identity, err := Resolve(context.Background(), root)
			if (err != nil) != tc.wantError {
				t.Fatalf("resolver error = %v, wantError %t", err, tc.wantError)
			}
			if !tc.wantError && (!identity.Root.BtrfsDefaultRoot || attestations != 2 || identity.Boot != identity.Root) {
				t.Fatal("default evidence was not positively attested/rechecked")
			}
			if tc.splitBoot && attestations != 0 {
				t.Fatal("unmounted split boot reached default attestation")
			}
		})
	}
}
