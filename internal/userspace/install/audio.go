package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// audioTarget maps one immutable release member to its compiled system path.
type audioTarget struct {
	source   string
	relative string
}

// audioTargets is the complete coherent topology and UCM install set.
var audioTargets = []audioTarget{
	{source: "X1E80100-Microsoft-Surface-Pro-11-tplg.bin", relative: "lib/firmware/qcom/x1e80100/X1E80100-Microsoft-Surface-Pro-11-tplg.bin"},
	{source: "MICROSOFT-Surface-Pro-11in.conf", relative: "usr/share/alsa/ucm2/Qualcomm/x1e80100/MICROSOFT-Surface-Pro-11in.conf"},
	{source: "SP11-HiFi.conf", relative: "usr/share/alsa/ucm2/Qualcomm/x1e80100/SP11-HiFi.conf"},
	{source: "x1e80100.conf", relative: "usr/share/alsa/ucm2/conf.d/x1e80100/x1e80100.conf"},
}

// audioChangePlan retains enough original metadata for safe rollback.
type audioChangePlan struct {
	change       FileChange
	relative     string
	sourceDigest string
	sourceSize   int64
	mode         os.FileMode
	originalMode os.FileMode
	originalSize int64
	originalHash string
	originalLink string
	originalInfo os.FileInfo
}

// Audio atomically installs the exact four-file v19c topology and UCM set.
// Existing files are copied into one timestamped backup before any member of
// the new coherent set is published.
func (installer *Installer) Audio(_ context.Context, options Options) (Result, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return Result{}, err
	}
	bundle, err := verifyBundle(options.BundleDir, audioSpec)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Component: AudioComponent, Root: options.Root, DryRun: options.DryRun,
		RebootRequired: true,
	}
	stamp := installer.now().UTC().Format("20060102T150405.000000000Z")
	backupRelative := filepath.ToSlash(filepath.Join("var/lib/lexr/backups/userspace", stamp, AudioComponent))
	backupSentinel, err := resolveTarget(options.Root, filepath.ToSlash(filepath.Join(backupRelative, ".sentinel")))
	if err != nil {
		return Result{}, err
	}
	backupDirectory := filepath.Dir(backupSentinel)

	immutableByName := make(map[string]immutableFile, len(audioSpec.files))
	for _, immutable := range audioSpec.files {
		immutableByName[immutable.name] = immutable
	}
	plans := make([]audioChangePlan, 0, len(audioTargets))
	for _, target := range audioTargets {
		destination, err := resolveAudioTarget(options.Root, target.relative)
		if err != nil {
			return Result{}, err
		}
		immutable := immutableByName[target.source]
		plan := audioChangePlan{
			change:       FileChange{Source: bundle.paths[target.source], Target: destination},
			relative:     target.relative,
			sourceDigest: immutable.sha256,
			sourceSize:   immutable.size,
			mode:         0o644,
		}
		if info, err := os.Lstat(destination); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				plan.originalLink, err = os.Readlink(destination)
				if err != nil || target.relative != audioSelectorPath || plan.originalLink != audioSelectorLink {
					return Result{}, fmt.Errorf("refusing unexpected audio selector link %s", destination)
				}
				plan.originalInfo = info
				plan.change.Action = "replace-link"
			} else if !info.Mode().IsRegular() {
				return Result{}, fmt.Errorf("refusing to replace non-regular audio target %s", destination)
			}
			backupTarget, err := resolveTarget(options.Root, filepath.ToSlash(filepath.Join(backupRelative, target.relative)))
			if err != nil {
				return Result{}, err
			}
			plan.change.Replaced = true
			plan.change.Backup = backupTarget
			plan.originalMode = info.Mode().Perm()
			plan.originalSize = info.Size()
			if plan.originalLink == "" {
				plan.originalHash, _, err = hashRegularNoFollow(destination)
				if err != nil {
					return Result{}, fmt.Errorf("hash existing audio target %s: %w", destination, err)
				}
			}
			result.BackupDirectory = backupDirectory
		} else if !errors.Is(err, os.ErrNotExist) {
			return Result{}, fmt.Errorf("inspect audio target %s: %w", destination, err)
		}
		plans = append(plans, plan)
		result.Files = append(result.Files, plan.change)
	}
	if options.DryRun {
		return result, nil
	}
	if err := installer.requireRoot(false); err != nil {
		return Result{}, err
	}
	if result.BackupDirectory != "" {
		if err := os.MkdirAll(filepath.Dir(backupDirectory), 0o700); err != nil {
			return Result{}, fmt.Errorf("create audio backup parent: %w", err)
		}
		if err := os.Mkdir(backupDirectory, 0o700); err != nil {
			return Result{}, fmt.Errorf("create unique audio backup: %w", err)
		}
		for _, plan := range plans {
			if !plan.change.Replaced {
				continue
			}
			if plan.originalLink != "" {
				if err := backupAudioSelector(plan); err != nil {
					return Result{}, err
				}
				continue
			}
			if err := atomicCopyVerified(plan.change.Target, plan.change.Backup, plan.originalMode, plan.originalHash, plan.originalSize); err != nil {
				return Result{}, fmt.Errorf("back up audio target %s: %w", plan.change.Target, err)
			}
		}
	}

	applied := make([]audioChangePlan, 0, len(plans))
	for _, plan := range plans {
		// Re-resolve immediately before mutation so a changed parent symlink
		// cannot redirect a previously-reviewed target.
		revalidated, err := resolveAudioTarget(options.Root, plan.relative)
		if err != nil || revalidated != plan.change.Target {
			installErr := fmt.Errorf("audio target changed after planning: %s", plan.change.Target)
			return Result{}, errors.Join(installErr, rollbackAudio(applied))
		}
		if err := publishAudioTarget(plan); err != nil {
			// Publication can succeed before a directory sync fails. Include
			// this member in rollback only if it contains our verified bytes.
			if digest, info, inspectErr := hashRegularNoFollow(plan.change.Target); inspectErr == nil && digest == plan.sourceDigest && info.Size() == plan.sourceSize {
				applied = append(applied, plan)
			}
			return Result{}, errors.Join(err, rollbackAudio(applied))
		}
		applied = append(applied, plan)
	}
	return result, nil
}

// relativeToRoot converts a resolved target back into a containment-safe path.
func relativeToRoot(root, target string) string {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return ".."
	}
	return filepath.ToSlash(relative)
}

// rollbackAudio restores old members and removes newly-created members in
// reverse publication order.
func rollbackAudio(applied []audioChangePlan) error {
	var rollbackErr error
	for index := len(applied) - 1; index >= 0; index-- {
		plan := applied[index]
		if plan.originalLink != "" {
			rollbackErr = errors.Join(rollbackErr, restoreAudioSelector(plan))
			continue
		}
		if plan.change.Replaced {
			if err := atomicCopyVerified(plan.change.Backup, plan.change.Target, plan.originalMode, plan.originalHash, plan.originalSize); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore %s: %w", plan.change.Target, err))
			}
			continue
		}
		if err := os.Remove(plan.change.Target); err != nil && !errors.Is(err, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove new target %s: %w", plan.change.Target, err))
		}
	}
	return rollbackErr
}
