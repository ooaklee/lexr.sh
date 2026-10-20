//go:build linux || darwin

package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRefusesFIFOAndDeterministicLeafSwap(t *testing.T) {
	t.Run("existing-fifo", func(t *testing.T) {
		f := newFixture(t)
		member := f.file("libqmi/source/data.c")
		if err := os.Rename(member, filepath.Join(f.root, "held-regular")); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mkfifo(member, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := capture(f.root, f.policy); err == nil {
			t.Fatal("FIFO source accepted")
		}
	})
	for _, variant := range []string{"fifo", "symlink"} {
		t.Run("after-lstat-"+variant, func(t *testing.T) {
			f := newFixture(t)
			swapped := false
			f.policy.beforeOpen = func(root *os.Root, name string) error {
				if !strings.HasSuffix(name, "libqmi/source/data.c") {
					return nil
				}
				swapped = true
				if err := root.Rename(name, name+".held"); err != nil {
					return err
				}
				if variant == "fifo" {
					return unix.Mkfifo(filepath.Join(f.root, name), 0o600)
				}
				return root.Symlink("data.c.held", name)
			}
			done := make(chan error, 1)
			go func() { _, err := capture(f.root, f.policy); done <- err }()
			select {
			case err := <-done:
				if !swapped || err == nil || !strings.Contains(err.Error(), "open regular member") {
					t.Fatalf("leaf swap was not refused at no-follow descriptor open: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("source leaf open blocked on a deterministic FIFO/type swap")
			}
		})
	}
}
