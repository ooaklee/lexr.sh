//go:build !linux && !darwin

package build

import "errors"

func inspectWorkParent(string) error {
	return errors.New("sensor work parent checks require Linux or Darwin")
}
func buildDirectoryContains(string, string) (bool, error) { return false, inspectWorkParent("") }
