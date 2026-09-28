package bootidentity

import (
	"errors"
	"strings"
	"testing"
)

// TestParseMountInfoWellFormed retains kernel-provided device and subvolume identity.
func TestParseMountInfoWellFormed(t *testing.T) {
	data := []byte(strings.Join([]string{
		"36 35 98:0 /mnt1 /mnt2 rw,noatime master:0 - ext3 /dev/root rw,errors=continue",
		"37 36 254:1 /@root / rw,relatime - btrfs /dev/sda2 rw,subvol=@root",
		"",
	}, "\n"))
	entries, err := parseMountInfo(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[1].fstype != "btrfs" || entries[1].root != "/@root" || entries[1].devMajor != 254 || entries[1].devMinor != 1 {
		t.Fatalf("unexpected entry %+v", entries[1])
	}
}

// TestParseMountInfoOptionalFields accepts records without optional propagation fields.
func TestParseMountInfoOptionalFields(t *testing.T) {
	line := "1 0 8:1 / / rw - ext4 /dev/sda1 rw"
	entries, err := parseMountInfo([]byte(line))
	if err != nil || len(entries) != 1 {
		t.Fatalf("parse optional fields: %v %v", entries, err)
	}
}

// TestParseMountInfoEscapes decodes escaped spaces without changing path meaning.
func TestParseMountInfoEscapes(t *testing.T) {
	line := `27 0 8:1 /my\040root /mnt/my\040root rw - ext4 /dev/disk/by-uuid/x rw`
	entries, err := parseMountInfo([]byte(line))
	if err != nil {
		t.Fatalf("parse escaped: %v", err)
	}
	if entries[0].root != "/my root" || entries[0].mountPoint != "/mnt/my root" {
		t.Fatalf("decode failed: %+v", entries[0])
	}
}

// TestParseMountInfoMalformed rejects incomplete, relative and malformed records.
func TestParseMountInfoMalformed(t *testing.T) {
	cases := map[string]string{
		"too few fields":   "1 0 8:1 / / rw - ext4",
		"two separators":   "1 0 8:1 / / rw - - ext4 src rw",
		"bad dev number":   "1 0 8:x / / rw - ext4 src rw",
		"relative root":    "1 0 8:1 rel / rw - ext4 src rw",
		"relative point":   "1 0 8:1 / rel rw - ext4 src rw",
		"nul decode":       "1 0 8:1 /a\\000b / rw - ext4 src rw",
		"bad octal":        "1 0 8:1 /a\\04b / rw - ext4 src rw",
		"truncated escape": "1 0 8:1 /a\\04 / rw - ext4 src rw",
		"bad mount id":     "x 0 8:1 / / rw - ext4 src rw",
	}
	for name, line := range cases {
		if _, err := parseMountInfo([]byte(line)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := parseMountInfo(nil); err == nil {
		t.Error("empty input: expected error")
	}
}

// TestParseMountInfoBounds enforces byte, entry and field limits.
func TestParseMountInfoBounds(t *testing.T) {
	big := strings.Repeat("1 0 8:1 / / rw - ext4 src rw\n", maxMountEntries+2)
	if _, err := parseMountInfo([]byte(big)); err == nil {
		t.Error("entry bound not enforced")
	}
	huge := "1 0 8:1 / " + strings.Repeat("a", maxFieldLength+10) + " rw - ext4 src rw"
	if _, err := parseMountInfo([]byte(huge)); err == nil {
		t.Error("field bound not enforced")
	}
	oversize := strings.Repeat("1 0 8:1 / / rw - ext4 src rw\n", (maxMountInfoBytes/34)+2)
	if _, err := parseMountInfo([]byte(oversize)); err == nil {
		t.Error("byte bound not enforced")
	}
}

// entry constructs one synthetic mount record for pure selection tests.
func entry(point string, major, minor int, root, fstype string) mountEntry {
	return mountEntry{mountID: 1, parentID: 0, devMajor: major, devMinor: minor, root: root, mountPoint: point, fstype: fstype}
}

// TestSelectRootAndBootShared uses the root view when boot has no distinct mount.
func TestSelectRootAndBootShared(t *testing.T) {
	entries := []mountEntry{
		entry("/", 8, 1, "/", "ext4"),
	}
	root, boot, within, err := selectRootAndBoot(entries, "/")
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if root.mountPoint != "/" || boot.mountPoint != "/" || !within {
		t.Fatalf("unexpected selection %+v %+v %v", root, boot, within)
	}
}

// TestSelectRootAndBootSeparateBoot retains the separate boot filesystem.
func TestSelectRootAndBootSeparateBoot(t *testing.T) {
	entries := []mountEntry{
		entry("/", 8, 2, "/", "ext4"),
		entry("/boot", 8, 1, "/", "ext4"),
	}
	_, boot, within, err := selectRootAndBoot(entries, "/")
	if err != nil || within || boot.devMinor != 1 {
		t.Fatalf("separate boot: %v %+v %v", err, boot, within)
	}
}

// TestSelectRootAndBootBtrfsSubvol preserves the exact subvolume view.
func TestSelectRootAndBootBtrfsSubvol(t *testing.T) {
	entries := []mountEntry{
		entry("/", 254, 1, "/@root", "btrfs"),
	}
	root, boot, within, err := selectRootAndBoot(entries, "/")
	if err != nil || !within {
		t.Fatalf("btrfs: %v %v", err, within)
	}
	if root.root != "/@root" || boot.root != "/@root" {
		t.Fatalf("btrfs roots: %+v", root)
	}
}

// TestSelectRootSubMountUnderRootFails requires the selected root to be mounted exactly.
func TestSelectRootSubMountUnderRootFails(t *testing.T) {
	// A directory inside a mounted view does not prove an installed root.
	entries := []mountEntry{entry("/mnt/target", 8, 1, "/", "ext4")}
	_, _, _, err := selectRootAndBoot(entries, "/mnt/target/subdirectory")
	if !errors.Is(err, errRootNotMountpoint) {
		t.Fatalf("want errRootNotMountpoint, got %v", err)
	}
	// Exact match with a subvolume root is fine.
	entries = []mountEntry{entry("/mnt/target", 8, 1, "/@", "btrfs")}
	if _, _, _, err := selectRootAndBoot(entries, "/mnt/target"); err != nil {
		t.Fatalf("exact subvolume: %v", err)
	}
}

// TestSelectRootStackedAmbiguous rejects competing views of one device.
func TestSelectRootStackedAmbiguous(t *testing.T) {
	entries := []mountEntry{
		entry("/", 8, 1, "/", "ext4"),
		entry("/", 8, 1, "/other", "ext4"), // stacked same device, different fs root
	}
	_, _, _, err := selectRootAndBoot(entries, "/")
	if !errors.Is(err, errAmbiguousIdentity) {
		t.Fatalf("want errAmbiguousIdentity, got %v", err)
	}
}

// TestSelectRootStackedCompatible permits repeated evidence of the same mounted view.
func TestSelectRootStackedCompatible(t *testing.T) {
	// Bind mounts / remounts of the same view are fine; topmost wins.
	entries := []mountEntry{
		entry("/", 8, 1, "/", "ext4"),
		entry("/", 8, 1, "/", "ext4"),
	}
	_, _, _, err := selectRootAndBoot(entries, "/")
	if err != nil {
		t.Fatalf("compatible stack: %v", err)
	}
}

// TestSelectRootBindReMountOfSubvol rejects a competing subvolume at the same mountpoint.
func TestSelectRootBindReMountOfSubvol(t *testing.T) {
	// btrfs with FSRoot "/" plus a later subvolume view at the same point
	// sharing the device: unresolvable without extra ranking heuristics.
	entries := []mountEntry{
		entry("/", 254, 1, "/", "btrfs"),
		entry("/", 254, 1, "/@root", "btrfs"),
	}
	_, _, _, err := selectRootAndBoot(entries, "/")
	if !errors.Is(err, errAmbiguousIdentity) {
		t.Fatalf("want errAmbiguousIdentity, got %v", err)
	}
}

// TestSelectRootExactOnly refuses to infer identity for an unpacked directory.
func TestSelectRootExactOnly(t *testing.T) {
	entries := []mountEntry{entry("/rooty", 8, 1, "/", "ext4")}
	_, _, _, err := selectRootAndBoot(entries, "/rooty/sub")
	if !errors.Is(err, errRootNotMountpoint) {
		t.Fatalf("want errRootNotMountpoint for non-exact, got %v", err)
	}
}

// TestNamespaceNeighboursPreserveDiskViews reproduces normal Snap namespace
// mounts without capturing real filesystem or namespace identifiers.
func TestNamespaceNeighboursPreserveDiskViews(t *testing.T) {
	const disks = "10 1 8:1 / / rw - ext4 /dev/example-root rw\n11 10 8:2 / /boot rw - ext4 /dev/example-boot rw\n"
	const namespaces = "12 10 0:4 mnt:[12345] /run/snapd/ns/one.mnt rw - nsfs nsfs rw\n" +
		"13 10 0:4 mnt:[12346] /run/snapd/ns/two.mnt rw shared:2 - nsfs nsfs rw\n" +
		`14 10 0:4 net:[12347] /run/example\040namespace rw - nsfs nsfs rw` + "\n"
	entries, err := parseMountInfo([]byte(disks + namespaces))
	if err != nil || len(entries) != 5 {
		t.Fatalf("namespace neighbours: entries=%d err=%v", len(entries), err)
	}
	root, boot, within, err := selectRootAndBoot(entries, "/")
	if err != nil || within || root.devMinor != 1 || boot.devMinor != 2 {
		t.Fatalf("disk selection changed: root=%+v boot=%+v within=%v err=%v", root, boot, within, err)
	}
	if entries[4].mountPoint != "/run/example namespace" || entries[4].root != "net:[12347]" {
		t.Fatal("namespace record was discarded or rewritten")
	}
}

// TestNamespaceRootsNeverQualify rejects non-path roots selected directly or
// stacked over a disk view, regardless of mount table ordering.
func TestNamespaceRootsNeverQualify(t *testing.T) {
	for _, point := range []string{"/", "/boot"} {
		for _, order := range []string{"only", "before", "after"} {
			t.Run(point+"/"+order, func(t *testing.T) {
				ns := entry(point, 0, 4, "mnt:[12345]", "nsfs")
				disk := entry(point, 8, 1, "/", "ext4")
				var entries []mountEntry
				if point == "/boot" {
					entries = append(entries, entry("/", 8, 2, "/", "ext4"))
				}
				switch order {
				case "only":
					entries = append(entries, ns)
				case "before":
					entries = append(entries, ns, disk)
				case "after":
					entries = append(entries, disk, ns)
				}
				if _, _, _, err := selectRootAndBoot(entries, "/"); !errors.Is(err, errAmbiguousIdentity) {
					t.Fatalf("namespace selected or hidden: %v", err)
				}
			})
		}
	}
}

// TestNamespaceSyntaxRemainsBounded keeps the exception narrow: nsfs display
// names do not relax path, field, escape or filesystem-type validation.
func TestNamespaceSyntaxRemainsBounded(t *testing.T) {
	for name, line := range map[string]string{
		"ext4 root":       "12 10 8:1 mnt:[123] /boot rw - ext4 src rw",
		"relative point":  "12 10 0:4 mnt:[123] run/ns rw - nsfs nsfs rw",
		"unclean point":   "12 10 0:4 mnt:[123] /run/../boot rw - nsfs nsfs rw",
		"relative root":   "12 10 0:4 relative /run/ns rw - nsfs nsfs rw",
		"unclean root":    "12 10 0:4 /a/../b /run/ns rw - nsfs nsfs rw",
		"missing inode":   "12 10 0:4 mnt:[] /run/ns rw - nsfs nsfs rw",
		"invalid inode":   "12 10 0:4 mnt:[abc] /run/ns rw - nsfs nsfs rw",
		"trailing bytes":  "12 10 0:4 mnt:[123]extra /run/ns rw - nsfs nsfs rw",
		"invalid escape":  `12 10 0:4 mnt:[123] /run/\000ns rw - nsfs nsfs rw`,
		"extra field":     "12 10 0:4 mnt:[123] /run/ns rw - nsfs nsfs rw extra",
		"extra separator": "12 10 0:4 mnt:[123] /run/ns rw - - nsfs nsfs rw",
		"long field":      "12 10 0:4 mnt:[" + strings.Repeat("1", maxFieldLength) + "] /run/ns rw - nsfs nsfs rw",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseMountInfo([]byte(line)); !errors.Is(err, errMalformedMountInfo) {
				t.Fatalf("malformed record accepted: %v", err)
			}
		})
	}
}
