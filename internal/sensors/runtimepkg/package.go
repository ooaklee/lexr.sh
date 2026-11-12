// Package runtimepkg produces an inert, local qualification-only sensor
// package. It is not a public release, catalogue entry or production installer
// authority, and it never executes a program or selects a target filesystem.
package runtimepkg

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/sensors/build"
	"github.com/ooaklee/lexr.sh/internal/sensors/builderimage"
	"github.com/ooaklee/lexr.sh/internal/sensors/packageplan"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// Compiled bounds, asset names and qualification labels keep the archive
// contract independent of editable package metadata and caller input.
const (
	// MaximumPackageBytes bounds the uncompressed outer package and its copies.
	// The native payload is bounded below 113 MiB and source below 65 MiB.
	MaximumPackageBytes = int64(256 << 20)
	maximumPayloadBytes = int64(128 << 20)
	maximumSourceBytes  = int64(66 << 20)
	metadataFilename    = "lexr-sensors-runtime-package.json"
	payloadFilename     = "runtime-payload.tar"
	sourceFilename      = "corresponding-source.tar"
	// Kind deliberately does not match any public userspace installer kind.
	Kind = "lexr.sensor-runtime-qualification-only"
	// Purpose is a closed qualification label, never production authority.
	Purpose = "local-software-qualification-only"
)

// Reference is a retained producer byte identity. A digest is not a signature,
// public release trust chain, hardware qualification or installation approval.
type Reference struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// File is one copied runtime regular file below packageplan.PrivatePrefix.
type File struct {
	Path string
	Data []byte
	Mode int64
}

// Link is one fixed relative alias below packageplan.PrivatePrefix.
type Link struct {
	Path   string
	Target string
}

// VerifiedPackage is an opaque, inactive qualification package. Its zero value
// has no authority. Only Build and Decode can produce valid handles.
type VerifiedPackage struct {
	data        []byte
	reference   Reference
	files       []File
	links       []Link
	declaration []byte
	valid       bool
	profile     packageProfile
}

// packageProfile is sealed by an explicitly selected constructor, never JSON.
type packageProfile uint8

const (
	// legacy047 retains the original immutable sample-runtime package profile.
	legacy047 packageProfile = iota + 1
	// metadata048 selects only the independently verified diagnostic profile.
	metadata048
)

// payloadRule fixes the original artefact role, corresponding licence member,
// maximum byte length and private-file mode for one runtime destination.
type payloadRule struct {
	artifact, licenceSource string
	maximum, mode           int64
}

// Closed paths are independent of package metadata and decoding caller input.
func payloadRules() map[string]payloadRule {
	return map[string]payloadRule{
		"bin/ssccli":                              {"ssccli", "", 8 << 20, 0o755},
		"bin/monitor-sensor":                      {"monitor-sensor", "", 8 << 20, 0o755},
		"lib/libqmi-glib.so.5.12.0":               {"libqmi-glib.so.5.12.0", "", 64 << 20, 0o755},
		"lib/libssc.so.2":                         {"libssc.so.2", "", 16 << 20, 0o755},
		"libexec/iio-sensor-proxy":                {"iio-sensor-proxy", "", 16 << 20, 0o755},
		"share/licenses/COPYING.libqmi":           {"COPYING.libqmi", "libqmi/source/COPYING", 18092, 0o644},
		"share/licenses/COPYING.LIB.libqmi":       {"COPYING.LIB.libqmi", "libqmi/source/COPYING.LIB", 26530, 0o644},
		"share/licenses/LICENSE.libssc":           {"LICENSE.libssc", "libssc/source/LICENSE", 35149, 0o644},
		"share/licenses/COPYING.iio-sensor-proxy": {"COPYING.iio-sensor-proxy", "iio-sensor-proxy/source/COPYING", 35147, 0o644},
	}
}

// fixedLinks returns a fresh copy of the only three permitted library aliases.
func fixedLinks() []Link {
	return []Link{{"lib/libqmi-glib.so", "libqmi-glib.so.5"}, {"lib/libqmi-glib.so.5", "libqmi-glib.so.5.12.0"}, {"lib/libssc.so", "libssc.so.2"}}
}

// fileIdentity records one complete runtime file's path, byte identity and mode.
type fileIdentity struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Mode   int64  `json:"mode"`
}

// recipeIdentity describes a required current recipe, not an observed native
// producer identity inferred from the retained development archive.
type recipeIdentity struct {
	Path   string `json:"path"`
	Role   string `json:"role"`
	SHA256 string `json:"sha256"`
}

// metadata is the closed, canonical qualification provenance record. Every
// field is derived from compiled policy and verified source or payload bytes.
type metadata struct {
	Schema                     int              `json:"schema_version"`
	Kind                       string           `json:"kind"`
	Purpose                    string           `json:"purpose"`
	ComponentID                string           `json:"component_id"`
	Release                    string           `json:"release"`
	PrivatePrefix              string           `json:"private_prefix"`
	SourceIdentityKind         string           `json:"source_identity_kind"`
	ApprovedCompositionLineage string           `json:"approved_composition_lineage"`
	RawSourceRegularFiles      int              `json:"raw_source_regular_files"`
	SourceMetadataRecords      int              `json:"source_metadata_records"`
	SourceLinks                int              `json:"source_links"`
	ArtifactEvidenceKind       string           `json:"artifact_evidence_kind"`
	RecipeEvidenceKind         string           `json:"recipe_evidence_kind"`
	CurrentRecipeRequirements  []recipeIdentity `json:"current_recipe_requirements"`
	RecipeProvenanceLimitation string           `json:"recipe_provenance_limitation"`
	QualificationLimitation    string           `json:"qualification_limitation"`
	Payload                    []fileIdentity   `json:"payload"`
}

// recipeEntries copies the three exact current recipes into non-executable
// corresponding-source members with no host-specific logs or paths.
func recipeEntries() []archiveEntry {
	return recipeEntriesProfile(legacy047)
}

// recipeEntriesProfile selects exact compiled requirements, not build evidence.
func recipeEntriesProfile(profile packageProfile) []archiveEntry {
	script := build.SourceScript
	if profile == metadata048 {
		script = build.SourceScript048()
	}
	return []archiveEntry{
		{Path: "recipes/dependency-image.sh", Data: []byte(builderimage.DependencyScript), Mode: 0o644},
		{Path: "recipes/offline-source-build.sh", Data: []byte(script), Mode: 0o644},
		{Path: "recipes/volume-initialisation.sh", Data: []byte(build.InitScript), Mode: 0o644},
	}
}

// packageMetadata derives honest replay lineage and byte identities while
// explicitly keeping current requirements separate from observed provenance.
func packageMetadata(files []File, composition source.Composition) metadata {
	return packageMetadataProfile(files, composition, legacy047)
}

// packageMetadataProfile distinguishes static archive checks from observations.
func packageMetadataProfile(files []File, composition source.Composition, profile packageProfile) metadata {
	m := metadata{
		Schema: 1, Kind: Kind, Purpose: Purpose, ComponentID: packageplan.ComponentID, Release: packageplan.ProposedRelease, PrivatePrefix: packageplan.PrivatePrefix,
		SourceIdentityKind:         "approved_source_composition_lineage_not_observed_checkout",
		ApprovedCompositionLineage: composition.SourceCommit, RawSourceRegularFiles: composition.SourceFileCount, SourceMetadataRecords: len(composition.Metadata()), SourceLinks: composition.SourceSymlinkCount,
		ArtifactEvidenceKind: "retained_archive_replay", RecipeEvidenceKind: "recipe_hashes_not_observed",
		RecipeProvenanceLimitation: "Included current recipe bytes are requirements, not observed provenance for the retained development producer; this package does not establish that those artefacts were built by these recipes.",
		QualificationLimitation:    "Local software qualification only; no public release, production installation, activation, hardware qualification, minimum functional kernel or supported target is established.",
		CurrentRecipeRequirements: []recipeIdentity{
			{"recipes/dependency-image.sh", "dependency_image", builderimage.RecipeSHA256()},
			{"recipes/offline-source-build.sh", "offline_source_build", build.SourceRecipeSHA256()},
			{"recipes/volume-initialisation.sh", "volume_initialisation", build.InitRecipeSHA256()},
		},
	}
	if profile == metadata048 {
		m.Schema = 2
		m.ArtifactEvidenceKind = "static_archive_verification"
		m.RecipeEvidenceKind = "compiled_recipe_requirements_only"
		m.RecipeProvenanceLimitation = "Archive and corresponding-source bytes were statically verified against compiled requirements. Included recipe bytes do not establish an observed producer invocation; a separate external receipt must bind any actual producer execution to this package reference."
		m.CurrentRecipeRequirements[1].SHA256 = build.SourceRecipeSHA256048()
	}
	for _, file := range files {
		m.Payload = append(m.Payload, fileIdentity{file.Path, int64(len(file.Data)), digest(file.Data), file.Mode})
	}
	return m
}

// canonicalJSON emits the package record's fixed indentation and final newline.
func canonicalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// digest returns the exact lowercase SHA-256 identity of complete bytes.
func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// reference derives a byte identity which the producer must retain separately;
// this helper does not turn package contents into a signature or trust chain.
func reference(data []byte) Reference {
	return Reference{Bytes: int64(len(data)), SHA256: digest(data)}
}

// validReference requires bounded nonempty data and a canonical SHA-256 value.
func validReference(ref Reference) bool {
	decoded, err := hex.DecodeString(ref.SHA256)
	return ref.Bytes > 0 && ref.Bytes <= MaximumPackageBytes && err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == ref.SHA256
}

// Build requires already authenticated development artefacts and complete
// captured corresponding source. It creates no files and grants no live-root
// authority. Its output's reference must be retained independently by a caller.
func Build(artifacts *build.VerifiedArtifacts, sources *source.Captured, declaration []byte) (*VerifiedPackage, error) {
	return buildPackage(artifacts, sources, declaration, legacy047)
}

// Build048 composes only independently verified metadata0.4.8 archive/source
// handles. Static package construction is not observed producer provenance.
func Build048(artifacts *build.VerifiedArtifacts, sources *source.Captured, declaration []byte) (*VerifiedPackage, error) {
	return buildPackage(artifacts, sources, declaration, metadata048)
}

// buildPackage keeps profile selection outside all editable archive metadata.
func buildPackage(artifacts *build.VerifiedArtifacts, sources *source.Captured, declaration []byte, profile packageProfile) (*VerifiedPackage, error) {
	if artifacts == nil || sources == nil {
		return nil, errors.New("qualification packaging requires verified artefacts and captured source")
	}
	if artifacts.IsMetadata048() != (profile == metadata048) || sources.IsMetadata048() != (profile == metadata048) {
		return nil, errors.New("qualification artefact/source profiles differ")
	}
	var plan *packageplan.Plan
	var err error
	if profile == metadata048 {
		plan, err = packageplan.FromVerifiedArtifacts048(artifacts)
	} else {
		plan, err = packageplan.FromVerifiedArtifacts(artifacts)
	}
	if err != nil {
		return nil, err
	}
	snapshot, err := plan.Snapshot()
	if err != nil {
		return nil, err
	}
	composition := sources.Composition()
	lineage, count := build.SourceCommit, 949
	if profile == metadata048 {
		lineage, count = build.SourceCommit048, source.Metadata048SourceFileCount
	}
	if composition.SourceCommit != lineage || composition.SourceFileCount != count || composition.SourceSymlinkCount != 6 || len(composition.Metadata()) != 4 {
		return nil, errors.New("qualification packaging requires the complete approved source composition")
	}
	if _, err := verifyDeclaration(declaration); err != nil {
		return nil, err
	}
	var payload []archiveEntry
	for _, file := range snapshot.Files {
		data, ok := artifacts.Data(file.SourceArtifact)
		if !ok {
			return nil, errors.New("verified runtime artefact bytes are unavailable")
		}
		name := strings.TrimPrefix(file.Destination, packageplan.PrivatePrefix+"/")
		if name == file.Destination {
			return nil, errors.New("planned runtime path escaped the fixed private prefix")
		}
		payload = append(payload, archiveEntry{Path: name, Data: data, Mode: file.Mode})
	}
	for _, alias := range snapshot.Aliases {
		payload = append(payload, archiveEntry{Path: strings.TrimPrefix(alias.Destination, packageplan.PrivatePrefix+"/"), Mode: 0o777, LinkTarget: alias.Target})
	}
	payloadData, err := writeArchive(payload, maximumPayloadBytes)
	if err != nil {
		return nil, err
	}
	sourceEntries := recipeEntriesProfile(profile)
	for _, entry := range sources.Entries() {
		member := archiveEntry{Path: entry.Path, Data: entry.Data, Mode: 0o644, LinkTarget: entry.LinkTarget}
		if entry.GitMode == "100755" {
			member.Mode = 0o755
		} else if entry.GitMode == "120000" {
			member.Mode = 0o777
		}
		sourceEntries = append(sourceEntries, member)
	}
	sourceData, err := writeArchive(sourceEntries, maximumSourceBytes)
	if err != nil {
		return nil, err
	}
	// Revalidate the source and payload closures even in the producer path.
	files, _, err := validatePayloadProfile(payload, sources, profile)
	if err != nil {
		return nil, err
	}
	metadataData, err := canonicalJSON(packageMetadataProfile(files, composition, profile))
	if err != nil {
		return nil, err
	}
	outer := []archiveEntry{{Path: payloadFilename, Data: payloadData, Mode: 0o644}, {Path: sourceFilename, Data: sourceData, Mode: 0o644}, {Path: compatibility.Filename, Data: append([]byte(nil), declaration...), Mode: 0o644}, {Path: metadataFilename, Data: metadataData, Mode: 0o644}}
	outer = append(outer, archiveEntry{Path: "SHA256SUMS", Data: checksumList(outer), Mode: 0o644})
	data, err := writeArchive(outer, MaximumPackageBytes)
	if err != nil {
		return nil, err
	}
	return decodePackage(data, reference(data), profile)
}

// checksumList emits a sorted exact checksum record for the four outer assets.
func checksumList(entries []archiveEntry) []byte {
	entries = append([]archiveEntry(nil), entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	var result strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&result, "%s  %s\n", digest(entry.Data), entry.Path)
	}
	return []byte(result.String())
}

// Decode verifies retained exact byte authority before parsing. No package
// contents, metadata, checksum list or compatibility claims can replace that
// independent reference. This is local qualification, not release trust.
func Decode(data []byte, expected Reference) (*VerifiedPackage, error) {
	return decodePackage(data, expected, legacy047)
}

// Decode048 statically verifies only the separately compiled metadata0.4.8
// package profile. A correct digest cannot select or widen this profile.
func Decode048(data []byte, expected Reference) (*VerifiedPackage, error) {
	return decodePackage(data, expected, metadata048)
}

// decodePackage authenticates the selected closed source/payload/recipe tuple.
func decodePackage(data []byte, expected Reference, profile packageProfile) (*VerifiedPackage, error) {
	if profile != legacy047 && profile != metadata048 {
		return nil, errors.New("unknown qualification package profile")
	}
	if !validReference(expected) || int64(len(data)) != expected.Bytes || expected != reference(data) {
		return nil, errors.New("qualification package differs from retained size/SHA-256 authority")
	}
	// Clone before retaining bytes; all further checks run on this owned copy.
	data = append([]byte(nil), data...)
	outer, err := readArchive(data, MaximumPackageBytes, maximumPayloadBytes, 5)
	if err != nil || len(outer) != 5 {
		return nil, fmt.Errorf("qualification package outer archive differs: %v", err)
	}
	assets := make(map[string][]byte, 5)
	allowed := map[string]bool{payloadFilename: true, sourceFilename: true, compatibility.Filename: true, metadataFilename: true, "SHA256SUMS": true}
	var checksummed []archiveEntry
	for _, entry := range outer {
		if !allowed[entry.Path] || entry.Mode != 0o644 || entry.LinkTarget != "" {
			return nil, errors.New("qualification outer asset roster differs")
		}
		assets[entry.Path] = entry.Data
		if entry.Path != "SHA256SUMS" {
			checksummed = append(checksummed, entry)
		}
	}
	if !bytes.Equal(assets["SHA256SUMS"], checksumList(checksummed)) {
		return nil, errors.New("qualification package checksum list differs")
	}
	if _, err := verifyDeclaration(assets[compatibility.Filename]); err != nil {
		return nil, err
	}
	sources, err := decodeSourceProfile(assets[sourceFilename], profile)
	if err != nil {
		return nil, err
	}
	payload, err := readArchive(assets[payloadFilename], maximumPayloadBytes, 64<<20, 12)
	if err != nil {
		return nil, err
	}
	files, links, err := validatePayloadProfile(payload, sources, profile)
	if err != nil {
		return nil, err
	}
	wantedMetadata, err := canonicalJSON(packageMetadataProfile(files, sources.Composition(), profile))
	if err != nil || !bytes.Equal(assets[metadataFilename], wantedMetadata) {
		return nil, errors.New("qualification package metadata differs from its fixed source and payload contract")
	}
	return &VerifiedPackage{data: data, reference: expected, files: files, links: links, declaration: append([]byte(nil), assets[compatibility.Filename]...), valid: true, profile: profile}, nil
}

// decodeSource verifies current recipe bytes and reauthenticates every copied
// source member under the source package's independent compiled inventories.
func decodeSource(data []byte) (*source.Captured, error) {
	return decodeSourceProfile(data, legacy047)
}

// decodeSourceProfile reauthenticates the exact selected corresponding source.
func decodeSourceProfile(data []byte, profile packageProfile) (*source.Captured, error) {
	count := 962
	if profile == metadata048 {
		count = 968
	}
	entries, err := readArchive(data, maximumSourceBytes, 32<<20, count)
	if err != nil || len(entries) != count {
		return nil, fmt.Errorf("corresponding source archive differs: %v", err)
	}
	recipes := map[string]archiveEntry{}
	for _, entry := range recipeEntriesProfile(profile) {
		recipes[entry.Path] = entry
	}
	var copied []source.Entry
	for _, entry := range entries {
		if wanted, ok := recipes[entry.Path]; ok {
			if entry.Mode != 0o644 || entry.LinkTarget != "" || !bytes.Equal(entry.Data, wanted.Data) {
				return nil, errors.New("corresponding source recipe bytes differ from current compiled requirements")
			}
			delete(recipes, entry.Path)
			continue
		}
		gitMode := "100644"
		if entry.LinkTarget != "" {
			gitMode = "120000"
		} else if entry.Mode == 0o755 {
			gitMode = "100755"
		}
		copied = append(copied, source.Entry{Path: entry.Path, Data: entry.Data, GitMode: gitMode, LinkTarget: entry.LinkTarget})
	}
	if len(recipes) != 0 {
		return nil, errors.New("corresponding source recipes are missing")
	}
	if profile == metadata048 {
		return source.FromEntries048(copied)
	}
	return source.FromEntries(copied)
}

// validatePayload closes the runtime roster, checks source-bound licences and
// applies the unchanged strict ELF verifier to exactly five native roles.
func validatePayload(entries []archiveEntry, sources *source.Captured) ([]File, []Link, error) {
	return validatePayloadProfile(entries, sources, legacy047)
}

// validatePayloadProfile retains strict ELF closure for the selected exact ABI.
func validatePayloadProfile(entries []archiveEntry, sources *source.Captured, profile packageProfile) ([]File, []Link, error) {
	if sources == nil || sources.IsMetadata048() != (profile == metadata048) {
		return nil, nil, errors.New("runtime payload source profile differs")
	}
	rules := payloadRules()
	if len(entries) != len(rules)+len(fixedLinks()) {
		return nil, nil, errors.New("runtime payload member count differs")
	}
	sourceFiles := map[string][]byte{}
	for _, entry := range sources.Entries() {
		if entry.LinkTarget == "" {
			sourceFiles[entry.Path] = entry.Data
		}
	}
	var files []File
	var links []Link
	elfs := map[string][]byte{}
	seen := map[string]bool{}
	for _, entry := range entries {
		if seen[entry.Path] {
			return nil, nil, errors.New("runtime payload repeats a member")
		}
		seen[entry.Path] = true
		if entry.LinkTarget != "" {
			if entry.Mode != 0o777 || len(entry.Data) != 0 {
				return nil, nil, errors.New("runtime alias has unexpected bytes or mode")
			}
			links = append(links, Link{entry.Path, entry.LinkTarget})
			continue
		}
		rule, ok := rules[entry.Path]
		if !ok || entry.Mode != rule.mode || len(entry.Data) == 0 || int64(len(entry.Data)) > rule.maximum {
			return nil, nil, errors.New("runtime regular file differs from its closed path/mode/size contract")
		}
		if rule.licenceSource != "" {
			if !bytes.Equal(entry.Data, sourceFiles[rule.licenceSource]) {
				return nil, nil, errors.New("runtime licence differs from authenticated corresponding source")
			}
		} else {
			elfs[rule.artifact] = entry.Data
		}
		files = append(files, File{entry.Path, append([]byte(nil), entry.Data...), entry.Mode})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Slice(links, func(i, j int) bool { return links[i].Path < links[j].Path })
	if len(files) != len(rules) || !reflect.DeepEqual(links, fixedLinks()) {
		return nil, nil, errors.New("runtime payload regular files or exact aliases differ")
	}
	validate := build.ValidateRuntimeELFs
	if profile == metadata048 {
		validate = build.ValidateRuntimeELFs048
	}
	if err := validate(elfs); err != nil {
		return nil, nil, err
	}
	return files, links, nil
}

// IsMetadata048 reports only this opaque handle's sealed valid profile.
func (p *VerifiedPackage) IsMetadata048() bool {
	return p != nil && p.valid && p.profile == metadata048
}

// SourceLineage returns authenticated composition lineage, not observed build
// or checkout evidence. An invalid or unknown-profile handle returns no claim.
func (p *VerifiedPackage) SourceLineage() string {
	if p == nil || !p.valid {
		return ""
	}
	if p.profile == metadata048 {
		return source.Metadata048SourceCommit
	}
	if p.profile == legacy047 {
		return build.SourceCommit
	}
	return ""
}

// Bytes returns copied canonical archive bytes, nil for an invalid handle.
func (p *VerifiedPackage) Bytes() []byte {
	if p == nil || !p.valid {
		return nil
	}
	return append([]byte(nil), p.data...)
}

// Reference returns the retained identity, zero for an invalid handle.
func (p *VerifiedPackage) Reference() Reference {
	if p == nil || !p.valid {
		return Reference{}
	}
	return p.reference
}

// Files returns copied bytes and metadata below the fixed private prefix.
func (p *VerifiedPackage) Files() []File {
	if p == nil || !p.valid {
		return nil
	}
	files := make([]File, len(p.files))
	for n, file := range p.files {
		file.Data = append([]byte(nil), file.Data...)
		files[n] = file
	}
	return files
}

// Links returns copied fixed aliases, nil for an invalid handle.
func (p *VerifiedPackage) Links() []Link {
	if p == nil || !p.valid {
		return nil
	}
	return append([]Link(nil), p.links...)
}

// Declaration returns copied independently pinned qualification manifest bytes.
func (p *VerifiedPackage) Declaration() []byte {
	if p == nil || !p.valid {
		return nil
	}
	return append([]byte(nil), p.declaration...)
}

// Assess preserves hard compatibility bounds and unknown evidence. Even a
// matching result is unverified and does not grant production install authority.
func (p *VerifiedPackage) Assess(target compatibility.Target) compatibility.Decision {
	if p == nil || !p.valid {
		return compatibility.Decision{Status: compatibility.Unavailable, TargetIndex: -1, Reasons: []string{"qualification package handle is invalid"}, Target: target}
	}
	m, err := verifyDeclaration(p.declaration)
	if err != nil {
		return compatibility.Decision{Status: compatibility.Unavailable, TargetIndex: -1, Reasons: []string{err.Error()}, Target: target}
	}
	return compatibility.Evaluate(m, target)
}
