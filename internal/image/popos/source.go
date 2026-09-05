// Package popos implements the Pop!_OS ARM64 live-media contract.
package popos

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// maximumSourceConfigBytes bounds source metadata before interpreting paths.
const maximumSourceConfigBytes = 64 << 10

// liveDirectoryPattern accepts Pop's versioned, single-SquashFS directory.
// Catalogue build numbers need not match the source's internal debug build name.
var liveDirectoryPattern = regexp.MustCompile(`^casper_pop-os_[A-Za-z0-9][A-Za-z0-9._-]{0,160}$`)

// sourceLayout records the source's explicit Casper paths. The Pop installer
// and recovery setup retain the /casper symlink to this directory.
type sourceLayout struct {
	liveDirectory string
	kernel        string
	initrd        string
}

// parseSourceLayout reads the boot paths as data, without evaluating source
// GRUB commands. Only Pop's ARM64 single-filesystem contract is accepted.
func parseSourceLayout(grub, diskInfo []byte) (sourceLayout, error) {
	if len(grub) == 0 || len(grub) > maximumSourceConfigBytes || len(diskInfo) > maximumSourceConfigBytes {
		return sourceLayout{}, errors.New("Pop source metadata is empty or exceeds its size limit")
	}
	info := strings.Fields(string(diskInfo))
	if len(info) < 5 || info[0] != "Pop_OS" || !strings.Contains(string(diskInfo), " - Release arm64 (") {
		return sourceLayout{}, errors.New("source media does not identify a Pop!_OS ARM64 release")
	}
	var result sourceLayout
	linuxCount, initrdCount := 0, 0
	for _, line := range strings.Split(string(grub), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "linux":
			linuxCount++
			kernelPath := strings.TrimPrefix(fields[1], "/")
			directory := path.Dir(kernelPath)
			if fields[1] != "/"+kernelPath || !liveDirectoryPattern.MatchString(directory) || path.Base(kernelPath) != "vmlinuz.efi" {
				return sourceLayout{}, fmt.Errorf("unsupported Pop source kernel path %q", fields[1])
			}
			bootCount, mediaCount := 0, 0
			for _, argument := range fields[2:] {
				key, value, _ := strings.Cut(argument, "=")
				switch key {
				case "boot":
					bootCount++
					if value != "casper" {
						return sourceLayout{}, errors.New("Pop source does not select Casper")
					}
				case "live-media-path":
					mediaCount++
					if value != "/"+directory {
						return sourceLayout{}, errors.New("Pop source kernel and live-media-path disagree")
					}
				}
			}
			if bootCount != 1 || mediaCount != 1 {
				return sourceLayout{}, errors.New("Pop source requires one explicit Casper selector and live-media-path")
			}
			if result.kernel != "" && result.kernel != kernelPath {
				return sourceLayout{}, errors.New("Pop source selects multiple live kernels")
			}
			result.liveDirectory, result.kernel = directory, kernelPath
		case "initrd":
			initrdCount++
			if len(fields) != 2 || !strings.HasPrefix(fields[1], "/") {
				return sourceLayout{}, errors.New("Pop source requires one absolute initrd path")
			}
			initrdPath := strings.TrimPrefix(fields[1], "/")
			if !liveDirectoryPattern.MatchString(path.Dir(initrdPath)) || path.Base(initrdPath) != "initrd.gz" || result.initrd != "" && result.initrd != initrdPath {
				return sourceLayout{}, errors.New("unsupported or ambiguous Pop source initrd path")
			}
			result.initrd = initrdPath
		}
	}
	if linuxCount != 1 || initrdCount != 1 || path.Dir(result.initrd) != result.liveDirectory {
		return sourceLayout{}, errors.New("Pop source requires one paired live kernel and initrd")
	}
	return result, nil
}

// member names a known file under the source's validated live directory.
func (layout sourceLayout) member(name string) string {
	return path.Join(layout.liveDirectory, name)
}
