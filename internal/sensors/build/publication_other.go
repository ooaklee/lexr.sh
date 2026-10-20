//go:build !linux && !darwin

package build

import (
	"errors"
	"os"
)

// publicationSupported refuses hosts lacking the required no-replace operations.
func publicationSupported() error {
	return errors.New("sensor artifact publication requires Linux or Darwin no-replace directory operations")
}

// renamePublicationNoReplace refuses publication rather than falling back to a
// potentially overwriting rename on unsupported hosts.
func renamePublicationNoReplace(*os.File, string, string) error {
	return publicationSupported()
}

// openPublicationRegular refuses unsupported descriptor-relative member checks.
func openPublicationRegular(*os.File, string) (*os.File, error) {
	return nil, publicationSupported()
}
