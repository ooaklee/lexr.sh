//go:build darwin

package offline

import (
	"golang.org/x/sys/unix"
	"os"
)

// renameNoReplace exclusively publishes or quarantines within one retained FD.
func renameNoReplace(parent *os.File, from, to string) error {
	return unix.RenameatxNp(int(parent.Fd()), from, int(parent.Fd()), to, unix.RENAME_EXCL)
}
