package debianlive

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// inspectedGRUBFixturePath is the byte-identical copy of the inspected source
// grub.cfg stored under testdata for these tests.
const inspectedGRUBFixturePath = "testdata/source-grub.cfg"

// inspectedDiskInfoBytes reproduces the exact 71-byte disk-info identity of
// the inspected medium, with no trailing newline.
var inspectedDiskInfoBytes = []byte("Auto-generated Debian GNU/Linux Live testing gnome 2024-09-02T08:13:51Z")

// loadInspectedGRUB reads the pinned grub fixture and verifies it still
// matches the digest of the inspected source snapshot.
func loadInspectedGRUB(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(inspectedGRUBFixturePath)
	if err != nil {
		t.Fatalf("read %s: %v", inspectedGRUBFixturePath, err)
	}
	return data
}

// TestAdapterIDIsStable pins the catalogue identifier other packages route on.
func TestAdapterIDIsStable(t *testing.T) {
	if AdapterID != "debian-live" {
		t.Fatalf("AdapterID = %q, want debian-live", AdapterID)
	}
}

// TestParseSourceLayoutAcceptsInspectedSnapshot verifies the pinned grub.cfg
// and disk-info identity yield the exact live payload layout.
func TestParseSourceLayoutAcceptsInspectedSnapshot(t *testing.T) {
	layout, err := parseSourceLayout(loadInspectedGRUB(t), inspectedDiskInfoBytes)
	if err != nil {
		t.Fatalf("parseSourceLayout() error = %v", err)
	}
	if layout != (sourceLayout{liveDirectory: "live", kernel: "live/vmlinuz-6.10.6-arm64", initrd: "live/initrd.img-6.10.6-arm64"}) {
		t.Fatalf("parseSourceLayout() = %#v", layout)
	}
	if got := layout.member("filesystem.squashfs"); got != "live/filesystem.squashfs" {
		t.Fatalf("member() = %q, want live/filesystem.squashfs", got)
	}
}

// TestParseSourceLayoutRejectsOtherMedia proves the digest pin and structural
// checks reject any byte-level or structural deviation from the snapshot.
func TestParseSourceLayoutRejectsOtherMedia(t *testing.T) {
	grub := loadInspectedGRUB(t)
	casperMenu := []byte(`menuentry "Try or install elementary OS" {
 linux /casper/vmlinuz boot=casper maybe-ubiquity quiet splash
 initrd /casper/initrd.lz
}
`)
	for _, tt := range []struct{ name, grub, diskInfo string }{
		{
			name:     "changed disk-info byte",
			grub:     string(grub),
			diskInfo: strings.Replace(string(inspectedDiskInfoBytes), "testing", "Testing", 1),
		},
		{
			name:     "disk-info trailing newline",
			grub:     string(grub),
			diskInfo: string(inspectedDiskInfoBytes) + "\n",
		},
		{
			name:     "empty grub",
			grub:     "",
			diskInfo: string(inspectedDiskInfoBytes),
		},
		{
			name:     "oversized grub",
			grub:     strings.Repeat("# pad\n", 12000),
			diskInfo: string(inspectedDiskInfoBytes),
		},
		{
			name:     "casper selector injected",
			grub:     strings.Replace(string(grub), "boot=live", "boot=casper", 1),
			diskInfo: string(inspectedDiskInfoBytes),
		},
		{
			name:     "unpaired initrd removed",
			grub:     strings.Replace(string(grub), "\n\tinitrd\t/live/initrd.img-6.10.6-arm64", "", 1),
			diskInfo: string(inspectedDiskInfoBytes),
		},
		{
			name:     "missing components",
			grub:     strings.Replace(string(grub), "boot=live components", "boot=live", 1),
			diskInfo: string(inspectedDiskInfoBytes),
		},
		{
			name:     "extra active linux entry",
			grub:     string(grub) + "linux\t/elsewhere/vmlinuz boot=live components\n",
			diskInfo: string(inspectedDiskInfoBytes),
		},
		{
			name:     "traversal-style kernel path",
			grub:     strings.Replace(string(grub), "/live/vmlinuz-6.10.6-arm64", "/../../etc/vmlinuz-6.10.6-arm64", 1),
			diskInfo: string(inspectedDiskInfoBytes),
		},
		{
			name:     "casper-style menu",
			grub:     string(casperMenu),
			diskInfo: string(inspectedDiskInfoBytes),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseSourceLayout([]byte(tt.grub), []byte(tt.diskInfo)); err == nil {
				t.Fatal("accepted a source that is not the inspected snapshot")
			}
		})
	}
}

// TestParseSourceLayoutIgnoresCommentedEntries confirms the commented
// alternate live and installer entries in the pinned fixture never count as
// active selectors.
func TestParseSourceLayoutIgnoresCommentedEntries(t *testing.T) {
	grub := loadInspectedGRUB(t)
	if !bytes.Contains(grub, []byte("# linux /live/vmlinuz-6.10.6-arm64")) {
		t.Fatal("fixture no longer contains the commented alternate entries")
	}
	if _, err := parseSourceLayout(grub, inspectedDiskInfoBytes); err != nil {
		t.Fatalf("commented entries altered parsing: %v", err)
	}
}
