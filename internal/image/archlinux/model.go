package archlinux

import (
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/companion"
	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/plan"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// AdapterID identifies direct Arch Linux ARM live media generated from a rootfs.
const AdapterID = "archlinux-arm-live"

// maximumImageBytes bounds local snapshots and generated ISO inputs.
const maximumImageBytes int64 = 64 << 30

// secureBootPolicy records the custom kernel's unsigned firmware requirement.
const secureBootPolicy = "unsupported; disable Secure Boot for the unsigned custom kernel and external device tree"

// Request contains verified rootfs/kernel inputs and companion publication choices.
type Request struct {
	SourceRootfs   string
	SourceSHA256   string
	OutputISO      string
	Bundle         kernel.Bundle
	KernelProfile  string
	ToolVersion    string
	Companion      companion.BuildRequest
	CacheDirectory string
	WorkspaceRoot  string
	KeepWorkspace  bool
}

// Remasterer builds Arch live media in a fresh Linux filesystem.
type Remasterer struct {
	Docker     *platform.Docker
	Out        io.Writer
	Companions *companion.Builder
	Artifacts  *artifact.Resolver
}

// NewRemasterer supplies the standard process and immutable-input boundaries.
func NewRemasterer(docker *platform.Docker, out io.Writer) *Remasterer {
	if docker == nil {
		docker = platform.NewDocker(nil)
	}
	if out == nil {
		out = io.Discard
	}
	return &Remasterer{Docker: docker, Out: out, Companions: companion.NewBuilder(docker.Runner), Artifacts: artifact.NewResolver(nil)}
}

// creationSteps is the stable order of independently recorded build checkpoints.
var creationSteps = []plan.Step{
	{ID: "verify-source", Kind: "verify", Description: "Verify the pinned Arch Linux ARM root filesystem"},
	{ID: "verify-kernel", Kind: "verify", Description: "Verify the coherent Surface kernel and device trees"},
	{ID: "stage-companion", Kind: "companion", Description: "Build and retain Lexr with corresponding source and notices"},
	{ID: "prepare-tools", Kind: "prepare", Description: "Prepare isolated ARM64 build tools and Linux storage"},
	{ID: "acquire-packages", Kind: "verify", Description: "Acquire the fixed signed Arch terminal and live-boot package set"},
	{ID: "prepare-root", Kind: "filesystem", Description: "Extract the authenticated root and install the locked packages offline"},
	{ID: "install-kernel", Kind: "kernel", Description: "Register the Surface kernel as a native pacman package"},
	{ID: "prepare-live-session", Kind: "filesystem", Description: "Configure the terminal, networking and the temporary live account"},
	{ID: "build-boot-assets", Kind: "boot", Description: "Generate separate initramfs images and ARM64 GRUB with matching DTBs"},
	{ID: "pack-filesystem", Kind: "filesystem", Description: "Compress the live root and bind its completed bytes to the boot checksum"},
	{ID: "create-hybrid-boot", Kind: "boot", Description: "Create one GPT EFI partition shared by USB and optical boot"},
	{ID: "validate-output", Kind: "verify", Description: "Independently validate the ISO, live root and exact boot payload"},
	{ID: "publish-output", Kind: "publish", Description: "Exclusively publish the ISO, manifest and journal"},
}

// CreationStepIDs returns the journal order without exposing mutable shared data.
func CreationStepIDs() []string {
	result := make([]string, len(creationSteps))
	for i, step := range creationSteps {
		result[i] = step.ID
	}
	return result
}

// BuildPlan describes live image creation without network or filesystem mutation.
func BuildPlan(request Request) (plan.Plan, error) {
	if request.SourceRootfs == "" || request.OutputISO == "" || !strings.HasSuffix(filepath.Base(request.OutputISO), ".iso") {
		return plan.Plan{}, errors.New("Arch source rootfs and .iso output are required")
	}
	if !abiPattern.MatchString(request.Bundle.ABI) {
		return plan.Plan{}, errors.New("Arch requires a safe coherent kernel ABI")
	}
	if !sha256Pattern.MatchString(request.SourceSHA256) {
		return plan.Plan{}, errors.New("Arch rootfs requires a pinned SHA-256")
	}
	steps := append([]plan.Step(nil), creationSteps...)
	steps[0].Inputs = map[string]string{"path": request.SourceRootfs, "sha256": request.SourceSHA256}
	steps[3].Inputs = map[string]string{"adapter": AdapterID}
	steps[2].Inputs = map[string]string{"source": request.Companion.SourceDirectory}
	steps[1].Inputs = map[string]string{"abi": request.Bundle.ABI, "profile": request.KernelProfile}
	steps[len(steps)-1].Inputs = map[string]string{"path": request.OutputISO}
	return plan.New("image.create", steps...)
}

// Result uses the shared image publication contract.
type Result = imagecontract.Result
