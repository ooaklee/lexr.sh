package manager

import (
	"context"
	"errors"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/image/elementary"
	"github.com/ooaklee/lexr.sh/internal/plan"
)

// elementaryCasperImageAdapter connects common resolution to elementary OS's
// explicit Casper and GRUB contract.
type elementaryCasperImageAdapter struct{ remasterer *elementary.Remasterer }

// Plan renders elementary's single-filesystem creation sequence without mutations.
func (a elementaryCasperImageAdapter) Plan(request imageAdapterRequest) (plan.Plan, error) {
	return elementary.BuildPlan(elementaryAdapterRequest(request))
}

// Create executes elementary image preparation with verified manager inputs.
func (a elementaryCasperImageAdapter) Create(ctx context.Context, request imageAdapterRequest) (imagecontract.Result, error) {
	if a.remasterer == nil {
		return imagecontract.Result{}, errors.New("elementary OS remasterer is unavailable")
	}
	return a.remasterer.Create(ctx, elementaryAdapterRequest(request))
}

// elementaryAdapterRequest retains every common creation and provenance option.
func elementaryAdapterRequest(request imageAdapterRequest) elementary.Request {
	return elementary.Request{
		SourceISO: request.Source, SourceSHA256: request.SourceSHA256,
		OutputISO: request.Output, Bundle: request.Bundle,
		KernelProfile: request.KernelProfile, ToolVersion: request.ToolVersion,
		Companion: request.Companion, CompanionUserspace: request.CompanionUserspace,
		WorkspaceRoot: request.WorkspaceRoot, KeepWorkspace: request.KeepWorkspace,
	}
}
