package bootdoctor

import (
	"strings"
	"testing"
)

const (
	// candidateDigestA is one canonical fixture SHA-256 value.
	candidateDigestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// candidateDigestB is a distinct canonical fixture SHA-256 value.
	candidateDigestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// TestSelectDTBCandidateRanksPatchLineBeforeGeneration covers the regression
// matrix in which a large legacy generation must not outrank a newer patch.
func TestSelectDTBCandidateRanksPatchLineBeforeGeneration(t *testing.T) {
	tests := []struct {
		name       string
		candidates []string
		want       string
	}{
		{name: "newer patch v2", candidates: []string{"7.2.0-jg-0sp11v20-qcom-x1e", "7.2.2-jg-0sp11v2-qcom-x1e"}, want: "7.2.2-jg-0sp11v2-qcom-x1e"},
		{name: "newer patch v1", candidates: []string{"7.2.0-jg-0sp11v20-qcom-x1e", "7.2.2-jg-0sp11v1-qcom-x1e"}, want: "7.2.2-jg-0sp11v1-qcom-x1e"},
		{name: "same patch legacy generation", candidates: []string{"7.2.0-jg-0sp11v19-qcom-x1e", "7.2.0-jg-0sp11v20-qcom-x1e"}, want: "7.2.0-jg-0sp11v20-qcom-x1e"},
		{name: "same patch newer generation", candidates: []string{"7.2.2-jg-0sp11v1-qcom-x1e", "7.2.2-jg-0sp11v2-qcom-x1e"}, want: "7.2.2-jg-0sp11v2-qcom-x1e"},
		{name: "surface beats plain", candidates: []string{"7.2.2-jg-0-qcom-x1e", "7.2.2-jg-0sp11v1-qcom-x1e"}, want: "7.2.2-jg-0sp11v1-qcom-x1e"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates := make([]DTBCandidate, 0, len(test.candidates))
			for _, abi := range test.candidates {
				candidates = append(candidates, DTBCandidate{ABI: abi, Device: "x1e-oled", SHA256: candidateDigestA})
			}
			selected, found, err := SelectDTBCandidate(candidates, "x1e-oled")
			if err != nil || !found || selected.ABI != test.want {
				t.Fatalf("selection = %#v, %t, %v; want ABI %s", selected, found, err, test.want)
			}
		})
	}
}

// TestSelectDTBCandidateScopesAndExcludes verifies another device and a
// malformed ABI never compete with canonical candidates for the selected one.
func TestSelectDTBCandidateScopesAndExcludes(t *testing.T) {
	candidates := []DTBCandidate{
		{ABI: "9.9.9-jg-0sp11v99-qcom-x1e", Device: "x1p-lcd", SHA256: candidateDigestA},
		{ABI: "not-an-abi-qcom-x1e", Device: "x1e-oled", SHA256: candidateDigestA},
		{ABI: "7.2.2-jg-0sp11v1-qcom-x1e", Device: "x1e-oled", SHA256: candidateDigestB},
	}
	selected, found, err := SelectDTBCandidate(candidates, "x1e-oled")
	if err != nil || !found || selected.ABI != candidates[2].ABI {
		t.Fatalf("selection = %#v, %t, %v", selected, found, err)
	}
}

// TestSelectDTBCandidateRejectsAmbiguousIdentity verifies equal canonical
// identities with conflicting bytes fail closed without returning a selection.
func TestSelectDTBCandidateRejectsAmbiguousIdentity(t *testing.T) {
	abi := "7.2.2-jg-0sp11v2-qcom-x1e"
	selected, found, err := SelectDTBCandidate([]DTBCandidate{
		{ABI: abi, Device: "x1e-oled", SHA256: candidateDigestA},
		{ABI: abi, Device: "x1e-oled", SHA256: candidateDigestB},
	}, "x1e-oled")
	if err == nil || !strings.Contains(err.Error(), "different SHA-256") || found || selected != (DTBCandidate{}) {
		t.Fatalf("ambiguous selection = %#v, %t, %v", selected, found, err)
	}
}
