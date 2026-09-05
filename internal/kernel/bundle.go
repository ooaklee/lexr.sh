// Package kernel models a version-bound kernel, module, initramfs, and DTB set.
package kernel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// BundleSchemaVersion identifies the kernel-bundle manifest format emitted by
// this version of the CLI.
const BundleSchemaVersion = 2

// PackageRole identifies how a Debian package contributes to a kernel bundle.
type PackageRole string

const (
	// RoleImage identifies the bootable kernel image package.
	RoleImage PackageRole = "image"
	// RoleModules identifies the matching kernel modules package.
	RoleModules PackageRole = "modules"
	// RoleHeaders identifies ABI-specific development headers.
	RoleHeaders PackageRole = "headers"
	// RoleCommonHeaders identifies architecture-independent common headers.
	RoleCommonHeaders PackageRole = "common-headers"
	// RoleBootSupport identifies the generic architecture-independent package
	// which fulfils external-required DTB delivery.
	RoleBootSupport PackageRole = "boot-support"
)

// Package describes one immutable Debian package included in a kernel bundle.
type Package struct {
	// Role states how the package is used during installation or development.
	Role PackageRole `json:"role"`
	// Name is the Debian package filename from which ABI and version are derived.
	Name string `json:"name"`
	// Path is the local package location when the bytes have been acquired.
	Path string `json:"path,omitempty"`
	// URL is the original remote package location when applicable.
	URL string `json:"url,omitempty"`
	// SHA256 is the lowercase digest of the complete package.
	SHA256 string `json:"sha256"`
	// Size is the package length in bytes.
	Size int64 `json:"size_bytes,omitempty"`
	// Verified reports whether SHA256 matched an authoritative manifest.
	Verified bool `json:"verified"`
}

// DeviceTree identifies one declared hardware variant and its immutable
// packaged and embedded-delivery evidence.
type DeviceTree struct {
	// Device is the stable platform or hardware-variant identifier.
	Device string `json:"device"`
	// Basename is the exact DTB filename.
	Basename string `json:"basename"`
	// Path is the portable package-relative DTB path. Retaining the historical
	// JSON key permits schema-1 documents to be decoded for an explicit migration;
	// schema 2 requires the complete package path and the remaining evidence.
	Path string `json:"path"`
	// CompatibleStrings are the FDT root compatible values in source order.
	// Stubble selects .dtbauto payloads by the first value only.
	CompatibleStrings []string `json:"compatible_strings"`
	// SHA256 is the lowercase digest of the packaged DTB bytes.
	SHA256 string `json:"sha256"`
	// EmbeddedMatches is the number of byte-identical .dtbauto payloads found in
	// the packaged image.
	EmbeddedMatches int `json:"embedded_matches"`
	// Selectors records the HWID or compatible routes which may select this DTB.
	Selectors []DeviceTreeSelector `json:"selectors"`
	// Required reports whether the selected build profile requires this device.
	Required bool `json:"required"`
}

// DTBSelectionProvenance identifies the Stubble implementation and HWID
// database which produced an embedded delivery set.
type DTBSelectionProvenance struct {
	// Tool is the stable public tool name.
	Tool string `json:"tool"`
	// Version is the exact tool or package version reported by the build.
	Version string `json:"version"`
	// DatabaseSHA256 identifies the exact HWID selection database bytes.
	DatabaseSHA256 string `json:"database_sha256"`
	// StubSHA256 identifies the exact Stubble EFI stub bytes.
	StubSHA256 string `json:"stub_sha256,omitempty"`
	// HelperSHA256 identifies the exact DTB selection helper bytes.
	HelperSHA256 string `json:"helper_sha256,omitempty"`
	// SBATSHA256 identifies the exact SBAT input supplied to ukify.
	SBATSHA256 string `json:"sbat_sha256,omitempty"`
	// MachDBSHA256 identifies the optional machdb input embedded in the image.
	MachDBSHA256 string `json:"machdb_sha256,omitempty"`
	// UKifyTool is the stable public executable name.
	UKifyTool string `json:"ukify_tool,omitempty"`
	// UKifyPackage is the installed binary package owning ukify.
	UKifyPackage string `json:"ukify_package,omitempty"`
	// UKifyVersion is the exact installed ukify package version.
	UKifyVersion string `json:"ukify_version,omitempty"`
	// UKifySHA256 identifies the exact effective ukify executable bytes.
	UKifySHA256 string `json:"ukify_sha256,omitempty"`
	// Selections is the deterministic, host-path-free selector attribution.
	Selections []DeviceTreeSelectionEvidence `json:"selections,omitempty"`
}

// DeviceTreeSelectionEvidence binds one declared DTB to matching Stubble
// database records without publishing container paths or live-machine data.
type DeviceTreeSelectionEvidence struct {
	// Device is the stable device identifier from the DTB inventory.
	Device string `json:"device"`
	// Records are the exact HWID or machdb records which select the DTB.
	Records []DTBSelectionRecord `json:"records"`
}

// DTBSelectionRecord records one matching Stubble input record.
type DTBSelectionRecord struct {
	// Source distinguishes the HWID JSON database from the optional machdb.
	Source string `json:"source"`
	// Compatible is the record value matched against the DTB root compatibles.
	Compatible string `json:"compatible"`
	// HWIDs are canonical UUID selectors from one Stubble JSON record.
	HWIDs []string `json:"hwids,omitempty"`
	// Models are machdb model selectors targeting Compatible.
	Models []string `json:"models,omitempty"`
}

// Bundle is a validated, version-bound set of kernel packages and device trees.
type Bundle struct {
	// SchemaVersion identifies the serialised bundle contract.
	SchemaVersion int `json:"schema_version"`
	// Release is the upstream release tag or a local ABI-derived identifier.
	Release string `json:"release"`
	// Repository identifies the release source when the bundle was downloaded.
	Repository string `json:"repository,omitempty"`
	// ABI is the exact Surface kernel ABI shared by image and modules packages.
	ABI string `json:"abi"`
	// Version is the Debian package version shared by every package.
	Version string `json:"version"`
	// Architecture is the target Debian architecture.
	Architecture string `json:"architecture"`
	// RequestedBootImageMode is the caller-selected build policy. It is not an
	// assertion about the generated image.
	RequestedBootImageMode RequestedBootImageMode `json:"requested_boot_image_mode"`
	// EffectiveDTBDelivery is the structurally verified generated-image result.
	EffectiveDTBDelivery DTBDelivery `json:"effective_dtb_delivery"`
	// EmbeddedDTBCount is the total number of .dtbauto sections observed in the
	// packaged image. It must equal the attributable per-device match total.
	EmbeddedDTBCount int `json:"embedded_dtb_count"`
	// DTBSelectionProvenance is required only for embedded delivery.
	DTBSelectionProvenance *DTBSelectionProvenance `json:"dtb_selection_provenance,omitempty"`
	// Packages contains the immutable inputs sorted by filename.
	Packages []Package `json:"packages"`
	// DeviceTrees is the deterministic packaged and embedded DTB inventory.
	DeviceTrees []DeviceTree `json:"device_trees"`
}

// BundleOptions contains every authority needed to construct a schema-2
// bundle. No delivery field has a compatibility default because doing so would
// conflate requested policy with generated-image evidence.
type BundleOptions struct {
	Release                string
	Repository             string
	RequestedBootImageMode RequestedBootImageMode
	EffectiveDTBDelivery   DTBDelivery
	EmbeddedDTBCount       int
	DTBSelectionProvenance *DTBSelectionProvenance
	Packages               []Package
	DeviceTrees            []DeviceTree
}

// packagePatterns maps supported Debian package filename forms to their bundle
// roles and captures the ABI and package version.
var packagePatterns = []struct {
	// role is assigned when pattern matches a filename.
	role PackageRole
	// pattern captures ABI and Debian version from an exact package basename.
	pattern *regexp.Regexp
}{
	{RoleBootSupport, regexp.MustCompile(`^lexr-kernel-boot-support_([^_]+)_all\.deb$`)},
	{RoleModules, regexp.MustCompile(`^linux-modules-(.+)_([^_]+)_arm64\.deb$`)},
	{RoleImage, regexp.MustCompile(`^linux-image-(.+)_([^_]+)_arm64\.deb$`)},
	{RoleHeaders, regexp.MustCompile(`^linux-headers-(.+)_([^_]+)_arm64\.deb$`)},
	{RoleCommonHeaders, regexp.MustCompile(`^linux-qcom-x1e-headers-(.+)_([^_]+)_all\.deb$`)},
}

var (
	// stableIdentifierExpression bounds portable tool and device identifiers.
	stableIdentifierExpression = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	// compatibleExpression bounds portable firmware compatible strings.
	compatibleExpression = regexp.MustCompile(`^[a-z0-9][a-z0-9,._+\-]{0,255}$`)
	// canonicalUUIDExpression accepts the lower-case UUID form emitted by Stubble attribution.
	canonicalUUIDExpression = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

const (
	// maximumBundlePackages bounds one closed delivery package set.
	maximumBundlePackages = 5
	// maximumBundleDeviceTrees bounds structured inventory memory use.
	maximumBundleDeviceTrees = 64
	// maximumCompatibleStrings bounds compatible evidence per device tree.
	maximumCompatibleStrings = 64
	// maximumDeviceSelectors bounds routing evidence per device tree.
	maximumDeviceSelectors = 128
	// maximumSelectionRecords bounds detailed routing evidence per device tree.
	maximumSelectionRecords = 64
)

// NewBundle derives and validates the ABI/version from Debian package
// filenames and returns one canonical schema-2 delivery contract.
func NewBundle(options BundleOptions) (Bundle, error) {
	bundle := Bundle{
		SchemaVersion:          BundleSchemaVersion,
		Release:                options.Release,
		Repository:             options.Repository,
		Architecture:           "arm64",
		RequestedBootImageMode: options.RequestedBootImageMode,
		EffectiveDTBDelivery:   options.EffectiveDTBDelivery,
		EmbeddedDTBCount:       options.EmbeddedDTBCount,
		DTBSelectionProvenance: cloneDTBSelectionProvenance(options.DTBSelectionProvenance),
		Packages:               append([]Package(nil), options.Packages...),
		DeviceTrees:            CloneDeviceTrees(options.DeviceTrees),
	}
	var problems []error
	if !validRequestedBootImageMode(bundle.RequestedBootImageMode) {
		problems = append(problems, fmt.Errorf("unsupported requested boot-image mode %q", bundle.RequestedBootImageMode))
	}
	if !validDTBDelivery(bundle.EffectiveDTBDelivery) {
		problems = append(problems, fmt.Errorf("unsupported effective DTB delivery %q", bundle.EffectiveDTBDelivery))
	}
	if bundle.RequestedBootImageMode == RequestedBootImageModeStubble && bundle.EffectiveDTBDelivery != DTBDeliveryEmbedded {
		problems = append(problems, errors.New("stubble request requires embedded effective DTB delivery"))
	}
	if bundle.RequestedBootImageMode == RequestedBootImageModeNoStubble && bundle.EffectiveDTBDelivery != DTBDeliveryExternalRequired {
		problems = append(problems, errors.New("nostubble request requires external-required effective DTB delivery"))
	}
	if len(bundle.Packages) < 2 || len(bundle.Packages) > maximumBundlePackages {
		problems = append(problems, fmt.Errorf("kernel bundle contains %d packages; expected between 2 and %d", len(bundle.Packages), maximumBundlePackages))
	}
	seen := map[PackageRole]bool{}
	var bootSupportVersion string
	for i := range bundle.Packages {
		pkg := &bundle.Packages[i]
		role, abi, version, err := ParsePackageName(pkg.Name)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if pkg.Role != "" && pkg.Role != role {
			problems = append(problems, fmt.Errorf("package %s has role %q but filename identifies %q", pkg.Name, pkg.Role, role))
			continue
		}
		pkg.Role = role
		if seen[role] {
			problems = append(problems, fmt.Errorf("duplicate %s package", role))
		}
		seen[role] = true
		if role != RoleCommonHeaders && role != RoleBootSupport {
			if !strings.HasSuffix(abi, "-qcom-x1e") {
				problems = append(problems, fmt.Errorf("package %s uses non-Surface ABI %q", pkg.Name, abi))
			}
			if bundle.ABI == "" {
				bundle.ABI = abi
			} else if bundle.ABI != abi {
				problems = append(problems, fmt.Errorf("mixed kernel ABIs %q and %q", bundle.ABI, abi))
			}
		}
		if role == RoleBootSupport {
			bootSupportVersion = version
		} else {
			if bundle.Version == "" {
				bundle.Version = version
			} else if bundle.Version != version {
				problems = append(problems, fmt.Errorf("mixed kernel versions %q and %q", bundle.Version, version))
			}
		}
		if !validSHA256(pkg.SHA256) {
			problems = append(problems, fmt.Errorf("package %s has a non-canonical SHA-256", pkg.Name))
		}
		if pkg.Size <= 0 {
			problems = append(problems, fmt.Errorf("package %s has a non-positive size", pkg.Name))
		}
	}
	if !seen[RoleModules] {
		problems = append(problems, errors.New("kernel bundle requires a linux-modules package"))
	}
	if !seen[RoleImage] {
		problems = append(problems, errors.New("kernel bundle requires a linux-image package"))
	}
	if seen[RoleHeaders] != seen[RoleCommonHeaders] {
		problems = append(problems, errors.New("kernel headers and common headers must be supplied together"))
	}
	switch bundle.EffectiveDTBDelivery {
	case DTBDeliveryEmbedded:
		if len(bundle.Packages) != 2 && len(bundle.Packages) != 4 {
			problems = append(problems, fmt.Errorf("embedded kernel bundle contains %d packages; expected 2 or 4", len(bundle.Packages)))
		}
		if seen[RoleBootSupport] {
			problems = append(problems, errors.New("embedded kernel bundle must not contain an external DTB boot-support package"))
		}
		if err := validateDTBSelectionProvenance(bundle.DTBSelectionProvenance, bundle.DeviceTrees); err != nil {
			problems = append(problems, err)
		}
	case DTBDeliveryExternalRequired:
		if len(bundle.Packages) != 3 && len(bundle.Packages) != 5 {
			problems = append(problems, fmt.Errorf("external-required kernel bundle contains %d packages; expected 3 or 5", len(bundle.Packages)))
		}
		if !seen[RoleBootSupport] {
			problems = append(problems, errors.New("external-required kernel bundle requires one boot-support package"))
		}
		if bundle.DTBSelectionProvenance != nil {
			problems = append(problems, errors.New("external-required kernel bundle must not contain Stubble selection provenance"))
		}
		if bootSupportVersion != "" && bundle.Version != "" && bootSupportVersion != bundle.Version {
			problems = append(problems, fmt.Errorf("boot-support package version %q differs from kernel version %q", bootSupportVersion, bundle.Version))
		}
	}
	if err := validateDeviceTreeInventory(bundle.ABI, bundle.EffectiveDTBDelivery, bundle.EmbeddedDTBCount, bundle.DeviceTrees); err != nil {
		problems = append(problems, err)
	}
	if err := errors.Join(problems...); err != nil {
		return Bundle{}, err
	}
	sort.Slice(bundle.Packages, func(i, j int) bool { return bundle.Packages[i].Name < bundle.Packages[j].Name })
	sort.Slice(bundle.DeviceTrees, func(i, j int) bool {
		if bundle.DeviceTrees[i].Device != bundle.DeviceTrees[j].Device {
			return bundle.DeviceTrees[i].Device < bundle.DeviceTrees[j].Device
		}
		return bundle.DeviceTrees[i].Path < bundle.DeviceTrees[j].Path
	})
	return bundle, nil
}

// ParsePackageName derives the role, ABI, and Debian version from a supported
// kernel package filename. Common headers intentionally return an empty ABI.
func ParsePackageName(name string) (PackageRole, string, string, error) {
	base := filepath.Base(name)
	for _, candidate := range packagePatterns {
		matches := candidate.pattern.FindStringSubmatch(base)
		if matches == nil {
			continue
		}
		if candidate.role == RoleBootSupport {
			return candidate.role, "", matches[1], nil
		}
		abi := matches[1]
		version := matches[2]
		if candidate.role == RoleCommonHeaders {
			abi = ""
		}
		return candidate.role, abi, version, nil
	}
	return "", "", "", fmt.Errorf("unsupported kernel package filename %q", base)
}

// validRequestedBootImageMode reports whether mode belongs to the closed policy set.
func validRequestedBootImageMode(mode RequestedBootImageMode) bool {
	switch mode {
	case RequestedBootImageModeSource, RequestedBootImageModeStubble, RequestedBootImageModeNoStubble:
		return true
	default:
		return false
	}
}

// validDTBDelivery reports whether delivery is a supported effective result.
func validDTBDelivery(delivery DTBDelivery) bool {
	return delivery == DTBDeliveryEmbedded || delivery == DTBDeliveryExternalRequired
}

// validSHA256 reports whether value is one canonical lowercase SHA-256.
func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// cloneDTBSelectionProvenance returns an owned optional provenance value.
func cloneDTBSelectionProvenance(source *DTBSelectionProvenance) *DTBSelectionProvenance {
	if source == nil {
		return nil
	}
	copy := *source
	if source.Selections == nil {
		return &copy
	}
	copy.Selections = make([]DeviceTreeSelectionEvidence, len(source.Selections))
	for index, selection := range source.Selections {
		copy.Selections[index] = selection
		copy.Selections[index].Records = make([]DTBSelectionRecord, len(selection.Records))
		for recordIndex, record := range selection.Records {
			copy.Selections[index].Records[recordIndex] = record
			copy.Selections[index].Records[recordIndex].HWIDs = append([]string(nil), record.HWIDs...)
			copy.Selections[index].Records[recordIndex].Models = append([]string(nil), record.Models...)
			sort.Strings(copy.Selections[index].Records[recordIndex].HWIDs)
			sort.Strings(copy.Selections[index].Records[recordIndex].Models)
		}
		sort.Slice(copy.Selections[index].Records, func(left, right int) bool {
			leftRecord := copy.Selections[index].Records[left]
			rightRecord := copy.Selections[index].Records[right]
			if leftRecord.Source != rightRecord.Source {
				return leftRecord.Source < rightRecord.Source
			}
			return leftRecord.Compatible < rightRecord.Compatible
		})
	}
	sort.Slice(copy.Selections, func(left, right int) bool {
		return copy.Selections[left].Device < copy.Selections[right].Device
	})
	return &copy
}

// validateDTBSelectionProvenance checks embedded-selection identity evidence.
func validateDTBSelectionProvenance(provenance *DTBSelectionProvenance, trees []DeviceTree) error {
	if provenance == nil {
		return errors.New("embedded kernel bundle requires DTB selection provenance")
	}
	if !stableIdentifierExpression.MatchString(provenance.Tool) {
		return fmt.Errorf("invalid DTB selection tool %q", provenance.Tool)
	}
	if !validSelectorValue(provenance.Version) {
		return errors.New("invalid DTB selection tool version")
	}
	if !validSHA256(provenance.DatabaseSHA256) {
		return errors.New("DTB selection database has a non-canonical SHA-256")
	}
	detailed := provenance.StubSHA256 != "" || provenance.HelperSHA256 != "" || provenance.SBATSHA256 != "" ||
		provenance.MachDBSHA256 != "" || provenance.UKifyTool != "" || provenance.UKifyPackage != "" ||
		provenance.UKifyVersion != "" || provenance.UKifySHA256 != "" || len(provenance.Selections) != 0
	if !detailed {
		return errors.New("embedded kernel bundle requires detailed Stubble and ukify provenance")
	}
	for name, digest := range map[string]string{
		"Stubble stub": provenance.StubSHA256, "Stubble helper": provenance.HelperSHA256,
		"Stubble SBAT": provenance.SBATSHA256, "ukify executable": provenance.UKifySHA256,
	} {
		if !validSHA256(digest) {
			return fmt.Errorf("%s has a non-canonical SHA-256", name)
		}
	}
	if provenance.MachDBSHA256 != "" && !validSHA256(provenance.MachDBSHA256) {
		return errors.New("Stubble machdb has a non-canonical SHA-256")
	}
	if !stableIdentifierExpression.MatchString(provenance.UKifyTool) || !validSelectorValue(provenance.UKifyPackage) || !validSelectorValue(provenance.UKifyVersion) {
		return errors.New("ukify tool, package, or version identity is invalid")
	}
	if len(provenance.Selections) == 0 || len(provenance.Selections) > maximumBundleDeviceTrees {
		return errors.New("detailed DTB selection provenance has an invalid device count")
	}
	return validateDTBSelectionEvidence(provenance, trees)
}

// validateDTBSelectionEvidence checks exact agreement between detailed source
// records and the public selector inventory.
func validateDTBSelectionEvidence(provenance *DTBSelectionProvenance, trees []DeviceTree) error {
	treesByDevice := make(map[string]DeviceTree, len(trees))
	for _, tree := range trees {
		treesByDevice[tree.Device] = tree
	}
	seenDevices := make(map[string]bool, len(provenance.Selections))
	machdbUsed := false
	for _, selection := range provenance.Selections {
		tree, exists := treesByDevice[selection.Device]
		if !exists || seenDevices[selection.Device] {
			return fmt.Errorf("DTB selection evidence has unknown or duplicate device %q", selection.Device)
		}
		if tree.EmbeddedMatches != 1 {
			return fmt.Errorf("DTB selection evidence for %s has no embedded payload", selection.Device)
		}
		seenDevices[selection.Device] = true
		if len(selection.Records) == 0 || len(selection.Records) > maximumSelectionRecords {
			return fmt.Errorf("DTB selection evidence for %s has an invalid record count", selection.Device)
		}
		expectedSelectors := make(map[DeviceTreeSelector]bool)
		seenRecords := make(map[string]bool, len(selection.Records))
		for _, record := range selection.Records {
			key := record.Source + "\x00" + record.Compatible
			if seenRecords[key] || record.Compatible != tree.CompatibleStrings[0] {
				return fmt.Errorf("DTB selection record for %s is duplicate or does not match its primary compatible", selection.Device)
			}
			seenRecords[key] = true
			expectedSelectors[DeviceTreeSelector{Kind: DeviceTreeSelectorCompatible, Value: record.Compatible}] = true
			switch record.Source {
			case "hwids":
				if len(record.HWIDs) == 0 || len(record.HWIDs) > maximumDeviceSelectors || len(record.Models) != 0 {
					return fmt.Errorf("DTB HWID record for %s has invalid selector values", selection.Device)
				}
				previous := ""
				for _, hwid := range record.HWIDs {
					if !canonicalUUIDExpression.MatchString(hwid) || hwid <= previous {
						return fmt.Errorf("DTB HWID record for %s is malformed, duplicate, or unsorted", selection.Device)
					}
					previous = hwid
					expectedSelectors[DeviceTreeSelector{Kind: DeviceTreeSelectorHWID, Value: hwid}] = true
				}
			case "machdb":
				machdbUsed = true
				if len(record.Models) == 0 || len(record.Models) > maximumDeviceSelectors || len(record.HWIDs) != 0 {
					return fmt.Errorf("DTB machdb record for %s has invalid selector values", selection.Device)
				}
				previous := ""
				for _, model := range record.Models {
					if !validSelectorValue(model) || model <= previous {
						return fmt.Errorf("DTB machdb record for %s is malformed, duplicate, or unsorted", selection.Device)
					}
					previous = model
				}
			default:
				return fmt.Errorf("DTB selection record for %s has unsupported source %q", selection.Device, record.Source)
			}
		}
		actualSelectors := make(map[DeviceTreeSelector]bool, len(tree.Selectors))
		for _, selector := range tree.Selectors {
			actualSelectors[selector] = true
		}
		if len(actualSelectors) != len(expectedSelectors) {
			return fmt.Errorf("DTB selection evidence for %s disagrees with its selector inventory", selection.Device)
		}
		for selector := range expectedSelectors {
			if !actualSelectors[selector] {
				return fmt.Errorf("DTB selection evidence for %s disagrees with its selector inventory", selection.Device)
			}
		}
	}
	for _, tree := range trees {
		if tree.EmbeddedMatches == 1 && !seenDevices[tree.Device] {
			return fmt.Errorf("embedded DTB %s has no detailed selection evidence", tree.Device)
		}
	}
	if machdbUsed != (provenance.MachDBSHA256 != "") {
		return errors.New("Stubble machdb identity disagrees with detailed selection records")
	}
	return nil
}

// CloneDeviceTrees returns a deep copy with canonical selector order while
// retaining the FDT-compatible order used by the runtime chooser.
func CloneDeviceTrees(source []DeviceTree) []DeviceTree {
	result := make([]DeviceTree, len(source))
	for index, tree := range source {
		result[index] = tree
		result[index].CompatibleStrings = append([]string(nil), tree.CompatibleStrings...)
		result[index].Selectors = append([]DeviceTreeSelector(nil), tree.Selectors...)
		sort.Slice(result[index].Selectors, func(left, right int) bool {
			if result[index].Selectors[left].Kind != result[index].Selectors[right].Kind {
				return result[index].Selectors[left].Kind < result[index].Selectors[right].Kind
			}
			return result[index].Selectors[left].Value < result[index].Selectors[right].Value
		})
	}
	return result
}

// FirmwareRelativePath returns the DTB's path beneath the ABI's
// /usr/lib/firmware/<abi>/device-tree directory.
func (tree DeviceTree) FirmwareRelativePath(abi string) (string, bool) {
	prefix := "usr/lib/firmware/" + abi + "/device-tree/"
	if !strings.HasPrefix(tree.Path, prefix) {
		return "", false
	}
	relative := strings.TrimPrefix(tree.Path, prefix)
	return relative, relative != "" && path.Clean(relative) == relative && !strings.HasPrefix(relative, "../")
}

// validateDeviceTreeInventory checks deterministic packaged and embedded evidence.
func validateDeviceTreeInventory(abi string, delivery DTBDelivery, embeddedDTBCount int, trees []DeviceTree) error {
	if len(trees) == 0 || len(trees) > maximumBundleDeviceTrees {
		return fmt.Errorf("kernel bundle contains %d device trees; expected between 1 and %d", len(trees), maximumBundleDeviceTrees)
	}
	devices := make(map[string]struct{}, len(trees))
	paths := make(map[string]struct{}, len(trees))
	routes := make(map[DeviceTreeSelector]string)
	embedded := 0
	required := 0
	prefix := "usr/lib/firmware/" + abi + "/device-tree/"
	for _, tree := range trees {
		if !stableIdentifierExpression.MatchString(tree.Device) {
			return fmt.Errorf("device tree has invalid device identifier %q", tree.Device)
		}
		if _, duplicate := devices[tree.Device]; duplicate {
			return fmt.Errorf("kernel bundle contains duplicate device tree %q", tree.Device)
		}
		devices[tree.Device] = struct{}{}
		if tree.Path == "" || strings.HasPrefix(tree.Path, "/") || path.Clean(tree.Path) != tree.Path || strings.Contains(tree.Path, "\\") || !strings.HasPrefix(tree.Path, prefix) {
			return fmt.Errorf("device tree %s has invalid package path %q", tree.Device, tree.Path)
		}
		if _, duplicate := paths[tree.Path]; duplicate {
			return fmt.Errorf("kernel bundle contains duplicate device-tree path %q", tree.Path)
		}
		paths[tree.Path] = struct{}{}
		if tree.Basename == "" || path.Base(tree.Path) != tree.Basename || !strings.HasSuffix(tree.Basename, ".dtb") {
			return fmt.Errorf("device tree %s basename does not match its package path", tree.Device)
		}
		if !validSHA256(tree.SHA256) {
			return fmt.Errorf("device tree %s has a non-canonical SHA-256", tree.Device)
		}
		if len(tree.CompatibleStrings) == 0 || len(tree.CompatibleStrings) > maximumCompatibleStrings {
			return fmt.Errorf("device tree %s has an invalid compatible-string count", tree.Device)
		}
		seenCompatibles := make(map[string]bool, len(tree.CompatibleStrings))
		for _, compatible := range tree.CompatibleStrings {
			if !compatibleExpression.MatchString(compatible) || seenCompatibles[compatible] {
				return fmt.Errorf("device tree %s compatible strings are invalid or duplicate", tree.Device)
			}
			seenCompatibles[compatible] = true
		}
		if len(tree.Selectors) == 0 || len(tree.Selectors) > maximumDeviceSelectors {
			return fmt.Errorf("device tree %s has an invalid selector count", tree.Device)
		}
		previousSelector := DeviceTreeSelector{}
		for index, selector := range tree.Selectors {
			if selector.Kind != DeviceTreeSelectorHWID && selector.Kind != DeviceTreeSelectorCompatible {
				return fmt.Errorf("device tree %s has unsupported selector kind %q", tree.Device, selector.Kind)
			}
			if !validSelectorValue(selector.Value) {
				return fmt.Errorf("device tree %s has invalid selector value", tree.Device)
			}
			if selector.Kind == DeviceTreeSelectorCompatible && !seenCompatibles[selector.Value] {
				return fmt.Errorf("device tree %s compatible selector is absent from its FDT inventory", tree.Device)
			}
			if index > 0 && (selector.Kind < previousSelector.Kind || selector.Kind == previousSelector.Kind && selector.Value <= previousSelector.Value) {
				return fmt.Errorf("device tree %s selectors are duplicate or not sorted", tree.Device)
			}
			if owner, duplicate := routes[selector]; duplicate {
				return fmt.Errorf("device trees %s and %s declare the same selection route", owner, tree.Device)
			}
			routes[selector] = tree.Device
			previousSelector = selector
		}
		if tree.EmbeddedMatches < 0 || tree.EmbeddedMatches > 1 {
			return fmt.Errorf("device tree %s has invalid embedded match count %d", tree.Device, tree.EmbeddedMatches)
		}
		if tree.Required && delivery == DTBDeliveryEmbedded && tree.EmbeddedMatches != 1 {
			return fmt.Errorf("required device tree %s does not have exactly one embedded match", tree.Device)
		}
		if tree.Required {
			required++
		}
		if delivery == DTBDeliveryExternalRequired && tree.EmbeddedMatches != 0 {
			return fmt.Errorf("external-required device tree %s unexpectedly has an embedded match", tree.Device)
		}
		embedded += tree.EmbeddedMatches
	}
	if delivery == DTBDeliveryEmbedded && embedded == 0 {
		return errors.New("embedded kernel bundle has no attributable embedded device tree")
	}
	if required == 0 {
		return errors.New("kernel bundle has no device tree required by its selected profile")
	}
	if embeddedDTBCount != embedded {
		return fmt.Errorf("packaged image contains %d embedded device trees but %d are attributable", embeddedDTBCount, embedded)
	}
	if delivery == DTBDeliveryExternalRequired && embeddedDTBCount != 0 {
		return errors.New("external-required kernel bundle records embedded device-tree sections")
	}
	return nil
}

// validSelectorValue reports whether a selector is bounded printable text.
func validSelectorValue(value string) bool {
	if value == "" || len(value) > 512 || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

// Package returns the first package with role from an already validated bundle.
func (b Bundle) Package(role PackageRole) (Package, bool) {
	for _, pkg := range b.Packages {
		if pkg.Role == role {
			return pkg, true
		}
	}
	return Package{}, false
}

// WriteJSON writes a stable, indented bundle manifest without HTML escaping.
func (b Bundle) WriteJSON(w io.Writer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(b)
}
