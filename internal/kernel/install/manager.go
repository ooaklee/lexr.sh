package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
	"github.com/ooaklee/lexr.sh/internal/profile"
)

// prepare builds a complete immutable preflight plan without target mutation.
func (manager *Manager) prepare(ctx context.Context, request Request) (result Plan, resultErr error) {
	defer func() {
		if errors.Is(resultErr, os.ErrPermission) {
			resultErr = fmt.Errorf("%w; read-only kernel checks need access to root-owned boot files: rerun with sudo, retaining --profile or an absolute --config path (a login shell is not required)", resultErr)
		}
	}()
	if manager == nil || manager.runner == nil {
		return Plan{}, errors.New("kernel installation manager is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	root, err := canonicalRoot(request.Root)
	if err != nil {
		return Plan{}, err
	}
	if err := validateABI("target", request.Bundle.ABI); err != nil {
		return Plan{}, err
	}
	if err := validateABI("fallback", request.FallbackABI); err != nil {
		return Plan{}, err
	}
	if request.Bundle.ABI == request.FallbackABI {
		return Plan{}, fmt.Errorf("target ABI must differ from fallback ABI: %s", request.Bundle.ABI)
	}
	selected, err := installationProfile(request)
	if err != nil {
		return Plan{}, err
	}
	runningABI, err := manager.runningABI(ctx, root, request.RunningABI)
	if err != nil {
		return Plan{}, err
	}
	warnings := []string(nil)
	fallbackMismatchForced := false
	if runningABI != request.FallbackABI {
		if !request.ForceFallbackMismatch {
			return Plan{}, fmt.Errorf("running ABI must exactly match fallback ABI: running %s, fallback %s", runningABI, request.FallbackABI)
		}
		fallbackMismatchForced = true
		warnings = append(warnings, fmt.Sprintf("warning: fallback ABI does not match the running ABI: running %s, fallback %s", runningABI, request.FallbackABI))
	}

	packages, err := manager.inspectBundle(ctx, request.Bundle)
	if err != nil {
		return Plan{}, err
	}
	unverified := false
	for _, item := range packages {
		unverified = unverified || !item.PublisherVerified
	}
	if unverified && !request.AllowUnverified {
		return Plan{}, errors.New("kernel bundle is not covered by an authoritative checksum manifest; explicitly allow an unverified local bundle to continue")
	}
	fallback, err := verifyFallbackProfile(ctx, root, request.FallbackABI, selected.ID)
	if err != nil {
		return Plan{}, err
	}
	hookCleanup, err := planBootHookCleanup(root)
	if err != nil {
		return Plan{}, err
	}
	binding, err := planFallbackBinding(ctx, root, selected.ID, fallback)
	if err != nil {
		return Plan{}, err
	}
	if request.Overwrite {
		// Overwrite replaces only the target ABI; it must never target the
		// running ABI, which the fallback verification protects.
		if runningABI == request.Bundle.ABI {
			return Plan{}, errors.New("--overwrite must never replace the running ABI: choose a distinct target ABI")
		}
	}
	deviceTrees, err := plannedDeviceTrees(root, request.Bundle)
	if err != nil {
		return Plan{}, err
	}
	targetState, err := classifyTargetState(ctx, root, request.Bundle.ABI, packages, deviceTrees)
	if err != nil {
		return Plan{}, err
	}
	if request.Overwrite && targetState.Classification != TargetStateAbsent {
		warnings = append(warnings, overwriteTargetWarning(targetState.Classification))
	}
	if targetState.Classification != TargetStateAbsent && !request.Overwrite {
		// Return the partial plan with its evidence so both preflight and
		// install receipts can expose structured blocker diagnostics.
		return Plan{
			Profile:                selected.ID,
			Root:                   root,
			TargetABI:              request.Bundle.ABI,
			FallbackABI:            request.FallbackABI,
			RunningABI:             runningABI,
			FallbackMismatchForced: fallbackMismatchForced,
			Warnings:               warnings,
			Version:                request.Bundle.Version,
			EffectiveDTBDelivery:   request.Bundle.EffectiveDTBDelivery,
			DryRun:                 request.DryRun,
			UnverifiedAccepted:     unverified,
			Overwrite:              request.Overwrite,
			TargetState:            &targetState,
		}, &TargetStateError{Evidence: targetState}
	}
	packagePaths := make([]string, 0, len(packages))
	for _, item := range packages {
		path := item.Path
		if root != string(filepath.Separator) {
			path = "/var/tmp/" + stagingPrefix + "verified/" + item.Name
		}
		packagePaths = append(packagePaths, path)
	}
	commands, err := installationCommands(root, request.Bundle.ABI, packagePaths)
	if err != nil {
		return Plan{}, err
	}
	refresh, err := profileBootCommands(root, request.Bundle.ABI, selected.Platform, request.Bundle.EffectiveDTBDelivery)
	if err != nil {
		return Plan{}, err
	}
	commands = append(commands, refresh...)
	conditionalCommands, err := ensureInitramfsCommands(root, request.Bundle.ABI)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Profile:                selected.ID,
		BootHookCleanup:        hookCleanup,
		FallbackBinding:        binding,
		Root:                   root,
		TargetABI:              request.Bundle.ABI,
		FallbackABI:            request.FallbackABI,
		RunningABI:             runningABI,
		FallbackMismatchForced: fallbackMismatchForced,
		Warnings:               warnings,
		Version:                request.Bundle.Version,
		EffectiveDTBDelivery:   request.Bundle.EffectiveDTBDelivery,
		DryRun:                 request.DryRun,
		UnverifiedAccepted:     unverified,
		Overwrite:              request.Overwrite,
		TargetState:            &targetState,
		Packages:               packages,
		DeviceTrees:            deviceTrees,
		Fallback:               fallback,
		Commands:               commands,
		ConditionalCommands:    conditionalCommands,
	}, nil
}

// runningABI obtains live uname evidence or validates an alternate-root fixture override.
func (manager *Manager) runningABI(ctx context.Context, root, override string) (string, error) {
	if root != string(filepath.Separator) {
		if strings.TrimSpace(override) == "" {
			return "", errors.New("alternate-root preflight requires an explicit running ABI fixture value")
		}
		if err := validateABI("running", override); err != nil {
			return "", err
		}
		return override, nil
	}
	if override != "" {
		return "", errors.New("running ABI override is permitted only with an alternate target root")
	}
	command := Command{Operation: OperationInspectRunningABI, Name: unameCommand, Args: []string{"-r"}}
	if err := validateCommand(command); err != nil {
		return "", err
	}
	output, err := manager.captureCommand(ctx, command, maximumABIBytes+2)
	if err != nil {
		return "", fmt.Errorf("read running kernel ABI: %w", err)
	}
	if len(output) > maximumABIBytes+2 {
		return "", errors.New("running kernel ABI output is oversized")
	}
	abi := strings.TrimSuffix(strings.TrimSuffix(string(output), "\n"), "\r")
	if err := validateABI("running", abi); err != nil {
		return "", err
	}
	return abi, nil
}

// Install preflights the request and either returns its dry-run receipt or
// performs the guarded privileged transaction followed by complete verification.
func (manager *Manager) Install(ctx context.Context, request Request) (receipt Receipt, resultErr error) {
	started := managerTimestamp(manager)
	receipt.StartedAt = started
	defer func() {
		receipt.CompletedAt = managerTimestamp(manager)
	}()

	plan, err := manager.prepare(ctx, request)
	if err != nil {
		// Retain any partial plan so receipts can expose structured
		// fresh-target evidence alongside the blocking error.
		receipt.Plan = plan
		return receipt, err
	}
	receipt.Plan = plan
	if request.DryRun {
		return receipt, nil
	}
	if manager.effectiveUID == nil || manager.effectiveUID() != 0 {
		return receipt, errors.New("kernel installation requires effective UID 0; review a dry run, then rerun as root")
	}

	staged, cleanup, err := manager.stagePackages(ctx, plan.Root, plan.Packages)
	if err != nil {
		return receipt, err
	}
	defer cleanup()
	if err := manager.revalidateStagedMetadata(ctx, plan.Packages, staged); err != nil {
		return receipt, err
	}
	// Re-run the complete read-only classification immediately before
	// mutation so records or artefacts added after preflight cannot bypass
	// the fresh-target gate.
	recheckState, err := classifyTargetState(ctx, plan.Root, plan.TargetABI, plan.Packages, plan.DeviceTrees)
	if err != nil {
		return receipt, fmt.Errorf("target changed after preflight: %w", err)
	}
	if recheckState.Classification != TargetStateAbsent {
		if !plan.Overwrite || recheckState.Classification != plan.TargetState.Classification {
			// Attach the recheck evidence to the plan so blocked receipts and
			// diagnostics reflect the state observed just before mutation.
			plan.TargetState = &recheckState
			receipt.Plan = plan
			return receipt, &TargetStateError{Evidence: recheckState}
		}
	}
	currentFallback, err := verifyFallbackProfile(ctx, plan.Root, plan.FallbackABI, plan.Profile)
	if err != nil {
		return receipt, fmt.Errorf("fallback changed after preflight: %w", err)
	}
	if err := fallbackUnchanged(plan.Fallback, currentFallback); err != nil {
		return receipt, err
	}

	backup, backupCleanup, err := createGRUBBackup(ctx, plan.Root)
	if err != nil {
		return receipt, err
	}
	defer backupCleanup()
	// Retire recognised competing hooks and preserve the proven fallback before
	// a package script can regenerate GRUB. Both changes have recovery evidence.
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, restoreBootPreparation(&receipt))
		}
	}()
	if err := prepareBootChanges(ctx, plan, &receipt); err != nil {
		return receipt, err
	}
	commands, err := installationCommands(plan.Root, plan.TargetABI, staged.commandPaths)
	if err != nil {
		return receipt, err
	}
	mutationStarted := false
	runCommand := func(command Command) error {
		if err := validateCommand(command); err != nil {
			return err
		}
		receipt.Executed = append(receipt.Executed, cloneCommand(command))
		mutationStarted = true
		return manager.runMutationCommand(ctx, command, plan.Profile)
	}
	for _, command := range commands {
		if err := ctx.Err(); err != nil {
			if mutationStarted {
				return manager.failAndRollback(plan, backup, receipt, err)
			}
			return receipt, err
		}
		if err := runCommand(command); err != nil {
			return manager.failAndRollback(plan, backup, receipt, fmt.Errorf("%s: %w", command.Operation, err))
		}
	}

	// A staged install can complete without producing the new ABI's initramfs
	// when maintainer scripts skip it. Repair that evidence gap explicitly
	// before boot verification, so the target can never pass without an image.
	initramfsMissing, err := initramfsMissingAfterInstall(plan.Root, plan.TargetABI)
	if err != nil {
		return manager.failAndRollback(plan, backup, receipt, err)
	}
	if initramfsMissing {
		repairs, err := ensureInitramfsCommands(plan.Root, plan.TargetABI)
		if err != nil {
			return manager.failAndRollback(plan, backup, receipt, err)
		}
		for _, command := range repairs {
			if err := ctx.Err(); err != nil {
				return manager.failAndRollback(plan, backup, receipt, err)
			}
			if err := runCommand(command); err != nil {
				return manager.failAndRollback(plan, backup, receipt, fmt.Errorf("%s: %w", command.Operation, err))
			}
		}
	}

	if err := rejectCompetingBootHooks(plan.Root); err != nil {
		return manager.failAndRollback(plan, backup, receipt, err)
	}
	for _, command := range plan.Commands {
		if command.Operation == OperationRefreshBoot {
			if err := runCommand(command); err != nil {
				return manager.failAndRollback(plan, backup, receipt, fmt.Errorf("%s: %w", command.Operation, err))
			}
		}
	}
	installed, trees, err := verifyInstalled(ctx, plan.Root, plan.TargetABI, plan.DeviceTrees)
	if err != nil {
		return manager.failAndRollback(plan, backup, receipt, err)
	}
	if err := verifyBootProfile(ctx, plan.Root, plan.TargetABI, plan.Profile, installed.DeviceTreeBoot); err != nil {
		return manager.failAndRollback(plan, backup, receipt, err)
	}
	wantBootMode := DeviceTreeBootEmbedded
	if plan.EffectiveDTBDelivery == kernel.DTBDeliveryExternalRequired {
		wantBootMode = DeviceTreeBootExternal
	}
	if installed.DeviceTreeBoot.Mode != wantBootMode {
		return manager.failAndRollback(plan, backup, receipt, fmt.Errorf(
			"installed ABI %s uses %s DTB delivery; bundle requires %s", plan.TargetABI, installed.DeviceTreeBoot.Mode, plan.EffectiveDTBDelivery))
	}
	if installed.DeviceTreeBoot.NormalGRUBEntryCount == 0 || installed.DeviceTreeBoot.RecoveryGRUBEntryCount == 0 {
		return manager.failAndRollback(plan, backup, receipt, fmt.Errorf(
			"installed ABI %s requires normal and recovery GRUB bindings; verified %d normal and %d recovery entries",
			plan.TargetABI, installed.DeviceTreeBoot.NormalGRUBEntryCount, installed.DeviceTreeBoot.RecoveryGRUBEntryCount))
	}
	headers, err := verifyInstalledHeaders(ctx, plan.Root, plan.TargetABI, plan.Packages)
	if err != nil {
		return manager.failAndRollback(plan, backup, receipt, err)
	}
	currentFallback, err = verifyFallbackProfile(ctx, plan.Root, plan.FallbackABI, plan.Profile)
	if err != nil {
		return manager.failAndRollback(plan, backup, receipt, err)
	}
	if err := fallbackUnchanged(plan.Fallback, currentFallback); err != nil {
		return manager.failAndRollback(plan, backup, receipt, err)
	}
	receipt.Installed = &installed
	receipt.DeviceTrees = trees
	receipt.Headers = headers
	receipt.RebootRequired = true
	return receipt, nil
}

// plannedDeviceTrees converts the signed package-relative inventory into the
// exact target-root paths used by classification and post-install evidence.
func plannedDeviceTrees(root string, bundle kernel.Bundle) ([]DeviceTree, error) {
	trees := make([]DeviceTree, 0, len(bundle.DeviceTrees))
	for _, tree := range bundle.DeviceTrees {
		relative, valid := tree.FirmwareRelativePath(bundle.ABI)
		if !valid {
			return nil, fmt.Errorf("device tree %s is outside the target ABI firmware directory", tree.Device)
		}
		target, err := rootPath(root, tree.Path)
		if err != nil {
			return nil, err
		}
		trees = append(trees, DeviceTree{
			Device: tree.Device, RelativePath: relative, TargetPath: target,
			ExpectedSHA256: tree.SHA256, EmbeddedMatches: tree.EmbeddedMatches, Required: tree.Required,
		})
	}
	return trees, nil
}

// managerTimestamp safely obtains a timestamp even from a nil or test manager.
func managerTimestamp(manager *Manager) time.Time {
	if manager == nil || manager.now == nil {
		return time.Time{}
	}
	return manager.now().UTC()
}

// cloneCommand prevents later slice mutation from altering receipt evidence.
func cloneCommand(command Command) Command {
	command.Args = append([]string(nil), command.Args...)
	return command
}

// runMutationCommand preserves Lexr's stdout for its human or JSON result.
// Child stderr remains inherited, while operational stdout is routed to the
// manager's diagnostic sink, which defaults to the parent process's stderr.
func (manager *Manager) runMutationCommand(ctx context.Context, command Command, profileID string) error {
	if err := validateCommand(command); err != nil {
		return err
	}
	diagnostics := manager.diagnostics
	if diagnostics == nil {
		diagnostics = os.Stderr
	}
	// Package lifecycle hooks run inside apt/dpkg, before the final explicit
	// refresh. New boot-support helpers use this bounded transaction selection.
	// Clear any inherited value when the caller supplied no profile.
	environment := []string{"LEXR_KERNEL_PLATFORM="}
	if profileID != "" {
		selected, err := profile.Resolve(profileID)
		if err != nil {
			return err
		}
		environment[0] += selected.Platform
	}
	return manager.runner.Run(ctx, platform.Command{
		Name:   command.Name,
		Args:   append([]string(nil), command.Args...),
		Stdout: diagnostics,
		Env:    environment,
	})
}
