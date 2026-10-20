//go:build !linux

package build

// Linux bind-mount topology is not inferred from another host's path syntax.
// Native host support and retained-FD ancestry guards remain independently
// required by the controller on Darwin; unsupported hosts still fail there.
func verifyBuildMountTopology([]string) error { return nil }
