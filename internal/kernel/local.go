package kernel

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const (
	// localChecksumManifest is the optional authoritative digest file accepted
	// beside local packages.
	localChecksumManifest = "SHA256SUMS"
	// localBundleManifest records the package set intentionally emitted by Lexr.
	localBundleManifest = "lexr-kernel-bundle.json"
	// maximumLocalBundleManifestBytes bounds local package-set authority.
	maximumLocalBundleManifestBytes int64 = 64 << 10
	// localReleasePrefix distinguishes locally discovered bundles from release
	// tags in serialised output.
	localReleasePrefix = "local:"
	// surfaceABISuffix limits discovery to the Surface-specific kernel flavour.
	surfaceABISuffix = "-qcom-x1e"
)

// localCandidate is metadata derived from one safe, regular Debian package
// found directly inside the selected directory.
type localCandidate struct {
	// name is the package basename used in manifests and diagnostics.
	name string
	// path is the absolute local package path.
	path string
	// role distinguishes runtime, optional headers, and boot-support packages.
	role PackageRole
	// abi is the exact Surface kernel ABI encoded in the filename.
	abi string
	// version is the Debian package version encoded in the filename.
	version string
}

// LocalPackageSet selects one closed group of version-bound local packages.
type LocalPackageSet string

const (
	// LocalPackageSetRuntime selects only the mandatory image and modules pair.
	LocalPackageSetRuntime LocalPackageSet = "runtime"
	// LocalPackageSetAll adds the coherent development-header pair when present.
	LocalPackageSetAll LocalPackageSet = "all"
)

// LocalBundleOptions selects the closed package set local discovery may add to
// the mandatory runtime pair.
type LocalBundleOptions struct {
	// PackageSet selects all exact available roles or the runtime pair only.
	PackageSet LocalPackageSet
}

// DiscoverLocalBundle finds one version-bound Surface Pro 11 linux-image and
// linux-modules package pair in directory. If SHA256SUMS is present, both
// packages must be covered by it and match their declared digests. The
// schema-2 bundle manifest is always required; without SHA256SUMS its packages
// are still hashed, but are marked as unverified.
func DiscoverLocalBundle(directory string) (Bundle, error) {
	return DiscoverLocalBundleWithOptions(directory, LocalBundleOptions{PackageSet: LocalPackageSetRuntime})
}

// DiscoverLocalBundleWithOptions finds one version-bound Surface Pro 11 runtime
// pair and optionally includes its exact coherent development-header pair.
func DiscoverLocalBundleWithOptions(directory string, options LocalBundleOptions) (Bundle, error) {
	if options.PackageSet != LocalPackageSetRuntime && options.PackageSet != LocalPackageSetAll {
		return Bundle{}, fmt.Errorf("discover local kernel bundle: package set must be %q or %q, got %q", LocalPackageSetAll, LocalPackageSetRuntime, options.PackageSet)
	}
	if strings.TrimSpace(directory) == "" {
		return Bundle{}, errors.New("discover local kernel bundle: directory is required")
	}

	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return Bundle{}, fmt.Errorf("discover local kernel bundle: resolve directory: %w", err)
	}
	info, err := os.Stat(absoluteDirectory)
	if err != nil {
		return Bundle{}, fmt.Errorf("discover local kernel bundle: inspect directory %q: %w", absoluteDirectory, err)
	}
	if !info.IsDir() {
		return Bundle{}, fmt.Errorf("discover local kernel bundle: %q is not a directory", absoluteDirectory)
	}

	entries, err := os.ReadDir(absoluteDirectory)
	if err != nil {
		return Bundle{}, fmt.Errorf("discover local kernel bundle: read directory %q: %w", absoluteDirectory, err)
	}
	candidates := map[PackageRole][]localCandidate{
		RoleImage:         nil,
		RoleModules:       nil,
		RoleHeaders:       nil,
		RoleCommonHeaders: nil,
		RoleBootSupport:   nil,
	}
	for _, entry := range entries {
		candidate, applicable, candidateErr := inspectLocalCandidate(absoluteDirectory, entry, options.PackageSet)
		if candidateErr != nil {
			return Bundle{}, fmt.Errorf("discover local kernel bundle: %w", candidateErr)
		}
		if applicable {
			candidates[candidate.role] = append(candidates[candidate.role], candidate)
		}
	}

	image, err := selectLocalCandidate(RoleImage, candidates[RoleImage])
	if err != nil {
		return Bundle{}, fmt.Errorf("discover local kernel bundle: %w", err)
	}
	modules, err := selectLocalCandidate(RoleModules, candidates[RoleModules])
	if err != nil {
		return Bundle{}, fmt.Errorf("discover local kernel bundle: %w", err)
	}
	if image.abi != modules.abi {
		return Bundle{}, fmt.Errorf(
			"discover local kernel bundle: image and modules ABI mismatch: %s has %q, %s has %q",
			image.name,
			image.abi,
			modules.name,
			modules.abi,
		)
	}
	if image.version != modules.version {
		return Bundle{}, fmt.Errorf(
			"discover local kernel bundle: image and modules package version mismatch: %s has %q, %s has %q",
			image.name,
			image.version,
			modules.name,
			modules.version,
		)
	}

	checksums, checksumManifestPresent, err := loadLocalChecksums(absoluteDirectory)
	if err != nil {
		return Bundle{}, fmt.Errorf("discover local kernel bundle: %w", err)
	}
	declaredBundle, bundleManifestPresent, err := loadLocalBundleManifest(absoluteDirectory)
	if err != nil {
		return Bundle{}, fmt.Errorf("discover local kernel bundle: %w", err)
	}
	declaredPackageSet := LocalPackageSet("")
	if !bundleManifestPresent {
		return Bundle{}, errors.New("discover local kernel bundle: schema-2 lexr-kernel-bundle.json is required to prove requested and effective DTB delivery")
	}
	declaredPackageSet, err = validateLocalBundleManifest(declaredBundle, image, modules, checksums, checksumManifestPresent)
	if err != nil {
		return Bundle{}, fmt.Errorf("discover local kernel bundle: %w", err)
	}

	selected := []localCandidate{image, modules}
	if declaredBundle.EffectiveDTBDelivery == DTBDeliveryExternalRequired {
		declaredSupport, ok := declaredBundle.Package(RoleBootSupport)
		if !ok {
			return Bundle{}, errors.New("discover local kernel bundle: external-required manifest has no boot-support package")
		}
		support, present := localCandidateByName(candidates[RoleBootSupport], declaredSupport.Name)
		if !present {
			return Bundle{}, fmt.Errorf("discover local kernel bundle: missing declared boot-support package %s", declaredSupport.Name)
		}
		selected = append(selected, support)
	}
	if options.PackageSet == LocalPackageSetAll && declaredPackageSet == LocalPackageSetAll {
		headers, err := selectLocalHeaderPair(candidates, image, checksums, checksumManifestPresent, bundleManifestPresent)
		if err != nil {
			return Bundle{}, fmt.Errorf("discover local kernel bundle: %w", err)
		}
		selected = append(selected, headers...)
	}
	packages := make([]Package, 0, len(selected))
	for _, candidate := range selected {
		digest, size, hashErr := hashLocalPackage(candidate.path)
		if hashErr != nil {
			return Bundle{}, fmt.Errorf("discover local kernel bundle: %w", hashErr)
		}
		if bundleManifestPresent {
			declared, declaredRole := declaredBundle.Package(candidate.role)
			if !declaredRole || declared.Name != candidate.name {
				return Bundle{}, fmt.Errorf("discover local kernel bundle: %s does not declare selected package %s", localBundleManifest, candidate.name)
			}
			if declared.SHA256 != digest || declared.Size != size {
				return Bundle{}, fmt.Errorf("discover local kernel bundle: package %s bytes disagree with %s", candidate.name, localBundleManifest)
			}
		}
		if checksumManifestPresent {
			expected, covered := checksums[candidate.name]
			if !covered {
				return Bundle{}, fmt.Errorf("discover local kernel bundle: %s does not cover %s", localChecksumManifest, candidate.name)
			}
			if !strings.EqualFold(expected, digest) {
				return Bundle{}, fmt.Errorf(
					"discover local kernel bundle: SHA-256 mismatch for %s: expected %s, got %s",
					candidate.name,
					expected,
					digest,
				)
			}
		}
		packages = append(packages, Package{
			Role:     candidate.role,
			Name:     candidate.name,
			Path:     candidate.path,
			SHA256:   digest,
			Size:     size,
			Verified: checksumManifestPresent,
		})
	}

	bundle, err := NewBundle(BundleOptions{
		Release: localReleasePrefix + image.abi, Repository: declaredBundle.Repository,
		RequestedBootImageMode: declaredBundle.RequestedBootImageMode,
		EffectiveDTBDelivery:   declaredBundle.EffectiveDTBDelivery,
		EmbeddedDTBCount:       declaredBundle.EmbeddedDTBCount,
		DTBSelectionProvenance: declaredBundle.DTBSelectionProvenance,
		Packages:               packages, DeviceTrees: declaredBundle.DeviceTrees,
	})
	if err != nil {
		return Bundle{}, fmt.Errorf("discover local kernel bundle: validate package pair: %w", err)
	}
	if bundle.ABI != image.abi {
		return Bundle{}, fmt.Errorf("discover local kernel bundle: derived ABI %q changed to %q during validation", image.abi, bundle.ABI)
	}

	return bundle, nil
}

// inspectLocalCandidate accepts only regular, top-level Surface runtime or
// header packages and ignores unrelated directory entries.
func inspectLocalCandidate(directory string, entry os.DirEntry, packageSet LocalPackageSet) (localCandidate, bool, error) {
	name := entry.Name()
	if name == localChecksumManifest || !strings.HasSuffix(name, ".deb") {
		return localCandidate{}, false, nil
	}

	role, abi, version, err := ParsePackageName(name)
	if err != nil {
		return localCandidate{}, false, nil
	}
	switch role {
	case RoleImage, RoleModules:
		if !isSurfaceABI(abi) {
			return localCandidate{}, false, nil
		}
	case RoleHeaders:
		if packageSet != LocalPackageSetAll || !isSurfaceABI(abi) {
			return localCandidate{}, false, nil
		}
	case RoleCommonHeaders:
		if packageSet != LocalPackageSetAll {
			return localCandidate{}, false, nil
		}
	case RoleBootSupport:
		// Boot support is part of the runtime delivery contract, independent of
		// whether development headers were requested.
	default:
		return localCandidate{}, false, nil
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return localCandidate{}, false, fmt.Errorf("kernel package %s must be a regular file, not a symbolic link", name)
	}
	info, err := entry.Info()
	if err != nil {
		return localCandidate{}, false, fmt.Errorf("inspect kernel package %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return localCandidate{}, false, fmt.Errorf("kernel package %s is not a regular file", name)
	}

	return localCandidate{
		name:    name,
		path:    filepath.Join(directory, name),
		role:    role,
		abi:     abi,
		version: version,
	}, true, nil
}

// selectLocalHeaderPair returns only the two exact header filenames bound to
// runtime. Other versions in the directory are unrelated and remain ignored.
// An authoritative checksum declaration binds discovery to the complete pair,
// so removing both declared files cannot silently downgrade an all-package
// transaction to the runtime pair.
func selectLocalHeaderPair(candidates map[PackageRole][]localCandidate, runtime localCandidate, checksums map[string]string, checksumManifestPresent, bundleManifestRequiresPair bool) ([]localCandidate, error) {
	base := strings.TrimSuffix(runtime.abi, surfaceABISuffix)
	expectedHeaders := "linux-headers-" + runtime.abi + "_" + runtime.version + "_arm64.deb"
	expectedCommon := "linux-qcom-x1e-headers-" + base + "_" + runtime.version + "_all.deb"

	headers, hasHeaders := localCandidateByName(candidates[RoleHeaders], expectedHeaders)
	common, hasCommon := localCandidateByName(candidates[RoleCommonHeaders], expectedCommon)
	_, headersDeclared := checksums[expectedHeaders]
	_, commonDeclared := checksums[expectedCommon]
	manifestRequiresPair := bundleManifestRequiresPair || checksumManifestPresent && (headersDeclared || commonDeclared)
	if !hasHeaders && !hasCommon && !manifestRequiresPair {
		return nil, nil
	}
	if !hasHeaders || !hasCommon {
		missing := make([]string, 0, 2)
		if !hasHeaders {
			missing = append(missing, expectedHeaders)
		}
		if !hasCommon {
			missing = append(missing, expectedCommon)
		}
		return nil, fmt.Errorf("matching kernel headers must be supplied as a complete pair; missing %s", strings.Join(missing, ", "))
	}
	return []localCandidate{headers, common}, nil
}

// loadLocalBundleManifest safely decodes the Lexr delivery declaration without
// trusting any recorded package path.
func loadLocalBundleManifest(directory string) (Bundle, bool, error) {
	path := filepath.Join(directory, localBundleManifest)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Bundle{}, false, nil
	}
	if err != nil {
		return Bundle{}, false, fmt.Errorf("inspect %s: %w", localBundleManifest, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Bundle{}, false, fmt.Errorf("%s must be a regular file, not a symbolic link", localBundleManifest)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumLocalBundleManifestBytes {
		return Bundle{}, false, fmt.Errorf("%s must be a non-empty regular file no larger than %d bytes", localBundleManifest, maximumLocalBundleManifestBytes)
	}

	file, err := os.Open(path)
	if err != nil {
		return Bundle{}, false, fmt.Errorf("open %s: %w", localBundleManifest, err)
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = file.Close()
		return Bundle{}, false, fmt.Errorf("%s changed before it was read", localBundleManifest)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maximumLocalBundleManifestBytes+1))
	closeErr := file.Close()
	current, currentErr := os.Lstat(path)
	if err := errors.Join(readErr, closeErr, currentErr); err != nil {
		return Bundle{}, false, fmt.Errorf("read %s: %w", localBundleManifest, err)
	}
	if int64(len(data)) != info.Size() || int64(len(data)) > maximumLocalBundleManifestBytes ||
		current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) || current.Size() != info.Size() {
		return Bundle{}, false, fmt.Errorf("%s changed while it was read", localBundleManifest)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var bundle Bundle
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, false, fmt.Errorf("decode %s: %w", localBundleManifest, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return Bundle{}, false, fmt.Errorf("decode %s trailing data: %w", localBundleManifest, err)
	}
	return bundle, true, nil
}

// validateLocalBundleManifest proves that a decoded declaration is a canonical
// embedded or external-required package and device-tree contract.
func validateLocalBundleManifest(manifest Bundle, image, modules localCandidate, checksums map[string]string, checksumManifestPresent bool) (LocalPackageSet, error) {
	if manifest.SchemaVersion != BundleSchemaVersion {
		return "", fmt.Errorf("%s schema is %d, expected %d", localBundleManifest, manifest.SchemaVersion, BundleSchemaVersion)
	}
	normalized, err := NewBundle(BundleOptions{
		Release: manifest.Release, Repository: manifest.Repository,
		RequestedBootImageMode: manifest.RequestedBootImageMode,
		EffectiveDTBDelivery:   manifest.EffectiveDTBDelivery,
		EmbeddedDTBCount:       manifest.EmbeddedDTBCount,
		DTBSelectionProvenance: manifest.DTBSelectionProvenance,
		Packages:               manifest.Packages, DeviceTrees: manifest.DeviceTrees,
	})
	if err != nil {
		return "", fmt.Errorf("validate %s packages: %w", localBundleManifest, err)
	}
	if manifest.ABI != image.abi || normalized.ABI != image.abi || manifest.Version != image.version || normalized.Version != image.version || manifest.Architecture != "arm64" {
		return "", fmt.Errorf("%s identity does not match runtime ABI %s version %s", localBundleManifest, image.abi, image.version)
	}
	if !reflect.DeepEqual(manifest.DeviceTrees, normalized.DeviceTrees) {
		return "", fmt.Errorf("%s device-tree declaration is incomplete or not canonical", localBundleManifest)
	}

	base := strings.TrimSuffix(image.abi, surfaceABISuffix)
	expected := map[PackageRole]string{
		RoleImage:         image.name,
		RoleModules:       modules.name,
		RoleHeaders:       "linux-headers-" + image.abi + "_" + image.version + "_arm64.deb",
		RoleCommonHeaders: "linux-qcom-x1e-headers-" + base + "_" + image.version + "_all.deb",
	}
	roles := map[PackageRole]bool{RoleImage: true, RoleModules: true}
	if manifest.EffectiveDTBDelivery == DTBDeliveryExternalRequired {
		roles[RoleBootSupport] = true
	}
	packageSet := LocalPackageSetRuntime
	if _, hasHeaders := manifest.Package(RoleHeaders); hasHeaders {
		roles[RoleHeaders] = true
		roles[RoleCommonHeaders] = true
		packageSet = LocalPackageSetAll
	}
	for _, item := range manifest.Packages {
		if !roles[item.Role] {
			return "", fmt.Errorf("%s contains unexpected %s package %q", localBundleManifest, item.Role, item.Name)
		}
		if item.Role != RoleBootSupport && item.Name != expected[item.Role] {
			return "", fmt.Errorf("%s contains unexpected %s package %q", localBundleManifest, item.Role, item.Name)
		}
		if len(item.SHA256) != sha256.Size*2 || strings.ToLower(item.SHA256) != item.SHA256 {
			return "", fmt.Errorf("%s package %s has a non-canonical SHA-256", localBundleManifest, item.Name)
		}
		if _, err := hex.DecodeString(item.SHA256); err != nil {
			return "", fmt.Errorf("%s package %s has an invalid SHA-256: %w", localBundleManifest, item.Name, err)
		}
		if item.Size <= 0 {
			return "", fmt.Errorf("%s package %s has an invalid size", localBundleManifest, item.Name)
		}
		if checksumManifestPresent {
			expectedDigest, covered := checksums[item.Name]
			if !covered || expectedDigest != item.SHA256 {
				return "", fmt.Errorf("%s package %s disagrees with %s", localBundleManifest, item.Name, localChecksumManifest)
			}
		}
	}
	return packageSet, nil
}

// localCandidateByName locates one exact package basename in an already bounded
// role collection.
func localCandidateByName(candidates []localCandidate, name string) (localCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.name == name {
			return candidate, true
		}
	}
	return localCandidate{}, false
}

// isSurfaceABI reports whether abi has a non-empty version followed by the
// Surface-specific flavour suffix.
func isSurfaceABI(abi string) bool {
	prefix := strings.TrimSuffix(abi, surfaceABISuffix)
	return prefix != abi && prefix != ""
}

// selectLocalCandidate requires exactly one package for a runtime role, making
// ambiguous directories fail closed.
func selectLocalCandidate(role PackageRole, candidates []localCandidate) (localCandidate, error) {
	label := string(role)
	if len(candidates) == 0 {
		return localCandidate{}, fmt.Errorf("expected exactly one Surface Pro 11 linux-%s package, found none", label)
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}

	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.name)
	}
	sort.Strings(names)
	return localCandidate{}, fmt.Errorf("ambiguous Surface Pro 11 linux-%s packages: %s", label, strings.Join(names, ", "))
}

// loadLocalChecksums loads the optional regular-file manifest without following
// a symlink. The boolean reports whether a manifest was present.
func loadLocalChecksums(directory string) (map[string]string, bool, error) {
	manifestPath := filepath.Join(directory, localChecksumManifest)
	info, err := os.Lstat(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect %s: %w", localChecksumManifest, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, fmt.Errorf("%s must be a regular file, not a symbolic link", localChecksumManifest)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a regular file", localChecksumManifest)
	}

	checksums, err := parseLocalChecksums(manifestPath)
	if err != nil {
		return nil, false, err
	}
	return checksums, true, nil
}

// parseLocalChecksums decodes a non-empty SHA256SUMS file, rejecting malformed,
// duplicate, or path-bearing filenames.
func parseLocalChecksums(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", localChecksumManifest, err)
	}
	defer file.Close()

	checksums := make(map[string]string)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: expected '<sha256>  <filename>'", localChecksumManifest, lineNumber)
		}

		digest := strings.ToLower(fields[0])
		if len(digest) != sha256.Size*2 {
			return nil, fmt.Errorf("%s:%d: SHA-256 must contain 64 hexadecimal characters", localChecksumManifest, lineNumber)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return nil, fmt.Errorf("%s:%d: invalid SHA-256: %w", localChecksumManifest, lineNumber, err)
		}

		name := strings.TrimPrefix(fields[1], "*")
		if !safeLocalChecksumName(name) {
			return nil, fmt.Errorf("%s:%d: unsafe filename %q", localChecksumManifest, lineNumber, name)
		}
		if _, duplicate := checksums[name]; duplicate {
			return nil, fmt.Errorf("%s:%d: duplicate entry for %q", localChecksumManifest, lineNumber, name)
		}
		checksums[name] = digest
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", localChecksumManifest, err)
	}
	if len(checksums) == 0 {
		return nil, fmt.Errorf("%s is empty", localChecksumManifest)
	}

	return checksums, nil
}

// safeLocalChecksumName accepts only a single non-special filename component.
func safeLocalChecksumName(name string) bool {
	return name != "" &&
		name != "." &&
		name != ".." &&
		filepath.Base(name) == name &&
		!strings.ContainsAny(name, `/\`)
}

// hashLocalPackage verifies that path is a regular file and returns its digest
// and byte length.
func hashLocalPackage(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open kernel package %s: %w", filepath.Base(path), err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("inspect kernel package %s: %w", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("kernel package %s is not a regular file", filepath.Base(path))
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", 0, fmt.Errorf("hash kernel package %s: %w", filepath.Base(path), err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), info.Size(), nil
}
