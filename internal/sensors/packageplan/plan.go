// Package packageplan describes a fixed, inactive sensor runtime layout from
// verified development artefacts. It performs no packaging or installation.
package packageplan

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"sort"

	"github.com/ooaklee/lexr.sh/internal/sensors/build"
	"github.com/ooaklee/lexr.sh/internal/sensors/builderimage"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// The proposed runtime identity is separate from the experimental source-only
// controller. None of these constants enables a catalogue or install action.
const (
	ComponentID             = "native-sensors-runtime-v1"
	ProposedRelease         = "sp11-native-sensors-v1"
	PrivatePrefix           = "/usr/lib/lexr/sensors/" + ProposedRelease
	RequiredDeclarationPath = "userspace/compatibility/" + ComponentID + "/lexr-component-compatibility.json"
	Status                  = "qualification_required"
)

// PayloadFile describes a copied metadata record at one compiled private path.
// It contains no bytes, installation method or caller-selected destination.
type PayloadFile struct {
	SourceArtifact string `json:"source_artifact"`
	Destination    string `json:"destination"`
	Bytes          int64  `json:"bytes"`
	SHA256         string `json:"sha256"`
	Mode           int64  `json:"mode"`
}

// PayloadAlias identifies one fixed relative library alias below PrivatePrefix.
type PayloadAlias struct {
	SourceAlias string `json:"source_alias"`
	Destination string `json:"destination"`
	Target      string `json:"target"`
}

// Gate records unfinished qualification, never editable execution authority.
type Gate struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

// SourceComposition names an approved lineage, not an observed checkout HEAD.
type SourceComposition struct {
	IdentityKind          string `json:"identity_kind"`
	ApprovedLineageCommit string `json:"approved_lineage_commit"`
}

// MetadataRequirement identifies independently compiled source metadata which
// a future package producer must authenticate; this plan has not read it.
type MetadataRequirement struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Bytes   int64  `json:"bytes"`
	GitMode string `json:"git_mode"`
}

// RecipeRequirement names a required current recipe, not an observed producer
// identity inferred from old development artefacts or their adjacent report.
type RecipeRequirement struct {
	Role   string `json:"role"`
	SHA256 string `json:"sha256"`
}

// Snapshot is a defensive diagnostic copy. No consumer accepts it as an
// installation receipt, package, compatibility decision or activation request.
type Snapshot struct {
	Schema                       string                `json:"schema"`
	ComponentID                  string                `json:"component_id"`
	ProposedRelease              string                `json:"proposed_release"`
	SourceComposition            SourceComposition     `json:"source_composition"`
	RequiredDeclarationPath      string                `json:"required_declaration_path"`
	RequiredSourceMetadata       []MetadataRequirement `json:"required_source_metadata"`
	RequiredCurrentRecipes       []RecipeRequirement   `json:"required_current_recipes"`
	PrivatePrefix                string                `json:"private_prefix"`
	Status                       string                `json:"status"`
	InstallationEnabled          bool                  `json:"installation_enabled"`
	ActivationEnabled            bool                  `json:"activation_enabled"`
	Files                        []PayloadFile         `json:"files"`
	Aliases                      []PayloadAlias        `json:"aliases"`
	DevelopmentOnlyArtefacts     []string              `json:"development_only_artefacts"`
	ExcludedGlobalInstallTargets []string              `json:"excluded_global_install_targets"`
	PendingGates                 []Gate                `json:"pending_gates"`
}

// Plan is an opaque, non-executable layout observation. Only the unchanged
// strict development-artefact verifier can supply its public constructor.
type Plan struct {
	snapshot Snapshot
	valid    bool
}

// FromVerifiedArtifacts derives metadata only from an opaque verified handle.
// It never reads binary data, files, network state, target roots or services.
func FromVerifiedArtifacts(verified *build.VerifiedArtifacts) (*Plan, error) {
	if verified == nil || verified.IsMetadata048() {
		return nil, errors.New("sensor runtime planning requires verified development artefacts")
	}
	report := verified.Report()
	if report.Schema != "lexr-sensors-source-build-v1" || report.SourceCommit != build.SourceCommit {
		return nil, errors.New("sensor runtime planning requires the approved verified source composition")
	}
	return fromMetadata(verified.Files(), verified.Links(), report.SourceCommit)
}

// fileRule fixes the only metadata members accepted from the source contract.
type fileRule struct {
	maximum     int64
	mode        int64
	destination string
}

// sourceRules keeps the runtime mapping closed independently of editable inputs.
func sourceRules() map[string]fileRule {
	return map[string]fileRule{
		"libqmi-glib.so.5.12.0":    {64 << 20, 0o755, "lib/libqmi-glib.so.5.12.0"},
		"libssc.so.2":              {16 << 20, 0o755, "lib/libssc.so.2"},
		"ssccli":                   {8 << 20, 0o755, "bin/ssccli"},
		"iio-sensor-proxy":         {16 << 20, 0o755, "libexec/iio-sensor-proxy"},
		"monitor-sensor":           {8 << 20, 0o755, "bin/monitor-sensor"},
		"COPYING.libqmi":           {18092, 0o644, "share/licenses/COPYING.libqmi"},
		"COPYING.LIB.libqmi":       {26530, 0o644, "share/licenses/COPYING.LIB.libqmi"},
		"LICENSE.libssc":           {35149, 0o644, "share/licenses/LICENSE.libssc"},
		"COPYING.iio-sensor-proxy": {35147, 0o644, "share/licenses/COPYING.iio-sensor-proxy"},
		"testlog.json":             {8 << 20, 0o644, ""},
		"testlog.txt":              {8 << 20, 0o644, ""},
		"qmi-dependencies.json":    {2 << 20, 0o644, ""},
		"ssc-dependencies.json":    {2 << 20, 0o644, ""},
		"proxy-dependencies.json":  {2 << 20, 0o644, ""},
		"loader-evidence.txt":      {2 << 20, 0o644, ""},
		"build-report.json":        {1 << 20, 0o644, ""},
	}
}

// pendingGates identifies separate work without claiming compatibility evidence.
func pendingGates() []Gate {
	return []Gate{
		{"reviewed_manifest_and_independent_pin", "pending", "Review the complete hard target policy and independently pin the component compatibility declaration; no sensor compatibility decision exists yet."},
		{"corresponding_source_and_provenance", "pending", "Bind corresponding source, all required notices, the recipe and portable provenance to the exact runtime payload."},
		{"relocated_loader_and_dependencies", "pending", "Existing loader observations name " + build.ProviderDirectory + "/lib, not " + PrivatePrefix + "/lib. Qualify relocated private providers and exact target runtime dependencies without replacing distribution libraries."},
		{"inert_package_and_offline_transaction", "pending", "Validate a closed inactive package and an offline-root install transaction without executing target programs or exposing activation hooks."},
		{"distribution_service_ownership", "pending", "Resolve distribution proxy ownership and conflicts without overwriting unknown vendor files or allowing concurrent owners of the shared D-Bus name."},
		{"explicit_activation_and_rollback", "pending", "Qualify separately confirmed activation and transactional rollback, including previous service state and partial failures."},
		{"physical_sensor_qualification", "pending", "After a separately approved experimental deployment, qualify physical sensor identity, roles, axes, calibration, clocks and desktop behaviour on each declared hardware and kernel/OS pairing before tested or supported claims. This gate grants no installation or activation authority."},
	}
}

// excludedGlobalTargets makes every old logical destination explicitly absent.
// Both executables move into the private prefix; no integration hook is staged.
func excludedGlobalTargets() []string {
	return []string{
		"/usr/bin/monitor-sensor",
		"/usr/lib/systemd/system/iio-sensor-proxy.service",
		"/usr/lib/udev/rules.d/80-iio-sensor-proxy.rules",
		"/usr/libexec/iio-sensor-proxy",
		"/usr/share/dbus-1/system.d/net.hadess.SensorProxy.conf",
		"/usr/share/polkit-1/actions/net.hadess.SensorProxy.policy",
	}
}

// fromMetadata performs only mechanical mapping for the opaque public boundary.
// Unit fixtures may exercise it without manufacturing a verified build handle.
func fromMetadata(files []build.Artifact, aliases []build.Link, lineage string) (*Plan, error) {
	return fromMetadataProfile(files, aliases, lineage, false)
}

// fromMetadataProfile keeps the two compiled rosters and requirements separate.
func fromMetadataProfile(files []build.Artifact, aliases []build.Link, lineage string, metadata048 bool) (*Plan, error) {
	wantedLineage := build.SourceCommit
	recipes := build.SourceRecipeSHA256()
	metadata := source.ApprovedMetadata()
	rules := sourceRules()
	if metadata048 {
		wantedLineage = build.SourceCommit048
		recipes = build.SourceRecipeSHA256048()
		metadata = source.ApprovedMetadata048()
		rules["ssc-metadata-testlog.json"] = fileRule{8 << 20, 0o644, ""}
		rules["ssc-metadata-testlog.txt"] = fileRule{8 << 20, 0o644, ""}
	}
	if lineage != wantedLineage {
		return nil, errors.New("sensor runtime source-composition lineage differs")
	}
	if len(files) != len(rules) || !reflect.DeepEqual(aliases, build.ArtifactLinks()) {
		return nil, errors.New("sensor runtime development artefact roster differs")
	}
	result := Snapshot{
		Schema:                       "lexr-sensors-runtime-package-plan-v1",
		ComponentID:                  ComponentID,
		ProposedRelease:              ProposedRelease,
		SourceComposition:            SourceComposition{"approved_source_composition_lineage", lineage},
		RequiredDeclarationPath:      RequiredDeclarationPath,
		PrivatePrefix:                PrivatePrefix,
		Status:                       Status,
		PendingGates:                 pendingGates(),
		ExcludedGlobalInstallTargets: excludedGlobalTargets(),
		RequiredCurrentRecipes: []RecipeRequirement{
			{"dependency_image", builderimage.RecipeSHA256()},
			{"volume_initialisation", build.InitRecipeSHA256()},
			{"offline_source_build", recipes},
		},
	}
	for _, metadata := range metadata {
		result.RequiredSourceMetadata = append(result.RequiredSourceMetadata, MetadataRequirement{metadata.Path, metadata.SHA256, metadata.Bytes, metadata.GitMode})
	}
	seen := make(map[string]bool, len(files))
	for _, file := range files {
		rule, found := rules[file.Name]
		digest, err := hex.DecodeString(file.SHA256)
		if !found || seen[file.Name] || file.Bytes <= 0 || file.Bytes > rule.maximum || file.Mode != rule.mode || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != file.SHA256 {
			return nil, fmt.Errorf("sensor runtime development artefact metadata differs: %q", file.Name)
		}
		seen[file.Name] = true
		if rule.destination == "" {
			result.DevelopmentOnlyArtefacts = append(result.DevelopmentOnlyArtefacts, file.Name)
			continue
		}
		result.Files = append(result.Files, PayloadFile{file.Name, path.Join(PrivatePrefix, rule.destination), file.Bytes, file.SHA256, file.Mode})
	}
	for _, alias := range build.ArtifactLinks() {
		result.Aliases = append(result.Aliases, PayloadAlias{alias.Name, path.Join(PrivatePrefix, "lib", alias.Name), alias.Target})
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].Destination < result.Files[j].Destination })
	sort.Slice(result.Aliases, func(i, j int) bool { return result.Aliases[i].Destination < result.Aliases[j].Destination })
	sort.Strings(result.DevelopmentOnlyArtefacts)
	return &Plan{snapshot: result, valid: true}, nil
}

// FromVerifiedArtifacts048 derives the same inactive layout from the separately
// sealed metadata0.4.8 archive profile. It never selects that profile from JSON.
func FromVerifiedArtifacts048(verified *build.VerifiedArtifacts) (*Plan, error) {
	if verified == nil || !verified.IsMetadata048() {
		return nil, errors.New("metadata0.4.8 planning requires its sealed verified artefacts")
	}
	report := verified.Report()
	if report.Schema != "lexr-sensors-source-build-v1" || report.SourceCommit != build.SourceCommit048 {
		return nil, errors.New("metadata0.4.8 source-composition lineage differs")
	}
	return fromMetadataProfile(verified.Files(), verified.Links(), report.SourceCommit, true)
}

// Snapshot returns copied metadata, never an execution or installation grant.
func (p *Plan) Snapshot() (Snapshot, error) {
	if p == nil || !p.valid {
		return Snapshot{}, errors.New("sensor runtime package plan is invalid")
	}
	r := p.snapshot
	r.Files = append([]PayloadFile(nil), r.Files...)
	r.Aliases = append([]PayloadAlias(nil), r.Aliases...)
	r.DevelopmentOnlyArtefacts = append([]string(nil), r.DevelopmentOnlyArtefacts...)
	r.ExcludedGlobalInstallTargets = append([]string(nil), r.ExcludedGlobalInstallTargets...)
	r.RequiredSourceMetadata = append([]MetadataRequirement(nil), r.RequiredSourceMetadata...)
	r.RequiredCurrentRecipes = append([]RecipeRequirement(nil), r.RequiredCurrentRecipes...)
	r.PendingGates = append([]Gate(nil), r.PendingGates...)
	return r, nil
}

// MarshalJSON emits the same defensive diagnostic snapshot as Snapshot.
func (p *Plan) MarshalJSON() ([]byte, error) {
	snapshot, err := p.Snapshot()
	if err != nil {
		return nil, err
	}
	return json.Marshal(snapshot)
}
