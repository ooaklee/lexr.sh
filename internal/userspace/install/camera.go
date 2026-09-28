package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// Camera installs the exact five-package ARM64 runtime set in one apt-get
// transaction. Package-manager installation into an offline alternate root is
// intentionally unsupported.
func (installer *Installer) Camera(ctx context.Context, options Options) (Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return Result{}, err
	}
	if options.Root != string(filepath.Separator) {
		return Result{}, errors.New("camera package installation is supported only for target root /")
	}
	bundle, err := installer.verifyCameraInput(ctx, options)
	if err != nil {
		return Result{}, err
	}
	if bundle.compatibility != nil {
		if options.Compatibility != nil && *options.Compatibility != bundle.compatibility.Manifest {
			return Result{}, errors.New("native and catalogue compatibility authorities differ")
		}
		options.Compatibility = &bundle.compatibility.Manifest
		options.CompatibilityRelease = bundle.compatibility.Release
	}
	compatibilityRecord, err := assessCompatibility(options, CameraComponent)
	if err != nil {
		return Result{Component: CameraComponent, Root: options.Root, DryRun: options.DryRun, Compatibility: compatibilityRecord}, err
	}
	args := []string{"install", "--yes", "--no-install-recommends", "--"}
	for _, immutable := range bundle.runtimeFiles {
		path, ok := bundle.paths[immutable.name]
		if !ok {
			return Result{}, fmt.Errorf("verified camera bundle is missing %s", immutable.name)
		}
		args = append(args, path)
	}
	result := Result{
		Compatibility: compatibilityRecord,
		Component:     CameraComponent,
		Root:          options.Root,
		DryRun:        options.DryRun,
		Command:       &Command{Name: "apt-get", Args: append([]string(nil), args...)},
	}
	if err := planCompatibilityRecords(options, CameraComponent, &result); err != nil {
		return result, err
	}
	if options.DryRun {
		return result, nil
	}
	if err := revalidateCompatibility(options, CameraComponent, compatibilityRecord); err != nil {
		return result, err
	}
	if err := installer.requireRoot(false); err != nil {
		return Result{}, err
	}
	stage, err := createPrivateInstallStaging("lexr-camera-install-*")
	if err != nil {
		return Result{}, fmt.Errorf("create private camera staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	executionArgs := []string{"install", "--yes", "--no-install-recommends", "--"}
	for _, immutable := range bundle.runtimeFiles {
		staged := filepath.Join(stage, immutable.name)
		if err := atomicCopyVerified(bundle.paths[immutable.name], staged, 0o600, immutable.sha256, immutable.size); err != nil {
			return Result{}, fmt.Errorf("stage verified camera package %s: %w", immutable.name, err)
		}
		executionArgs = append(executionArgs, staged)
	}
	if err := revalidateCompatibility(options, CameraComponent, compatibilityRecord); err != nil {
		return result, err
	}
	if err := installer.runner.Run(ctx, platform.Command{Name: "apt-get", Args: executionArgs}); err != nil {
		return Result{}, fmt.Errorf("install exact IMX681 libcamera package set: %w", err)
	}
	result.FilesInstalled = true
	if err := persistCompatibilityRecords(options, CameraComponent, &result); err != nil {
		return result, err
	}
	return result, nil
}
