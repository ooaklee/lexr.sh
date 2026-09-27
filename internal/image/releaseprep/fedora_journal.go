package releaseprep

import (
	"errors"
	"maps"
	"slices"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/plan"
)

// normaliseImageJournal recognises the two contextual values that older Fedora
// producers placed in the digest map. Their typed image-manifest evidence must
// agree exactly before an in-memory copy omits them from the digest-only projection.
// The original journal bytes and their identity remain the preparation authority.
func normaliseImageJournal(journal plan.Journal, manifest imagecontract.Manifest) (plan.Journal, error) {
	legacyIndex := -1
	for index, record := range journal.Records {
		label, hasLabel := record.Digests["iso-volume-label"]
		marker, hasMarker := record.Digests["grub-search-marker"]
		if !hasLabel && !hasMarker {
			continue
		}
		if manifest.Adapter != "fedora-live" || record.StepID != "bind-live-media" || legacyIndex >= 0 ||
			!hasLabel || !hasMarker || label == "" || marker == "" {
			return plan.Journal{}, errors.New("image creation journal has invalid legacy Fedora media evidence")
		}
		if manifest.MediaDiscovery.Strategy != "direct-hybrid-iso" || manifest.MediaDiscovery.Protocol != "dracut-live" ||
			!matchesLegacyFedoraEvidence(manifest.MediaDiscovery, "iso-volume-label", "iso9660-pvd", label) ||
			!matchesLegacyFedoraEvidence(manifest.MediaDiscovery, "grub-search-marker", "grub", marker) {
			return plan.Journal{}, errors.New("image creation journal legacy Fedora media evidence differs from the image manifest")
		}
		legacyIndex = index
	}
	if legacyIndex < 0 {
		return journal, nil
	}
	clone := journal
	clone.Records = slices.Clone(journal.Records)
	for index := range clone.Records {
		clone.Records[index].Digests = maps.Clone(journal.Records[index].Digests)
	}
	if journal.Output != nil {
		output := *journal.Output
		clone.Output = &output
	}
	delete(clone.Records[legacyIndex].Digests, "iso-volume-label")
	delete(clone.Records[legacyIndex].Digests, "grub-search-marker")
	return clone, nil
}

// matchesLegacyFedoraEvidence requires one unambiguous typed value, without
// accepting a second role, a different scope, or unrelated artefact metadata.
func matchesLegacyFedoraEvidence(record imagecontract.MediaDiscoveryRecord, role, scope, value string) bool {
	matches := 0
	for _, evidence := range record.Evidence {
		if evidence.Role != role {
			continue
		}
		if evidence.Scope != scope || evidence.Value != value || evidence.Path != "" || evidence.Artifact != nil {
			return false
		}
		matches++
	}
	return matches == 1
}
