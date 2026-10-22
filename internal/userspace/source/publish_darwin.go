//go:build darwin

package source

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// publishNoReplace uses Darwin's exclusive descriptor-relative rename rather
// than a check-then-rename sequence which could overwrite a raced destination.
func publishNoReplace(parent *os.File, stagingName, destinationName string) error {
	if err := validPublicationNames(parent, stagingName, destinationName); err != nil {
		return err
	}
	fd := int(parent.Fd())
	if err := unix.RenameatxNp(fd, stagingName, fd, destinationName, unix.RENAME_EXCL); err != nil {
		return fmt.Errorf("publish userspace source without replacement: %w", err)
	}
	return nil
}
