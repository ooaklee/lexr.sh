//go:build !linux && !darwin

package source

import (
	"errors"
	"os"
)

// publishNoReplace refuses hosts without the reviewed exclusive-rename primitive.
func publishNoReplace(*os.File, string, string) error {
	return errors.New("atomic userspace source publication requires Linux or macOS")
}
