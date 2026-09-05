//go:build darwin || linux

package platform

import (
	"fmt"
	"os"
	"syscall"
)

// workspaceOwnerProbeContents authenticates the one private file used to
// prove effective Docker Desktop bind-mount access.
const workspaceOwnerProbeContents = "lexr-workspace-owner"

// numericWorkspaceOwner returns the decimal UID:GID which owns one private
// workspace without relying on usernames inside the tools image.
func numericWorkspaceOwner(workspace string) (string, error) {
	info, err := os.Stat(workspace)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("workspace does not expose POSIX ownership")
	}
	return fmt.Sprintf("%d:%d", stat.Uid, stat.Gid), nil
}
