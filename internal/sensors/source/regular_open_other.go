//go:build !linux && !darwin

package source

import (
	"errors"
	"os"
)

// regularReadSupported refuses source reads without the required descriptor APIs.
func regularReadSupported() error {
	return errors.New("sensor source authentication requires Linux or Darwin no-follow regular-file support")
}

// openRegularLeaf reports that confined regular-file reads are unsupported.
func openRegularLeaf(_ *os.Root, _ string) (*os.File, error) { return nil, regularReadSupported() }

// validateRegularFD refuses descriptor validation on an unsupported platform.
func validateRegularFD(_ *os.File) error { return regularReadSupported() }

// directoryContains refuses to infer ancestry without supported descriptor APIs.
func directoryContains(_, _ *os.Root) (bool, error) { return false, regularReadSupported() }

// directoryPathMatches refuses pathname identity checks on unsupported platforms.
func directoryPathMatches(_ string, _ *os.Root) error { return regularReadSupported() }
