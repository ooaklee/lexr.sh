// Package compatibility interprets authenticated component compatibility
// declarations without granting any installation or redistribution authority.
package compatibility

// Filename is the single canonical component compatibility asset.
const Filename = "lexr-component-compatibility.json"

// MaxBytes bounds complete manifest assets before decoding.
const MaxBytes = 64 << 10

// Manifest is schema v1, with alternatives kept as complete target tuples.
type Manifest struct {
	SchemaVersion int          `json:"schema_version"`
	Kind          string       `json:"kind"`
	ComponentID   string       `json:"component_id"`
	Release       string       `json:"release"`
	Capabilities  []string     `json:"capabilities"`
	Lexr          VersionRange `json:"lexr"`
	Targets       []TargetRule `json:"targets"`
}

// VersionRange distinguishes a hard release interval from recorded evidence.
type VersionRange struct {
	MinimumInclusive       string `json:"minimum_inclusive"`
	MaximumExclusive       string `json:"maximum_exclusive,omitempty"`
	TestedThroughInclusive string `json:"tested_through_inclusive"`
}

// TargetRule requires every dimension within one alternative to match.
// DeviceProfiles contains canonical IDs from the shared hardware registry.
type TargetRule struct {
	Architectures    []string          `json:"architectures"`
	DeviceProfiles   []string          `json:"device_profiles"`
	OperatingSystems []OperatingSystem `json:"operating_systems"`
	Kernels          []KernelRule      `json:"kernels"`
}

// OperatingSystem binds exact ID to its registered version comparator.
type OperatingSystem struct {
	ID             string    `json:"id"`
	VersionRanges  []OSRange `json:"version_ranges"`
	TestedVersions []string  `json:"tested_versions"`
}

// OSRange is a hard interval in one distribution's version grammar.
type OSRange struct {
	MinimumInclusive string `json:"minimum_inclusive"`
	MaximumExclusive string `json:"maximum_exclusive,omitempty"`
}

// KernelRule keeps ABI generations local to one kernel identity domain.
type KernelRule struct {
	PatchLine       string          `json:"patch_line"`
	PlatformFlavour string          `json:"platform_flavour"`
	Scope           string          `json:"scope"`
	ABIGeneration   GenerationRange `json:"abi_generation"`
}

// GenerationRange records hard and tested limits for a scoped numeric ABI.
type GenerationRange struct {
	MinimumInclusive       int `json:"minimum_inclusive"`
	MaximumExclusive       int `json:"maximum_exclusive,omitempty"`
	TestedThroughInclusive int `json:"tested_through_inclusive"`
}

// Reference is the independent repository-owned identity of canonical bytes.
type Reference struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Target records evidence for the selected root or explicit image target.
// DeviceProfile uses the canonical hardware profile ID, never a kernel alias.
type Target struct {
	LexrVersion   string `json:"lexr_version"`
	Architecture  string `json:"architecture"`
	DeviceProfile string `json:"device_profile"`
	OSID          string `json:"os_id"`
	OSVersion     string `json:"os_version"`
	KernelABI     string `json:"kernel_abi"`
}

// Decision records one deterministic assessment with a zero-based matched rule.
// TargetIndex is -1 when no complete target rule matches.
type Decision struct {
	Status      string   `json:"status"`
	TargetIndex int      `json:"target_index"`
	Reasons     []string `json:"reasons"`
	Target      Target   `json:"target"`
}

// Stable decision values are shared by install, status and producer workflows.
const (
	Tested       = "tested"
	Unverified   = "unverified"
	Incompatible = "incompatible"
	Unavailable  = "unavailable"
)
