//go:build linux || darwin

package publication

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestPublishRejectsFIFOSwapWithoutBlocking proves a concurrent final-name
// replacement cannot stall descriptor-relative post-rename verification.
func TestPublishRejectsFIFOSwapWithoutBlocking(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	sourceISO := filepath.Join(directory, "partial.iso")
	destinationISO := filepath.Join(directory, "output.iso")
	isoBytes := []byte("ISO")
	manifestBytes := []byte("manifest")
	journalBytes := []byte("journal")
	if err := os.WriteFile(sourceISO, isoBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	publisher := func(publicationDirectory *os.File, sourceName, destinationName string) error {
		if err := PublishOutputNoReplace(publicationDirectory, sourceName, destinationName); err != nil {
			return err
		}
		finalPath := filepath.Join(publicationDirectory.Name(), destinationName)
		if err := os.Rename(finalPath, finalPath+".transaction-owned"); err != nil {
			return err
		}
		return unix.Mkfifo(finalPath, 0o600)
	}

	result := make(chan error, 1)
	go func() {
		_, _, err := Publish(
			sourceISO,
			destinationISO,
			manifestBytes,
			journalBytes,
			IdentifyBytes(isoBytes),
			IdentifyBytes(manifestBytes),
			IdentifyBytes(journalBytes),
			publisher,
		)
		result <- err
	}()

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "not the retained staging object") {
			t.Fatalf("Publish() error = %v, want FIFO identity rejection", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Publish() blocked while opening a raced FIFO destination")
	}
	if info, err := os.Lstat(destinationISO + ".manifest.json"); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("raced FIFO was not preserved: mode=%v error=%v", func() os.FileMode {
			if info == nil {
				return 0
			}
			return info.Mode()
		}(), err)
	}
	if _, err := os.Lstat(destinationISO); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ISO commit marker exists after FIFO rejection: %v", err)
	}
}
