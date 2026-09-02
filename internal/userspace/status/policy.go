package status

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Catalogue component identifiers used by static diagnostics are kept stable
// so report consumers can join checks to catalogue entries.
const (
	firmwareComponent  = "firmware"
	wifiComponent      = "wifi"
	bluetoothComponent = "bluetooth"
	audioComponent     = "audio-fullio-v19c"
	iptsdComponent     = "iptsd-v1"
	g6PenComponent     = "g6-pen"
	ootTouchComponent  = "oot-touchscreen"
	cameraComponent    = "imx681-libcamera-v1"
	powerComponent     = "power-profiles"
)

// sp11Generation finds every versioned Surface integration marker in a
// qcom-x1e kernel ABI so ambiguous names can be rejected.
var sp11Generation = regexp.MustCompile(`sp11v([0-9]+)(?:[-._+]|$)`)

// kernelCompatibilityIdentity separates the upstream patch line from its
// patch-line-local Surface release generation. A maintained patch line may
// restart at sp11v1, so the generation cannot be compared on its own.
var kernelCompatibilityIdentity = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)-(?:[A-Za-z0-9.+~_]+-)*[A-Za-z0-9.+~_]*sp11v([0-9]+)-qcom-x1e$`)

const (
	// legacyKernelPatchLine identifies the line covered by current userspace evidence.
	legacyKernelPatchLine = "7.2.0"
	// newKernelPatchLine identifies the line whose Surface generation restarted.
	newKernelPatchLine = "7.2.2"
	// newKernelMinimumSP11 is the first release in the newer local sequence.
	newKernelMinimumSP11 = 1
)

// kernelIdentity keeps a Surface generation meaningful only within its patch line.
type kernelIdentity struct {
	patchLine  string
	generation int
}

// builtInComponentPolicies are the safe production fallback for direct package
// callers. The manager strictly cross-checks these values against its catalogue.
var builtInComponentPolicies = []ComponentPolicy{
	{ID: firmwareComponent, Feature: FeatureFirmware, SupportLevel: SupportRequired},
	{ID: wifiComponent, Feature: FeatureWiFi, SupportLevel: SupportSupported},
	{ID: bluetoothComponent, Feature: FeatureBluetooth, SupportLevel: SupportSupported},
	{ID: audioComponent, Feature: FeatureAudio, SupportLevel: SupportSupported, MinimumSP11Generation: 12, TestedThroughSP11Generation: 19},
	{ID: iptsdComponent, Feature: FeatureIPTSD, SupportLevel: SupportSupported, MinimumSP11Generation: 19, TestedThroughSP11Generation: 19},
	{ID: g6PenComponent, Feature: FeatureG6Pen, SupportLevel: SupportDiagnosticOnly},
	{ID: ootTouchComponent, Feature: FeatureTouch, SupportLevel: SupportObsolete},
	{ID: cameraComponent, Feature: FeatureCamera, SupportLevel: SupportExperimental, MinimumSP11Generation: 14, TestedThroughSP11Generation: 19},
	{ID: powerComponent, Feature: FeaturePower, SupportLevel: SupportSupported, MinimumSP11Generation: 8, TestedThroughSP11Generation: 19},
}

// policySet provides immutable component lookup and records whether the caller
// explicitly selected a diagnostic feature.
type policySet struct {
	byID              map[string]ComponentPolicy
	explicitSelection bool
}

// newPolicySet validates caller-supplied policy or uses the built-in production
// contracts when no catalogue projection was supplied.
func newPolicySet(policies []ComponentPolicy, explicitSelection bool) (policySet, error) {
	if len(policies) == 0 {
		policies = builtInComponentPolicies
	}
	set := policySet{byID: make(map[string]ComponentPolicy, len(policies)), explicitSelection: explicitSelection}
	for _, policy := range policies {
		compiled, known := builtInPolicy(policy.ID)
		if !known {
			return policySet{}, fmt.Errorf("unsupported userspace component policy %q", policy.ID)
		}
		if policy.Feature == "" || !validSupportLevel(policy.SupportLevel) {
			return policySet{}, fmt.Errorf("invalid userspace component policy %q", policy.ID)
		}
		if policy.Feature != compiled.Feature || policy.SupportLevel != compiled.SupportLevel ||
			policy.MinimumSP11Generation != compiled.MinimumSP11Generation ||
			policy.TestedThroughSP11Generation != compiled.TestedThroughSP11Generation {
			return policySet{}, fmt.Errorf("userspace component policy %q disagrees with compiled diagnostic policy", policy.ID)
		}
		if policy.MinimumSP11Generation < 0 || policy.TestedThroughSP11Generation < policy.MinimumSP11Generation {
			return policySet{}, fmt.Errorf("invalid kernel compatibility boundary for userspace component %q", policy.ID)
		}
		if _, duplicate := set.byID[policy.ID]; duplicate {
			return policySet{}, fmt.Errorf("duplicate userspace component policy %q", policy.ID)
		}
		set.byID[policy.ID] = policy
	}
	missingPolicies := make([]string, 0)
	for _, requiredPolicy := range builtInComponentPolicies {
		if _, found := set.byID[requiredPolicy.ID]; !found {
			missingPolicies = append(missingPolicies, requiredPolicy.ID)
		}
	}
	if len(missingPolicies) != 0 {
		sort.Strings(missingPolicies)
		return policySet{}, fmt.Errorf("userspace component policy is missing %s", strings.Join(missingPolicies, ", "))
	}
	return set, nil
}

// builtInPolicy returns the compiled contract for one known catalogue component.
func builtInPolicy(componentID string) (ComponentPolicy, bool) {
	for _, policy := range builtInComponentPolicies {
		if policy.ID == componentID {
			return policy, true
		}
	}
	return ComponentPolicy{}, false
}

// validSupportLevel checks the closed maturity vocabulary shared with the
// userspace catalogue.
func validSupportLevel(level SupportLevel) bool {
	switch level {
	case SupportRequired, SupportSupported, SupportExperimental, SupportDiagnosticOnly, SupportObsolete:
		return true
	default:
		return false
	}
}

// component returns a required policy and treats a missing compiled mapping as
// an internal configuration error.
func (set policySet) component(componentID string) ComponentPolicy {
	return set.byID[componentID]
}

// required reports whether a component failure should block this report.
// Catalogue-required components always block; an explicitly selected supported
// or experimental feature also blocks so automation receives a useful status.
func (set policySet) required(componentID string) bool {
	policy := set.component(componentID)
	return supportLevelBlocksReadiness(policy.SupportLevel, set.explicitSelection)
}

// supportLevelBlocksReadiness implements the documented severity policy:
// required always blocks; supported and experimental block only when selected;
// diagnostic-only and obsolete never block readiness.
func supportLevelBlocksReadiness(level SupportLevel, explicitlySelected bool) bool {
	if level == SupportRequired {
		return true
	}
	if !explicitlySelected {
		return false
	}
	return level == SupportSupported || level == SupportExperimental
}

// decorate attaches stable catalogue identity and maturity to a check while
// preserving its observed state and detail.
func (set policySet) decorate(check Check, componentID string) Check {
	policy := set.component(componentID)
	check.ComponentID = policy.ID
	check.SupportLevel = policy.SupportLevel
	check.Required = set.required(componentID)
	return check
}

// inspectKernelCompatibility compares one selected Surface kernel ABI with the
// catalogue-backed minimum and tested-through generations for a component.
func (set policySet) inspectKernelCompatibility(componentID, abi string) Check {
	policy := set.component(componentID)
	required := set.required(componentID)
	check := Check{
		ID:           "kernel-compatibility-" + componentID,
		Feature:      policy.Feature,
		ComponentID:  policy.ID,
		SupportLevel: policy.SupportLevel,
		Required:     required,
		Remediation:  fmt.Sprintf("install an sp11v%d or newer Surface qcom-x1e kernel before using %s", policy.MinimumSP11Generation, policy.ID),
	}
	if abi == "" {
		check.State = optionalState(required)
		check.Detail = fmt.Sprintf("%s requires sp11v%d or newer, but no installed Surface kernel ABI was selected", policy.ID, policy.MinimumSP11Generation)
		return check
	}
	identity, found := parseKernelCompatibilityIdentity(abi)
	if !found {
		check.State = optionalState(required)
		check.Detail = fmt.Sprintf("%s requires an unambiguous patch-line-qualified Surface kernel; selected ABI %s is not a valid <major>.<minor>.<patch>-...sp11vN-qcom-x1e identity", policy.ID, abi)
		check.Remediation = "select an unambiguous Surface qcom-x1e ABI from a supported kernel patch line"
		return check
	}
	if identity.patchLine == newKernelPatchLine {
		if identity.generation < newKernelMinimumSP11 {
			check.State = optionalState(required)
			check.Detail = fmt.Sprintf("%s requires kernel %s/sp11v%d or newer on that patch line; selected ABI %s is %s/sp11v%d", policy.ID, newKernelPatchLine, newKernelMinimumSP11, abi, identity.patchLine, identity.generation)
			check.Remediation = fmt.Sprintf("install a %s/sp11v%d or newer Surface qcom-x1e kernel before using %s", newKernelPatchLine, newKernelMinimumSP11, policy.ID)
			return check
		}
		check.State = StateWarn
		check.Detail = fmt.Sprintf("%s is only userspace-qualified on kernel %s through sp11v%d; selected ABI %s uses the newer %s/sp11v%d patch-line pairing, which is not yet userspace-qualified", policy.ID, legacyKernelPatchLine, policy.TestedThroughSP11Generation, abi, identity.patchLine, identity.generation)
		check.Remediation = "validate this newer kernel and userspace pairing before relying on it"
		return check
	}
	if identity.patchLine != legacyKernelPatchLine {
		check.State = optionalState(required)
		check.Detail = fmt.Sprintf("%s has no compatibility policy for kernel patch line %s; selected ABI is %s", policy.ID, identity.patchLine, abi)
		check.Remediation = "select a kernel from an explicitly supported patch line or add compatibility evidence before relying on this pairing"
		return check
	}
	generation := identity.generation
	if generation < policy.MinimumSP11Generation {
		check.State = optionalState(required)
		check.Detail = fmt.Sprintf("%s requires %s/sp11v%d or newer; selected ABI %s is %s/sp11v%d", policy.ID, legacyKernelPatchLine, policy.MinimumSP11Generation, abi, identity.patchLine, generation)
		return check
	}
	if generation > policy.TestedThroughSP11Generation {
		check.State = StateWarn
		check.Detail = fmt.Sprintf("%s supports %s/sp11v%d or newer but is only tested through sp11v%d; selected ABI %s is %s/sp11v%d", policy.ID, legacyKernelPatchLine, policy.MinimumSP11Generation, policy.TestedThroughSP11Generation, abi, identity.patchLine, generation)
		check.Remediation = "consult the current component catalogue before relying on this newer kernel pairing"
		return check
	}
	check.State = StatePass
	check.Detail = fmt.Sprintf("%s supports selected ABI %s (minimum %s/sp11v%d; tested through sp11v%d)", policy.ID, abi, legacyKernelPatchLine, policy.MinimumSP11Generation, policy.TestedThroughSP11Generation)
	check.Remediation = ""
	return check
}

// parseKernelCompatibilityIdentity requires one complete, canonical kernel ABI
// and returns the release generation in the context of its patch line.
func parseKernelCompatibilityIdentity(abi string) (kernelIdentity, bool) {
	matches := kernelCompatibilityIdentity.FindStringSubmatch(abi)
	if len(matches) != 5 || strings.Count(strings.ToLower(abi), "sp11v") != 1 {
		return kernelIdentity{}, false
	}
	for _, numeric := range matches[1:] {
		if len(numeric) > 1 && numeric[0] == '0' {
			return kernelIdentity{}, false
		}
	}
	generation, err := strconv.Atoi(matches[4])
	if err != nil || generation < 0 || generation > 999 {
		return kernelIdentity{}, false
	}
	return kernelIdentity{
		patchLine:  strings.Join(matches[1:4], "."),
		generation: generation,
	}, true
}

// parseSP11Generation extracts a bounded integer generation from an ABI marker.
func parseSP11Generation(abi string) (int, bool) {
	matches := sp11Generation.FindAllStringSubmatch(abi, -1)
	if len(matches) != 1 || len(matches[0]) != 2 {
		return 0, false
	}
	generation, err := strconv.Atoi(matches[0][1])
	if err != nil || generation < 1 || generation > 999 {
		return 0, false
	}
	return generation, true
}
