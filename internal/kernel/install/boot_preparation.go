package install

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ooaklee/lexr.sh/internal/cleanup"
	"github.com/ooaklee/lexr.sh/internal/profile"
)

// FallbackBindingPlan preserves an external fallback for stock GRUB before
// package hooks regenerate its menu. It never overwrites existing boot bytes.
type FallbackBindingPlan struct {
	// Source is the same-ABI firmware DTB already matched to the fallback boot.
	Source FileEvidence `json:"source"`
	// Destination is the exact-ABI /boot/dtb-<abi> path recognised by stock GRUB.
	Destination string `json:"destination"`
	// Create distinguishes a new copy from an existing, verified binding.
	Create bool `json:"create"`
}

// planFallbackBinding avoids changing the fallback through a shared DTB name
// when stock GRUB is regenerated for the newly installed kernel.
func planFallbackBinding(ctx context.Context, root, profileID string, fallback BootEvidence) (*FallbackBindingPlan, error) {
	if profileID == "" || fallback.DeviceTreeBoot.Mode != DeviceTreeBootExternal {
		return nil, nil
	}
	selected, err := profile.Resolve(profileID)
	if err != nil {
		return nil, err
	}
	relative, _ := DeviceTreeRelativePath(selected.Platform)
	source, err := HashRootFile(ctx, root, "usr/lib/firmware/"+fallback.ABI+"/device-tree/"+relative, "fallback device-tree")
	if err != nil {
		return nil, err
	}
	if source.SHA256 != fallback.DeviceTreeBoot.SHA256 {
		return nil, errors.New("fallback device-tree changed after verification")
	}
	destination, err := rootPath(root, "boot/dtb-"+fallback.ABI)
	if err != nil {
		return nil, err
	}
	if err := validateTargetRoute(root, destination, true); err != nil {
		return nil, err
	}
	binding := &FallbackBindingPlan{Source: source, Destination: destination}
	if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
		binding.Create = true
	} else if err != nil {
		return nil, err
	} else {
		existing, err := requireRegularEvidence(ctx, "exact-ABI fallback device-tree", destination)
		if err != nil {
			return nil, err
		}
		if existing.SHA256 != source.SHA256 || existing.Size != source.Size {
			return nil, fmt.Errorf("exact-ABI fallback device-tree %s conflicts with the verified fallback; review it before installing", destination)
		}
	}
	return binding, nil
}

// planBootHookCleanup uses the existing reversible cleanup boundary, restricted
// to two positively recognised retired kernel hooks. Unknown content is blocked.
func planBootHookCleanup(root string) (*cleanup.ScanReport, error) {
	report, err := cleanup.ScanWithOptions(cleanup.ScanOptions{Root: root, Features: []string{"kernel-boot"}})
	if err != nil {
		return nil, fmt.Errorf("inspect competing kernel boot hooks: %w", err)
	}
	for _, finding := range report.Findings {
		if !finding.Recognized && finding.Mode&0o111 != 0 {
			return nil, fmt.Errorf("unrecognised competing kernel boot hook %s; review and disable it before installing (lexr clean scan --feature kernel-boot shows the evidence)", finding.Path)
		}
	}
	if len(report.Findings) == 0 {
		return nil, nil
	}
	return &report, nil
}

// rejectCompetingBootHooks catches hooks introduced after preflight, including
// a package which reinstalls the retired injector during the transaction.
func rejectCompetingBootHooks(root string) error {
	report, err := planBootHookCleanup(root)
	if err != nil || report == nil {
		return err
	}
	for _, finding := range report.Findings {
		if finding.Mode&0o111 != 0 {
			return fmt.Errorf("competing kernel boot hook appeared after preflight: %s", finding.Path)
		}
	}
	return nil
}

// prepareBootChanges is called only after root privilege and full revalidation.
// Cleanup publishes its durable recovery receipt before removing any hook.
func prepareBootChanges(ctx context.Context, plan Plan, receipt *Receipt) error {
	if plan.BootHookCleanup != nil {
		retired, err := cleanup.Apply(*plan.BootHookCleanup, true)
		if retired.Backup != "" {
			receipt.BootHookCleanup = &retired
		}
		if err != nil {
			return fmt.Errorf("retire competing kernel boot hooks: %w", err)
		}
	}
	if err := rejectCompetingBootHooks(plan.Root); err != nil {
		return err
	}
	if plan.FallbackBinding != nil {
		created, err := createFallbackBinding(ctx, plan.Root, *plan.FallbackBinding)
		receipt.FallbackBindingCreated = created
		return err
	}
	return nil
}

// createFallbackBinding copies only bytes verified against the bootable fallback.
// Exclusive creation refuses any raced or pre-existing destination. A successful
// copy remains useful to the fallback even if the target installation fails.
func createFallbackBinding(ctx context.Context, root string, binding FallbackBindingPlan) (bool, error) {
	if err := validateTargetRoute(root, binding.Source.Path, false); err != nil {
		return false, err
	}
	if err := validateTargetRoute(root, binding.Destination, binding.Create); err != nil {
		return false, err
	}
	if !binding.Create {
		existing, err := requireRegularEvidence(ctx, "exact-ABI fallback device-tree", binding.Destination)
		if err != nil {
			return false, err
		}
		if existing.SHA256 != binding.Source.SHA256 || existing.Size != binding.Source.Size {
			return false, errors.New("exact-ABI fallback device-tree changed after preflight")
		}
		return false, nil
	}
	info, err := os.Lstat(binding.Source.Path)
	if err != nil {
		return false, err
	}
	source, _, err := openUnchangedRegular(binding.Source.Path, info)
	if err != nil {
		return false, err
	}
	defer source.Close()
	filesystem, err := os.OpenRoot(root)
	if err != nil {
		return false, err
	}
	defer filesystem.Close()
	boot, err := filesystem.OpenRoot("boot")
	if err != nil {
		return false, err
	}
	defer boot.Close()
	name := filepath.Base(binding.Destination)
	temporary := ".lexr-fallback-" + rand.Text()
	destination, err := boot.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, fmt.Errorf("create exact-ABI fallback device-tree: %w", err)
	}
	defer boot.Remove(temporary)
	digest, size, copyErr := digestReader(ctx, source, destination, binding.Source.Size)
	modeErr := destination.Chmod(0o644)
	syncErr := destination.Sync()
	closeErr := destination.Close()
	if copyErr == nil && (digest != binding.Source.SHA256 || size != binding.Source.Size) {
		copyErr = errors.New("fallback device-tree changed after preflight")
	}
	if err := errors.Join(copyErr, modeErr, syncErr, closeErr); err != nil {
		return false, err
	}
	// Hard-link publication is atomic and cannot replace a raced destination.
	if err := boot.Link(temporary, name); err != nil {
		return false, fmt.Errorf("publish exact-ABI fallback device-tree: %w", err)
	}
	directory, err := boot.Open(".")
	if err != nil {
		return true, err
	}
	return true, errors.Join(directory.Sync(), directory.Close())
}

// restoreBootPreparation restores retired hooks after package rollback. It does
// not overwrite changed local files; a recovery conflict is reported to the user.
func restoreBootPreparation(receipt *Receipt) error {
	if receipt.BootHookCleanup == nil {
		return nil
	}
	retired := *receipt.BootHookCleanup
	receiptName := "receipt.json"
	if retired.State != "complete" {
		receiptName = "receipt.pending.json"
	}
	_, err := cleanup.Restore(retired, filepath.Join(retired.Backup, receiptName), true)
	if err != nil {
		return fmt.Errorf("restore retired boot hooks from %s: %w", retired.Backup, err)
	}
	receipt.BootHooksRestored = true
	return nil
}
