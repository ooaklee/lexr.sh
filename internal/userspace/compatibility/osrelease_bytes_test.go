package compatibility

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestParseOSReleaseMatchesRootReader proves extraction preserves all reader
// semantics while allowing a caller to parse its exact descriptor-bound bytes.
func TestParseOSReleaseMatchesRootReader(t *testing.T) {
	for _, data := range [][]byte{
		[]byte("ID=ubuntu\nVERSION_ID=26.04\n"),
		[]byte("# comment\nNAME='ignored'\nID=\"ubuntu\"\nVERSION_ID='26.04'\n"),
		[]byte("ID=ubuntu\nID=fedora\nVERSION_ID=26.04\n"),
		[]byte("ID=ubuntu\nVERSION_ID=\"$(touch /not-executed)\"\n"),
		[]byte("ID=ubuntu\nVERSION_ID='26.04\n"),
		[]byte("ID=ubuntu\nVERSION_ID=26.04\x00\n"),
		[]byte{'I', 'D', '=', 0xff, '\n'},
		bytes.Repeat([]byte("#"), MaxBytes+1),
		[]byte("ID=ubuntu\n"),
		[]byte("ID=ubuntu\nVERSION_ID=26.04\nMALFORMED\n"),
	} {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "etc"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "etc/os-release"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		copyBefore := bytes.Clone(data)
		id, version, err := ParseOSRelease(data)
		readID, readVersion, readErr := ReadOSRelease(root)
		if (err == nil) != (readErr == nil) || id != readID || version != readVersion {
			t.Fatalf("descriptor parser and root reader differ for %q: %q %q %v versus %q %q %v", data, id, version, err, readID, readVersion, readErr)
		}
		if !bytes.Equal(data, copyBefore) {
			t.Fatal("parser changed caller bytes")
		}
		if err != nil && readErr != nil && len(data) <= MaxBytes && err.Error() != readErr.Error() {
			t.Fatalf("strict error semantics differ: %v versus %v", err, readErr)
		}
	}
}

// TestParseOSReleaseOwnsReturnedIdentity proves caller changes cannot alter the
// interpreted strings and duplicate/unsupported values stay strict failures.
func TestParseOSReleaseOwnsReturnedIdentity(t *testing.T) {
	data := []byte("ID=ubuntu\nVERSION_ID=26.04\n")
	id, version, err := ParseOSRelease(data)
	if err != nil {
		t.Fatal(err)
	}
	for i := range data {
		data[i] = 'x'
	}
	if id != "ubuntu" || version != "26.04" {
		t.Fatal("returned identity aliases mutable input")
	}
	for _, bad := range []string{"ID=ubuntu\nVERSION_ID=26.04\nVERSION_ID=26.10\n", "ID='ubuntu fedora'\nVERSION_ID=26.04\n", "ID=ubuntu\nVERSION_ID=$VERSION\n", "ID=ubuntu\nVERSION_ID=26\\.04\n"} {
		if _, _, err := ParseOSRelease([]byte(bad)); err == nil {
			t.Fatalf("unsafe or duplicate identity accepted: %q", bad)
		}
	}
}
