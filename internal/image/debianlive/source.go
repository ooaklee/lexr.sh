// Package debianlive implements Debian live ARM64 media-source identity and
// the live-boot media contract.
package debianlive

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path"
	"strings"
)

// AdapterID is the stable manifest and catalogue identifier.
const AdapterID = "debian-live"

// maximumSourceConfigBytes bounds metadata interpreted as paths, never commands.
const maximumSourceConfigBytes = 64 << 10

// inspectedSourceGRUBSHA256 pins the complete inspected grub.cfg of this first
// source snapshot so no other byte sequence can be interpreted as the medium.
const inspectedSourceGRUBSHA256 = "84e2dab866b41e55faabfea5dd12d12f40590c793829ada2b4a5e283bff1a777"

// inspectedDiskInfo is the exact disk-info identity of the inspected medium:
// 71 bytes with no trailing newline.
const inspectedDiskInfo = "Auto-generated Debian GNU/Linux Live testing gnome 2024-09-02T08:13:51Z"

// sourceLayout binds the live filesystem to the live kernel's exact paths.
type sourceLayout struct {
	// liveDirectory is the fixed live-media payload directory on the medium.
	liveDirectory string
	// kernel is the medium-relative live kernel selected by the pinned GRUB.
	kernel string
	// initrd is the medium-relative initramfs paired with kernel.
	initrd string
}

// parseSourceLayout verifies the pinned grub.cfg and disk-info identity byte
// for byte, then confirms the pinned GRUB structurally selects the inspected
// live payload before returning the fixed layout.
//
// The sourced install_start.cfg/install.cfg debian-installer routes present in
// the grub.cfg are replaced by the canonical SP11 live menu and are not a
// supported boot contract; the digest pin plus the structural checks below
// therefore deliberately make no claim about those d-i entries.
func parseSourceLayout(grub, diskInfo []byte) (sourceLayout, error) {
	empty := sourceLayout{}
	if len(grub) == 0 || len(grub) > maximumSourceConfigBytes {
		return empty, errors.New("source grub.cfg is empty or oversized")
	}
	digest := sha256.Sum256(grub)
	if hex.EncodeToString(digest[:]) != inspectedSourceGRUBSHA256 {
		return empty, errors.New("source grub.cfg does not match the inspected Debian live snapshot")
	}
	if string(diskInfo) != inspectedDiskInfo {
		return empty, errors.New("source disk-info does not match the inspected Debian live snapshot")
	}
	const kernelPath = "/live/vmlinuz-6.10.6-arm64"
	const initrdPath = "/live/initrd.img-6.10.6-arm64"
	linuxCount, initrdCount := 0, 0
	for _, line := range strings.Split(string(grub), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "linux":
			linuxCount++
			if linuxCount > 2 {
				return empty, errors.New("live-boot source accepts at most two active live entries")
			}
			if fields[1] != kernelPath {
				return empty, errors.New("unexpected Debian live kernel path")
			}
			boots := 0
			for _, arg := range fields[2:] {
				key, value, _ := strings.Cut(arg, "=")
				switch key {
				case "boot":
					boots++
					if value != "live" {
						return empty, errors.New("Debian live source does not select live-boot")
					}
				case "live-media-path":
					if value != "/live" {
						return empty, errors.New("Debian live media path differs from the inspected payload")
					}
				}
			}
			if boots != 1 {
				return empty, errors.New("Debian live source requires exactly one live-boot selector")
			}
			if !hasExactArgument(fields[2:], "components") {
				return empty, errors.New("Debian live source requires the components selector")
			}
		case "initrd":
			initrdCount++
			if initrdCount > 2 {
				return empty, errors.New("live-boot source accepts at most two active initrd entries")
			}
			if len(fields) != 2 || fields[1] != initrdPath {
				return empty, errors.New("unexpected Debian live initramfs path")
			}
		}
	}
	if linuxCount != 2 || initrdCount != 2 {
		return empty, errors.New("Debian live source requires paired live entries")
	}
	return sourceLayout{"live", "live/vmlinuz-6.10.6-arm64", "live/initrd.img-6.10.6-arm64"}, nil
}

// hasExactArgument reports whether a valueless kernel argument appears exactly.
func hasExactArgument(arguments []string, wanted string) bool {
	for _, argument := range arguments {
		if argument == wanted {
			return true
		}
	}
	return false
}

// member names a fixed file under the validated live directory.
func (layout sourceLayout) member(name string) string { return path.Join(layout.liveDirectory, name) }

// inspectedSourceISOHash authenticates the accepted historical source snapshot.
const inspectedSourceISOHash = "3260c69821f85464974e2136a0cda5d3954818467dda168bdfcb69547c4d7abc"

// inspectedSourceISOSize bounds the exact checksum-verified source ISO.
const inspectedSourceISOSize int64 = 3598430208

// outputLayout is Debian's canonical finished live-media payload layout.
var outputLayout = sourceLayout{"live", "live/vmlinuz", "live/initrd.img"}
