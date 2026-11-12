package packageplan

import (
	"sort"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/build"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// metadata048Fixture supplies only mechanical metadata, not an opaque producer.
func metadata048Fixture() []build.Artifact {
	files := metadataFixture()
	for _, name := range []string{"ssc-metadata-testlog.json", "ssc-metadata-testlog.txt"} {
		files = append(files, build.Artifact{Name: name, Bytes: 1, SHA256: strings.Repeat("a", 64), Mode: 0o644})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files
}

// TestMetadata048MechanicalProfile retains the same inert payload and new
// independent source/recipe requirements without claiming a producer ran.
func TestMetadata048MechanicalProfile(t *testing.T) {
	plan, err := fromMetadataProfile(metadata048Fixture(), build.ArtifactLinks(), build.SourceCommit048, true)
	if err != nil {
		t.Fatal(err)
	}
	s, err := plan.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Files) != 9 || len(s.Aliases) != 3 || len(s.DevelopmentOnlyArtefacts) != 9 ||
		s.SourceComposition.ApprovedLineageCommit != source.Metadata048SourceCommit ||
		s.InstallationEnabled || s.ActivationEnabled {
		t.Fatal("metadata0.4.8 layout or inactive boundaries changed")
	}
	if s.RequiredCurrentRecipes[2].SHA256 != build.SourceRecipeSHA256048() ||
		s.RequiredCurrentRecipes[2].SHA256 == build.SourceRecipeSHA256() {
		t.Fatal("metadata0.4.8 recipe requirements mixed with legacy")
	}
	if len(s.RequiredSourceMetadata) != 4 {
		t.Fatal("source metadata closure changed")
	}
	for _, m := range source.ApprovedMetadata048() {
		found := false
		for _, got := range s.RequiredSourceMetadata {
			found = found || (got.Path == m.Path && got.SHA256 == m.SHA256 && got.Bytes == m.Bytes)
		}
		if !found {
			t.Fatal("new independently pinned source metadata missing")
		}
	}
}

// TestMetadata048MechanicalCrossRefusals never selects profiles from lineage.
func TestMetadata048MechanicalCrossRefusals(t *testing.T) {
	for _, x := range []struct {
		files []build.Artifact
		line  string
		new   bool
	}{
		{metadataFixture(), build.SourceCommit, true},
		{metadata048Fixture(), build.SourceCommit048, false},
		{metadata048Fixture(), build.SourceCommit, true},
		{metadataFixture(), build.SourceCommit048, false},
		{metadata048Fixture(), strings.Repeat("a", 40), true},
	} {
		if _, err := fromMetadataProfile(x.files, build.ArtifactLinks(), x.line, x.new); err == nil {
			t.Fatal("cross-profile or unknown lineage accepted")
		}
	}
	for _, handle := range []*build.VerifiedArtifacts{nil, {}} {
		if _, err := FromVerifiedArtifacts048(handle); err == nil {
			t.Fatal("zero artefact handle granted metadata0.4.8 planning authority")
		}
	}
}
