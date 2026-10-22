// Package identity parses kernel ABI generations in their exact compatibility
// domain. It does not infer supported machines or compare unrelated domains.
package identity

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Identity separates the generation from its patch line, scope and flavour.
type Identity struct {
	PatchLine       string `json:"patch_line"`
	PlatformFlavour string `json:"platform_flavour"`
	Scope           string `json:"scope"`
	ABIGeneration   int    `json:"abi_generation"`
}

// patchPattern accepts canonical numeric kernel patch lines and preserves an
// upstream release candidate as a distinct, incomparable compatibility domain.
var patchPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-rc[1-9][0-9]*)?$`)

// abiPattern accepts the maintained legacy and Ubuntu package-derived ABI forms.
var abiPattern = regexp.MustCompile(`^((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-rc[1-9][0-9]*)?)-(?:jg-)?(?:0|[1-9][0-9]*)?(sp11|sl7|x1e|x1p|x1)v([1-9][0-9]*)-(qcom-x1e)$`)

// ValidDomain validates a manifest's registered generation domain.
func ValidDomain(patchLine, flavour, scope string) bool {
	if len(patchLine) > 64 || !patchPattern.MatchString(patchLine) || flavour != "qcom-x1e" {
		return false
	}
	switch scope {
	case "sp11", "sl7", "x1e", "x1p", "x1":
		return true
	}
	return false
}

// Parse accepts one complete canonical running/module ABI without package
// iteration or prerelease suffixes. Those are package identities, not ABIs.
func Parse(abi string) (Identity, error) {
	if len(abi) > 160 {
		return Identity{}, errors.New("kernel ABI exceeds bound")
	}
	parts := abiPattern.FindStringSubmatch(abi)
	if parts == nil || !ValidDomain(parts[1], parts[4], parts[2]) {
		return Identity{}, fmt.Errorf("malformed or unregistered kernel ABI %q", abi)
	}
	generation, err := strconv.Atoi(parts[3])
	if err != nil || generation < 1 {
		return Identity{}, errors.New("kernel ABI generation is out of range")
	}
	return Identity{PatchLine: parts[1], Scope: parts[2], ABIGeneration: generation, PlatformFlavour: parts[4]}, nil
}

// CompareGeneration refuses ordering across patch lines, scopes or flavours.
func CompareGeneration(a, b Identity) (int, error) {
	if !ValidDomain(a.PatchLine, a.PlatformFlavour, a.Scope) || !ValidDomain(b.PatchLine, b.PlatformFlavour, b.Scope) || a.ABIGeneration < 1 || b.ABIGeneration < 1 {
		return 0, errors.New("invalid kernel identity")
	}
	if a.PatchLine != b.PatchLine || a.Scope != b.Scope || a.PlatformFlavour != b.PlatformFlavour {
		return 0, errors.New("kernel generations belong to different compatibility domains")
	}
	if a.ABIGeneration < b.ABIGeneration {
		return -1, nil
	}
	if a.ABIGeneration > b.ABIGeneration {
		return 1, nil
	}
	return 0, nil
}

// ABIFromPackage strips Ubuntu packaging iteration and prerelease metadata
// before parsing the module ABI. No ordering of Debian versions is implied.
func ABIFromPackage(version, flavour string) (string, Identity, error) {
	split := strings.LastIndexByte(version, '-')
	if split <= 0 {
		return "", Identity{}, errors.New("package version lacks revision")
	}
	upstream, revision := version[:split], version[split+1:]
	dot := strings.IndexByte(revision, '.')
	if dot <= 0 {
		return "", Identity{}, errors.New("package revision lacks iteration")
	}
	iteration := revision[dot+1:]
	if cut := strings.IndexByte(iteration, '~'); cut >= 0 {
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+]*$`).MatchString(iteration[cut+1:]) {
			return "", Identity{}, errors.New("invalid package prerelease")
		}
		iteration = iteration[:cut]
	}
	if !regexp.MustCompile(`^(0|[1-9][0-9]*)$`).MatchString(iteration) {
		return "", Identity{}, errors.New("invalid package iteration")
	}
	abi := upstream + "-" + revision[:dot] + "-" + flavour
	parsed, err := Parse(abi)
	return abi, parsed, err
}
