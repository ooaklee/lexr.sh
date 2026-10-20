//go:build !linux && !darwin

package source

import (
	"errors"
	"os"
)

func regularReadSupported() error {
	return errors.New("sensor source authentication requires Linux or Darwin no-follow regular-file support")
}

func openRegularLeaf(_ *os.Root, _ string) (*os.File, error) { return nil, regularReadSupported() }

func validateRegularFD(_ *os.File) error { return regularReadSupported() }

func directoryContains(_, _ *os.Root) (bool, error) { return false, regularReadSupported() }

func directoryPathMatches(_ string, _ *os.Root) error { return regularReadSupported() }
