package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	sensorbuild "github.com/ooaklee/lexr.sh/internal/sensors/build"
)

// This regular-file marker locates a checkout only. No bytes from it become
// source authority here; sensorbuild.Manager.Run owns authentication/staging.
const sensorSourceInventoryMarker = "userspace/sensors/libssc/SOURCE.json"

// Sensor source selectors choose separately compiled owners, not manifest data.
const (
	SensorSourceLegacy047   = "legacy-047"
	SensorSourceMetadata048 = "metadata-048"
)

// The private seam permits typed adapter tests without exposing arbitrary
// images, recipes, dependency handles or a public execution interface.
type sensorBuildManager interface {
	// Run delegates an explicit typed request to the native source controller.
	Run(context.Context, sensorbuild.Request) (sensorbuild.Result, error)
}

// validateSensorAdapterRequest refuses unrelated options and implicit parent paths.
func validateSensorAdapterRequest(request Request) error {
	if request.SensorSourceProfile != "" && request.SensorSourceProfile != SensorSourceLegacy047 && request.SensorSourceProfile != SensorSourceMetadata048 {
		return errors.New("sensor-source-profile must be legacy-047 or metadata-048")
	}
	if request.Image != "" || request.WorkVolume != "" || request.MinimumFreeGiB != 0 || request.NoPull {
		return errors.New("sensors builds do not accept image, work-volume, minimum-free-gib or no-pull; the native manager owns immutable policy and fresh dependency readiness")
	}
	if request.Jobs < 0 || request.Jobs > 8 {
		return errors.New("sensor jobs must be between 1 and 8, or zero to use the native default")
	}
	for _, parent := range []struct{ label, path string }{
		{"sensor-work-parent", request.SensorWorkParent}, {"output-dir", request.OutputDirectory},
	} {
		if strings.TrimSpace(parent.path) == "" {
			return fmt.Errorf("%s must explicitly name an existing directory for sensors builds", parent.label)
		}
		info, err := os.Stat(parent.path)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("%s must name an existing directory for sensors builds", parent.label)
		}
	}
	// Canonicalisation, ownership/mode, overlap, mount topology, free-space and
	// full source checks remain the native manager's own gates. No defaults,
	// directory creation, filesystem cleanup or Docker calls occur here.
	return nil
}

// runSensors preserves the native controller's typed result and original failure.
func (m *Manager) runSensors(ctx context.Context, root string, request Request) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("sensor build context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	builder := m.sensorBuilder
	if request.SensorSourceProfile == SensorSourceMetadata048 {
		builder = m.sensorBuilder048
	}
	if builder == nil {
		return Result{}, errors.New("native sensor source build manager is unavailable")
	}
	buildResult, err := builder.Run(ctx, sensorbuild.Request{
		RepositoryRoot: root,
		WorkParent:     request.SensorWorkParent,
		OutputParent:   request.OutputDirectory,
		Jobs:           request.Jobs,
		DryRun:         request.DryRun,
	})
	// Native phase failure can still carry its exact retained work/resource
	// names and uncertainty. Never erase that partial result at the adapter.
	return Result{Component: ComponentSensors, Sensors: &buildResult}, err
}
