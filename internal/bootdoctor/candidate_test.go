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
		{name: "qcom only ranks with x1e", candidates: []string{"7.2.2-jg-0sp11v2-qcom", "7.2.2-jg-0sp11v1-qcom-x1e"}, want: "7.2.2-jg-0sp11v2-qcom"},
		{name: "qcom only same patch line", candidates: []string{"7.2.2-jg-0sp11v1-qcom", "7.2.2-jg-0sp11v2-qcom"}, want: "7.2.2-jg-0sp11v2-qcom"},
		{name: "suffix free ranks by patch line", candidates: []string{"7.2.0-jg-0sp11v20", "7.2.2-jg-0sp11v1"}, want: "7.2.2-jg-0sp11v1"},
		{name: "patch line beats qcom only generation", candidates: []string{"7.2.0-jg-0sp11v20-qcom", "7.2.2-jg-0sp11v1"}, want: "7.2.2-jg-0sp11v1"},
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
	malformed := []string{
		"not-an-abi-qcom-x1e",
		"7.2.2-qcom-x1e-qcom-x1e",
		"7.2.2-qcom-qcom",
		"7.2.2-x1e-qcom-x1e",
		"7.2.2-x1e-qcom",
		"7.2.2-qcom-x1e-extra",
	}
	for _, abi := range malformed {
		if _, valid := parseKernelRank(abi); valid {
			t.Fatalf("malformed ABI %q parsed as canonical", abi)
		}
	}
	candidates := []DTBCandidate{
		{ABI: "9.9.9-jg-0sp11v99-qcom-x1e", Device: "x1p-lcd", SHA256: candidateDigestA},
		{ABI: "not-an-abi-qcom-x1e", Device: "x1e-oled", SHA256: candidateDigestA},
		{ABI: "7.2.2-qcom-x1e-qcom-x1e", Device: "x1e-oled", SHA256: candidateDigestA},
		{ABI: "7.2.2-qcom-qcom", Device: "x1e-oled", SHA256: candidateDigestA},
		{ABI: "7.2.2-x1e-qcom-x1e", Device: "x1e-oled", SHA256: candidateDigestA},
		{ABI: "7.2.2-x1e-qcom", Device: "x1e-oled", SHA256: candidateDigestA},
		{ABI: "7.2.2-qcom-x1e-extra", Device: "x1e-oled", SHA256: candidateDigestA},
		{ABI: "7.2.2-jg-0sp11v1-qcom-x1e", Device: "x1e-oled", SHA256: candidateDigestB},
	}
	selected, found, err := SelectDTBCandidate(candidates, "x1e-oled")
	if err != nil || !found || selected.ABI != candidates[7].ABI {
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

// TestSelectDTBCandidateRejectsEqualRankDistinctSuffixes verifies that a
// -qcom-only identity and its -qcom-x1e counterpart fail closed instead of
// silently preferring one platform flavour over the other.
func TestSelectDTBCandidateRejectsEqualRankDistinctSuffixes(t *testing.T) {
	selected, found, err := SelectDTBCandidate([]DTBCandidate{
		{ABI: "7.2.2-jg-0sp11v2-qcom", Device: "x1e-oled", SHA256: candidateDigestA},
		{ABI: "7.2.2-jg-0sp11v2-qcom-x1e", Device: "x1e-oled", SHA256: candidateDigestA},
	}, "x1e-oled")
	if err == nil || !strings.Contains(err.Error(), "equal patch-line and generation rank") || found || selected != (DTBCandidate{}) {
		t.Fatalf("equal-rank selection = %#v, %t, %v", selected, found, err)
	}
}

// TestSelectDTBCandidateEqualRankFailsClosedRegardlessOfOrder verifies that a
// higher-ranked candidate interleaved between an equal-rank pair cannot mask
// the ambiguity, so fail-closed behaviour does not depend on input order.
func TestSelectDTBCandidateEqualRankFailsClosedRegardlessOfOrder(t *testing.T) {
	orders := [][]string{
		{"7.2.2-jg-0sp11v2-qcom", "7.2.3-jg-0sp11v1-qcom-x1e", "7.2.2-jg-0sp11v2-qcom-x1e"},
		{"7.2.2-jg-0sp11v2-qcom-x1e", "7.2.3-jg-0sp11v1-qcom-x1e", "7.2.2-jg-0sp11v2-qcom"},
	}
	for _, order := range orders {
		candidates := make([]DTBCandidate, 0, len(order))
		for _, abi := range order {
			candidates = append(candidates, DTBCandidate{ABI: abi, Device: "x1e-oled", SHA256: candidateDigestA})
		}
		selected, found, err := SelectDTBCandidate(candidates, "x1e-oled")
		if err == nil || !strings.Contains(err.Error(), "equal patch-line and generation rank") || found || selected != (DTBCandidate{}) {
			t.Fatalf("order %v selection = %#v, %t, %v", order, selected, found, err)
		}
	}
}
