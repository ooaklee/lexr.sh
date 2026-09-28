package release

import (
	"context"
	"github.com/ooaklee/lexr.sh/internal/userspace/producer"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewAudioCannotUseLegacyExemption prevents old tags from bypassing the
// manifest requirement for a newly prepared local release.
func TestNewAudioCannotUseLegacyExemption(t *testing.T) {
	fixture := newReleaseFixture(t)
	request := fixture.request
	request.Tag = SupportedTag
	manager := newManagerWithPolicies(productionPolicy(SupportedTag), fixture.policy)
	for _, dry := range []bool{false, true} {
		request.DryRun = dry
		if _, err := manager.Prepare(context.Background(), request); err == nil {
			t.Fatal("new legacy preparation was accepted")
		}
	}
}

// TestAudioCompatibilityPreflightAndPublication rechecks source authority at
// both dry-run and publication boundaries and retains the complete plan.
func TestAudioCompatibilityPreflightAndPublication(t *testing.T) {
	fixture := newReleaseFixture(t)
	manager := newManagerWithPolicies(productionPolicy(SupportedTag), fixture.policy)
	request := fixture.request
	request.DryRun = true
	plan, err := manager.Plan(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Compatibility == nil || !plan.Compatibility.AllowUnverified {
		t.Fatal("dry run omitted compatibility decision")
	}
	request.AllowUnverifiedCompatibility = false
	if _, err := manager.Prepare(context.Background(), request); err == nil || !strings.Contains(err.Error(), "allow-unverified-compatibility") {
		t.Fatalf("dry-run compatibility bypass: %v", err)
	}
	request = fixture.request
	manager.beforePublish = func(context.Context, Plan) error {
		return os.WriteFile(filepath.Join(fixture.repositoryRoot, producer.DeclarationPath("audio-fullio-v19c")), []byte("{}\n"), 0o644)
	}
	if _, err := manager.Prepare(context.Background(), request); err == nil {
		t.Fatal("changed source declaration published")
	}
	assertNoReleaseOrTransaction(t, fixture)
}
