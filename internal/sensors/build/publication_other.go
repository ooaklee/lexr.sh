//go:build !linux && !darwin

package build

import (
	"errors"
	"os"
)

func publicationSupported() error {
	return errors.New("sensor artifact publication requires Linux or Darwin no-replace directory operations")
}

func renamePublicationNoReplace(*os.File, string, string) error {
	return publicationSupported()
}

func openPublicationRegular(*os.File, string) (*os.File, error) {
	return nil, publicationSupported()
}
