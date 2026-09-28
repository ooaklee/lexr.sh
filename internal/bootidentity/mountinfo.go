package bootidentity

import (
	"bytes"
	"errors"
	"path"
	"strconv"
	"strings"
)

// Bounds for mountinfo parsing. Real mountinfo files are far smaller; larger
// input or entry counts indicate malformed or hostile data and fail closed.
const (
	maxMountInfoBytes = 1 << 20
	maxMountEntries   = 8192
	maxFieldLength    = 4096
)

// errMalformedMountInfo is returned for unparsable, truncated, oversized, or
// ambiguous mount tables. Errors carry no identifiers or paths.
var errMalformedMountInfo = errors.New("boot ownership could not read mounted filesystem evidence")

// mountEntry is one parsed /proc/self/mountinfo record.
type mountEntry struct {
	mountID      int
	parentID     int
	devMajor     int
	devMinor     int
	root         string // root within the filesystem, e.g. "/@root" for btrfs
	mountPoint   string // decoded absolute mount point
	mountOptions string
	fstype       string
	source       string
}

// parseMountInfo parses mountinfo content with strict bounds. It is pure so it
// can be unit-tested on any platform.
func parseMountInfo(data []byte) ([]mountEntry, error) {
	if len(data) > maxMountInfoBytes {
		return nil, errMalformedMountInfo
	}
	var entries []mountEntry
	for len(data) > 0 {
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			line, data = data, nil
		}
		if len(line) == 0 {
			continue
		}
		entry, err := parseMountEntry(string(line))
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
		if len(entries) > maxMountEntries {
			return nil, errMalformedMountInfo
		}
	}
	if len(entries) == 0 {
		return nil, errMalformedMountInfo
	}
	return entries, nil
}

// parseMountEntry retains only the bounded fields used for ownership.
func parseMountEntry(line string) (mountEntry, error) {
	var entry mountEntry
	if len(line) > maxFieldLength*7 {
		return entry, errMalformedMountInfo
	}
	fields := strings.Fields(line)
	// 6 mandatory fields, separator "-", then 3 more = 10 minimum.
	if len(fields) < 10 {
		return entry, errMalformedMountInfo
	}
	for _, f := range fields {
		if len(f) > maxFieldLength {
			return entry, errMalformedMountInfo
		}
	}
	id, err := strconv.Atoi(fields[0])
	if err != nil || id < 0 {
		return entry, errMalformedMountInfo
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil || parent < 0 {
		return entry, errMalformedMountInfo
	}
	major, minor, err := parseDevNumber(fields[2])
	if err != nil {
		return entry, err
	}
	root, err := decodeMountInfoPath(fields[3])
	if err != nil {
		return entry, err
	}
	point, err := decodeMountInfoPath(fields[4])
	if err != nil {
		return entry, err
	}
	if !strings.HasPrefix(root, "/") || !strings.HasPrefix(point, "/") || path.Clean(root) != root || path.Clean(point) != point {
		return entry, errMalformedMountInfo
	}
	// Optional fields end at the "-" separator; require exactly one.
	sep := -1
	for i := 6; i < len(fields); i++ {
		if fields[i] == "-" {
			if sep >= 0 {
				return entry, errMalformedMountInfo
			}
			sep = i
		}
	}
	if sep < 0 || len(fields)-sep != 4 {
		return entry, errMalformedMountInfo
	}
	source, err := decodeMountInfoPath(fields[sep+2])
	if err != nil {
		return entry, err
	}
	entry = mountEntry{
		mountID:      id,
		parentID:     parent,
		devMajor:     major,
		devMinor:     minor,
		root:         root,
		mountPoint:   point,
		mountOptions: fields[5],
		fstype:       fields[sep+1],
		source:       source,
	}
	return entry, nil
}

// parseDevNumber reads the decimal device number supplied by mountinfo.
func parseDevNumber(field string) (int, int, error) {
	majorMinor := strings.Split(field, ":")
	if len(majorMinor) != 2 {
		return 0, 0, errMalformedMountInfo
	}
	major, err := strconv.Atoi(majorMinor[0])
	if err != nil || major < 0 {
		return 0, 0, errMalformedMountInfo
	}
	minor, err := strconv.Atoi(majorMinor[1])
	if err != nil || minor < 0 {
		return 0, 0, errMalformedMountInfo
	}
	return major, minor, nil
}

// decodeMountInfoPath decodes octal escapes such as \040 for spaces and
// \134 for backslashes, rejecting malformed escapes and NUL bytes.
func decodeMountInfoPath(field string) (string, error) {
	if !strings.Contains(field, `\`) {
		if strings.Contains(field, "\x00") {
			return "", errMalformedMountInfo
		}
		return field, nil
	}
	var b strings.Builder
	for i := 0; i < len(field); i++ {
		if field[i] != '\\' {
			if field[i] == 0 {
				return "", errMalformedMountInfo
			}
			b.WriteByte(field[i])
			continue
		}
		if i+4 > len(field) {
			return "", errMalformedMountInfo
		}
		v := 0
		for j := 1; j <= 3; j++ {
			c := field[i+j]
			if c < '0' || c > '7' {
				return "", errMalformedMountInfo
			}
			v = v*8 + int(c-'0')
		}
		if v != 32 && v != 9 && v != 10 && v != 92 {
			return "", errMalformedMountInfo
		}
		b.WriteByte(byte(v))
		i += 3
	}
	return b.String(), nil
}

// errAmbiguousIdentity marks records that cannot be uniquely interpreted.
var errAmbiguousIdentity = errors.New("boot ownership found ambiguous mounted filesystem evidence")

// errRootNotMountpoint reports that the selected root is not an exact mount
// point. Sanitised: no paths or identifiers.
var errRootNotMountpoint = errors.New("boot ownership requires an exactly mounted Linux root")

// selectRootAndBoot picks the authoritative mountinfo records for an exact
// mounted root and its boot directory. It is pure. When bootWithinRoot is
// true, no separate boot mount exists and boot belongs to the root filesystem.
func selectRootAndBoot(entries []mountEntry, root string) (rootEntry, bootEntry mountEntry, bootWithinRoot bool, err error) {
	rootEntry, ok := lastExactMount(entries, root)
	if !ok {
		return mountEntry{}, mountEntry{}, false, errRootNotMountpoint
	}
	if err := checkUnambiguous(entries, root, rootEntry); err != nil {
		return mountEntry{}, mountEntry{}, false, err
	}
	bootMount := joinMountPath(root, "boot")
	bootEntry, hasBoot := lastExactMount(entries, bootMount)
	if hasBoot {
		if err := checkUnambiguous(entries, bootMount, bootEntry); err != nil {
			return mountEntry{}, mountEntry{}, false, err
		}
		return rootEntry, bootEntry, false, nil
	}
	return rootEntry, rootEntry, true, nil
}

// lastExactMount returns one exact candidate; checkUnambiguous must reject
// conflicting views rather than assuming mountinfo order describes visibility.
func lastExactMount(entries []mountEntry, mountPoint string) (mountEntry, bool) {
	var found mountEntry
	ok := false
	for _, e := range entries {
		if e.mountPoint == mountPoint {
			found, ok = e, true
		}
	}
	return found, ok
}

// checkUnambiguous rejects conflicting stacked views at a selected mountpoint.
func checkUnambiguous(entries []mountEntry, mountPoint string, top mountEntry) error {
	for _, e := range entries {
		if e.mountPoint != mountPoint {
			continue
		}
		if e.devMajor != top.devMajor || e.devMinor != top.devMinor || e.root != top.root || e.fstype != top.fstype || e.source != top.source {
			return errAmbiguousIdentity
		}
	}
	return nil
}

// joinMountPath joins two already-decoded absolute mountinfo paths.
func joinMountPath(root, name string) string {
	return strings.TrimRight(root, "/") + "/" + name
}
