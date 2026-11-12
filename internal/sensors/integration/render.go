// Package integration renders a standalone, inert qualification bundle from
// authenticated runtime and source handles. It never writes registration paths,
// runs a command, observes a live service or grants activation authority.
package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"

	"github.com/ooaklee/lexr.sh/internal/sensors/build"
	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// Asset is a passive copied file in the inert bundle, never a target path.
type Asset struct {
	Path string
	Data []byte
	Mode int64
}

// AssetIdentity describes one complete inert asset without exposing its bytes.
type AssetIdentity struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Mode   int64  `json:"mode"`
}

// TemplateIdentity binds one unmodified source template to its inert asset.
type TemplateIdentity struct {
	SourcePath string `json:"source_path"`
	AssetPath  string `json:"asset_path"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
}

// Snapshot is descriptive metadata, not a service plan or execution grant.
// Type=exec only establishes process execution, not the named bus owner.
type Snapshot struct {
	Schema              string               `json:"schema"`
	Kind                string               `json:"kind"`
	RuntimePackage      runtimepkg.Reference `json:"runtime_package"`
	SourceIdentityKind  string               `json:"source_identity_kind"`
	SourceLineage       string               `json:"source_lineage"`
	UnitName            string               `json:"unit_name"`
	VendorUnitName      string               `json:"vendor_unit_name"`
	BusName             string               `json:"bus_name"`
	LoaderPath          string               `json:"loader_path"`
	ProgramPath         string               `json:"program_path"`
	ProgramSHA256       string               `json:"program_sha256"`
	LibraryPath         string               `json:"library_path"`
	Argv                []string             `json:"argv"`
	UnitBytes           int64                `json:"unit_bytes"`
	UnitSHA256          string               `json:"unit_sha256"`
	ReadinessKind       string               `json:"readiness_kind"`
	QualificationOnly   bool                 `json:"qualification_only"`
	RegistrationEnabled bool                 `json:"registration_enabled"`
	ActivationEnabled   bool                 `json:"activation_enabled"`
	RawTemplates        []TemplateIdentity   `json:"raw_templates"`
	Assets              []AssetIdentity      `json:"assets"`
}

// Bundle is an opaque, standalone in-memory qualification object. Its assets
// must not be appended to the already closed installed runtime prefix. Only
// Render and Render048 can construct valid handles for their separate fixed
// profiles; the zero value grants no authority.
type Bundle struct {
	assets   []Asset
	snapshot Snapshot
	unit     []byte
	valid    bool
	profile  bundleProfile
}

// bundleProfile is selected only by an explicit authenticated constructor.
type bundleProfile uint8

// The closed profiles are not inferred from snapshots or version strings.
const (
	legacy047 bundleProfile = iota + 1
	metadata048
)

// digest returns the complete lowercase SHA-256 identity of copied bytes.
func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// validReference recognises the nonempty reference returned only by a valid
// opaque runtime package; a caller-supplied digest is not accepted by Render.
func validReference(ref runtimepkg.Reference) bool {
	decoded, err := hex.DecodeString(ref.SHA256)
	return ref.Bytes > 0 && ref.Bytes <= runtimepkg.MaximumPackageBytes && err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == ref.SHA256
}

// Render authenticates the complete copied source and independently pinned
// templates, then derives the sole proxy path from a valid opaque package.
// It has no filesystem, network, registration or command-execution interface.
func Render(pkg *runtimepkg.VerifiedPackage, sources *source.Captured) (*Bundle, error) {
	return renderProfile(pkg, sources, legacy047)
}

// Render048 authenticates only the separately qualified metadata0.4.8 profile.
// It renders the same passive unit and assets without registration or execution.
func Render048(pkg *runtimepkg.VerifiedPackage, sources *source.Captured) (*Bundle, error) {
	return renderProfile(pkg, sources, metadata048)
}

// renderProfile retains exact source/runtime pairing for one selected profile.
// It does not widen the default renderer or accept caller-selected authority.
func renderProfile(pkg *runtimepkg.VerifiedPackage, sources *source.Captured, profile bundleProfile) (*Bundle, error) {
	if pkg == nil || sources == nil || !validReference(pkg.Reference()) {
		return nil, errors.New("inert integration rendering requires verified runtime and captured source")
	}
	lineage, fileCount := build.SourceCommit, 949
	var captured *source.Captured
	var err error
	switch profile {
	case legacy047:
		if pkg.IsMetadata048() || sources.IsMetadata048() || pkg.SourceLineage() != build.SourceCommit {
			return nil, errors.New("inert integration rendering remains restricted to its independently qualified0.4.7 profile")
		}
		captured, err = source.FromEntries(sources.Entries())
	case metadata048:
		if !pkg.IsMetadata048() || !sources.IsMetadata048() || pkg.SourceLineage() != build.SourceCommit048 {
			return nil, errors.New("metadata0.4.8 integration requires its exact runtime and source profiles")
		}
		lineage, fileCount = build.SourceCommit048, source.Metadata048SourceFileCount
		captured, err = source.FromEntries048(sources.Entries())
	default:
		return nil, errors.New("inert integration profile is unknown")
	}
	// Revalidate defensive source copies rather than accepting caller metadata or
	// treating a diagnostic composition snapshot as byte authentication.
	if err != nil {
		return nil, err
	}
	composition := captured.Composition()
	if composition.SourceCommit != lineage || composition.SourceFileCount != fileCount || composition.SourceSymlinkCount != 6 || len(composition.Metadata()) != 4 {
		return nil, errors.New("inert integration source composition differs")
	}
	files, links := pkg.Files(), pkg.Links()
	if len(files) != 9 || len(links) != 3 {
		return nil, errors.New("inert integration runtime roster differs")
	}
	var program, licence []byte
	for _, file := range files {
		switch file.Path {
		case "libexec/iio-sensor-proxy":
			if file.Mode != 0o755 || len(file.Data) == 0 {
				return nil, errors.New("verified sensor proxy is not a regular executable role")
			}
			program = file.Data
		case "share/licenses/COPYING.iio-sensor-proxy":
			if file.Mode != 0o644 || len(file.Data) == 0 {
				return nil, errors.New("verified proxy licence role differs")
			}
			licence = file.Data
		}
	}
	if len(program) == 0 || len(licence) == 0 {
		return nil, errors.New("verified proxy or licence role is missing")
	}
	entries := make(map[string]source.Entry)
	for _, entry := range captured.Entries() {
		entries[entry.Path] = entry
	}
	licenceSource, found := entries["iio-sensor-proxy/source/COPYING"]
	if !found || licenceSource.GitMode != "100644" || licenceSource.LinkTarget != "" || !bytes.Equal(licence, licenceSource.Data) {
		return nil, errors.New("inert integration notice differs from authenticated source")
	}
	result := &Bundle{unit: []byte(renderedUnit), profile: profile}
	result.snapshot = Snapshot{
		Schema: "lexr-sensors-inert-integration-bundle-v1", Kind: Kind,
		RuntimePackage: pkg.Reference(), SourceIdentityKind: "approved_source_composition_lineage_not_observed_checkout", SourceLineage: composition.SourceCommit,
		UnitName: UnitName, VendorUnitName: VendorUnitName, BusName: BusName, LoaderPath: LoaderPath,
		ProgramPath: programPath, ProgramSHA256: digest(program), LibraryPath: libraryPath, Argv: fixedArgv(),
		UnitBytes: int64(len(result.unit)), UnitSHA256: digest(result.unit),
		ReadinessKind: "external_bus_owner_verification_required", QualificationOnly: true,
	}
	for _, policy := range templatePolicies() {
		entry, ok := entries[policy.sourcePath]
		if !ok || entry.GitMode != "100644" || entry.LinkTarget != "" || int64(len(entry.Data)) != policy.bytes || digest(entry.Data) != policy.sha256 {
			return nil, errors.New("inert integration template differs from independent compiled authority")
		}
		result.assets = append(result.assets, Asset{Path: policy.assetPath, Data: append([]byte(nil), entry.Data...), Mode: 0o644})
		result.snapshot.RawTemplates = append(result.snapshot.RawTemplates, TemplateIdentity{SourcePath: policy.sourcePath, AssetPath: policy.assetPath, Bytes: policy.bytes, SHA256: policy.sha256})
	}
	result.assets = append(result.assets,
		Asset{Path: "licenses/COPYING.iio-sensor-proxy", Data: append([]byte(nil), licence...), Mode: 0o644},
		Asset{Path: unitAsset, Data: append([]byte(nil), result.unit...), Mode: 0o644},
	)
	sort.Slice(result.assets, func(i, j int) bool { return result.assets[i].Path < result.assets[j].Path })
	for _, asset := range result.assets {
		result.snapshot.Assets = append(result.snapshot.Assets, AssetIdentity{Path: asset.Path, Bytes: int64(len(asset.Data)), SHA256: digest(asset.Data), Mode: asset.Mode})
	}
	result.valid = true
	return result, nil
}

// IsMetadata048 reports only an opaque valid bundle's selected successor seal.
// Invalid, zero, unknown and legacy handles return false; snapshots grant none.
func (b *Bundle) IsMetadata048() bool {
	return b != nil && b.valid && b.profile == metadata048
}

// IsLegacy047 positively identifies only a valid opaque legacy profile.
// Future profiles cannot acquire legacy constructor authority by exclusion.
func (b *Bundle) IsLegacy047() bool {
	return b != nil && b.valid && b.profile == legacy047
}

// Assets returns defensive passive bytes and relative names. It never produces
// a global registration destination or permits mutation of the runtime tree.
func (b *Bundle) Assets() ([]Asset, error) {
	if b == nil || !b.valid {
		return nil, errors.New("inert integration bundle handle is invalid")
	}
	result := make([]Asset, len(b.assets))
	for n, asset := range b.assets {
		asset.Data = append([]byte(nil), asset.Data...)
		result[n] = asset
	}
	return result, nil
}

// Snapshot returns a deep diagnostic copy without execution or install rights.
func (b *Bundle) Snapshot() (Snapshot, error) {
	if b == nil || !b.valid {
		return Snapshot{}, errors.New("inert integration bundle handle is invalid")
	}
	result := b.snapshot
	result.Argv = append([]string(nil), result.Argv...)
	result.RawTemplates = append([]TemplateIdentity(nil), result.RawTemplates...)
	result.Assets = append([]AssetIdentity(nil), result.Assets...)
	return result, nil
}

// Unit returns copied static manual-unit bytes, not a registration request.
func (b *Bundle) Unit() ([]byte, error) {
	if b == nil || !b.valid {
		return nil, errors.New("inert integration bundle handle is invalid")
	}
	return append([]byte(nil), b.unit...), nil
}
