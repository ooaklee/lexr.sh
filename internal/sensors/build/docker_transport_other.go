//go:build !linux && !darwin

package build

import "errors"

// sourceTransportCurrentUID supplies no Unix ownership identity on this host.
func sourceTransportCurrentUID() int { return -1 }

// sourceTransportLstat refuses unsupported Unix transport metadata observations.
func sourceTransportLstat(string) (sourceTransportPathInfo, error) {
	return sourceTransportPathInfo{}, errors.New("local Unix Docker transport is unsupported on this host")
}
