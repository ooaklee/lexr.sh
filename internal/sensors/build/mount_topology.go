package build

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Mount-table bounds limit host metadata reads and parser work before effects.
const (
	maximumBuildMountInfoBytes   = 4 << 20
	maximumBuildMountInfoRecords = 10000
)

// buildMountRecord maps a visible mountpoint to its underlying device/root.
type buildMountRecord struct {
	device, root, mountpoint string
}

// buildMountCoordinate identifies a selected path in underlying mount coordinates.
type buildMountCoordinate struct {
	device, name string
}

// validateBuildMountTopology interprets Linux mountinfo, using POSIX path rules
// even when its pure fixture tests run on another host. This is not a portable
// Windows filesystem-identity rule. The namespace and its administrator remain
// trusted; callers recheck immediately before effects to bound stale topology.
func validateBuildMountTopology(data []byte, paths []string) error {
	if len(paths) != 3 {
		return errors.New("mount topology requires source, work and output paths")
	}
	for _, name := range paths {
		if !normalizedBuildMountPath(name) {
			return errors.New("selected mount topology paths must be canonical POSIX absolute paths")
		}
	}
	if len(data) == 0 || len(data) > maximumBuildMountInfoBytes || data[len(data)-1] != '\n' || !utf8.Valid(data) {
		return errors.New("mountinfo is empty, truncated, oversized or not UTF-8")
	}
	if records := bytes.Count(data, []byte{'\n'}); records == 0 || records > maximumBuildMountInfoRecords {
		return errors.New("mountinfo exceeds its fixed record bound")
	}
	lines := strings.Split(string(data[:len(data)-1]), "\n")
	mounts := make([]buildMountRecord, 0, len(lines))
	ids := make(map[string]bool, len(lines))
	for i, line := range lines {
		record, id, err := parseBuildMountRecord(line)
		if err != nil {
			return fmt.Errorf("mountinfo record %d: %w", i+1, err)
		}
		if ids[id] {
			return errors.New("mountinfo repeats a mount ID")
		}
		ids[id] = true
		mounts = append(mounts, record)
	}
	coordinates := make([]buildMountCoordinate, 0, len(paths))
	for _, selected := range paths {
		var deepest *buildMountRecord
		containing := make(map[string]bool)
		for i := range mounts {
			mount := &mounts[i]
			if !buildMountContains(mount.mountpoint, selected) {
				continue
			}
			// A stacked ancestor may hide an older deeper child. Do not guess
			// visibility from pathname depth when ANY containing ancestor stacks.
			if containing[mount.mountpoint] {
				return errors.New("mountinfo has ambiguous stacked mounts on a selected path")
			}
			containing[mount.mountpoint] = true
			if deepest == nil || len(mount.mountpoint) > len(deepest.mountpoint) {
				deepest = mount
			}
		}
		if deepest == nil {
			return errors.New("selected path has no mountinfo mapping")
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(selected, deepest.mountpoint), "/")
		physical := path.Join(deepest.root, relative)
		coordinates = append(coordinates, buildMountCoordinate{deepest.device, physical})
	}
	for i, a := range coordinates {
		for _, b := range coordinates[i+1:] {
			if a.device == b.device && (buildMountContains(a.name, b.name) || buildMountContains(b.name, a.name)) {
				return errors.New("source, work and output overlap through Linux mount topology")
			}
		}
	}
	return nil
}

// parseBuildMountRecord decodes one bounded Linux mountinfo record without
// guessing the visibility or meaning of malformed known fields.
func parseBuildMountRecord(line string) (buildMountRecord, string, error) {
	var zero buildMountRecord
	// Kernel mountinfo has fixed space separators and escaped whitespace.
	// Optional tags are extensible; unknown tags do not change coordinates.
	if line == "" || strings.Count(line, " ") > 32 {
		return zero, "", errors.New("invalid or excessive mountinfo fields")
	}
	fields := strings.Split(line, " ")
	if len(fields) < 10 {
		return zero, "", errors.New("missing mountinfo fields")
	}
	separator := -1
	for i, field := range fields {
		if _, err := decodeBuildMountField(field); err != nil {
			return zero, "", err
		}
		if field == "-" {
			if separator != -1 {
				return zero, "", errors.New("ambiguous mountinfo separator")
			}
			separator = i
		}
	}
	if separator < 6 || len(fields) != separator+4 {
		return zero, "", errors.New("mountinfo separator or trailing fields differ")
	}
	if !canonicalBuildMountNumber(fields[0], false, 64) || !canonicalBuildMountNumber(fields[1], true, 64) {
		return zero, "", errors.New("invalid mount or parent ID")
	}
	device := strings.Split(fields[2], ":")
	if len(device) != 2 || !canonicalBuildMountNumber(device[0], true, 32) || !canonicalBuildMountNumber(device[1], true, 32) {
		return zero, "", errors.New("invalid mount device identity")
	}
	root, err := decodeBuildMountField(fields[3])
	if err != nil || !normalizedBuildMountPath(root) {
		return zero, "", errors.New("mount root is not a normalized absolute path")
	}
	mountpoint, err := decodeBuildMountField(fields[4])
	if err != nil || !normalizedBuildMountPath(mountpoint) {
		return zero, "", errors.New("mountpoint is not a normalized absolute path")
	}
	if !buildMountAccessOptions(fields[5]) || !buildMountAccessOptions(fields[separator+3]) {
		return zero, "", errors.New("mount access options are missing")
	}
	tags := make(map[string]bool)
	for _, field := range fields[6:separator] {
		tag, value, colon := strings.Cut(field, ":")
		if tag != "unbindable" && tag != "shared" && tag != "master" && tag != "propagate_from" {
			// The Linux mountinfo ABI explicitly tells parsers to ignore
			// unrecognised optional fields. They supply no path authority.
			continue
		}
		valid := tag == "unbindable" && !colon
		if tag == "shared" || tag == "master" || tag == "propagate_from" {
			valid = colon && canonicalBuildMountNumber(value, false, 64)
		}
		if !valid || tags[tag] {
			return zero, "", errors.New("malformed or repeated known mount propagation tag")
		}
		tags[tag] = true
	}
	return buildMountRecord{fields[2], root, mountpoint}, fields[0], nil
}

// canonicalBuildMountNumber checks decimal mount identifiers without aliases.
func canonicalBuildMountNumber(value string, allowZero bool, bits int) bool {
	if value == "" || len(value) > 1 && value[0] == '0' || strings.Trim(value, "0123456789") != "" {
		return false
	}
	n, err := strconv.ParseUint(value, 10, bits)
	return err == nil && (allowZero || n != 0)
}

// buildMountAccessOptions requires the leading read-only or read-write marker.
func buildMountAccessOptions(value string) bool {
	first, _, _ := strings.Cut(value, ",")
	return first == "rw" || first == "ro"
}

// decodeBuildMountField accepts only the documented kernel whitespace escapes
// and refuses unescaped controls or truncated field encodings.
func decodeBuildMountField(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty mountinfo field")
	}
	var result strings.Builder
	result.Grow(len(raw))
	for i := 0; i < len(raw); {
		c := raw[i]
		if c == '\\' {
			if len(raw)-i < 4 {
				return "", errors.New("truncated mountinfo escape")
			}
			var decoded byte
			switch raw[i : i+4] {
			case `\040`:
				decoded = ' '
			case `\011`:
				decoded = '\t'
			case `\012`:
				decoded = '\n'
			case `\134`:
				decoded = '\\'
			default:
				return "", errors.New("unsupported mountinfo escape")
			}
			result.WriteByte(decoded)
			i += 4
			continue
		}
		if c <= ' ' || c == 0x7f {
			return "", errors.New("unescaped whitespace or control in mountinfo")
		}
		result.WriteByte(c)
		i++
	}
	return result.String(), nil
}

// normalizedBuildMountPath requires canonical absolute UTF-8 POSIX coordinates.
func normalizedBuildMountPath(name string) bool {
	return utf8.ValidString(name) && strings.IndexByte(name, 0) < 0 && path.IsAbs(name) && path.Clean(name) == name
}

// buildMountContains tests whole-component ancestry rather than path prefixes.
func buildMountContains(parent, child string) bool {
	return parent == "/" || parent == child || strings.HasPrefix(child, parent+"/")
}
