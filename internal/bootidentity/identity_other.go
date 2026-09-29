//go:build !linux

package bootidentity

import (
	"context"
	"errors"
)

// errUnsupportedPlatform is returned when boot identity is resolved on a
// build that cannot read Linux mounted-filesystem metadata. Sanitised.
var errUnsupportedPlatform = errors.New("boot ownership identity requires a Linux target")

// resolvePlatform is unsupported on non-Linux builds so the package
// cross-compiles; callers get an honest error instead of a partial identity.
func resolvePlatform(ctx context.Context, root string) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	return Identity{}, errUnsupportedPlatform
}
