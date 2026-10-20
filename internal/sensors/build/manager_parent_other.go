//go:build !linux && !darwin

package build

import "errors"

// inspectWorkParent refuses work-parent checks on unsupported operating systems.
func inspectWorkParent(string) error {
	return errors.New("sensor work parent checks require Linux or Darwin")
}

// buildDirectoryContains refuses ancestry claims without supported FD checks.
func buildDirectoryContains(string, string) (bool, error) { return false, inspectWorkParent("") }
