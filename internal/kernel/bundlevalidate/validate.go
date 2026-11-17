// Package bundlevalidate performs target-independent validation of a local
// kernel build bundle directory on any host without root privileges. It
// reuses the checksum and manifest contracts already enforced by local bundle
// discovery and augments them with Debian package structure, per-package ABI
// (vermagic) uniformity, and modules.dep presence checks.
package bundlevalidate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// manifestFileNames lists the bundle manifests validated beside packages.
const (
	checksumManifestName = "SHA256SUMS"
	bundleManifestName   = "lexr-kernel-bundle.json"
	provenanceName       = "lexr-kernel-build-provenance.json"
)

// maximumFileBytes bounds every hashed file to four GiB like build output.
const maximumFileBytes int64 = 4 << 30

// CheckResult records the outcome of one named validation check.
type CheckResult struct {
	// Name identifies the check in reports.
	Name string
	// Passed reports whether the check succeeded.
	Passed bool
	// Details summarises evidence on success or the precise failure reason.
	Details string
}

// fail marks the check failed with one reason.
func (result *CheckResult) fail(reason string) {
	result.Passed = false
	result.Details = reason
}

// Report is the complete target-independent validation outcome.
type Report struct {
	// Directory is the validated bundle directory.
	Directory string
	// ABI is the bundle ABI proven by every package.
	ABI string
	// Version is the Debian version proven by every package.
	Version string
	// Architecture is the target architecture.
	Architecture string
	// Checks lists every performed check in report order.
	Checks []CheckResult
	// Warnings lists degraded checks that require unavailable host tooling.
	Warnings []string
}

// OK reports whether every check passed.
func (report Report) OK() bool {
	for _, check := range report.Checks {
		if !check.Passed {
			return false
		}
	}
	return true
}

// Failures returns every failed check in report order.
func (report Report) Failures() []CheckResult {
	failures := make([]CheckResult, 0)
	for _, check := range report.Checks {
		if !check.Passed {
			failures = append(failures, check)
		}
	}
	return failures
}

// Validator performs bundle validation with optional dependency injection.
type Validator struct {
	// Runner is the external-process boundary used for optional host tools.
	Runner platform.Runner
	// DisableExternalTools forces pure-Go parsing without dpkg-deb or zstd.
	DisableExternalTools bool

	// dpkgDebPresent memoises dpkg-deb availability.
	dpkgDebPresent *bool
}

// Validate performs every target-independent bundle check against directory.
func (validator *Validator) Validate(ctx context.Context, directory string) (Report, error) {
	if strings.TrimSpace(directory) == "" {
		return Report{}, fmt.Errorf("validate kernel bundle: directory is required")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return Report{}, fmt.Errorf("validate kernel bundle: resolve directory: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return Report{}, fmt.Errorf("validate kernel bundle: inspect directory %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return Report{}, fmt.Errorf("validate kernel bundle: %q is not a directory", absolute)
	}

	report := Report{Directory: absolute}
	bundle, err := kernel.DiscoverLocalBundleWithOptions(absolute, kernel.LocalBundleOptions{
		PackageSet: kernel.LocalPackageSetAll,
	})
	if err != nil {
		// Discovery failure is still reported as named checks so the caller
		// sees a precise, per-check breakdown rather than one opaque error.
		report.Checks = append(report.Checks, discoveryFailureChecks(err)...)
		return report, nil
	}
	report.ABI = bundle.ABI
	report.Version = bundle.Version
	report.Architecture = bundle.Architecture

	checksums := checkChecksumCoverage(absolute, bundle)
	report.Checks = append(report.Checks, checksums...)

	provenance := checkProvenance(absolute, bundle)
	report.Checks = append(report.Checks, provenance...)

	report.Checks = append(report.Checks, validator.checkPackages(ctx, absolute, bundle)...)

	if validator.DisableExternalTools {
		report.Warnings = append(report.Warnings,
			"dpkg-deb and zstd are unavailable or disabled; deb structure was checked with the built-in ar/tar parser and zstd members could not be inspected")
	}
	return report, nil
}

// discoveryFailureChecks converts one discovery error into named per-area
// failures so reports stay precise when discovery aborts early.
func discoveryFailureChecks(err error) []CheckResult {
	message := err.Error()
	checks := []CheckResult{{Name: "bundle discovery (package set, manifests, checksum agreement)", Passed: false, Details: message}}
	if strings.Contains(message, localChecksumManifestLabel) {
		checks = append(checks, CheckResult{
			Name: "SHA256SUMS coverage", Passed: false, Details: "checksum manifest failed to parse; see bundle discovery failure",
		})
	}
	checks = append(checks, CheckResult{
		Name: "provenance consistency", Passed: false, Details: "not evaluated because bundle discovery failed",
	})
	checks = append(checks, CheckResult{
		Name: "deb structure, ABI uniformity, and modules.dep presence", Passed: false,
		Details: "not evaluated because bundle discovery failed",
	})
	return checks
}

// localChecksumManifestLabel mirrors the checksum file name in error text.
const localChecksumManifestLabel = "SHA256SUMS"

// checkChecksumCoverage proves SHA256SUMS exists, covers every package file
// exactly, and every digest matches the bytes on disk. The bundle and
// provenance manifests are authority documents verified separately: Lexr's
// own build output intentionally excludes them from SHA256SUMS.
func checkChecksumCoverage(directory string, bundle kernel.Bundle) []CheckResult {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return []CheckResult{{Name: "SHA256SUMS coverage", Passed: false, Details: fmt.Sprintf("read directory: %v", err)}}
	}
	covered := map[string]bool{}
	for _, item := range bundle.Packages {
		covered[item.Name] = true
	}
	covered[checksumManifestName] = true
	covered[bundleManifestName] = true
	covered[provenanceName] = true

	uncovered := []string{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			uncovered = append(uncovered, entry.Name()+" (non-regular)")
			continue
		}
		if !covered[entry.Name()] {
			uncovered = append(uncovered, entry.Name())
		}
	}
	sort.Strings(uncovered)

	result := CheckResult{Name: "SHA256SUMS coverage", Passed: true, Details: fmt.Sprintf("%d package digests match", len(bundle.Packages))}
	if len(uncovered) > 0 {
		result.fail(fmt.Sprintf("files not covered by SHA256SUMS or the bundle manifest: %s", strings.Join(uncovered, ", ")))
		return []CheckResult{result}
	}

	unmatched := []string{}
	for _, item := range bundle.Packages {
		digest, _, hashErr := hashFile(filepath.Join(directory, item.Name))
		if hashErr != nil {
			unmatched = append(unmatched, fmt.Sprintf("%s: %v", item.Name, hashErr))
			continue
		}
		if digest != item.SHA256 {
			unmatched = append(unmatched, fmt.Sprintf("%s: expected %s, got %s", item.Name, item.SHA256, digest))
		}
	}
	if len(unmatched) > 0 {
		result.fail(fmt.Sprintf("SHA-256 mismatch: %s", strings.Join(unmatched, "; ")))
	}
	return []CheckResult{result}
}

// checkProvenance proves the build provenance document exists and agrees with
// the bundle manifest on identity and DTB delivery evidence.
func checkProvenance(directory string, bundle kernel.Bundle) []CheckResult {
	result := CheckResult{Name: "provenance consistency", Passed: true, Details: "lexr-kernel-build-provenance.json agrees with lexr-kernel-bundle.json"}
	provenance, err := readProvenanceDocument(filepath.Join(directory, provenanceName))
	if err != nil {
		result.fail(fmt.Sprintf("read %s: %v", provenanceName, err))
		return []CheckResult{result}
	}
	if provenance.BootImageMode != string(bundle.RequestedBootImageMode) {
		result.fail(fmt.Sprintf("requested_boot_image_mode %q does not match bundle %q", provenance.BootImageMode, bundle.RequestedBootImageMode))
	}
	if provenance.EffectiveDTBDelivery != string(bundle.EffectiveDTBDelivery) {
		result.fail(fmt.Sprintf("effective_dtb_delivery %q does not match bundle %q", provenance.EffectiveDTBDelivery, bundle.EffectiveDTBDelivery))
	}
	if provenance.EmbeddedDTBCount != bundle.EmbeddedDTBCount {
		result.fail(fmt.Sprintf("embedded_dtb_count %d does not match bundle %d", provenance.EmbeddedDTBCount, bundle.EmbeddedDTBCount))
	}
	if len(provenance.DeviceTrees) != len(bundle.DeviceTrees) {
		result.fail(fmt.Sprintf("device_trees count %d does not match bundle %d", len(provenance.DeviceTrees), len(bundle.DeviceTrees)))
	}
	if provenance.GitURL != "" && bundle.Repository != "" && !strings.EqualFold(provenance.GitURL, bundle.Repository) {
		result.fail(fmt.Sprintf("git_url %q does not match bundle repository %q", provenance.GitURL, bundle.Repository))
	}
	if result.Passed {
		result.Details = fmt.Sprintf("source %s@%s revision %s; %d device trees", provenance.GitURL, provenance.GitRef, provenance.Revision, len(provenance.DeviceTrees))
	}
	return []CheckResult{result}
}

// provenanceDocument holds the provenance fields cross-checked with the
// bundle manifest.
type provenanceDocument struct {
	SourceKind           string `json:"source_kind"`
	GitURL               string `json:"git_url"`
	GitRef               string `json:"git_ref"`
	Revision             string `json:"revision"`
	BootImageMode        string `json:"requested_boot_image_mode"`
	EffectiveDTBDelivery string `json:"effective_dtb_delivery"`
	EmbeddedDTBCount     int    `json:"embedded_dtb_count"`
	DeviceTrees          []struct {
		Device string `json:"device"`
	} `json:"device_trees"`
}

// checkPackages validates deb structure, ABI uniformity, and modules.dep for
// every package declared by the bundle.
func (validator *Validator) checkPackages(ctx context.Context, directory string, bundle kernel.Bundle) []CheckResult {
	structure := CheckResult{Name: "deb structure integrity", Passed: true, Details: fmt.Sprintf("%d packages parsed", len(bundle.Packages))}
	abi := CheckResult{Name: "ABI uniformity (vermagic across image/modules/headers)", Passed: true, Details: "every package carries ABI " + bundle.ABI}
	modulesDep := CheckResult{Name: "modules.dep presence", Passed: true, Details: "modules.dep present in the modules payload"}
	modulesSeen := false

	for _, item := range bundle.Packages {
		path := filepath.Join(directory, item.Name)
		integrity, warnings := validator.inspectDeb(ctx, path, item.Name)
		if len(warnings) > 0 {
			// Retained by caller through report warnings if needed.
			_ = warnings
		}
		if integrity.Control.Package == "" {
			structure.fail(fmt.Sprintf("%s: control stanza could not be read", item.Name))
			continue
		}
		if integrity.Control.Version != bundle.Version {
			structure.fail(fmt.Sprintf("%s: control Version %q does not match bundle version %q", item.Name, integrity.Control.Version, bundle.Version))
		}
		if integrity.DataEntryCount == 0 {
			structure.fail(fmt.Sprintf("%s: data payload is empty or unreadable", item.Name))
		}
		if !strings.HasSuffix(integrity.Control.Package, bundle.ABI) && item.Role != kernel.RoleCommonHeaders && item.Role != kernel.RoleBootSupport {
			abi.fail(fmt.Sprintf("%s: control Package %q does not end with ABI %q", item.Name, integrity.Control.Package, bundle.ABI))
		}
		if integrity.BootImage != "" && integrity.BootImage != bundle.ABI {
			abi.fail(fmt.Sprintf("%s: boot image %q does not match ABI %q", item.Name, integrity.BootImage, bundle.ABI))
		}
		if integrity.ModulesDirectory != "" && integrity.ModulesDirectory != bundle.ABI {
			abi.fail(fmt.Sprintf("%s: modules directory %q does not match ABI %q", item.Name, integrity.ModulesDirectory, bundle.ABI))
		}
		if integrity.HeaderDirectory != "" && integrity.HeaderDirectory != bundle.ABI {
			abi.fail(fmt.Sprintf("%s: headers directory %q does not match ABI %q", item.Name, integrity.HeaderDirectory, bundle.ABI))
		}
		if item.Role == kernel.RoleModules {
			modulesSeen = true
			if !integrity.ModulesDependPresent && !integrity.DepmodPostinst {
				modulesDep.fail(fmt.Sprintf("%s: usr/lib/modules/%s/modules.dep is absent and no postinst generates it with depmod", item.Name, bundle.ABI))
			}
			if integrity.ModulesDependPresent {
				modulesDep.Details = "modules.dep present in the modules payload"
			} else if integrity.DepmodPostinst {
				modulesDep.Details = "modules.dep generated at install time by a depmod postinst"
			}
		}
	}
	if !modulesSeen {
		modulesDep.fail("no modules package present")
	}
	return []CheckResult{structure, abi, modulesDep}
}

// readProvenanceDocument decodes one bounded provenance JSON document.
func readProvenanceDocument(path string) (provenanceDocument, error) {
	data, err := readFileBounded(path)
	if err != nil {
		return provenanceDocument{}, err
	}
	var document provenanceDocument
	if err := decodeStrict(data, &document); err != nil {
		return provenanceDocument{}, err
	}
	return document, nil
}

// readFileBounded reads one regular file up to maximumFileBytes.
func readFileBounded(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must be a regular file, not a symbolic link", filepath.Base(path))
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	if info.Size() > maximumFileBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", filepath.Base(path), maximumFileBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximumFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() {
		return nil, fmt.Errorf("%s changed while it was read", filepath.Base(path))
	}
	return data, nil
}

// hashFile returns the SHA-256 digest and byte length of one regular file.
func hashFile(path string) (string, int64, error) {
	data, err := readFileBounded(path)
	if err != nil {
		return "", 0, err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), int64(len(data)), nil
}

// decodeStrict decodes exactly one JSON document without trailing data.
func decodeStrict(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return fmt.Errorf("decode trailing data: %w", err)
	}
	return nil
}
