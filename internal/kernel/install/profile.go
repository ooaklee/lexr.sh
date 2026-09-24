package install

import (
	"context"
	"fmt"
	"slices"

	"github.com/ooaklee/lexr.sh/internal/profile"
)

// installationProfile validates caller intent against the verified boot contract.
// Direct domain callers may inspect generic bundles without a hardware choice;
// the CLI always resolves a profile before invoking native installation.
func installationProfile(request Request) (profile.Profile, error) {
	if request.Profile == "" {
		return profile.Profile{}, nil
	}
	selected, err := profile.Resolve(request.Profile)
	if err != nil {
		return profile.Profile{}, err
	}
	if !slices.Contains(request.Bundle.BootPlatforms(), selected.Platform) {
		return profile.Profile{}, fmt.Errorf("kernel bundle does not declare profile %q", selected.ID)
	}
	if observed, err := profile.Detect(request.Root); err == nil && observed.ID != selected.ID {
		return profile.Profile{}, fmt.Errorf("selected profile %q conflicts with target hardware %q", selected.ID, observed.ID)
	}
	return selected, nil
}

// verifyBootProfile requires the selected model's exact same-ABI firmware bytes
// among the already verified boot payloads. Keep embedded inventory verification
// complete: choosing one model does not discard other declared Stubble routes.
func verifyBootProfile(ctx context.Context, root, abi, profileID string, evidence DeviceTreeBootEvidence) error {
	if profileID == "" {
		return nil
	}
	selected, err := profile.Resolve(profileID)
	if err != nil {
		return err
	}
	relative, valid := DeviceTreeRelativePath(selected.Platform)
	if !valid {
		return fmt.Errorf("profile %q has no installed device-tree contract", selected.ID)
	}
	installed, err := HashRootFile(ctx, root, "usr/lib/firmware/"+abi+"/device-tree/"+relative, "selected profile device-tree")
	if err != nil {
		return err
	}
	if !slices.Contains(evidence.SHA256s, installed.SHA256) {
		return fmt.Errorf("boot device-tree does not match selected profile %q for ABI %s", selected.ID, abi)
	}
	return nil
}
