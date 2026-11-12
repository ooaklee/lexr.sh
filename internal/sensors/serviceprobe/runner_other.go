//go:build !linux

package serviceprobe

import (
	"context"
	"errors"
)

// Observe explicitly refuses unsupported hosts without attempting a process,
// bus connection, alternate target root or platform-specific fallback.
func Observe(context.Context) (*Snapshot, error) {
	return nil, errors.New("passive sensor service observation is supported only on Linux")
}
