// Package bootdoctor inspects static Surface kernel boot evidence without
// executing hooks, changing GRUB state, or rewriting device trees.
package bootdoctor

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// qcomPlatformSuffixes lists the platform flavour suffixes, longest first,
// that one canonical Qualcomm kernel identity may end with. The suffix is
// peeled before matching so it can never be captured as version text.
var qcomPlatformSuffixes = []string{"-qcom-x1e", "-qcom"}

// qcomPlatformSegments are version-suffix segments that always belong to the
// platform flavour. Any of them remaining in the peeled version core marks
// the identity as malformed, so platform text is never ranked as a version.
var qcomPlatformSegments = map[string]bool{"qcom": true, "x1e": true}

// qcomKernelIdentityPattern accepts one versioned kernel identity core,
// mirroring the canonical hardware-doctor ABI shape while also admitting a
// same-patch-line build without an sp11 marker. The optional platform
// flavour suffix (-qcom, -qcom-x1e) or no suffix at all is accepted.
var qcomKernelIdentityPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([A-Za-z0-9.+~_]+(?:-[A-Za-z0-9.+~_]+)*))?$`)

// surfaceGenerationPattern extracts the one optional patch-line-local Surface
// generation marker from a canonical version suffix.
var surfaceGenerationPattern = regexp.MustCompile(`sp11v([0-9]+)$`)

// DTBCandidate associates one installed ABI and hardware variant with the
// digest of its firmware-side device tree.
type DTBCandidate struct {
	// ABI is the complete canonical qcom kernel identity.
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
// Two distinct ABIs sharing one rank always fail closed regardless of input
// order.
func SelectDTBCandidate(candidates []DTBCandidate, device string) (DTBCandidate, bool, error) {
	var selected DTBCandidate
	var selectedRank kernelRank
	found := false
	identities := make(map[string]string)
	ranks := make(map[kernelRank]string)
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
		if existing, exists := ranks[rank]; exists && existing != candidate.ABI {
			return DTBCandidate{}, false, errors.New("canonical DTB candidates have an equal patch-line and generation rank")
		}
		ranks[rank] = candidate.ABI
		if !found || rank.greaterThan(selectedRank) {
			selected = candidate
			selectedRank = rank
			found = true
		}
	}
	return selected, found, nil
}

// parseKernelRank accepts one complete canonical qcom kernel identity, with
// or without the platform flavour suffix, and keeps a Surface generation
// meaningful only within its numeric kernel patch line.
func parseKernelRank(abi string) (kernelRank, bool) {
	if len(abi) == 0 || len(abi) > 128 {
		return kernelRank{}, false
	}
	core := abi
	for _, suffix := range qcomPlatformSuffixes {
		if strings.HasSuffix(core, suffix) {
			core = strings.TrimSuffix(core, suffix)
			break
		}
	}
	for _, segment := range strings.Split(core, "-") {
		if qcomPlatformSegments[segment] {
			return kernelRank{}, false
		}
	}
	matches := qcomKernelIdentityPattern.FindStringSubmatch(core)
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
