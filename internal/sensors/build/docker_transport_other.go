//go:build !linux && !darwin

package build

import "errors"

func sourceTransportCurrentUID() int { return -1 }
func sourceTransportLstat(string) (sourceTransportPathInfo, error) {
	return sourceTransportPathInfo{}, errors.New("local Unix Docker transport is unsupported on this host")
}
