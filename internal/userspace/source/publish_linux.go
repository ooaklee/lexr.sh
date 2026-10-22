//go:build linux

package source

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// publishNoReplace atomically renames within the descriptor-held parent and
// refuses every existing destination, including an empty directory or symlink.
func publishNoReplace(parent *os.File, stagingName, destinationName string) error {
	if err := validPublicationNames(parent, stagingName, destinationName); err != nil {
		return err
	}
	fd := int(parent.Fd())
	if err := unix.Renameat2(fd, stagingName, fd, destinationName, unix.RENAME_NOREPLACE); err != nil {
		return fmt.Errorf("publish userspace source without replacement: %w", err)
	}
	return nil
}
