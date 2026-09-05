//go:build linux

package platform

// workspaceOwnerDockerArgs runs extraction as the exact numeric owner of the
// Linux host workspace.
func workspaceOwnerDockerArgs(owner string) []string {
	return []string{"--user", owner}
}

// workspaceOwnerCommand requires a conventional Linux bind mount to expose
// the same numeric owner inside and outside the container before extraction.
// Rootless mappings which cannot satisfy this contract fail closed.
func workspaceOwnerCommand(owner, probeName string, args []string) []string {
	command := []string{
		"sh", "-ceu",
		`actual="$(stat -c '%u:%g' /work)"; if [ "$actual" != "$1" ]; then echo "workspace ownership mapping is unsupported: container sees $actual, host reports $1" >&2; exit 125; fi; shift 2; exec "$@"`,
		"lexr-workspace-owner", owner, probeName,
	}
	return append(command, args...)
}

// verifyWorkspaceOwnerExecution is unnecessary after Linux's strict
// pre-execution numeric-identity check.
func verifyWorkspaceOwnerExecution(_, _ string) error {
	return nil
}
