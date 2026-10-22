package build

import (
	"context"
	"errors"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// AuthenticateSource validates the fixed camera input files against their
// committed source authority without planning a build or interpreting payload
// compatibility. It invokes only read-only Git queries, creates no output and
// does not grant build, installation or hardware-compatibility authority.
func AuthenticateSource(ctx context.Context, runner platform.Runner, repositoryRoot string) error {
	if ctx == nil || runner == nil {
		return errors.New("camera source authentication requires a context and Git runner")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := resolveRoot(repositoryRoot)
	if err != nil {
		return err
	}
	_, err = authenticateInputs(ctx, runner, root)
	return err
}
