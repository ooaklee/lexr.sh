// Package fedora implements the Fedora Workstation EROFS live-media adapter.
package fedora

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/plan"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

const (
	// AdapterID is the stable manifest and catalogue identifier.
	AdapterID = "fedora-live"
	// SourceVolumeID is the publisher label used by Fedora 44 dracut-live.
	SourceVolumeID = "Fedora-WS-Live-44"
	// secureBootPolicy is embedded verbatim so generators and validators agree.
	secureBootPolicy = "unsupported; disable Secure Boot for the unsigned custom Stubble kernel"
	// x1pQualificationStatus avoids claiming an unverified custom Stubble identity.
	x1pQualificationStatus = "live-only stock-kernel path with external DTB; installed-system support not qualified"
)

// bootPolicy is the strict on-media split between persistent and USB-only arguments.
type bootPolicy struct {
	SchemaVersion int      `json:"schema_version"`
	KernelABI     string   `json:"kernel_abi"`
	Installed     []string `json:"installed_arguments"`
	LiveOnly      []string `json:"live_only_arguments"`
	X1PStatus     string   `json:"x1p_status"`
}

// expectedBootPolicy returns the one policy accepted for a generated Fedora image.
func expectedBootPolicy(abi string) bootPolicy {
	return bootPolicy{
		SchemaVersion: 1,
		KernelABI:     abi,
		Installed:     append([]string(nil), installedBootArguments...),
		LiveOnly:      append([]string(nil), liveOnlyBootArguments...),
		X1PStatus:     x1pQualificationStatus,
	}
}

// kernelABIExpression extracts a complete kernel patch line and its local
// Surface release generation. The generation is deliberately not treated as a
// global counter: a maintained patch line can restart at sp11v1.
var kernelABIExpression = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)-(?:[A-Za-z0-9.+~_]+-)*[A-Za-z0-9.+~_]*sp11v([0-9]+)-qcom-x1e$`)

// kernelPatchLine identifies one explicitly qualified upstream kernel base.
type kernelPatchLine struct {
	major uint64
	minor uint64
	patch uint64
}

// patchLineMinimumKernelGenerations is an explicit allowlist. Each patch line
// has its own release sequence, so a high generation on an unknown patch line
// does not imply Fedora compatibility.
var patchLineMinimumKernelGenerations = map[kernelPatchLine]uint64{
	{major: 7, minor: 2, patch: 0}: 19,
	{major: 7, minor: 2, patch: 2}: 1,
}

// Request contains verified inputs and output policy for one Fedora remaster.
type Request struct {
	SourceISO          string
	SourceSHA256       string
	OutputISO          string
	Bundle             kernel.Bundle
	ToolVersion        string
	Companion          companion.BuildRequest
	CompanionUserspace []string
	WorkspaceRoot      string
	KeepWorkspace      bool
}

// Result is the distribution-neutral image result contract.
type Result = imagecontract.Result

// Remasterer coordinates host artefact handling with isolated Linux tooling.
type Remasterer struct {
	Docker     *platform.Docker
	Out        io.Writer
	Companions *companion.Builder
}

// NewRemasterer constructs a Fedora adapter with production defaults.
func NewRemasterer(docker *platform.Docker, out io.Writer) *Remasterer {
	if docker == nil {
		docker = platform.NewDocker(nil)
	}
	if out == nil {
		out = io.Discard
	}
	return &Remasterer{Docker: docker, Out: out, Companions: companion.NewBuilder(nil)}
}

// BuildPlan returns the ordered adapter workflow without external mutation.
func BuildPlan(request Request) (plan.Plan, error) {
	if request.SourceISO == "" || request.OutputISO == "" {
		return plan.Plan{}, errors.New("source and output ISO paths are required")
	}
	if request.Bundle.ABI == "" {
		return plan.Plan{}, errors.New("kernel bundle ABI is required")
	}
	if request.Bundle.ABI != "resolved-at-execution" {
		if err := requireSupportedKernel(request.Bundle.ABI); err != nil {
			return plan.Plan{}, err
		}
	}
	companionSource := "not-requested"
	if request.Companion.SourceDirectory != "" {
		companionSource = request.Companion.SourceDirectory
	}
	companionUserspace := "none"
	if len(request.CompanionUserspace) != 0 {
		companionUserspace = strings.Join(request.CompanionUserspace, ",")
	}
	return plan.New("image.create", []plan.Step{
		{ID: "verify-source", Kind: "verify", Description: "Verify the Fedora 44 Workstation Live ISO", Inputs: map[string]string{"path": request.SourceISO, "sha256": request.SourceSHA256}},
		{ID: "verify-kernel", Kind: "verify", Description: "Verify a patch-line-qualified Stubble kernel bundle", Inputs: map[string]string{"release": request.Bundle.Release, "abi": request.Bundle.ABI}},
		{ID: "stage-companion", Kind: "companion", Description: "Stage the optional Linux ARM64 CLI and eligible offline userspace", Inputs: map[string]string{"source": companionSource, "userspace": companionUserspace}},
		{ID: "prepare-tools", Kind: "prepare", Description: "Prepare ARM64 ISO, EROFS, RPM, and boot inspection tools", Inputs: map[string]string{"adapter": AdapterID}},
		{ID: "extract-live-root", Kind: "extract", Description: "Validate and extract the Fedora EROFS live root"},
		{ID: "install-kernel", Kind: "kernel", Description: "Build and register a Lexr-built Fedora RPM from the exact kernel payload", Inputs: map[string]string{"abi": request.Bundle.ABI}},
		{ID: "install-userspace", Kind: "userspace", Description: "Build and install a native Fedora IPTSD RPM only when the exact source-bearing companion release is requested", Inputs: map[string]string{"userspace": companionUserspace}},
		{ID: "assemble-initramfs-root", Kind: "filesystem", Description: "Prepare the Fedora root for exact-ABI dracut generation"},
		{ID: "build-initramfs", Kind: "initramfs", Description: "Generate a non-host-only dracut-live initramfs for the custom ABI"},
		{ID: "bind-live-media", Kind: "boot", Description: "Bind every live entry to the pinned Fedora ISO volume label"},
		{ID: "pair-device-trees", Kind: "device-tree", Description: "Verify Stubble X1E auto-DTB data and retain paired loose X1E/X1P DTBs"},
		{ID: "repack-live-root", Kind: "filesystem", Description: "Repack the Fedora root as LZMA EROFS with SELinux labels"},
		{ID: "replay-hybrid-boot", Kind: "boot", Description: "Replay the source GPT/El-Torito layout with custom and fallback kernels"},
		{ID: "validate-output", Kind: "verify", Description: "Validate GPT, ESP, GRUB, EROFS, RPM, dracut, Stubble, and installed hand-off"},
		{ID: "publish-output", Kind: "publish", Description: "Exclusively publish the ISO, manifest sidecar, and journal", Inputs: map[string]string{"path": request.OutputISO}},
	}...)
}

// requireSupportedKernel rejects bundles outside the adapter's patch-line-aware
// support baseline.
func requireSupportedKernel(abi string) error {
	patchLine, generation, err := parseKernelIdentity(abi)
	if err != nil {
		return err
	}
	if minimumGeneration, ok := patchLineMinimumKernelGenerations[patchLine]; ok && generation >= minimumGeneration {
		return nil
	}
	return fmt.Errorf(
		"Fedora Live requires kernel 7.2.0 sp11v19+ or kernel 7.2.2 sp11v1+, got %q",
		abi,
	)
}

// parseKernelIdentity separates a canonical qcom-x1e ABI into its patch line
// and patch-line-local Surface generation.
func parseKernelIdentity(abi string) (kernelPatchLine, uint64, error) {
	matches := kernelABIExpression.FindStringSubmatch(abi)
	if len(matches) != 5 || strings.Count(abi, "sp11v") != 1 {
		return kernelPatchLine{}, 0, fmt.Errorf(
			"kernel ABI %q must use <major>.<minor>.<patch>-...sp11v<generation>-qcom-x1e",
			abi,
		)
	}

	values := make([]uint64, 4)
	for index, value := range matches[1:] {
		if len(value) > 1 && value[0] == '0' {
			return kernelPatchLine{}, 0, fmt.Errorf("kernel ABI %q contains a noncanonical numeric component", abi)
		}
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return kernelPatchLine{}, 0, fmt.Errorf("kernel ABI %q contains an out-of-range version or sp11v generation", abi)
		}
		values[index] = parsed
	}
	return kernelPatchLine{major: values[0], minor: values[1], patch: values[2]}, values[3], nil
}
