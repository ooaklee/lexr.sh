package bootidentity

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// devRoot bounds canonical block-device links; portable tests substitute a tree.
var devRoot = "/dev"

// deviceIdentifier admits literal udev identifiers, not paths or substitutions.
var deviceIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// errNoDeviceIdentity avoids disclosing private paths or identifiers in errors.
var errNoDeviceIdentity = errors.New("boot ownership found no unambiguous persistent identifiers for the mounted device")

// deviceIdentity carries private comparison evidence for one block device.
type deviceIdentity struct {
	uuid     string
	partUUID string
	device   string
}

// resolveDeviceIdentity associates udev symlinks with a stat-confirmed device,
// never with a caller's guess about the filesystem or a filename alone.
func resolveDeviceIdentity(ctx context.Context, uuidDir, partUUIDDir string, major, minor int) (deviceIdentity, error) {
	uuid, device, err := resolveByDir(ctx, uuidDir, major, minor)
	if err != nil {
		return deviceIdentity{}, err
	}
	partUUID, partDevice, err := resolveByDir(ctx, partUUIDDir, major, minor)
	if err != nil {
		return deviceIdentity{}, err
	}
	if uuid == "" && partUUID == "" {
		return deviceIdentity{}, errNoDeviceIdentity
	}
	if device == "" {
		device = partDevice
	}
	if partDevice != "" && partDevice != device {
		return deviceIdentity{}, errNoDeviceIdentity
	}
	return deviceIdentity{uuid: uuid, partUUID: partUUID, device: device}, nil
}

// resolveByDir scans bounded metadata once and rejects contradictory aliases.
func resolveByDir(ctx context.Context, dir string, major, minor int) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	names, err := readDirNames(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", errNoDeviceIdentity
	}
	var found, device string
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		if !deviceIdentifier.MatchString(name) {
			continue
		}
		dev, err := canonicalLinkTarget(dir, name)
		if err != nil {
			continue
		}
		devMajor, devMinor, err := statDeviceFn(dev)
		if err != nil || devMajor != major || devMinor != minor {
			continue
		}
		if found != "" && (found != name || device != dev) {
			return "", "", errNoDeviceIdentity
		}
		found, device = name, dev
	}
	return found, device, nil
}

// readDirNames bounds resource use even if the metadata directory is malformed.
func readDirNames(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(maxMountEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(names) > maxMountEntries {
		return nil, errNoDeviceIdentity
	}
	return names, nil
}

// canonicalLinkTarget follows a metadata symlink, including parent links, with
// the standard library's bounded link traversal and a final device-tree bound.
func canonicalLinkTarget(dir, name string) (string, error) {
	link := filepath.Join(dir, name)
	info, err := os.Lstat(link)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return "", errNoDeviceIdentity
	}
	target, err := filepath.EvalSymlinks(link)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(target, devRoot+string(filepath.Separator)) {
		return "", errNoDeviceIdentity
	}
	return target, nil
}
