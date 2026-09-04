// Package install performs a guarded, native Debian kernel installation.
//
// The package deliberately installs only a coherent Surface Pro 11 kernel
// package set. It does not install historical out-of-tree touchscreen modules,
// firmware copies, GRUB hooks, or other broad support workarounds.
package install

import (
	"context"
	"time"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// Operation identifies one bounded stage in a kernel installation.
type Operation string

const (
	// OperationInspectPackage reads bounded Debian metadata without installation.
	OperationInspectPackage Operation = "inspect-package"
	// OperationInspectRunningABI reads the live system's exact running ABI.
	OperationInspectRunningABI Operation = "inspect-running-abi"
	// OperationInstallPackages installs only the verified local Debian packages.
	OperationInstallPackages Operation = "install-packages"
	// OperationUpdateInitramfs refreshes the initramfs for the exact target ABI.
	OperationUpdateInitramfs Operation = "update-initramfs"
	// OperationEnsureInitramfs regenerates a missing initramfs after a staged
	// install whose maintainer scripts did not produce the ABI image.
	OperationEnsureInitramfs Operation = "ensure-initramfs"
	// OperationRollbackPackages purges only target packages from a failed run.
	OperationRollbackPackages Operation = "rollback-packages"
)

// Request contains every caller-selected input used to prepare an installation.
type Request struct {
	// Bundle is the already acquired and hashed Surface kernel package set.
	Bundle kernel.Bundle
	// Root is the explicit absolute filesystem root that will receive the kernel.
	Root string
	// FallbackABI is the distinct ABI that must be verified and remain usable.
	// It normally matches the running ABI unless ForceFallbackMismatch is set.
	FallbackABI string
	// ForceFallbackMismatch permits a verified fallback ABI to differ from the
	// running ABI. It does not bypass any fallback bootability checks.
	ForceFallbackMismatch bool
	// RunningABI supplies uname evidence only for an alternate-root fixture.
	// It must be empty when Root is the live system root.
	RunningABI string
	// DryRun performs the complete read-only preflight without privileged changes.
	DryRun bool
	// AllowUnverified explicitly accepts locally hashed packages that were not
	// covered by an authoritative checksum manifest.
	AllowUnverified bool
	// Overwrite explicitly replaces an already installed complete or partial
	// target ABI. It never bypasses fallback bootability checks, and the
	// running or fallback ABI is never removed or replaced by this request.
	Overwrite bool
}

// Command describes one direct process invocation without shell interpretation.
type Command struct {
	// Operation states why the executable is needed.
	Operation Operation `json:"operation"`
	// Name is the fixed executable name selected by this package.
	Name string `json:"name"`
	// Args contains separately bounded arguments passed to the executable.
	Args []string `json:"args"`
}

// Package records immutable package bytes and inspected Debian metadata.
type Package struct {
	// Role states the package's exact place in the allow-listed transaction.
	Role kernel.PackageRole `json:"role"`
	// Name is the validated Debian package filename.
	Name string `json:"name"`
	// Path is the canonical, non-symlink source path reviewed by the caller.
	Path string `json:"path"`
	// SHA256 is the lowercase digest of the complete package bytes.
	SHA256 string `json:"sha256"`
	// Size is the complete package length in bytes.
	Size int64 `json:"size_bytes"`
	// DebianPackage is the Package field read with dpkg-deb.
	DebianPackage string `json:"debian_package"`
	// Version is the Debian package version shared by the transaction.
	Version string `json:"version"`
	// Architecture is arm64 for runtime/flavour headers and all for common headers.
	Architecture string `json:"architecture"`
	// Depends is the bounded dependency expression read from the package.
	Depends string `json:"depends,omitempty"`
	// PublisherVerified reports whether the input bundle had authoritative hashes.
	PublisherVerified bool `json:"publisher_verified"`
}

// DeviceTree records one exact DTB required after package installation.
type DeviceTree struct {
	// Device is the stable Surface Pro 11 hardware variant identifier.
	Device string `json:"device"`
	// RelativePath is the compiled device-tree path beneath the ABI directory.
	RelativePath string `json:"relative_path"`
	// TargetPath is the absolute target-root path checked after installation.
	TargetPath string `json:"target_path"`
}

// HeaderEvidence proves that one selected development-header package left its
// exact, non-symlink source tree and a non-empty top-level Makefile installed.
type HeaderEvidence struct {
	// Role distinguishes the ABI-specific and common development-header trees.
	Role kernel.PackageRole `json:"role"`
	// DebianPackage is the exact package name whose successful command is being
	// corroborated by filesystem evidence.
	DebianPackage string `json:"debian_package"`
	// TreePath is the exact non-symlink directory installed beneath /usr/src.
	TreePath string `json:"tree_path"`
	// Marker is the hashed, non-empty top-level Makefile inside TreePath.
	Marker FileEvidence `json:"marker"`
}

// FileEvidence records the identity of one safety-critical regular file.
type FileEvidence struct {
	// Kind distinguishes kernel images, initramfs files, module indexes, and DTBs.
	Kind string `json:"kind"`
	// Path is the absolute path beneath the selected target root.
	Path string `json:"path"`
	// SHA256 is the lowercase digest of the complete file.
	SHA256 string `json:"sha256"`
	// Size is the complete file length in bytes.
	Size int64 `json:"size_bytes"`
}

// BootEvidence proves that an ABI has a usable boot and module baseline.
type BootEvidence struct {
	// ABI is the exact Surface kernel ABI represented by the evidence.
	ABI string `json:"abi"`
	// KernelImage identifies the non-empty kernel image.
	KernelImage FileEvidence `json:"kernel_image"`
	// Initramfs identifies the non-empty matching initramfs.
	Initramfs FileEvidence `json:"initramfs"`
	// SystemMap identifies the non-empty symbol map supplied for the ABI.
	SystemMap FileEvidence `json:"system_map"`
	// KernelConfig identifies the non-empty packaged kernel configuration.
	KernelConfig FileEvidence `json:"kernel_config"`
	// ModulesDependencyIndex identifies the non-empty modules.dep file.
	ModulesDependencyIndex FileEvidence `json:"modules_dependency_index"`
	// ModuleTree is the canonical directory containing at least one kernel module.
	ModuleTree string `json:"module_tree"`
	// ModuleFile identifies one non-empty regular module proving the tree is populated.
	ModuleFile FileEvidence `json:"module_file"`
	// GRUBEntryCount is the number of matching non-recovery boot entries.
	GRUBEntryCount int `json:"grub_entry_count"`
	// DeviceTreeBoot records the verified boot-time DTB delivery path shared by
	// every matching normal and recovery GRUB entry.
	DeviceTreeBoot DeviceTreeBootEvidence `json:"device_tree_boot"`
}

// DeviceTreeBootMode identifies how a verified kernel receives its boot-time
// device tree without conflating installed firmware files with boot evidence.
type DeviceTreeBootMode string

const (
	// DeviceTreeBootEmbedded means the kernel PE contains one exact required
	// same-ABI DTB in a .dtbauto section.
	DeviceTreeBootEmbedded DeviceTreeBootMode = "embedded"
	// DeviceTreeBootExternal means every matching GRUB entry references one
	// exact required same-ABI external DTB.
	DeviceTreeBootExternal DeviceTreeBootMode = "grub-external"
)

// DeviceTreeBootEvidence records the bounded result of boot-time DTB
// verification. SHA256 identifies either the embedded payload or the
// boot-side external DTB; GRUBEntryCount includes normal and recovery entries.
type DeviceTreeBootEvidence struct {
	// Mode identifies the verified embedded or external delivery path.
	Mode DeviceTreeBootMode `json:"mode"`
	// SHA256 is the exact digest shared by every verified boot-time DTB path.
	SHA256 string `json:"sha256"`
	// GRUBEntryCount is the number of matching normal and recovery entries
	// covered by this evidence.
	GRUBEntryCount int `json:"grub_entry_count"`
}

// GRUBPathToken records one bounded boot artefact path without retaining any
// unrelated kernel arguments from its source stanza.
type GRUBPathToken struct {
	// Command is the recognised GRUB command that introduced the path.
	Command string `json:"command"`
	// Path is the single normalised path token supplied to that command.
	Path string `json:"path"`
}

// GRUBPathAvailability classifies bounded filesystem evidence for one safe
// GRUB path without conflating a missing file with permission-denied access.
type GRUBPathAvailability string

const (
	// GRUBPathPresent means the token resolved to non-empty regular evidence.
	GRUBPathPresent GRUBPathAvailability = "present"
	// GRUBPathMissing means every safe resolution was absent.
	GRUBPathMissing GRUBPathAvailability = "missing"
	// GRUBPathInaccessible means permission denied prevented verification.
	GRUBPathInaccessible GRUBPathAvailability = "inaccessible"
)

// GRUBEntry records only the bounded, non-sensitive fields needed to inspect
// one normal or recovery menu entry.
type GRUBEntry struct {
	// Index is the zero-based order of the menu entry in the parsed file.
	Index int `json:"index"`
	// Depth is the number of enclosing GRUB submenus.
	Depth int `json:"depth"`
	// MenuPath is the numeric GRUB selection path from the top-level menu.
	MenuPath []int `json:"menu_path"`
	// MenuTitlePath is the title-based GRUB selection path through any submenus.
	MenuTitlePath []string `json:"menu_title_path"`
	// Title is the bounded GRUB menu title used for default selection.
	Title string `json:"title"`
	// ID is the optional bounded menu entry identifier.
	ID string `json:"id,omitempty"`
	// Recovery reports whether the title marks this as a recovery entry.
	Recovery bool `json:"recovery"`
	// Linux contains recognised linux and linuxefi path tokens.
	Linux []GRUBPathToken `json:"linux,omitempty"`
	// Initrd contains recognised initrd and initrdefi path tokens.
	Initrd []GRUBPathToken `json:"initrd,omitempty"`
	// DeviceTrees contains recognised devicetree path tokens.
	DeviceTrees []GRUBPathToken `json:"devicetree,omitempty"`
	// UnsafeCommands names recognised commands whose path token was rejected.
	UnsafeCommands []string `json:"unsafe_commands,omitempty"`
}

// Plan is the complete read-only result that must be reviewed before mutation.
type Plan struct {
	// Root is the canonical target filesystem root.
	Root string `json:"root"`
	// TargetABI is the distinct Surface kernel ABI selected for installation.
	TargetABI string `json:"target_abi"`
	// FallbackABI is the verified, preserved Surface kernel ABI.
	FallbackABI string `json:"fallback_abi"`
	// RunningABI records the trusted live or fixture uname evidence.
	RunningABI string `json:"running_abi"`
	// FallbackMismatchForced reports that the caller explicitly allowed the
	// verified fallback ABI to differ from the running ABI.
	FallbackMismatchForced bool `json:"fallback_mismatch_forced"`
	// Warnings records non-fatal safety information retained in JSON receipts.
	Warnings []string `json:"warnings,omitempty"`
	// Version is the coherent Debian version shared by all selected packages.
	Version string `json:"version"`
	// DryRun reports whether execution was intentionally disabled.
	DryRun bool `json:"dry_run"`
	// UnverifiedAccepted reports that the caller explicitly accepted local trust.
	UnverifiedAccepted bool `json:"unverified_accepted"`
	// Overwrite reports that the caller explicitly allowed replacing an
	// existing complete or partial target ABI installation.
	Overwrite bool `json:"overwrite"`
	// TargetState records the read-only fresh-target classification evidence.
	TargetState *TargetStateEvidence `json:"target_state,omitempty"`
	// Packages is the exact allow-listed, metadata-verified transaction.
	Packages []Package `json:"packages"`
	// DeviceTrees lists the exact DTBs that the installed modules must provide.
	DeviceTrees []DeviceTree `json:"device_trees"`
	// Fallback captures the bootable recovery kernel before any mutation.
	Fallback BootEvidence `json:"fallback"`
	// Commands previews the direct commands using reviewed source package paths.
	Commands []Command `json:"commands"`
	// ConditionalCommands previews bounded repair commands the manager may run
	// after installation when filesystem evidence shows the target ABI's
	// initramfs image missing. They are disclosed here because a dry run
	// cannot know whether the maintainer scripts will produce the image.
	ConditionalCommands []Command `json:"conditional_commands,omitempty"`
}

// RollbackReceipt records best-effort recovery after a failed package operation.
type RollbackReceipt struct {
	// Attempted reports whether any potentially mutating command had started.
	Attempted bool `json:"attempted"`
	// Commands contains the exact direct recovery commands that were attempted.
	Commands []Command `json:"commands,omitempty"`
	// GRUBRestored reports whether the pre-transaction GRUB bytes were restored.
	GRUBRestored bool `json:"grub_restored"`
	// Error contains a bounded joined diagnostic when recovery was incomplete.
	Error string `json:"error,omitempty"`
}

// Receipt records the reviewed plan, executed commands, and final boot evidence.
type Receipt struct {
	// Plan is the immutable preflight result used by this execution.
	Plan Plan `json:"plan"`
	// StartedAt records when the manager began the requested operation.
	StartedAt time.Time `json:"started_at"`
	// CompletedAt records when the manager returned its final result.
	CompletedAt time.Time `json:"completed_at"`
	// Executed contains only commands that were actually handed to the runner.
	Executed []Command `json:"executed,omitempty"`
	// Installed captures the verified target ABI after successful installation.
	Installed *BootEvidence `json:"installed,omitempty"`
	// DeviceTrees contains verified installed DTB evidence.
	DeviceTrees []FileEvidence `json:"device_trees,omitempty"`
	// Headers contains post-install evidence for every selected development-header package.
	Headers []HeaderEvidence `json:"headers,omitempty"`
	// Rollback records recovery work after a failed mutating operation.
	Rollback *RollbackReceipt `json:"rollback,omitempty"`
	// RebootRequired is true only after a successful non-dry-run installation.
	RebootRequired bool `json:"reboot_required"`
}

// Manager owns package inspection, privilege enforcement, command execution,
// and post-install verification.
type Manager struct {
	// runner is the injectable direct-process boundary.
	runner platform.Runner
	// effectiveUID returns the process privilege identity.
	effectiveUID func() int
	// now supplies receipt timestamps.
	now func() time.Time
}

// New constructs a native kernel installation manager.
func New(runner platform.Runner) *Manager {
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	return &Manager{
		runner:       runner,
		effectiveUID: effectiveUserID,
		now:          time.Now,
	}
}

// Preflight performs every read-only package, fallback, path, and boot check.
func (manager *Manager) Preflight(ctx context.Context, request Request) (Plan, error) {
	return manager.prepare(ctx, request)
}
