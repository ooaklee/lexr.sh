package archlinux

import (
	"context"
	"fmt"
	"github.com/ooaklee/lexr.sh/internal/artifact"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"os"
	"path/filepath"
)

// recordFile binds a generated regular file to its portable on-media path.
func recordFile(file, mediaPath string) (imagecontract.ArtifactRecord, error) {
	info, err := os.Lstat(file)
	if err != nil {
		return imagecontract.ArtifactRecord{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return imagecontract.ArtifactRecord{}, fmt.Errorf("generated artefact %s is not a non-empty regular file", mediaPath)
	}
	digest, err := artifact.HashFile(file)
	if err != nil {
		return imagecontract.ArtifactRecord{}, err
	}
	return imagecontract.ArtifactRecord{Path: mediaPath, SHA256: digest, Size: info.Size()}, nil
}

// stageKernelBundle copies only the runtime package set into the private
// workspace, checking each copy against the already verified bundle identity.
func stageKernelBundle(ctx context.Context, bundle kernel.Bundle, workspace string) error {
	if err := os.MkdirAll(filepath.Join(workspace, "kernel"), 0o755); err != nil {
		return err
	}
	for _, pkg := range bundle.Packages {
		if pkg.Role != kernel.RoleImage && pkg.Role != kernel.RoleModules && pkg.Role != kernel.RoleBootSupport {
			continue
		}
		digest, size, err := imagecontract.SnapshotFile(ctx, pkg.Path, filepath.Join(workspace, "kernel", pkg.Name), maximumImageBytes, nil)
		if err != nil {
			return err
		}
		if digest != pkg.SHA256 || size != pkg.Size {
			return fmt.Errorf("kernel package %s changed during staging", pkg.Name)
		}
	}
	return nil
}

// portableBundle strips local paths from the self-contained media manifest.
func portableBundle(bundle kernel.Bundle) kernel.Bundle {
	result := bundle
	result.Packages = append([]kernel.Package(nil), bundle.Packages...)
	result.DeviceTrees = kernel.CloneDeviceTrees(bundle.DeviceTrees)
	for index := range result.Packages {
		pkg := &result.Packages[index]
		pkg.Path = ""
		if pkg.Role == kernel.RoleImage || pkg.Role == kernel.RoleModules || pkg.Role == kernel.RoleBootSupport {
			pkg.Path = "sp11/kernel/" + pkg.Name
		}
	}
	return result
}
