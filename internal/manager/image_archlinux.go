package manager

import (
	"context"
	"errors"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/archlinux"
	"github.com/ooaklee/lexr.sh/internal/plan"
)

// archLinuxARMImageAdapter connects common resolution to Arch Linux ARM's terminal live-media contract.
type archLinuxARMImageAdapter struct{ remasterer *archlinux.Remasterer }

// Plan renders Arch terminal filesystem creation sequence without mutations.
func (a archLinuxARMImageAdapter) Plan(request imageAdapterRequest) (plan.Plan, error) {
	return archlinux.BuildPlan(archLinuxARMAdapterRequest(request))
}

// Create executes Arch image preparation with verified manager inputs.
func (a archLinuxARMImageAdapter) Create(ctx context.Context, request imageAdapterRequest) (imagecontract.Result, error) {
	if a.remasterer == nil {
		return imagecontract.Result{}, errors.New("Arch Linux ARM remasterer is unavailable")
	}
	return a.remasterer.Create(ctx, archLinuxARMAdapterRequest(request))
}

// archLinuxARMAdapterRequest retains every common creation and provenance option.
func archLinuxARMAdapterRequest(request imageAdapterRequest) archlinux.Request {
	return archlinux.Request{
		SourceRootfs: request.Source, SourceSHA256: request.SourceSHA256,
		OutputISO: request.Output, Bundle: request.Bundle,
		KernelProfile: request.KernelProfile, ToolVersion: request.ToolVersion,
		Companion: request.Companion, CacheDirectory: request.CacheDirectory,
		WorkspaceRoot: request.WorkspaceRoot, KeepWorkspace: request.KeepWorkspace,
	}
}
