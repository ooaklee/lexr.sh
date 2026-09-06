// Package elementary implements elementary OS ARM64 Casper media and its GRUB installer hand-off.
package elementary

import (
	"errors"
	"path"
	"regexp"
	"strings"
)

// maximumSourceConfigBytes bounds metadata interpreted as paths, never commands.
const maximumSourceConfigBytes = 64 << 10

// liveDirectoryPattern deliberately accepts only elementary's inspected fixed directory.
var liveDirectoryPattern = regexp.MustCompile(`^casper$`)

// sourceLayout binds the installer filesystem to the live kernel's exact paths.
type sourceLayout struct{ liveDirectory, kernel, initrd string }

// parseSourceLayout accepts the normal and safe-graphics source entries only
// when every selector names elementary's one fixed Casper payload.
func parseSourceLayout(grub, diskInfo []byte) (sourceLayout, error) {
	empty := sourceLayout{}
	if len(grub) == 0 || len(grub) > maximumSourceConfigBytes || len(diskInfo) > maximumSourceConfigBytes || !strings.HasPrefix(string(diskInfo), `elementary OS 8.1 "circe" - stable arm64 (`) {
		return empty, errors.New("source is not the inspected elementary OS 8.1 ARM64 medium")
	}
	linuxCount, initrdCount := 0, 0
	for _, line := range strings.Split(string(grub), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "linux":
			linuxCount++
			if fields[1] != "/casper/vmlinuz" {
				return empty, errors.New("unexpected elementary live kernel path")
			}
			boots := 0
			for _, arg := range fields[2:] {
				key, value, _ := strings.Cut(arg, "=")
				switch key {
				case "boot":
					boots++
					if value != "casper" {
						return empty, errors.New("elementary source does not select Casper")
					}
				case "live-media-path":
					if value != "/casper" {
						return empty, errors.New("elementary live media path differs from installer")
					}
				}
			}
			if boots != 1 {
				return empty, errors.New("elementary source requires exactly one Casper selector")
			}
		case "initrd":
			initrdCount++
			if len(fields) != 2 || fields[1] != "/casper/initrd.lz" {
				return empty, errors.New("unexpected elementary live initramfs path")
			}
		}
	}
	if linuxCount < 1 || linuxCount > 2 || linuxCount != initrdCount {
		return empty, errors.New("elementary source requires paired live entries")
	}
	return sourceLayout{"casper", "casper/vmlinuz", "casper/initrd.lz"}, nil
}

// member names a fixed file under the validated Casper directory.
func (layout sourceLayout) member(name string) string { return path.Join(layout.liveDirectory, name) }
