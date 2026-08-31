//go:build windows

package cleanup

import (
	"errors"
	"os"
)

// errUnsupportedWindows reports that reversible Linux clean-up is unavailable
// on Windows hosts.
var errUnsupportedWindows = errors.New("Linux clean-up operations are unsupported on Windows")

// directoryIdentity rejects clean-up inspection because Unix directory
// identities are unavailable on Windows.
func directoryIdentity(os.FileInfo) (DirectoryIdentity, error) {
	return DirectoryIdentity{}, errUnsupportedWindows
}

// unsupportedAnchoredRecoveryMetadata rejects recovery inspection because its
// Unix metadata contract is unavailable on Windows.
func unsupportedAnchoredRecoveryMetadata(*os.Root, string, os.FileInfo, uint64) (string, error) {
	return "", errUnsupportedWindows
}

// anchoredExtendedAttributeNames rejects recovery inspection because its Unix
// extended-attribute contract is unavailable on Windows.
func anchoredExtendedAttributeNames(*os.File) ([]string, error) {
	return nil, errUnsupportedWindows
}

// fileOwnership rejects clean-up inspection because Unix ownership metadata is
// unavailable on Windows.
func fileOwnership(os.FileInfo) (uint32, uint32, error) {
	return 0, 0, errUnsupportedWindows
}

// renameAnchoredDirectories rejects clean-up application because descriptor-
// relative Unix renames are unavailable on Windows.
func renameAnchoredDirectories(*os.File, string, *os.File, string) error {
	return errUnsupportedWindows
}

// linkAnchoredDirectories rejects clean-up restoration because descriptor-
// relative Unix hard links are unavailable on Windows.
func linkAnchoredDirectories(*os.File, string, *os.File, string) error {
	return errUnsupportedWindows
}
