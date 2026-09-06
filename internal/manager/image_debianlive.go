package manager

import (
	"context"
	"errors"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/debianlive"
	"github.com/ooaklee/lexr.sh/internal/plan"
)

// debianLiveImageAdapter connects common resolution to Debian's
// explicit live-boot and Calamares contract.
type debianLiveImageAdapter struct{ remasterer *debianlive.Remasterer }

// Plan renders Debian's single-filesystem creation sequence without mutations.
func (a debianLiveImageAdapter) Plan(request imageAdapterRequest) (plan.Plan, error) {
	return debianlive.BuildPlan(debianLiveAdapterRequest(request))
}

// Create executes Debian image preparation with verified manager inputs.
func (a debianLiveImageAdapter) Create(ctx context.Context, request imageAdapterRequest) (imagecontract.Result, error) {
	if a.remasterer == nil {
		return imagecontract.Result{}, errors.New("Debian remasterer is unavailable")
	}
	return a.remasterer.Create(ctx, debianLiveAdapterRequest(request))
}

// debianLiveAdapterRequest retains every common creation and provenance option.
func debianLiveAdapterRequest(request imageAdapterRequest) debianlive.Request {
	return debianlive.Request{
		SourceISO: request.Source, SourceSHA256: request.SourceSHA256,
		OutputISO: request.Output, Bundle: request.Bundle,
		KernelProfile: request.KernelProfile, ToolVersion: request.ToolVersion,
		Companion: request.Companion, CompanionUserspace: request.CompanionUserspace,
		WorkspaceRoot: request.WorkspaceRoot, KeepWorkspace: request.KeepWorkspace,
	}
}
