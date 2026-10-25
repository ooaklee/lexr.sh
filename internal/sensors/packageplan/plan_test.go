package packageplan

import (
	"encoding/json"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/build"
	"github.com/ooaklee/lexr.sh/internal/sensors/builderimage"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// metadataFixture supplies mechanical records, never verified binary authority.
func metadataFixture() []build.Artifact {
	rules := sourceRules()
	files := make([]build.Artifact, 0, len(rules))
	for name, rule := range rules {
		files = append(files, build.Artifact{Name: name, Bytes: 1, SHA256: strings.Repeat("a", 64), Mode: rule.mode})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files
}

// TestPublicBoundaryRejectsInvalidHandles keeps fixtures below the opaque gate.
func TestPublicBoundaryRejectsInvalidHandles(t *testing.T) {
	for _, input := range []*build.VerifiedArtifacts{nil, {}} {
		if plan, err := FromVerifiedArtifacts(input); err == nil || plan != nil {
			t.Fatal("unverified build handle produced a runtime plan")
		}
	}
	for _, input := range []*Plan{nil, {}} {
		if snapshot, err := input.Snapshot(); err == nil || !reflect.DeepEqual(snapshot, Snapshot{}) {
			t.Fatal("invalid plan produced a snapshot")
		}
		if _, err := input.MarshalJSON(); err == nil {
			t.Fatal("invalid plan produced diagnostic JSON")
		}
	}
}

// TestMechanicalLayout checks every proposed path without invoking installation.
func TestMechanicalLayout(t *testing.T) {
	files := metadataFixture()
	plan, err := fromMetadata(files, build.ArtifactLinks(), build.SourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := plan.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Schema != "lexr-sensors-runtime-package-plan-v1" || snapshot.ComponentID != ComponentID || snapshot.ProposedRelease != ProposedRelease || snapshot.PrivatePrefix != PrivatePrefix || snapshot.RequiredDeclarationPath != RequiredDeclarationPath {
		t.Fatal("fixed runtime identity differs")
	}
	if snapshot.Status != "qualification_required" || snapshot.InstallationEnabled || snapshot.ActivationEnabled {
		t.Fatal("a layout plan granted executable authority")
	}
	if snapshot.SourceComposition.IdentityKind != "approved_source_composition_lineage" || snapshot.SourceComposition.ApprovedLineageCommit != build.SourceCommit {
		t.Fatal("source lineage is absent or presented as observed checkout identity")
	}
	want := map[string]string{
		"libqmi-glib.so.5.12.0":    PrivatePrefix + "/lib/libqmi-glib.so.5.12.0",
		"libssc.so.2":              PrivatePrefix + "/lib/libssc.so.2",
		"ssccli":                   PrivatePrefix + "/bin/ssccli",
		"iio-sensor-proxy":         PrivatePrefix + "/libexec/iio-sensor-proxy",
		"monitor-sensor":           PrivatePrefix + "/bin/monitor-sensor",
		"COPYING.libqmi":           PrivatePrefix + "/share/licenses/COPYING.libqmi",
		"COPYING.LIB.libqmi":       PrivatePrefix + "/share/licenses/COPYING.LIB.libqmi",
		"LICENSE.libssc":           PrivatePrefix + "/share/licenses/LICENSE.libssc",
		"COPYING.iio-sensor-proxy": PrivatePrefix + "/share/licenses/COPYING.iio-sensor-proxy",
	}
	if len(snapshot.Files) != len(want) {
		t.Fatal("private runtime file roster differs")
	}
	seen := map[string]bool{}
	for _, file := range snapshot.Files {
		if seen[file.SourceArtifact] || want[file.SourceArtifact] != file.Destination || file.Bytes != 1 || file.SHA256 != strings.Repeat("a", 64) || file.Mode != sourceRules()[file.SourceArtifact].mode {
			t.Fatalf("private runtime file differs: %+v", file)
		}
		seen[file.SourceArtifact] = true
	}
	wantExcluded := []string{"build-report.json", "loader-evidence.txt", "proxy-dependencies.json", "qmi-dependencies.json", "ssc-dependencies.json", "testlog.json", "testlog.txt"}
	if !reflect.DeepEqual(snapshot.DevelopmentOnlyArtefacts, wantExcluded) || !reflect.DeepEqual(snapshot.ExcludedGlobalInstallTargets, build.ProxyInstallTargets()) {
		t.Fatal("development evidence or old global installation paths were not explicitly excluded")
	}
	for _, global := range snapshot.ExcludedGlobalInstallTargets {
		for _, file := range snapshot.Files {
			if file.Destination == global {
				t.Fatal("a global integration path entered the private layout")
			}
		}
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(decoded, snapshot) {
		t.Fatal("diagnostic JSON differs from its snapshot")
	}
}

// TestPrivateAliasClosure retains the exact two-hop QMI route below one prefix.
func TestPrivateAliasClosure(t *testing.T) {
	plan, err := fromMetadata(metadataFixture(), build.ArtifactLinks(), build.SourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := plan.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	want := []PayloadAlias{
		{"libqmi-glib.so", PrivatePrefix + "/lib/libqmi-glib.so", "libqmi-glib.so.5"},
		{"libqmi-glib.so.5", PrivatePrefix + "/lib/libqmi-glib.so.5", "libqmi-glib.so.5.12.0"},
		{"libssc.so", PrivatePrefix + "/lib/libssc.so", "libssc.so.2"},
	}
	if !reflect.DeepEqual(snapshot.Aliases, want) {
		t.Fatal("library alias roster or order differs")
	}
	nodes := map[string]string{}
	regular := map[string]bool{}
	for _, file := range snapshot.Files {
		regular[file.Destination] = true
	}
	for _, alias := range snapshot.Aliases {
		nodes[alias.Destination] = path.Join(path.Dir(alias.Destination), alias.Target)
	}
	for _, alias := range snapshot.Aliases {
		current := alias.Destination
		seen := map[string]bool{}
		for !regular[current] {
			if seen[current] || !strings.HasPrefix(current, PrivatePrefix+"/lib/") || nodes[current] == "" {
				t.Fatal("private library alias escaped, looped or became unresolved")
			}
			seen[current] = true
			current = nodes[current]
		}
		if path.Dir(current) != PrivatePrefix+"/lib" {
			t.Fatal("private library alias resolves outside its library directory")
		}
	}
	if nodes[nodes[PrivatePrefix+"/lib/libqmi-glib.so"]] != PrivatePrefix+"/lib/libqmi-glib.so.5.12.0" {
		t.Fatal("QMI's two-hop alias route was flattened or lost")
	}
}

// TestRequiredPolicyRemainsUnobserved preserves complete pending requirements.
func TestRequiredPolicyRemainsUnobserved(t *testing.T) {
	plan, err := fromMetadata(metadataFixture(), build.ArtifactLinks(), build.SourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := plan.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	metadata := source.ApprovedMetadata()
	if len(snapshot.RequiredSourceMetadata) != 4 || len(metadata) != 4 {
		t.Fatal("required source authority is incomplete")
	}
	for i, file := range metadata {
		if snapshot.RequiredSourceMetadata[i] != (MetadataRequirement{file.Path, file.SHA256, file.Bytes, file.GitMode}) {
			t.Fatal("required source authority differs")
		}
	}
	wantRecipes := []RecipeRequirement{{"dependency_image", builderimage.RecipeSHA256()}, {"volume_initialisation", build.InitRecipeSHA256()}, {"offline_source_build", build.SourceRecipeSHA256()}}
	if !reflect.DeepEqual(snapshot.RequiredCurrentRecipes, wantRecipes) {
		t.Fatal("required current recipes differ")
	}
	wantGates := []string{"reviewed_manifest_and_independent_pin", "corresponding_source_and_provenance", "relocated_loader_and_dependencies", "inert_package_and_offline_transaction", "distribution_service_ownership", "explicit_activation_and_rollback", "physical_sensor_qualification"}
	if len(snapshot.PendingGates) != len(wantGates) {
		t.Fatal("pending qualification gates are incomplete")
	}
	for i, gate := range snapshot.PendingGates {
		if gate.ID != wantGates[i] || gate.Status != "pending" || gate.Description == "" {
			t.Fatal("a qualification gate was omitted or treated as passed")
		}
	}
	if text := snapshot.PendingGates[2].Description; !strings.Contains(text, build.ProviderDirectory+"/lib") || !strings.Contains(text, PrivatePrefix+"/lib") {
		t.Fatal("old build-prefix loader observations were not distinguished from runtime qualification")
	}
	if text := snapshot.PendingGates[6].Description; !strings.Contains(text, "After a separately approved experimental deployment") || !strings.Contains(text, "before tested or supported claims") {
		t.Fatal("physical qualification was confused with permission for the first separately approved experiment")
	}
}

// TestPlanCopiesAllMetadata refuses slice aliasing across its diagnostic API.
func TestPlanCopiesAllMetadata(t *testing.T) {
	files, aliases := metadataFixture(), build.ArtifactLinks()
	plan, err := fromMetadata(files, aliases, build.SourceCommit)
	if err != nil {
		t.Fatal(err)
	}
	original, err := plan.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	files[0].SHA256 = "changed"
	aliases[0].Target = "changed"
	changed, err := plan.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	changed.ComponentID = "changed"
	changed.InstallationEnabled = true
	changed.ActivationEnabled = true
	changed.SourceComposition.ApprovedLineageCommit = "changed"
	changed.Files[0].Destination = "/etc/changed"
	changed.Aliases[0].Target = "../../changed"
	changed.DevelopmentOnlyArtefacts[0] = "changed"
	changed.ExcludedGlobalInstallTargets[0] = "changed"
	changed.RequiredSourceMetadata[0].SHA256 = "changed"
	changed.RequiredCurrentRecipes[0].SHA256 = "changed"
	changed.PendingGates[0].Status = "passed"
	current, err := plan.Snapshot()
	if err != nil || !reflect.DeepEqual(current, original) {
		t.Fatal("input or output mutation changed the opaque layout plan")
	}
}

// TestMechanicalBoundaryRejectsMetadataDrift checks closed metadata failures.
func TestMechanicalBoundaryRejectsMetadataDrift(t *testing.T) {
	mutations := map[string]func(*[]build.Artifact, *[]build.Link, *string){
		"missing-file":   func(files *[]build.Artifact, _ *[]build.Link, _ *string) { *files = (*files)[1:] },
		"extra-file":     func(files *[]build.Artifact, _ *[]build.Link, _ *string) { *files = append(*files, (*files)[0]) },
		"duplicate-file": func(files *[]build.Artifact, _ *[]build.Link, _ *string) { (*files)[1] = (*files)[0] },
		"unknown-file":   func(files *[]build.Artifact, _ *[]build.Link, _ *string) { (*files)[0].Name = "unknown" },
		"path-file":      func(files *[]build.Artifact, _ *[]build.Link, _ *string) { (*files)[0].Name = "../outside" },
		"upper-digest": func(files *[]build.Artifact, _ *[]build.Link, _ *string) {
			(*files)[0].SHA256 = strings.Repeat("A", 64)
		},
		"short-digest": func(files *[]build.Artifact, _ *[]build.Link, _ *string) { (*files)[0].SHA256 = "a" },
		"invalid-digest": func(files *[]build.Artifact, _ *[]build.Link, _ *string) {
			(*files)[0].SHA256 = strings.Repeat("x", 64)
		},
		"zero-size": func(files *[]build.Artifact, _ *[]build.Link, _ *string) { (*files)[0].Bytes = 0 },
		"oversize": func(files *[]build.Artifact, _ *[]build.Link, _ *string) {
			(*files)[0].Bytes = sourceRules()[(*files)[0].Name].maximum + 1
		},
		"mode":          func(files *[]build.Artifact, _ *[]build.Link, _ *string) { (*files)[0].Mode = 0o777 },
		"missing-alias": func(_ *[]build.Artifact, aliases *[]build.Link, _ *string) { *aliases = (*aliases)[1:] },
		"extra-alias": func(_ *[]build.Artifact, aliases *[]build.Link, _ *string) {
			*aliases = append(*aliases, (*aliases)[0])
		},
		"escape-alias": func(_ *[]build.Artifact, aliases *[]build.Link, _ *string) { (*aliases)[0].Target = "../../outside" },
		"flatten-alias": func(_ *[]build.Artifact, aliases *[]build.Link, _ *string) {
			(*aliases)[0].Target = "libqmi-glib.so.5.12.0"
		},
		"alias-name": func(_ *[]build.Artifact, aliases *[]build.Link, _ *string) { (*aliases)[0].Name = "other" },
		"lineage":    func(_ *[]build.Artifact, _ *[]build.Link, lineage *string) { *lineage = strings.Repeat("0", 40) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			files, aliases, lineage := metadataFixture(), build.ArtifactLinks(), build.SourceCommit
			mutate(&files, &aliases, &lineage)
			if plan, err := fromMetadata(files, aliases, lineage); err == nil || plan != nil {
				t.Fatal("changed development metadata produced a plan")
			}
		})
	}
}
