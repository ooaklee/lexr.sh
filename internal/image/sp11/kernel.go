package sp11

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// ValidateBundlePaths proves that package inputs and device-tree paths are safe,
// regular, digest-matched files before any of them enters a container command.
func ValidateBundlePaths(bundle kernel.Bundle) error {
	if _, err := kernel.NewBundle(kernel.BundleOptions{
		Release: bundle.Release, Repository: bundle.Repository,
		RequestedBootImageMode: bundle.RequestedBootImageMode, EffectiveDTBDelivery: bundle.EffectiveDTBDelivery,
		EmbeddedDTBCount: bundle.EmbeddedDTBCount, DTBSelectionProvenance: bundle.DTBSelectionProvenance,
		Packages: bundle.Packages, DeviceTrees: bundle.DeviceTrees,
	}); err != nil {
		return fmt.Errorf("kernel bundle delivery contract is invalid: %w", err)
	}
	if !SafeKernelABI(bundle.ABI) {
		return fmt.Errorf("kernel bundle ABI %q is not a safe path component", bundle.ABI)
	}
	for _, pkg := range bundle.Packages {
		if pkg.Name == "" || filepath.Base(pkg.Name) != pkg.Name {
			return fmt.Errorf("kernel package name %q is not a safe filename", pkg.Name)
		}
		if pkg.Path == "" {
			continue
		}
		info, err := os.Stat(pkg.Path)
		if err != nil {
			return fmt.Errorf("inspect kernel package %s: %w", pkg.Name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("kernel package %s is not a regular file", pkg.Name)
		}
		digest, err := artifact.HashFile(pkg.Path)
		if err != nil {
			return fmt.Errorf("verify kernel package %s: %w", pkg.Name, err)
		}
		if !strings.EqualFold(digest, pkg.SHA256) {
			return fmt.Errorf("kernel package %s digest mismatch", pkg.Name)
		}
	}
	for _, role := range []kernel.PackageRole{kernel.RoleImage, kernel.RoleModules} {
		pkg, ok := bundle.Package(role)
		if !ok {
			return fmt.Errorf("kernel bundle has no %s package", role)
		}
		if pkg.Path == "" {
			return fmt.Errorf("kernel bundle %s package has no local path", role)
		}
	}
	requiredTrees := map[string]bool{
		"qcom/x1e80100-microsoft-denali-oled.dtb": false,
		"qcom/x1p64100-microsoft-denali.dtb":      false,
	}
	for _, tree := range bundle.DeviceTrees {
		if strings.Contains(tree.Path, "\\") || filepath.IsAbs(tree.Path) {
			return fmt.Errorf("device tree path %q is not a safe relative path", tree.Path)
		}
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(tree.Path)))
		if clean != tree.Path || clean == "." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("device tree path %q is not a safe relative path", tree.Path)
		}
		relative, ok := tree.FirmwareRelativePath(bundle.ABI)
		if !ok {
			return fmt.Errorf("device tree path %q is not beneath the bundle firmware directory", tree.Path)
		}
		if _, required := requiredTrees[relative]; required {
			requiredTrees[relative] = true
		}
	}
	for tree, present := range requiredTrees {
		if !present {
			return fmt.Errorf("kernel bundle has no required device tree %s", tree)
		}
	}
	return nil
}

// SafeKernelABI reports whether an ABI can be embedded in filesystem paths and
// shell arguments without separators or control characters.
func SafeKernelABI(abi string) bool {
	if abi == "" || len(abi) > 255 {
		return false
	}
	for _, character := range abi {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune(".+~_-", character) {
			continue
		}
		return false
	}
	return true
}
