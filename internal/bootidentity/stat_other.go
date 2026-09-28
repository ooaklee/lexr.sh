//go:build !linux

package bootidentity

import "errors"

// statDeviceFn is unsupported off Linux; portable tests inject their own.
// Production identity resolution is Linux-only.
var statDeviceFn = func(path string) (int, int, error) {
	return 0, 0, errors.New("statDevice requires a Linux build")
}
