package debianlive

import (
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/plan"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// outputNamePattern limits filenames to the shared portable release vocabulary.
var outputNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~%-]{0,199}\.iso$`)

// secureBootPolicy describes the unsigned custom-kernel and external-DTB path.
const secureBootPolicy = "unsupported; disable Secure Boot for the unsigned custom kernel and external device tree"

// Request carries resolved inputs and publication policy for a Debian live image.
type Request struct {
	SourceISO          string
	SourceSHA256       string
	OutputISO          string
	Bundle             kernel.Bundle
	KernelProfile      string
	ToolVersion        string
	Companion          companion.BuildRequest
	CompanionUserspace []string
	WorkspaceRoot      string
	KeepWorkspace      bool
}

// Result shares the image publication contract with the other distributions.
type Result = imagecontract.Result

// Remasterer coordinates Debian-specific filesystem and bootloader preparation.
type Remasterer struct {
	Docker     *platform.Docker
	Out        io.Writer
	Companions *companion.Builder
}

// NewRemasterer supplies the common Docker and companion services by default.
func NewRemasterer(docker *platform.Docker, out io.Writer) *Remasterer {
	if docker == nil {
		docker = platform.NewDocker(nil)
	}
	if out == nil {
		out = io.Discard
	}
	return &Remasterer{Docker: docker, Out: out, Companions: companion.NewBuilder(nil)}
}

// creationSteps records Debian's actual single-root workflow. Journal validators
// must select this adapter's order, not infer a workflow from its step count.
var creationSteps = []plan.Step{
	{ID: "verify-source", Kind: "verify", Description: "Verify the Debian ARM64 source ISO"},
	{ID: "verify-kernel", Kind: "verify", Description: "Verify the matching kernel, modules and device trees"},
	{ID: "stage-companion", Kind: "companion", Description: "Stage Lexr, corresponding source and selected offline userspaces"},
	{ID: "prepare-tools", Kind: "prepare", Description: "Prepare isolated ARM64 image tooling"},
	{ID: "extract-live-root", Kind: "extract", Description: "Validate Debian's pinned live-boot directory and extract its single filesystem"},
	{ID: "prepare-wifi", Kind: "firmware", Description: "Stage pinned public GPU firmware and derive SP11 Wi-Fi board data before the first probe"},
	{ID: "prepare-installed-grub", Kind: "boot", Description: "Install pinned offline Debian GRUB packages and exact-ABI device-tree support"},
	{ID: "install-kernel", Kind: "kernel", Description: "Register the custom kernel and install Debian's GRUB support"},
	{ID: "build-initramfs", Kind: "initramfs", Description: "Generate separate live and installed-system initramfs images for the selected ABI"},
	{ID: "bind-live-media", Kind: "boot", Description: "Bind the generated live-boot UUID to the direct ISO medium"},
	{ID: "pair-device-trees", Kind: "device-tree", Description: "Stage the matching X1E and X1P device trees"},
	{ID: "repack-live-root", Kind: "filesystem", Description: "Repack Debian's installer filesystem and update its package inventory"},
	{ID: "create-hybrid-boot", Kind: "boot", Description: "Create the GPT EFI partition and direct GRUB live boot paths"},
	{ID: "validate-output", Kind: "verify", Description: "Validate Debian's media, installer payload and boot-artifact agreement"},
	{ID: "publish-output", Kind: "publish", Description: "Exclusively publish the validated ISO, manifest and journal"},
}

// CreationStepIDs returns a copy of the adapter's successful journal order.
func CreationStepIDs() []string {
	ids := make([]string, len(creationSteps))
	for index, step := range creationSteps {
		ids[index] = step.ID
	}
	return ids
}

// BuildPlan validates portable inputs without downloading or mutating files.
func BuildPlan(request Request) (plan.Plan, error) {
	if request.SourceISO == "" || request.OutputISO == "" {
		return plan.Plan{}, errors.New("source and output ISO paths are required")
	}
	if !outputNamePattern.MatchString(filepath.Base(request.OutputISO)) {
		return plan.Plan{}, errors.New("output ISO must have a bounded portable .iso filename")
	}
	if !kernelABIPattern.MatchString(request.Bundle.ABI) {
		return plan.Plan{}, errors.New("kernel bundle requires a safe, non-empty ABI")
	}
	steps := append([]plan.Step(nil), creationSteps...)
	steps[0].Inputs = map[string]string{"path": request.SourceISO, "sha256": request.SourceSHA256}
	steps[1].Inputs = map[string]string{"release": request.Bundle.Release, "abi": request.Bundle.ABI, "profile": request.KernelProfile}
	steps[2].Inputs = map[string]string{"source": request.Companion.SourceDirectory, "userspace": strings.Join(request.CompanionUserspace, ",")}
	steps[3].Inputs = map[string]string{"adapter": AdapterID}
	steps[len(steps)-1].Inputs = map[string]string{"path": request.OutputISO}
	return plan.New("image.create", steps...)
}
