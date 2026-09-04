//go:build darwin

package platform

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// workspaceOwnerDockerArgs relies on Docker Desktop's root-to-host bind-mount
// mapping, which is verified after execution with a private file probe.
func workspaceOwnerDockerArgs(string) []string {
	return nil
}

// workspaceOwnerCommand verifies Docker Desktop's virtual bind mount through
// an effective private-file probe because its in-container numeric owner is
// intentionally different from the macOS host owner.
func workspaceOwnerCommand(owner, probeName string, args []string) []string {
	command := []string{
		"sh", "-ceu",
		`umask 077; printf '%s' 'lexr-workspace-owner' > "/work/$2"; shift 2; exec "$@"`,
		"lexr-workspace-owner", owner, probeName,
	}
	return append(command, args...)
}

// verifyWorkspaceOwnerExecution proves the extraction identity created a
// private regular file which the host can read and remove.
func verifyWorkspaceOwnerExecution(workspace, probeName string) (resultErr error) {
	probePath := filepath.Join(workspace, probeName)
	defer func() {
		if err := os.Remove(probePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove workspace ownership probe: %w", err))
		}
	}()
	info, err := os.Lstat(probePath)
	if err != nil {
		return fmt.Errorf("inspect workspace ownership probe: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return fmt.Errorf("workspace ownership probe has unsafe mode %s", info.Mode())
	}
	contents, err := os.ReadFile(probePath)
	if err != nil {
		return fmt.Errorf("read workspace ownership probe: %w", err)
	}
	if string(contents) != workspaceOwnerProbeContents {
		return errors.New("workspace ownership probe has unexpected contents")
	}
	return nil
}
