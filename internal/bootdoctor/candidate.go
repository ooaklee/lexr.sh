// Package bootdoctor inspects static Surface kernel boot evidence without
// executing hooks, changing GRUB state, or rewriting device trees.
package bootdoctor

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// surfaceKernelIdentityPattern mirrors the canonical hardware-doctor ABI
// shape while also admitting a same-patch-line build without an sp11 marker.
var surfaceKernelIdentityPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([A-Za-z0-9.+~_]+(?:-[A-Za-z0-9.+~_]+)*))?-qcom-x1e$`)

// surfaceGenerationPattern extracts the one optional patch-line-local Surface
// generation marker from a canonical ABI suffix.
var surfaceGenerationPattern = regexp.MustCompile(`sp11v([0-9]+)$`)

// DTBCandidate associates one installed ABI and hardware variant with the
// digest of its firmware-side device tree.
type DTBCandidate struct {
	// ABI is the complete canonical qcom-x1e kernel identity.
	ABI string `json:"abi"`
	// Device is the stable Surface hardware variant identifier.
	Device string `json:"device"`
	// SHA256 is the lowercase firmware-side device-tree digest.
	SHA256 string `json:"sha256"`
}

// kernelRank keeps an sp11 generation subordinate to the numeric patch line.
type kernelRank struct {
	// major is the numeric kernel major component.
	major uint64
	// minor is the numeric kernel minor component.
	minor uint64
	// patch is the numeric kernel patch component.
	patch uint64
	// generation is zero for a plain build and positive for an sp11 build.
	generation uint64
}

// SelectDTBCandidate returns the greatest canonical candidate for one device,
// ranking the numeric kernel patch line before the local sp11 generation.
// Malformed candidates and candidates for other devices do not compete.
func SelectDTBCandidate(candidates []DTBCandidate, device string) (DTBCandidate, bool, error) {
	var selected DTBCandidate
	var selectedRank kernelRank
	found := false
	identities := make(map[string]string)
	for _, candidate := range candidates {
		if candidate.Device != device || !canonicalDigest(candidate.SHA256) {
			continue
		}
		rank, valid := parseKernelRank(candidate.ABI)
		if !valid {
			continue
		}
		identityKey := candidate.Device + "\x00" + candidate.ABI
		if digest, exists := identities[identityKey]; exists && digest != candidate.SHA256 {
			return DTBCandidate{}, false, errors.New("equal canonical DTB identities have different SHA-256 digests")
		}
		identities[identityKey] = candidate.SHA256
		if found && rank == selectedRank && candidate.ABI != selected.ABI {
			return DTBCandidate{}, false, errors.New("canonical DTB candidates have an equal patch-line and generation rank")
		}
		if !found || rank.greaterThan(selectedRank) {
			selected = candidate
			selectedRank = rank
			found = true
		}
	}
	return selected, found, nil
}

// parseKernelRank accepts one complete canonical qcom-x1e identity and keeps
// a Surface generation meaningful only within its numeric kernel patch line.
func parseKernelRank(abi string) (kernelRank, bool) {
	if len(abi) == 0 || len(abi) > 128 {
		return kernelRank{}, false
	}
	matches := surfaceKernelIdentityPattern.FindStringSubmatch(abi)
	if len(matches) != 5 {
		return kernelRank{}, false
	}
	numbers := make([]uint64, 3)
	for index, value := range matches[1:4] {
		if len(value) > 1 && value[0] == '0' {
			return kernelRank{}, false
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return kernelRank{}, false
		}
		numbers[index] = parsed
	}
	suffix := matches[4]
	generationMatches := surfaceGenerationPattern.FindAllStringSubmatch(suffix, -1)
	if strings.Count(strings.ToLower(suffix), "sp11v") != len(generationMatches) || len(generationMatches) > 1 {
		return kernelRank{}, false
	}
	generation := uint64(0)
	if len(generationMatches) == 1 {
		value := generationMatches[0][1]
		if len(value) > 1 && value[0] == '0' {
			return kernelRank{}, false
		}
		parsed, err := strconv.ParseUint(value, 10, 10)
		if err != nil || parsed == 0 || parsed > 999 {
			return kernelRank{}, false
		}
		generation = parsed
	}
	return kernelRank{major: numbers[0], minor: numbers[1], patch: numbers[2], generation: generation}, true
}

// greaterThan reports whether rank sorts after another patch-line-first rank.
func (rank kernelRank) greaterThan(other kernelRank) bool {
	left := [...]uint64{rank.major, rank.minor, rank.patch, rank.generation}
	right := [...]uint64{other.major, other.minor, other.patch, other.generation}
	for index := range left {
		if left[index] != right[index] {
			return left[index] > right[index]
		}
	}
	return false
}

// canonicalDigest reports whether value is one lowercase SHA-256 encoding.
func canonicalDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return true
}
