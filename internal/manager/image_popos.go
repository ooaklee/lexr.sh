package manager

import (
	"context"
	"errors"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/popos"
	"github.com/ooaklee/lexr.sh/internal/plan"
)

// popCasperImageAdapter connects common resolution to Pop's explicit contract.
type popCasperImageAdapter struct{ remasterer *popos.Remasterer }

// Plan renders Pop's single-filesystem creation sequence without mutations.
func (a popCasperImageAdapter) Plan(request imageAdapterRequest) (plan.Plan, error) {
	return popos.BuildPlan(popAdapterRequest(request))
}

// Create executes Pop image preparation with verified manager inputs.
func (a popCasperImageAdapter) Create(ctx context.Context, request imageAdapterRequest) (imagecontract.Result, error) {
	if a.remasterer == nil {
		return imagecontract.Result{}, errors.New("Pop remasterer is unavailable")
	}
	return a.remasterer.Create(ctx, popAdapterRequest(request))
}

// popAdapterRequest retains every common creation and provenance option.
func popAdapterRequest(request imageAdapterRequest) popos.Request {
	return popos.Request{
		SourceISO: request.Source, SourceSHA256: request.SourceSHA256,
		OutputISO: request.Output, Bundle: request.Bundle,
		KernelProfile: request.KernelProfile, ToolVersion: request.ToolVersion,
		Companion: request.Companion, CompanionUserspace: request.CompanionUserspace,
		WorkspaceRoot: request.WorkspaceRoot, KeepWorkspace: request.KeepWorkspace,
	}
}
