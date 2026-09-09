package sp11

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestWiFiDatabaseXZSnapshot runs the production decompression pipeline against
// real XZ data, retaining its byte comparisons and expanded-output limit.
func TestWiFiDatabaseXZSnapshot(t *testing.T) {
	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("xz is required for the compressed database fixture")
	}
	for _, scenario := range []string{"valid", "corrupt", "symlink", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			root, workspace := t.TempDir(), t.TempDir()
			directory := filepath.Join(root, "usr/lib/firmware/ath12k/WCN7850/hw2.0")
			if err := os.MkdirAll(directory, 0755); err != nil {
				t.Fatal(err)
			}
			payload := []byte("synthetic firmware database")
			if scenario == "oversized" {
				payload = bytes.Repeat([]byte("x"), (16<<20)+1)
			}
			compress := exec.Command("xz", "--compress", "--stdout")
			compress.Stdin = bytes.NewReader(payload)
			compressed, err := compress.Output()
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "corrupt" {
				compressed = []byte("invalid XZ stream")
			}
			source := filepath.Join(directory, "board-2.bin.xz")
			if err := os.WriteFile(source, compressed, 0644); err != nil {
				t.Fatal(err)
			}
			if scenario == "symlink" {
				if err := os.Rename(source, source+".saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(source+".saved", source); err != nil {
					t.Fatal(err)
				}
			}
			// Keep host portability separate from the decoder under test. The
			// Linux tools container supplies GNU stat and timeout in production.
			prelude := `stat() { wc -c < "${@: -1}"; }
timeout() { shift; "$@"; }
`
			script := prelude + strings.ReplaceAll(strings.Replace(wifiDatabaseSnapshotScript, "root=/linux-work/$1", "root=$TEST_ROOT", 1), "/work/", "$TEST_WORK/")
			command := exec.Command("bash", "-ceu", script, "snapshot-wifi", "rootfs")
			command.Env = append(os.Environ(), "TEST_ROOT="+root, "TEST_WORK="+workspace)
			output, err := command.CombinedOutput()
			if (err == nil) != (scenario == "valid") {
				t.Fatalf("error=%v output=%s", err, output)
			}
			if scenario == "valid" {
				actual, err := os.ReadFile(filepath.Join(workspace, "rootfs-board-2.bin"))
				if err != nil || !bytes.Equal(actual, payload) {
					t.Fatalf("snapshot differs: %v", err)
				}
				retained, err := os.ReadFile(source)
				if err != nil || !bytes.Equal(retained, compressed) {
					t.Fatalf("source database changed: %v", err)
				}
			}
		})
	}
}
