package debianlive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMediaChecksumsPreserveUnchangedSourceMembers checks Debian's legacy media
// inventory after a root replacement and addition of Lexr support files.
func TestMediaChecksumsPreserveUnchangedSourceMembers(t *testing.T) {
	workspace := t.TempDir()
	source := "d41d8cd98f00b204e9800998ecf8427e  ./casper/filesystem.squashfs\n" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  ./pool/package.deb\n"
	if err := os.WriteFile(filepath.Join(workspace, "source-md5sum.txt"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(workspace, "replacement")
	if err := os.WriteFile(file, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateMediaChecksums(workspace, map[string]string{"casper/filesystem.squashfs": file, "sp11/new-file": file}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "md5sum.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := "900150983cd24fb0d6963f7d28e17f72  ./casper/filesystem.squashfs\n" +
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  ./pool/package.deb\n" +
		"900150983cd24fb0d6963f7d28e17f72  ./sp11/new-file\n"
	if string(data) != want {
		t.Fatalf("unexpected media inventory: %s", data)
	}
}

// TestMediaChecksumsRejectAmbiguousMembers prevents traversal, alternate path
// spellings and duplicate records from reaching the published media inventory.
func TestMediaChecksumsRejectAmbiguousMembers(t *testing.T) {
	for _, member := range []string{"", ".", "..", "../outside", "/absolute", "dir/../file", "dir//file", "dir/./file", "dir/", "dir\\file", "a b", "a\nfile", "a\x00file"} {
		t.Run(strings.ReplaceAll(member, "/", "_"), func(t *testing.T) {
			if safeChecksumMember(member) {
				t.Fatalf("accepted unsafe member %q", member)
			}
		})
	}
	for _, source := range []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  ./../outside\n",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  ./file\naaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  ./file\n",
	} {
		workspace := t.TempDir()
		if err := os.WriteFile(filepath.Join(workspace, "source-md5sum.txt"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := updateMediaChecksums(workspace, nil); err == nil {
			t.Fatalf("accepted ambiguous source inventory %q", source)
		}
		if _, err := os.Stat(filepath.Join(workspace, "md5sum.txt")); !os.IsNotExist(err) {
			t.Fatal("published an inventory after rejection")
		}
	}
}

// TestValidationRejectsStaleMediaChecksums catches an output whose bytes changed
// without updating the distribution's integrity-check inventory.
func TestValidationRejectsStaleMediaChecksums(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "md5sum.txt"), []byte("900150983cd24fb0d6963f7d28e17f72  ./live/vmlinuz\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kernel"), []byte("abc"), 0644); err != nil {
		t.Fatal(err)
	}
	mappings := map[string]string{"live/vmlinuz": "kernel"}
	if err := validateMediaChecksums(root, mappings); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kernel"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateMediaChecksums(root, mappings); err == nil {
		t.Fatal("accepted an outdated integrity checksum")
	}
}
