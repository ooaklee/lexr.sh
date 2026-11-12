package runtimepkg

import (
	"bytes"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/build"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// TestMetadata048StaticProvenance keeps archive checks separate from execution.
func TestMetadata048StaticProvenance(t *testing.T) {
	composition := source.Composition{SourceCommit: source.Metadata048SourceCommit,
		SourceFileCount: source.Metadata048SourceFileCount, SourceSymlinkCount: 6}
	m := packageMetadataProfile(nil, composition, metadata048)
	if m.Schema != 2 || m.ArtifactEvidenceKind != "static_archive_verification" ||
		m.RecipeEvidenceKind != "compiled_recipe_requirements_only" ||
		m.ApprovedCompositionLineage != source.Metadata048SourceCommit {
		t.Fatal("new package inferred observed producer or wrong lineage")
	}
	if m.CurrentRecipeRequirements[1].SHA256 != build.SourceRecipeSHA256048() {
		t.Fatal("new package used legacy source-build recipe")
	}
	recipes := recipeEntriesProfile(metadata048)
	if len(recipes) != 3 || !bytes.Equal(recipes[1].Data, []byte(build.SourceScript048())) ||
		bytes.Equal(recipes[1].Data, recipeEntries()[1].Data) {
		t.Fatal("new corresponding-source recipe bytes differ")
	}
	old := packageMetadata(nil, source.Composition{})
	if old.Schema != 1 || old.ArtifactEvidenceKind != "retained_archive_replay" ||
		old.RecipeEvidenceKind != "recipe_hashes_not_observed" {
		t.Fatal("legacy provenance contract changed")
	}
}

// TestMetadata048OpaqueProfileAccessors exercises only private accessor
// fixtures; these are not decoded packages or filesystem authority.
func TestMetadata048OpaqueProfileAccessors(t *testing.T) {
	for _, p := range []*VerifiedPackage{nil, {}, {valid: true, profile: 99}} {
		if p.SourceLineage() != "" || p.IsMetadata048() {
			t.Fatal("invalid or unknown profile exposed source authority")
		}
	}
	old := &VerifiedPackage{valid: true, profile: legacy047}
	fresh := &VerifiedPackage{valid: true, profile: metadata048}
	if old.IsMetadata048() || old.SourceLineage() != build.SourceCommit ||
		!fresh.IsMetadata048() || fresh.SourceLineage() != source.Metadata048SourceCommit {
		t.Fatal("sealed profile lineage changed")
	}
	value := fresh.SourceLineage()
	value = "changed"
	if value == fresh.SourceLineage() || fresh.SourceLineage() != source.Metadata048SourceCommit {
		t.Fatal("lineage accessor retained caller state")
	}
}

// TestMetadata048InvalidConstructionAndDecode preserves zero/unknown refusal.
func TestMetadata048InvalidConstructionAndDecode(t *testing.T) {
	if _, err := Build048(&build.VerifiedArtifacts{}, &source.Captured{}, []byte(qualificationFixture)); err == nil {
		t.Fatal("zero handles constructed metadata0.4.8 package")
	}
	raw := smallArchive(t)
	for _, p := range []packageProfile{legacy047, metadata048, 99} {
		if _, err := decodePackage(raw, reference(raw), p); err == nil {
			t.Fatal("digest or unknown profile replaced package grammar")
		}
	}
	for _, p := range []packageProfile{legacy047, metadata048} {
		if _, err := decodeSourceProfile(nil, p); err == nil {
			t.Fatal("missing corresponding source accepted")
		}
	}
}
