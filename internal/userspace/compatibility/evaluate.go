package compatibility

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/kernel/identity"
)

// ReferenceFor records the complete canonical manifest's exact byte identity.
func ReferenceFor(data []byte) Reference {
	sum := sha256.Sum256(data)
	return Reference{Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
}

// Verify authenticates bytes against an independently retained authority before
// decoding any compatibility claim or accepting a component/release identity.
func Verify(data []byte, ref Reference, component, release string) (*Manifest, error) {
	if ref.Size <= 0 || ref.Size > MaxBytes || ref != ReferenceFor(data) {
		return nil, errors.New("component compatibility manifest disagrees with independent size/SHA-256 authority")
	}
	m, err := Load(data)
	if err != nil {
		return nil, err
	}
	if m.ComponentID != component || m.Release != release {
		return nil, errors.New("compatibility manifest component or release identity mismatch")
	}
	return m, nil
}

// Evaluate requires an entire rule to match and prefers tested alternatives.
// Missing identities never become compatible through an operator override.
func Evaluate(m *Manifest, target Target) Decision {
	result := Decision{Status: Unavailable, TargetIndex: -1, Reasons: []string{}, Target: target}
	if err := Validate(m); err != nil {
		result.Reasons = []string{err.Error()}
		return result
	}
	if target.Architecture == "" || target.DeviceProfile == "" || target.OSID == "" || target.OSVersion == "" || target.KernelABI == "" || target.LexrVersion == "" {
		result.Reasons = []string{"required target identity is missing"}
		return result
	}
	if len(target.Architecture) > 64 || len(target.DeviceProfile) > 128 || len(target.OSID) > 64 || !componentPattern.MatchString(target.Architecture) || !componentPattern.MatchString(target.DeviceProfile) || !componentPattern.MatchString(target.OSID) {
		result.Reasons = []string{"malformed target identity"}
		return result
	}
	if !osVersion(target.OSID, target.OSVersion) {
		result.Reasons = []string{"operating system identity has no registered version semantics or is malformed"}
		return result
	}
	kernel, err := identity.Parse(target.KernelABI)
	if err != nil {
		result.Reasons = []string{err.Error()}
		return result
	}
	version, knownVersion := runtimeVersion(target.LexrVersion)
	if knownVersion && (compareVersion(version, m.Lexr.MinimumInclusive) < 0 || m.Lexr.MaximumExclusive != "" && compareVersion(version, m.Lexr.MaximumExclusive) >= 0) {
		result.Status = Incompatible
		result.Reasons = []string{"Lexr version is outside a hard bound"}
		return result
	}
	result.Status = Incompatible
	result.Reasons = []string{"no complete target rule matches architecture, device, OS and scoped kernel bounds"}
	for index, rule := range m.Targets {
		if !slices.Contains(rule.Architectures, target.Architecture) || !slices.Contains(rule.DeviceProfiles, target.DeviceProfile) {
			continue
		}
		osMatches, osTested := false, false
		for _, system := range rule.OperatingSystems {
			if system.ID == target.OSID && withinOS(system, target.OSVersion) {
				osMatches = true
				osTested = slices.Contains(system.TestedVersions, target.OSVersion)
			}
		}
		if !osMatches {
			continue
		}
		kernelMatches, kernelTested := false, false
		for _, bound := range rule.Kernels {
			r := bound.ABIGeneration
			if kernel.PatchLine == bound.PatchLine && kernel.PlatformFlavour == bound.PlatformFlavour && kernel.Scope == bound.Scope && kernel.ABIGeneration >= r.MinimumInclusive && (r.MaximumExclusive == 0 || kernel.ABIGeneration < r.MaximumExclusive) {
				kernelMatches = true
				kernelTested = kernel.ABIGeneration <= r.TestedThroughInclusive
			}
		}
		if !kernelMatches {
			continue
		}
		reasons := []string{}
		if !knownVersion {
			reasons = append(reasons, "Lexr build identity is unverified")
		} else if compareVersion(version, m.Lexr.TestedThroughInclusive) > 0 {
			reasons = append(reasons, "Lexr version is beyond recorded evidence")
		}
		if !osTested {
			reasons = append(reasons, "operating system version is within hard bounds but untested")
		}
		if !kernelTested {
			reasons = append(reasons, "kernel generation is beyond recorded evidence")
		}
		if len(reasons) == 0 {
			return Decision{Status: Tested, TargetIndex: index, Reasons: []string{"complete target rule is within recorded evidence"}, Target: target}
		}
		if result.Status != Unverified {
			result.Status = Unverified
			result.TargetIndex = index
			result.Reasons = reasons
		}
	}
	return result
}

// RequireAllowed implements the dedicated override without weakening hard or
// unavailable decisions. Confirmation and force flags have no role here.
func RequireAllowed(decision Decision, allowUnverified bool) error {
	if decision.Status == Tested || decision.Status == Unverified && allowUnverified {
		return nil
	}
	if decision.Status == Unverified {
		return fmt.Errorf("unverified component compatibility requires --allow-unverified-compatibility: %s", strings.Join(decision.Reasons, "; "))
	}
	return fmt.Errorf("component compatibility %s: %s", decision.Status, strings.Join(decision.Reasons, "; "))
}
