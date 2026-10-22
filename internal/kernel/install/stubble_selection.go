package install

import (
	"context"
	"fmt"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// plannedStubbleSelection takes ownership of ordered evidence before any
// package script can run. Legacy unique-route bundles require no new fields.
func plannedStubbleSelection(bundle kernel.Bundle) (*kernel.StubbleSelection, []kernel.DeviceTree) {
	if bundle.DTBSelectionProvenance == nil || bundle.DTBSelectionProvenance.StubbleSelection == nil {
		return nil, nil
	}
	source := bundle.DTBSelectionProvenance.StubbleSelection
	selection := *source
	selection.PayloadOrder = append([]string(nil), source.PayloadOrder...)
	selection.HWIDRecords = append([]kernel.StubbleHWIDRecord(nil), source.HWIDRecords...)
	return &selection, kernel.CloneDeviceTrees(bundle.DeviceTrees)
}

// verifyInstalledStubbleSelection re-observes actual installed PE/FDT/HWID
// bytes. An otherwise valid manifest cannot substitute a guessed table order.
func verifyInstalledStubbleSelection(ctx context.Context, root, abi string, selection *kernel.StubbleSelection, trees []kernel.DeviceTree) error {
	if selection == nil {
		return nil
	}
	image, err := ReadRootFile(ctx, root, "boot/vmlinuz-"+abi, "ordered Stubble kernel image", maximumBootImageInspectionBytes)
	if err != nil {
		return err
	}
	if err := kernel.VerifyStubbleSelection(image, trees, selection); err != nil {
		return fmt.Errorf("installed Stubble inventory order differs from the measured bundle: %w", err)
	}
	return nil
}
