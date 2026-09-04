package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// packageRoleOrder gives package-manager input a dependency-friendly stable order.
var packageRoleOrder = map[kernel.PackageRole]int{
	kernel.RoleModules:       0,
	kernel.RoleImage:         1,
	kernel.RoleBootSupport:   2,
	kernel.RoleCommonHeaders: 3,
	kernel.RoleHeaders:       4,
}

// inspectBundle validates all package bytes and Debian control metadata without
// granting package-manager privileges.
func (manager *Manager) inspectBundle(ctx context.Context, bundle kernel.Bundle) ([]Package, error) {
	if bundle.SchemaVersion != kernel.BundleSchemaVersion {
		return nil, fmt.Errorf("kernel bundle schema is %d, expected %d", bundle.SchemaVersion, kernel.BundleSchemaVersion)
	}
	if err := validateABI("target", bundle.ABI); err != nil {
		return nil, err
	}
	if bundle.Architecture != "arm64" {
		return nil, fmt.Errorf("kernel bundle architecture is %q, expected arm64", bundle.Architecture)
	}
	if err := validateText("kernel bundle version", bundle.Version, maximumVersionBytes, false); err != nil {
		return nil, err
	}
	if err := validateDeviceTrees(bundle); err != nil {
		return nil, err
	}
	wantCounts := map[int]bool{2: true, 4: true}
	if bundle.EffectiveDTBDelivery == kernel.DTBDeliveryExternalRequired {
		wantCounts = map[int]bool{3: true, 5: true}
	}
	if !wantCounts[len(bundle.Packages)] {
		return nil, fmt.Errorf("kernel transaction package count %d does not match %s DTB delivery", len(bundle.Packages), bundle.EffectiveDTBDelivery)
	}

	expected := expectedPackageNames(bundle.ABI)
	seen := make(map[kernel.PackageRole]bool, len(bundle.Packages))
	packages := make([]Package, 0, len(bundle.Packages))
	for _, manifestPackage := range bundle.Packages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		role, abi, version, err := kernel.ParsePackageName(manifestPackage.Name)
		if err != nil {
			return nil, err
		}
		if manifestPackage.Role != "" && manifestPackage.Role != role {
			return nil, fmt.Errorf("package %s role is %q, filename identifies %q", manifestPackage.Name, manifestPackage.Role, role)
		}
		if seen[role] {
			return nil, fmt.Errorf("kernel transaction contains duplicate %s packages", role)
		}
		seen[role] = true
		packageArchitecture := "arm64"
		if role == kernel.RoleCommonHeaders || role == kernel.RoleBootSupport {
			packageArchitecture = "all"
		}
		expectedFilename := expected[role] + "_" + bundle.Version + "_" + packageArchitecture + ".deb"
		if manifestPackage.Name != expectedFilename {
			return nil, fmt.Errorf("unexpected %s package %q; expected %q", role, manifestPackage.Name, expectedFilename)
		}
		if role != kernel.RoleCommonHeaders && role != kernel.RoleBootSupport && abi != bundle.ABI {
			return nil, fmt.Errorf("package %s ABI is %q, expected %q", manifestPackage.Name, abi, bundle.ABI)
		}
		if version != bundle.Version {
			return nil, fmt.Errorf("package %s filename version is %q, expected %q", manifestPackage.Name, version, bundle.Version)
		}
		if err := validateText("package name", manifestPackage.Name, maximumPackageNameBytes, false); err != nil {
			return nil, err
		}
		if err := validateDigest(manifestPackage.Name, manifestPackage.SHA256); err != nil {
			return nil, err
		}
		path, info, err := canonicalPackagePath(manifestPackage.Path, manifestPackage.Name)
		if err != nil {
			return nil, err
		}
		evidence, err := hashRegular(ctx, path, info, maximumPackageBytes)
		if err != nil {
			return nil, fmt.Errorf("hash package %s: %w", manifestPackage.Name, err)
		}
		if manifestPackage.Size <= 0 || manifestPackage.Size != evidence.Size {
			return nil, fmt.Errorf("package %s size is %d, manifest records %d", manifestPackage.Name, evidence.Size, manifestPackage.Size)
		}
		if evidence.SHA256 != manifestPackage.SHA256 {
			return nil, fmt.Errorf("package %s SHA-256 is %s, manifest records %s", manifestPackage.Name, evidence.SHA256, manifestPackage.SHA256)
		}

		metadata, err := manager.inspectPackageMetadata(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("inspect package %s: %w", manifestPackage.Name, err)
		}
		if metadata.DebianPackage != expected[role] {
			return nil, fmt.Errorf("package %s control name is %q, expected %q", manifestPackage.Name, metadata.DebianPackage, expected[role])
		}
		if metadata.Version != bundle.Version {
			return nil, fmt.Errorf("package %s control version is %q, expected %q", manifestPackage.Name, metadata.Version, bundle.Version)
		}
		wantArchitecture := packageArchitecture
		if metadata.Architecture != wantArchitecture {
			return nil, fmt.Errorf("package %s architecture is %q, expected %q", manifestPackage.Name, metadata.Architecture, wantArchitecture)
		}
		metadata.Role = role
		metadata.Name = manifestPackage.Name
		metadata.Path = path
		metadata.SHA256 = evidence.SHA256
		metadata.Size = evidence.Size
		metadata.PublisherVerified = manifestPackage.Verified
		packages = append(packages, metadata)
	}

	if !seen[kernel.RoleImage] || !seen[kernel.RoleModules] {
		return nil, errors.New("kernel transaction requires one image and one modules package")
	}
	if seen[kernel.RoleBootSupport] != (bundle.EffectiveDTBDelivery == kernel.DTBDeliveryExternalRequired) {
		return nil, errors.New("kernel transaction boot-support package does not match effective DTB delivery")
	}
	if seen[kernel.RoleHeaders] != seen[kernel.RoleCommonHeaders] {
		return nil, errors.New("kernel headers and common headers must be supplied together")
	}
	allowed := make(map[string]bool, len(packages))
	for _, item := range packages {
		allowed[item.DebianPackage] = true
	}
	dependencies := make(map[kernel.PackageRole][]dependency, len(packages))
	for _, item := range packages {
		parsed, err := validateLocalDependencies(item, allowed, bundle.Version)
		if err != nil {
			return nil, err
		}
		dependencies[item.Role] = parsed
	}
	if !hasDependency(dependencies[kernel.RoleImage], expected[kernel.RoleModules]) {
		return nil, fmt.Errorf("%s must depend on %s", expected[kernel.RoleImage], expected[kernel.RoleModules])
	}
	if seen[kernel.RoleHeaders] && !hasDependency(dependencies[kernel.RoleHeaders], expected[kernel.RoleCommonHeaders]) {
		return nil, fmt.Errorf("%s must depend on %s", expected[kernel.RoleHeaders], expected[kernel.RoleCommonHeaders])
	}
	if seen[kernel.RoleBootSupport] {
		bootSupport := packageForRole(packages, kernel.RoleBootSupport)
		recommendations, err := validateLocalRelationships(bootSupport.Name, "Recommends", bootSupport.Recommends, allowed, bundle.Version)
		if err != nil {
			return nil, err
		}
		if !hasExactDependency(recommendations, expected[kernel.RoleImage], bundle.Version) ||
			!hasExactDependency(recommendations, expected[kernel.RoleModules], bundle.Version) {
			return nil, fmt.Errorf("%s must recommend the exact image and modules pair", expected[kernel.RoleBootSupport])
		}
	}
	sort.Slice(packages, func(left, right int) bool {
		return packageRoleOrder[packages[left].Role] < packageRoleOrder[packages[right].Role]
	})
	return packages, nil
}

// inspectPackageMetadata reads the five bounded control fields required by policy.
func (manager *Manager) inspectPackageMetadata(ctx context.Context, path string) (Package, error) {
	packageName, err := manager.captureDebianField(ctx, path, "Package", maximumPackageNameBytes)
	if err != nil {
		return Package{}, err
	}
	if !packageNameExpression.MatchString(packageName) {
		return Package{}, fmt.Errorf("invalid Debian Package field %q", packageName)
	}
	version, err := manager.captureDebianField(ctx, path, "Version", maximumVersionBytes)
	if err != nil {
		return Package{}, err
	}
	architecture, err := manager.captureDebianField(ctx, path, "Architecture", 32)
	if err != nil {
		return Package{}, err
	}
	depends, err := manager.captureDebianField(ctx, path, "Depends", maximumDependencyBytes)
	if err != nil {
		return Package{}, err
	}
	recommends, err := manager.captureDebianField(ctx, path, "Recommends", maximumDependencyBytes)
	if err != nil {
		return Package{}, err
	}
	return Package{
		DebianPackage: packageName,
		Version:       version,
		Architecture:  architecture,
		Depends:       depends,
		Recommends:    recommends,
	}, nil
}

// packageForRole returns the package assigned to role, or a zero value if absent.
func packageForRole(packages []Package, role kernel.PackageRole) Package {
	for _, item := range packages {
		if item.Role == role {
			return item
		}
	}
	return Package{}
}

// captureDebianField invokes dpkg-deb directly for one allow-listed field.
func (manager *Manager) captureDebianField(ctx context.Context, path, field string, maximum int) (string, error) {
	command := Command{
		Operation: OperationInspectPackage,
		Name:      dpkgDebCommand,
		Args:      []string{"--field", path, field},
	}
	if err := validateCommand(command); err != nil {
		return "", err
	}
	output, err := manager.captureCommand(ctx, command, maximum+2)
	if err != nil {
		return "", fmt.Errorf("read Debian field %s: %w", field, err)
	}
	if len(output) > maximum+2 {
		return "", fmt.Errorf("Debian field %s exceeds %d bytes", field, maximum)
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(output), "\n"), "\r")
	if err := validateText("Debian field "+field, value, maximum, field == "Depends" || field == "Recommends"); err != nil {
		return "", err
	}
	return value, nil
}

// packageSourceInfo recovers an inspected package's current path identity.
func packageSourceInfo(item Package) (os.FileInfo, error) {
	path, info, err := canonicalPackagePath(item.Path, item.Name)
	if err != nil {
		return nil, err
	}
	if path != item.Path || info.Size() != item.Size {
		return nil, fmt.Errorf("package %s changed after preflight", item.Name)
	}
	return info, nil
}
