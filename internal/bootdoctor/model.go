package bootdoctor

import "github.com/ooaklee/lexr.sh/internal/kernel/install"

// State is the stable severity assigned to one static boot check.
type State string

const (
	// StatePass means the inspected static evidence agrees.
	StatePass State = "pass"
	// StateNeutral means optional evidence was absent without reducing readiness.
	StateNeutral State = "neutral"
	// StateWarn means drift needs review but is outside the required boot path.
	StateWarn State = "warning"
	// StateFail means the effective or explicitly required boot path is not ready.
	StateFail State = "fail"
)

// Options selects the target root, hardware variant, and optional required ABIs.
type Options struct {
	// Root is the canonical Linux filesystem root inspected read-only.
	Root string
	// Device is x1e-oled or x1p-lcd and may be discovered on the live root.
	Device string
	// TargetABI optionally marks one target ABI as required.
	TargetABI string
	// FallbackABI optionally marks one fallback ABI as required.
	FallbackABI string
}

// Check is one stable, bounded diagnostic conclusion.
type Check struct {
	// ID is the stable machine-readable check identifier.
	ID string `json:"id"`
	// State is the check severity.
	State State `json:"state"`
	// Required reports whether this check determines readiness.
	Required bool `json:"required"`
	// ABI is the optional canonical kernel identity covered by the check.
	ABI string `json:"abi,omitempty"`
	// Detail is a bounded conclusion that does not echo raw configuration.
	Detail string `json:"detail"`
}

// DefaultSelection records the configured, saved, and resolved GRUB default.
type DefaultSelection struct {
	// Configured is the bounded GRUB_DEFAULT value or its implicit zero default.
	Configured string `json:"configured"`
	// SavedEntry is the bounded grubenv saved_entry value when present.
	SavedEntry string `json:"saved_entry,omitempty"`
	// Effective is the title or identifier used to resolve the selected entry.
	Effective string `json:"effective"`
	// EntryIndex is the resolved zero-based entry index, or nil when stale.
	EntryIndex *int `json:"entry_index,omitempty"`
	// Stale reports that the configured effective selection names no entry.
	Stale bool `json:"stale"`
}

// Entry records recognised path evidence for one normal or recovery stanza.
type Entry struct {
	// Index is the zero-based order in the bounded GRUB configuration.
	Index int `json:"index"`
	// Depth is the number of enclosing GRUB submenus.
	Depth int `json:"depth"`
	// MenuPath is the numeric GRUB selection path from the top-level menu.
	MenuPath []int `json:"menu_path"`
	// Title is the bounded menu title used by GRUB selection.
	Title string `json:"title"`
	// ID is the optional bounded menu entry identifier.
	ID string `json:"id,omitempty"`
	// ABI is the canonical ABI inferred only from a vmlinuz basename.
	ABI string `json:"abi,omitempty"`
	// Recovery reports whether the title marks the entry for recovery.
	Recovery bool `json:"recovery"`
	// Linux contains only recognised linux or linuxefi path tokens.
	Linux []install.GRUBPathToken `json:"linux,omitempty"`
	// Initrd contains only recognised initrd or initrdefi path tokens.
	Initrd []install.GRUBPathToken `json:"initrd,omitempty"`
	// DeviceTrees contains only recognised devicetree path tokens.
	DeviceTrees []install.GRUBPathToken `json:"devicetree,omitempty"`
	// UnsafeCommands names recognised commands whose path tokens were rejected.
	UnsafeCommands []string `json:"unsafe_commands,omitempty"`
	// KernelExists reports safe regular-file evidence for the kernel token.
	KernelExists bool `json:"kernel_exists"`
	// KernelState distinguishes present, missing, and permission-inaccessible evidence.
	KernelState install.GRUBPathAvailability `json:"kernel_state,omitempty"`
	// InitramfsExists reports safe regular-file evidence for the initramfs token.
	InitramfsExists bool `json:"initramfs_exists"`
	// InitramfsState distinguishes present, missing, and permission-inaccessible evidence.
	InitramfsState install.GRUBPathAvailability `json:"initramfs_state,omitempty"`
	// BootDTBState classifies the boot-side devicetree path when recognised.
	BootDTBState install.GRUBPathAvailability `json:"boot_dtb_state,omitempty"`
	// InstalledDTBState classifies the same-ABI firmware-side DTB evidence.
	InstalledDTBState install.GRUBPathAvailability `json:"installed_dtb_state,omitempty"`
	// BootDTBSHA256 is the exact digest of the referenced boot-side DTB.
	BootDTBSHA256 string `json:"boot_dtb_sha256,omitempty"`
	// InstalledDTBSHA256 is the exact same-ABI firmware-side digest.
	InstalledDTBSHA256 string `json:"installed_dtb_sha256,omitempty"`
	// DTBMatches reports byte equality when both DTB digests were available.
	DTBMatches *bool `json:"dtb_matches,omitempty"`
}

// DTBAttribution records patch-line-first selection and observed shared-DTB bytes.
type DTBAttribution struct {
	// SelectedABI is the highest canonical installed candidate for this device.
	SelectedABI string `json:"selected_abi,omitempty"`
	// AttributedABI is the highest canonical candidate matching the boot digest.
	AttributedABI string `json:"attributed_abi,omitempty"`
	// BootSHA256 is the effective entry's exact boot-side digest.
	BootSHA256 string `json:"boot_sha256,omitempty"`
	// InstalledSHA256 is the selected ABI's exact firmware-side digest.
	InstalledSHA256 string `json:"installed_sha256,omitempty"`
}

// HookEvidence reports only the presence of the retired helper and hook paths.
type HookEvidence struct {
	// RetiredHelper reports /usr/local/sbin/sp11-grub-inject-dtb presence.
	RetiredHelper bool `json:"retired_helper"`
	// PostInstallHook reports the retired post-install hook presence.
	PostInstallHook bool `json:"postinst_hook"`
	// PostRemoveHook reports the retired post-remove hook presence.
	PostRemoveHook bool `json:"postrm_hook"`
}

// Report is one complete point-in-time, strictly read-only boot diagnosis.
type Report struct {
	// Ready reports whether every effective or explicitly required check passed.
	Ready bool `json:"ready"`
	// Device is the selected short Surface hardware variant.
	Device string `json:"device"`
	// TargetABI is the optional explicitly required target ABI.
	TargetABI string `json:"target_abi,omitempty"`
	// FallbackABI is the optional explicitly required fallback ABI.
	FallbackABI string `json:"fallback_abi,omitempty"`
	// Default records GRUB default resolution.
	Default DefaultSelection `json:"default"`
	// Entries contains normal and recovery stanza evidence.
	Entries []Entry `json:"entries"`
	// Attribution records shared boot-DTB candidate and digest evidence.
	Attribution DTBAttribution `json:"dtb_attribution"`
	// Hooks records attribution evidence without executing any helper.
	Hooks HookEvidence `json:"legacy_hooks"`
	// Checks contains stable diagnostic conclusions.
	Checks []Check `json:"checks"`
	// PhysicalBootability states the unavoidable static-evidence limitation.
	PhysicalBootability string `json:"physical_bootability"`
}
