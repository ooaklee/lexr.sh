// Package producer authenticates source-owned component declarations against
// reviewed compiled byte identities before evaluating an explicit payload target.
package producer

import (
	"context"
	"errors"
	"fmt"
	"github.com/ooaklee/lexr.sh/internal/platform"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	"strconv"
	"strings"
)

// Policy is the compiled authority ceiling for one component declaration.
// Updating a source declaration requires a reviewed pin update; an editable
// receipt or repository override cannot widen compatibility or capabilities.
type Policy struct {
	ComponentID string
	Release     string
	Capability  string
	Manifest    compatibility.Reference
}

// compiledPolicies pins reviewed canonical copies of the declarations authored
// in the support repository. Testdata are copies, never another authored source.
var compiledPolicies = map[string]Policy{
	"audio-fullio-v19c":   {ComponentID: "audio-fullio-v19c", Release: "sp11-audio-v19c-full", Capability: "audio", Manifest: compatibility.Reference{Size: 979, SHA256: "d5facbf3fd34d11df22091a0bb8292433a9509c7298dea4983da20629493191e"}},
	"iptsd-v1":            {ComponentID: "iptsd-v1", Release: "sp11-iptsd-v3", Capability: "pen", Manifest: compatibility.Reference{Size: 961, SHA256: "a5e214aaad89c97273225ec6f4269d43e1b4ca987977785a08a99918bb6e6737"}},
	"imx681-libcamera-v1": {ComponentID: "imx681-libcamera-v1", Release: "sp11-imx681-libcamera-v2", Capability: "camera", Manifest: compatibility.Reference{Size: 986, SHA256: "ec0938dd9f0e57d9d72fe791204ba3a097b03f34e34d3086f7797254bf038936"}},
}

// PolicyFor returns an immutable value copy of one reviewed producer authority.
func PolicyFor(componentID string) (Policy, error) {
	policy, ok := compiledPolicies[componentID]
	if !ok {
		return Policy{}, fmt.Errorf("component %q has no reviewed producer policy", componentID)
	}
	return policy, nil
}

// DeclarationPath is the fixed source-owned path for a registered component.
func DeclarationPath(componentID string) string {
	return "userspace/compatibility/" + componentID + "/" + compatibility.Filename
}

// Authenticate bounds the Git blob before reading it and requires clean HEAD
// evidence as well as an independent compiled pin. Working-tree bytes never
// authorise a new declaration.
func Authenticate(ctx context.Context, runner platform.Runner, root, component string) (*compatibility.Manifest, compatibility.Reference, error) {
	policy, err := PolicyFor(component)
	if err != nil {
		return nil, compatibility.Reference{}, err
	}
	if runner == nil || root == "" {
		return nil, compatibility.Reference{}, errors.New("source declaration requires an explicit Git root and runner")
	}
	status, err := runner.Capture(ctx, platform.Command{Name: "git", Args: []string{"-C", root, "status", "--porcelain"}})
	if err != nil {
		return nil, compatibility.Reference{}, fmt.Errorf("inspect repository cleanliness: %w", err)
	}
	if len(status) > 64<<10 || strings.TrimSpace(string(status)) != "" {
		return nil, compatibility.Reference{}, errors.New("declaration authentication requires a clean repository HEAD")
	}
	object := "HEAD:" + DeclarationPath(component)
	sizeData, err := runner.Capture(ctx, platform.Command{Name: "git", Args: []string{"-C", root, "cat-file", "-s", object}})
	if err != nil {
		return nil, compatibility.Reference{}, fmt.Errorf("inspect declaration size: %w", err)
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(sizeData)), 10, 64)
	if err != nil || size != policy.Manifest.Size || size <= 0 || size > compatibility.MaxBytes {
		return nil, compatibility.Reference{}, errors.New("source declaration size differs from reviewed authority")
	}
	data, err := runner.Capture(ctx, platform.Command{Name: "git", Args: []string{"-C", root, "show", object}})
	if err != nil {
		return nil, compatibility.Reference{}, fmt.Errorf("read HEAD declaration: %w", err)
	}
	return VerifyDeclaration(data, policy)
}

// VerifyDeclaration intersects exact source bytes with independently compiled
// identity and capability policy before interpreting their compatibility claims.
func VerifyDeclaration(data []byte, policy Policy) (*compatibility.Manifest, compatibility.Reference, error) {
	manifest, err := compatibility.Verify(data, policy.Manifest, policy.ComponentID, policy.Release)
	if err != nil {
		return nil, compatibility.Reference{}, err
	}
	if len(manifest.Capabilities) != 1 || manifest.Capabilities[0] != policy.Capability {
		return nil, compatibility.Reference{}, errors.New("source declaration exceeds compiled capability authority")
	}
	return manifest, policy.Manifest, nil
}

// Revalidate repeats source authentication and the complete recorded decision
// immediately before a local build or release becomes public in its output root.
func Revalidate(ctx context.Context, runner platform.Runner, root string, record Record) error {
	manifest, ref, err := Authenticate(ctx, runner, root, record.Component)
	if err != nil {
		return err
	}
	if ref != record.Manifest {
		return errors.New("source declaration changed after planning")
	}
	data, err := compatibility.Marshal(manifest)
	if err != nil {
		return err
	}
	return ValidatePublication(data, record)
}
