package producer

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/ooaklee/lexr.sh/internal/platform"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	"github.com/ooaklee/lexr.sh/internal/version"
)

// Request selects one authenticated declaration preparation decision.
type Request struct {
	// Component identifies the reviewed component policy.
	Component string
	// RepositoryRoot is the explicit OE checkout with a clean HEAD.
	RepositoryRoot string
	// PayloadTarget is the declared target evidence for the payload being
	// prepared. It describes the payload target, never the build container
	// host, and must be complete: evidence is never assembled across rules.
	PayloadTarget compatibility.Target
	// AllowUnverifiedCompatibility is the dedicated override recorded when the
	// payload target is within hard bounds but beyond recorded evidence. It is
	// deliberately separate from any general confirmation flag such as --yes.
	AllowUnverifiedCompatibility bool
}

// Record is the portable preparation decision retained by release authority.
type Record struct {
	// SchemaVersion identifies this preparation record contract.
	SchemaVersion int `json:"schema_version"`
	// Component is the stable component identity.
	Component string `json:"component"`
	// Release is the exact next packaging identity.
	Release string `json:"release"`
	// Manifest is the independently pinned canonical declaration identity.
	Manifest compatibility.Reference `json:"manifest"`
	// Decision is the deterministic assessment of the declared payload target.
	Decision compatibility.Decision `json:"decision"`
	// AllowUnverified records the dedicated operator override.
	AllowUnverified bool `json:"allow_unverified"`
	// ProducerLexrVersion is the Lexr identity of this producer invocation.
	ProducerLexrVersion string `json:"producer_lexr_version"`
}

// Prepare authenticates one declaration from clean repository HEAD bytes,
// validates the declared payload target against it with the shared core
// evaluator, and returns the release record alongside the canonical bytes to
// include in the prepared release. A development, dirty, or malformed producer
// Lexr identity is unverified and can never produce a tested decision.
func Prepare(ctx context.Context, runner platform.Runner, request Request) (Record, []byte, error) {
	lexrVersion, _, _ := version.Info()
	return prepareVersion(ctx, runner, request, lexrVersion)
}

// prepareVersion is the private seam for testing trusted runtime version evidence.
func prepareVersion(ctx context.Context, runner platform.Runner, request Request, lexrVersion string) (Record, []byte, error) {
	policy, err := PolicyFor(request.Component)
	if err != nil {
		return Record{}, nil, err
	}
	manifest, reference, err := Authenticate(ctx, runner, request.RepositoryRoot, request.Component)
	if err != nil {
		return Record{}, nil, fmt.Errorf("authenticate source declaration: %w", err)
	}
	if manifest.ComponentID != policy.ComponentID || manifest.Release != policy.Release {
		return Record{}, nil, errors.New("authenticated declaration differs from the reviewed packaging identity")
	}
	canonical, err := compatibility.Marshal(manifest)
	if err != nil {
		return Record{}, nil, err
	}
	target := request.PayloadTarget
	target.LexrVersion = lexrVersion
	decision := compatibility.Evaluate(manifest, target)
	if err := compatibility.RequireAllowed(decision, request.AllowUnverifiedCompatibility); err != nil {
		return Record{}, nil, err
	}
	return Record{
		SchemaVersion: 1, Component: manifest.ComponentID, Release: manifest.Release,
		Manifest: reference, Decision: decision, AllowUnverified: request.AllowUnverifiedCompatibility,
		ProducerLexrVersion: lexrVersion,
	}, canonical, nil
}

// ValidatePublication revalidates exact staged canonical bytes against the
// recorded decision immediately before publication.
func ValidatePublication(data []byte, record Record) error {
	policy, err := PolicyFor(record.Component)
	if err != nil {
		return err
	}
	manifest, reference, err := VerifyDeclaration(data, policy)
	if err != nil {
		return fmt.Errorf("revalidate staged declaration bytes: %w", err)
	}
	if manifest.ComponentID != record.Component || manifest.Release != record.Release {
		return errors.New("staged declaration differs from the recorded release identity")
	}
	if reference != record.Manifest {
		return errors.New("staged declaration digest differs from the pinned source authority")
	}
	if record.SchemaVersion != 1 || record.ProducerLexrVersion != record.Decision.Target.LexrVersion {
		return errors.New("unsupported preparation schema or inconsistent producer identity")
	}
	decision := compatibility.Evaluate(manifest, record.Decision.Target)
	if !reflect.DeepEqual(decision, record.Decision) {
		return errors.New("staged declaration no longer reproduces the recorded decision")
	}
	return compatibility.RequireAllowed(decision, record.AllowUnverified)
}
