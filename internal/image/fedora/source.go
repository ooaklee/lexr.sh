package fedora

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// The Fedora release policy is deliberately separate from layout discovery.
// Another release needs an inspected EROFS/RPM/dracut fixture before accepting
// it; an arbitrary filename or a similar ISO label is not sufficient evidence.
const supportedFedoraRelease = "44"

// SourceVolumeID is the live-media label for the inspected Fedora release policy.
const SourceVolumeID = "Fedora-WS-Live-" + supportedFedoraRelease

// sourceLayout binds the observed publisher volume and GRUB search marker.
type sourceLayout struct {
	VolumeID string
	Marker   string
}

// markerPattern limits publisher markers to the inspected hexadecimal path grammar.
var markerPattern = regexp.MustCompile(`^/boot/0x[0-9a-f]{8}$`)

// parseVolumeID rejects label-prefix matches and duplicate volume descriptors.
func parseVolumeID(pvd string) (string, error) {
	label := ""
	for _, line := range strings.Split(pvd, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Volume Id    : ") {
			continue
		}
		if label != "" {
			return "", errors.New("Fedora ISO has ambiguous volume identity")
		}
		label = strings.TrimPrefix(line, "Volume Id    : ")
	}
	if label != SourceVolumeID {
		return "", fmt.Errorf("unsupported Fedora ISO volume identity %q; inspected release is %s", label, supportedFedoraRelease)
	}
	return label, nil
}

// parseESPMarker accepts the complete inspected indirection grammar, including
// CRLF media, without evaluating source GRUB commands or accepting extra code.
func parseESPMarker(stub string) (string, error) {
	var lines []string
	for _, line := range strings.Split(stub, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "search --file --set=root ") ||
		lines[1] != "set prefix=($root)/boot/grub2" || lines[2] != "configfile ($root)/boot/grub2/grub.cfg" {
		return "", errors.New("unsupported Fedora ESP GRUB indirection")
	}
	marker := strings.TrimPrefix(lines[0], "search --file --set=root ")
	if !markerPattern.MatchString(marker) {
		return "", errors.New("Fedora GRUB marker is not a bounded boot marker")
	}
	return marker, nil
}

// discoverSourceLayout checks source kernel, initramfs, marker and live-label agreement.
func discoverSourceLayout(pvd, stub, grub string) (sourceLayout, error) {
	label, err := parseVolumeID(pvd)
	if err != nil {
		return sourceLayout{}, err
	}
	marker, err := parseESPMarker(stub)
	if err != nil {
		return sourceLayout{}, err
	}
	linuxCount, initrdCount, markerCount := 0, 0, 0
	for _, line := range strings.Split(grub, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "search":
			if strings.Join(fields, " ") != "search --file --set=root "+marker {
				return sourceLayout{}, errors.New("Fedora outer GRUB search disagrees with its ESP")
			}
			markerCount++
		case "linux":
			if len(fields) < 4 || fields[1] != "($root)/boot/aarch64/loader/linux" {
				return sourceLayout{}, errors.New("unsupported Fedora source kernel path")
			}
			rootCount, liveCount := 0, 0
			for _, argument := range fields[2:] {
				if strings.HasPrefix(argument, "root=") {
					if argument != "root=live:CDLABEL="+label {
						return sourceLayout{}, errors.New("Fedora source live root disagrees with its volume label")
					}
					rootCount++
				}
				if argument == "rd.live.image" {
					liveCount++
				}
			}
			if rootCount != 1 || liveCount != 1 {
				return sourceLayout{}, errors.New("Fedora source live discovery is missing or ambiguous")
			}
			linuxCount++
		case "initrd":
			if len(fields) != 2 || fields[1] != "($root)/boot/aarch64/loader/initrd" {
				return sourceLayout{}, errors.New("unsupported Fedora source initramfs path")
			}
			initrdCount++
		case "linuxefi", "initrdefi":
			return sourceLayout{}, errors.New("unsupported Fedora boot command variant")
		}
	}
	if linuxCount == 0 || initrdCount != linuxCount || markerCount != 1 {
		return sourceLayout{}, errors.New("incomplete Fedora source boot layout")
	}
	return sourceLayout{VolumeID: label, Marker: marker}, nil
}

// layoutFromMediaDiscovery reconstructs a unique validated on-media boot identity.
func layoutFromMediaDiscovery(record imagecontract.MediaDiscoveryRecord) (sourceLayout, error) {
	if record.Strategy != "direct-hybrid-iso" || record.Protocol != "dracut-live" {
		return sourceLayout{}, errors.New("invalid Fedora media discovery protocol")
	}
	layout := sourceLayout{}
	liveRoot := ""
	for _, evidence := range record.Evidence {
		switch evidence.Role {
		case "iso-volume-label":
			if evidence.Scope != "iso9660-pvd" || layout.VolumeID != "" {
				return sourceLayout{}, errors.New("invalid or duplicate Fedora volume evidence")
			}
			layout.VolumeID = evidence.Value
		case "grub-search-marker":
			if evidence.Scope != "grub" || layout.Marker != "" {
				return sourceLayout{}, errors.New("invalid or duplicate Fedora marker evidence")
			}
			layout.Marker = evidence.Value
		case "live-root":
			if evidence.Scope != "grub" || liveRoot != "" {
				return sourceLayout{}, errors.New("invalid or duplicate Fedora live root evidence")
			}
			liveRoot = evidence.Value
		}
	}
	if layout.VolumeID != SourceVolumeID || !markerPattern.MatchString(layout.Marker) || liveRoot != "root=live:CDLABEL="+layout.VolumeID+" rd.live.image" {
		return sourceLayout{}, errors.New("Fedora media discovery evidence does not describe an inspected layout")
	}
	return layout, nil
}

// validateSourceRoot checks release identity inside the filesystem as well as
// the outer publisher label before installing any native kernel integration.
func validateSourceRoot(ctx context.Context, docker *platform.Docker, image, workspace, volume string) error {
	const script = `python3 - "$1" <<'PY'
import pathlib, shlex, sys
root = pathlib.Path('/linux-work/rootfs')
fields = {}
for line in (root/'usr/lib/os-release').read_text().splitlines():
    if not line.strip() or line.startswith('#'):
        continue
    key, sep, value = line.partition('=')
    if not sep or key in fields:
        raise SystemExit('unsupported Fedora os-release metadata')
    words = shlex.split(value)
    if len(words) != 1:
        raise SystemExit('unsupported Fedora os-release value')
    fields[key] = words[0]
if fields.get('ID') != 'fedora' or fields.get('VERSION_ID') != sys.argv[1]:
    raise SystemExit('Fedora live root release disagrees with the inspected source policy')
for name in ('usr/bin/dracut', 'usr/bin/lsinitrd', 'usr/lib/kernel/install.d/20-grub.install'):
    if not (root/name).is_file():
        raise SystemExit('Fedora live root is missing ' + name)
PY
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "bash", "-ceu", script, "lexr-fedora-source-root", supportedFedoraRelease); err != nil {
		return fmt.Errorf("unsupported Fedora live root: %w", err)
	}
	return nil
}
