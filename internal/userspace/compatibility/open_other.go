//go:build !linux && !darwin

package compatibility

import (
	"errors"
	"os"
)

// OpenRegular permits rooted regular-file diagnosis on other host platforms.
// Descriptor identity is checked against the preflight file observation.
func OpenRegular(root *os.Root, path string) (*os.File, error) {
	before, err := root.Stat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("target evidence is not a regular file")
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		file.Close()
		return nil, errors.New("target evidence changed while opening")
	}
	return file, nil
}
