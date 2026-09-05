//go:build !darwin && !linux

package platform

import "fmt"

// numericWorkspaceOwner rejects hosts where the Docker image workflow cannot
// preserve a POSIX workspace ownership boundary.
func numericWorkspaceOwner(string) (string, error) {
	return "", fmt.Errorf("POSIX workspace ownership is unavailable on this host")
}

// workspaceOwnerDockerArgs is unreachable on unsupported hosts because owner
// resolution fails first.
func workspaceOwnerDockerArgs(string) []string {
	return nil
}

// workspaceOwnerCommand is unreachable on unsupported hosts because owner
// resolution fails first.
func workspaceOwnerCommand(_, _ string, _ []string) []string {
	return nil
}

// verifyWorkspaceOwnerExecution is unreachable on unsupported hosts because
// owner resolution fails first.
func verifyWorkspaceOwnerExecution(_, _ string) error {
	return fmt.Errorf("POSIX workspace ownership is unavailable on this host")
}
