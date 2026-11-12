//go:build !linux && !darwin

package offline

import (
	"errors"
	"io/fs"
	"os"
)

// supported refuses before creating qualification output on other platforms.
func supported() error { return errors.New("offline sensor qualification requires Linux or Darwin") }

// currentOwner is deliberately unavailable off reviewed platforms.
func currentOwner(fs.FileInfo) bool { return false }

// singleLink is deliberately unavailable off reviewed platforms.
func singleLink(fs.FileInfo) bool { return false }

// privateParent refuses unsupported parent operations.
func privateParent(string) error { return supported() }

// openDirectory refuses unsupported descriptor operations.
func openDirectory(*os.File, string) (*os.File, error) { return nil, supported() }

// openRegular refuses unsupported descriptor operations.
func openRegular(*os.File, string) (*os.File, error) { return nil, supported() }

// renameNoReplace refuses unsupported rename operations.
func renameNoReplace(*os.File, string, string) error { return supported() }

// removeFile refuses unsupported unlink operations.
func removeFile(*os.File, string) error { return supported() }

// removeDirectory refuses unsupported rmdir operations.
func removeDirectory(*os.File, string) error { return supported() }

// availableSpace refuses unsupported filesystem observations.
func availableSpace(*os.File) (int64, error) { return 0, supported() }
