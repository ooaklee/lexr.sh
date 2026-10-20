package build

import (
	"context"
	"errors"
)

// A Manager has one sequential owner. Commit the complete binding once, before
// its producer can acquire dependencies. Retain the dependency Manager itself:
// its process-local Prepared handle is authorized by that exact owner pointer.
func (m *Manager) ensureTransport(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.transport != nil {
		return m.transport.Verify(ctx)
	}
	if m.prepared != nil {
		return errors.New("prepared sensor dependencies have lost their local transport binding")
	}
	binding, err := m.bindTransport(ctx, m.runner, m.host)
	if err != nil {
		return err
	}
	if binding == nil || binding.Runner() == nil {
		return errors.New("sensor transport resolution returned no bound runner")
	}
	engine, version := binding.EngineIdentity()
	if !sourceEngineID.MatchString(engine) || !sourceServerVersion.MatchString(version) {
		return errors.New("sensor transport resolution returned an invalid engine identity")
	}
	if err := binding.Verify(ctx); err != nil {
		return err
	}
	// No partial assignment on resolver/verification failure, no replacement of
	// the producer, and no automatic fallback after a later binding failure.
	m.runner = binding.Runner()
	m.dependencies.Runner = m.runner
	m.transport = binding
	return nil
}
