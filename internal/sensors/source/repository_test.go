package source

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func snapshotTestParent(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	return parent
}

// Restore only this test's owned temporary snapshot before testing.T removes its
// temporary directory. Production never deletes or changes permissions back.
func restoreSnapshotForTest(t *testing.T, name string) {
	t.Helper()
	t.Cleanup(func() {
		_ = filepath.WalkDir(name, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return os.Chmod(p, 0o700)
			}
			return nil
		})
	})
}

func TestRepositorySnapshotCopiesReadonlyWithoutChangingOriginal(t *testing.T) {
	f := newFixture(t)
	parent := snapshotTestParent(t)
	target := filepath.Join(parent, "snapshot")
	restoreSnapshotForTest(t, target)
	original, err := capture(f.root, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	s, err := stageRepository(f.root, target, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil || s.Path() != canonical {
		t.Fatalf("snapshot canonical path differs: got %q, want %q, %v", s.Path(), canonical, err)
	}
	if !reflect.DeepEqual(s.Composition(), original.composition) {
		t.Fatal("snapshot composition differs")
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
	after, err := capture(f.root, f.policy)
	if err != nil || !reflect.DeepEqual(after.composition, original.composition) {
		t.Fatalf("original changed: %v", err)
	}
	if info, err := os.Stat(f.file("libqmi/source/data.c")); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatal("original source permission changed")
	}
	if info, err := os.Stat(parent); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("private parent permission changed")
	}
	copy := s.Composition()
	copy.SourceCommit = "changed"
	if s.Composition().SourceCommit != f.policy.commit {
		t.Fatal("caller changed retained composition")
	}
	// Once captured, later original changes do not become mountable inputs.
	if err := os.WriteFile(f.file("libqmi/source/data.c"), []byte("changed original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("snapshot consulted mutable original: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(); err == nil {
		t.Fatal("closed snapshot remained usable")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal("Close removed retained snapshot")
	}
}

func TestRepositorySnapshotRefusesOverlapAndReuseBeforeSourceWrites(t *testing.T) {
	f := newFixture(t)
	inside := filepath.Join(f.root, "forbidden-snapshot")
	if _, err := stageRepository(f.root, inside, f.policy); err == nil {
		t.Fatal("source overlap accepted")
	}
	if _, err := os.Stat(inside); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("overlap wrote in source")
	}
	alias := filepath.Join(t.TempDir(), "source-alias")
	if err := os.Symlink(f.root, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := stageRepository(f.root, filepath.Join(alias, "forbidden"), f.policy); err == nil {
		t.Fatal("aliased source overlap accepted")
	}
	if _, err := os.Stat(filepath.Join(f.root, "forbidden")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("aliased overlap wrote in source")
	}
	existing := t.TempDir()
	marker := filepath.Join(existing, "marker")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := stageRepository(f.root, existing, f.policy); err == nil {
		t.Fatal("existing destination accepted")
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "preserve" {
		t.Fatal("existing output altered")
	}
}

func TestRepositorySnapshotRetainsInvalidSourcePartialWithoutGrant(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(f.file("libqmi/source/data.c"), []byte("tamper"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(snapshotTestParent(t), "partial")
	restoreSnapshotForTest(t, target)
	s, err := stageRepository(f.root, target, f.policy)
	if err == nil || s != nil {
		t.Fatal("invalid source received snapshot authority")
	}
	if _, err := os.Stat(filepath.Join(target, "userspace")); err != nil {
		t.Fatal("failed partial not retained")
	}
}

func TestRepositorySnapshotRechecksClosedWrapperModesBytesAndLinks(t *testing.T) {
	for _, kind := range []string{"mode", "bytes", "extra-wrapper", "link", "path-replacement", "parent-mode"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			parent := snapshotTestParent(t)
			target := filepath.Join(parent, "snapshot")
			restoreSnapshotForTest(t, target)
			restoreSnapshotForTest(t, filepath.Join(parent, "held"))
			s, err := stageRepository(f.root, target, f.policy)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			member := filepath.Join(target, sourcePrefix, "libqmi/source/data.c")
			switch kind {
			case "mode":
				if err := os.Chmod(member, 0o644); err != nil {
					t.Fatal(err)
				}
			case "bytes":
				if err := os.Chmod(member, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(member, []byte("changed captured bytes\n"), 0o444); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(member, 0o444); err != nil {
					t.Fatal(err)
				}
			case "extra-wrapper":
				if err := os.Chmod(target, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "unlisted"), []byte("extra"), 0o444); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(target, 0o555); err != nil {
					t.Fatal(err)
				}
			case "link":
				dir := filepath.Dir(member)
				if err := os.Chmod(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(dir, "data-link")
				if err := os.Rename(link, filepath.Join(parent, "held-link")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("missing", link); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(dir, 0o555); err != nil {
					t.Fatal(err)
				}
			case "path-replacement":
				if err := os.Rename(target, filepath.Join(parent, "held")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			case "parent-mode":
				if err := os.Chmod(parent, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Verify(); err == nil {
				t.Fatal("changed snapshot accepted")
			}
			if _, err := capture(f.root, f.policy); err != nil {
				t.Fatalf("original was affected: %v", err)
			}
		})
	}
}

func TestApprovedRepositorySnapshotOptIn(t *testing.T) {
	root := os.Getenv("LEXR_TEST_SENSOR_OE_ROOT")
	if root == "" {
		t.Skip("set LEXR_TEST_SENSOR_OE_ROOT to the approved source checkout")
	}
	parent := snapshotTestParent(t)
	target := filepath.Join(parent, "snapshot")
	restoreSnapshotForTest(t, target)
	before, err := Verify(root)
	if err != nil {
		t.Fatal(err)
	}
	s, err := StageRepository(root, target)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !reflect.DeepEqual(s.Composition(), before) || before.SourceFileCount != 949 || before.SourceSymlinkCount != 6 {
		t.Fatal("approved source composition differs")
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
	if after, err := Verify(root); err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("original approved sources changed")
	}
	if strings.Contains(s.Path(), root) {
		t.Fatal("approved snapshot overlaps original")
	}
}

func TestRepositorySnapshotRequiresPrivateParentBeforeWrites(t *testing.T) {
	f := newFixture(t)
	parent := snapshotTestParent(t)
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "must-not-exist")
	if s, err := stageRepository(f.root, target, f.policy); err == nil || s != nil {
		t.Fatal("public parent accepted")
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unsafe parent caused output creation")
	}
}
