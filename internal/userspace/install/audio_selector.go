package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ALSA's packaged selector is a relative link on Arch. Only this exact link
// may be replaced; its shared Qualcomm referent is never read or modified.
const (
	audioSelectorPath = "usr/share/alsa/ucm2/conf.d/x1e80100/x1e80100.conf"
	audioSelectorLink = "../../Qualcomm/x1e80100/x1e80100.conf"
)

// resolveAudioTarget retains normal confinement while inspecting the one
// supported selector link without following its final component.
func resolveAudioTarget(root, relative string) (string, error) {
	if relative != audioSelectorPath {
		return resolveTarget(root, relative)
	}
	sentinel, err := resolveTarget(root, filepath.ToSlash(filepath.Join(filepath.Dir(relative), ".lexr-audio-parent")))
	if err != nil {
		return "", err
	}
	path := filepath.Join(filepath.Dir(sentinel), filepath.Base(relative))
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(path)
		if err != nil || link != audioSelectorLink {
			return "", fmt.Errorf("refusing unexpected audio selector link %s", path)
		}
	} else if !info.Mode().IsRegular() {
		return "", fmt.Errorf("audio selector is not a regular file or supported link: %s", path)
	}
	return path, nil
}

// revalidateAudioSelector requires the same link inode and value observed in
// the plan, including when another link with the same text replaced it.
func revalidateAudioSelector(plan audioChangePlan) error {
	info, err := os.Lstat(plan.change.Target)
	if err != nil || plan.originalInfo == nil || !os.SameFile(plan.originalInfo, info) || info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("audio selector changed after planning: %s", plan.change.Target)
	}
	link, err := os.Readlink(plan.change.Target)
	if err != nil || link != audioSelectorLink || link != plan.originalLink {
		return fmt.Errorf("audio selector link changed after planning: %s", plan.change.Target)
	}
	return nil
}

// backupAudioSelector preserves the link itself in the private backup tree.
func backupAudioSelector(plan audioChangePlan) error {
	if err := revalidateAudioSelector(plan); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plan.change.Backup), 0o700); err != nil {
		return err
	}
	if err := os.Symlink(plan.originalLink, plan.change.Backup); err != nil {
		return fmt.Errorf("back up audio selector link: %w", err)
	}
	return syncDirectory(filepath.Dir(plan.change.Backup))
}

// publishAudioTarget stages and verifies file bytes before replacing a known
// selector link by rename. The generic no-symlink copy policy is unchanged.
func publishAudioTarget(plan audioChangePlan) error {
	if plan.originalLink == "" {
		return atomicCopyVerified(plan.change.Source, plan.change.Target, plan.mode, plan.sourceDigest, plan.sourceSize)
	}
	stage, err := os.MkdirTemp(filepath.Dir(plan.change.Target), ".lexr-audio-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	file := filepath.Join(stage, "selector")
	if err := atomicCopyVerified(plan.change.Source, file, plan.mode, plan.sourceDigest, plan.sourceSize); err != nil {
		return err
	}
	if err := revalidateAudioSelector(plan); err != nil {
		return err
	}
	if err := os.Rename(file, plan.change.Target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(plan.change.Target))
}

// restoreAudioSelector restores the original link type without opening its
// referent and refuses to overwrite a subsequently modified configuration.
func restoreAudioSelector(plan audioChangePlan) error {
	link, err := os.Readlink(plan.change.Backup)
	if err != nil || link != audioSelectorLink || link != plan.originalLink {
		return errors.New("audio selector backup link changed")
	}
	digest, info, err := hashRegularNoFollow(plan.change.Target)
	if err != nil || digest != plan.sourceDigest || info.Size() != plan.sourceSize {
		return errors.New("installed audio selector changed before rollback")
	}
	stage, err := os.MkdirTemp(filepath.Dir(plan.change.Target), ".lexr-audio-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	file := filepath.Join(stage, "selector")
	if err := os.Symlink(link, file); err != nil {
		return err
	}
	if err := os.Rename(file, plan.change.Target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(plan.change.Target))
}
