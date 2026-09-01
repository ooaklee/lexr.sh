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
	// minimumKernelGeneration is the first Surface kernel with the in-tree
	// touchscreen and HIDRAW/IPTSD integration required by this adapter.
	minimumKernelGeneration = 19
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

// kernelGenerationExpression extracts the Surface release generation from an ABI.
var kernelGenerationExpression = regexp.MustCompile(`sp11v([0-9]+)`)

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
		{ID: "verify-kernel", Kind: "verify", Description: "Verify the v19-or-newer Stubble kernel bundle", Inputs: map[string]string{"release": request.Bundle.Release, "abi": request.Bundle.ABI}},
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

// requireSupportedKernel rejects bundles older than the adapter's supported baseline.
func requireSupportedKernel(abi string) error {
	matches := kernelGenerationExpression.FindStringSubmatch(abi)
	if len(matches) != 2 {
		return fmt.Errorf("kernel ABI %q does not declare an sp11v generation", abi)
	}
	generation, err := strconv.Atoi(matches[1])
	if err != nil || generation < minimumKernelGeneration {
		return fmt.Errorf("Fedora Live requires an sp11v%d-or-newer kernel ABI, got %q", minimumKernelGeneration, abi)
	}
	return nil
}
