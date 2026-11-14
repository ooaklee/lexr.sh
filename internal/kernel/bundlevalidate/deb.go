// Package bundlevalidate performs target-independent validation of a local
// kernel build bundle directory on any host without root privileges. It
// reuses the checksum, manifest, and identity contracts already enforced by
// local bundle discovery and augments them with Debian package structure,
// per-package ABI (vermagic) uniformity, and modules.dep presence checks.
package bundlevalidate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// maximumControlMemberBytes bounds each control archive member used for
// control field inspection.
const maximumControlMemberBytes int64 = 1 << 20

// debIntegrity summarizes the structural inspection of one Debian package.
type debIntegrity struct {
	// Control holds the decoded debian/control stanza.
	Control debControl
	// DataEntryCount is the number of regular files and directories in the
	// data payload.
	DataEntryCount int
	// ModulesDependPresent records whether usr/lib/modules/<abi>/modules.dep
	// exists in the data payload.
	ModulesDependPresent bool
	// DepmodPostinst records whether the package maintainer script generates
	// module dependency metadata at install time using depmod.
	DepmodPostinst bool
	// ModulesDirectory records the exact usr/lib/modules/<abi> directory
	// observed in the data payload, when present.
	ModulesDirectory string
	// BootImage records the exact /boot/vmlinuz-<abi> image observed in the
	// data payload, when present.
	BootImage string
	// HeaderDirectory records the exact usr/src/linux-headers-<abi> directory
	// observed in the data payload, when present.
	HeaderDirectory string
}

// debControl holds the Debian control fields needed by bundle validation.
type debControl struct {
	Package      string
	Version      string
	Architecture string
}

// arReader walks one Unix ar archive member by member.
type arReader struct {
	reader io.Reader
}

// newArReader begins reading one ar archive after validating its magic.
func newArReader(data []byte) (*arReader, error) {
	if len(data) < 8 || string(data[:8]) != "!<arch>\n" {
		return nil, errors.New("missing ar archive magic")
	}
	return &arReader{reader: bytes.NewReader(data[8:])}, nil
}

// next returns the name and contents of the next member.
func (archive *arReader) next() (string, []byte, error) {
	header := make([]byte, 60)
	if _, err := io.ReadFull(archive.reader, header); err != nil {
		if errors.Is(err, io.EOF) {
			return "", nil, io.EOF
		}
		return "", nil, fmt.Errorf("read ar member header: %w", err)
	}
	name := strings.TrimRight(string(header[0:16]), " ")
	sizeText := strings.TrimSpace(string(header[48:58]))
	var size int64
	if _, err := fmt.Sscanf(sizeText, "%d", &size); err != nil {
		return "", nil, fmt.Errorf("parse ar member size %q", sizeText)
	}
	if size < 0 {
		return "", nil, fmt.Errorf("negative ar member size %q", sizeText)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(archive.reader, payload); err != nil {
		return "", nil, fmt.Errorf("read ar member %q: %w", name, err)
	}
	if size%2 == 1 {
		if _, err := io.ReadFull(archive.reader, make([]byte, 1)); err != nil {
			return "", nil, fmt.Errorf("read ar member padding: %w", err)
		}
	}
	return name, payload, nil
}

// decompressMember returns the plain bytes of one possibly compressed tar
// member, using the zstd command-line tool for zstandard payloads.
func (validator *Validator) decompressMember(ctx context.Context, member string, payload []byte) ([]byte, error) {
	switch {
	case strings.HasSuffix(member, ".gz"):
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("open gzip member %q: %w", member, err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("decompress gzip member %q: %w", member, err)
		}
		return data, nil
	case strings.HasSuffix(member, ".zst"):
		return validator.decompressZstd(ctx, member, payload)
	default:
		return payload, nil
	}
}

// decompressZstd pipes payload through the host zstd tool, mirroring the
// external-tool boundary already used by image release preparation.
func (validator *Validator) decompressZstd(ctx context.Context, member string, payload []byte) ([]byte, error) {
	runner := validator.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	var output bytes.Buffer
	err := runner.Run(ctx, platform.Command{
		Name: "zstd", Args: []string{"--decompress", "--stdout", "--no-progress", "--quiet"},
		Env:   []string{"LC_ALL=C"},
		Stdin: bytes.NewReader(payload), Stdout: &output,
	})
	if err != nil {
		return nil, fmt.Errorf("decompress zstd member %q (the zstd tool must be installed): %w", member, err)
	}
	return output.Bytes(), nil
}

// inspectDeb performs structural validation of one Debian package using
// dpkg-deb when available and the built-in ar/tar parser otherwise.
func (validator *Validator) inspectDeb(ctx context.Context, path, name string) (debIntegrity, []string) {
	if validator.dpkgDebAvailable() {
		if integrity, warnings, ok := validator.inspectDebWithDpkg(ctx, path, name); ok {
			return integrity, warnings
		}
	}
	integrity, err := validator.inspectDebNative(ctx, path)
	if err != nil {
		return debIntegrity{}, []string{fmt.Sprintf("deb structure: %s: %v", name, err)}
	}
	return integrity, nil
}

// inspectDebWithDpkg runs dpkg-deb --info and --contents and reports whether
// the tool was usable, so the caller can fall back to the built-in parser.
func (validator *Validator) inspectDebWithDpkg(ctx context.Context, path, name string) (debIntegrity, []string, bool) {
	runner := validator.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	controlBytes, err := runner.Capture(ctx, platform.Command{
		Name: "dpkg-deb", Args: []string{"--info", path}, Env: []string{"LC_ALL=C"},
	})
	if err != nil {
		return debIntegrity{}, []string{
			fmt.Sprintf("dpkg-deb could not read %s: %v; falling back to the built-in ar/tar parser", name, err),
		}, false
	}
	control, controlErr := parseControlStanza(controlBytes)
	warnings := []string{}
	if controlErr != nil {
		warnings = append(warnings, fmt.Sprintf("dpkg-deb control for %s: %v", name, controlErr))
	}
	integrity := debIntegrity{Control: control}
	script, scriptErr := runner.Capture(ctx, platform.Command{
		Name: "dpkg-deb", Args: []string{"--info", path, "postinst"}, Env: []string{"LC_ALL=C"},
	})
	if scriptErr == nil && strings.Contains(string(script), "depmod") {
		integrity.DepmodPostinst = true
	}
	listing, listErr := runner.Capture(ctx, platform.Command{
		Name: "dpkg-deb", Args: []string{"--contents", path}, Env: []string{"LC_ALL=C"},
	})
	if listErr != nil {
		warnings = append(warnings, fmt.Sprintf("dpkg-deb could not list %s: %v", name, listErr))
		return integrity, warnings, true
	}
	for _, raw := range strings.Split(string(listing), "\n") {
		entry, ok := dataPathFromListing(raw)
		if !ok {
			continue
		}
		integrity.DataEntryCount++
		integrity.record(entry)
	}
	return integrity, warnings, true
}

// dataPathFromListing extracts the payload path from one dpkg-deb --contents
// line, tolerating spaces in the remainder of the line.
func dataPathFromListing(raw string) (string, bool) {
	fields := strings.Fields(raw)
	if len(fields) < 6 || !strings.HasPrefix(fields[0], "-") && !strings.HasPrefix(fields[0], "d") {
		return "", false
	}
	rest := raw[strings.LastIndex(raw, "-> ")+1:]
	if strings.Contains(raw, " -> ") {
		rest = raw[strings.Index(raw, " -> ")+4:]
	} else {
		rest = raw
	}
	// The path starts after the date/time columns; find " ./".
	index := strings.Index(rest, " ./")
	if index < 0 {
		return "", false
	}
	return strings.TrimSpace(rest[index+1:]), true
}

// parseControlStanza extracts the required identity fields from one control
// document that may contain multiple paragraphs.
func parseControlStanza(document []byte) (debControl, error) {
	var control debControl
	for _, line := range strings.Split(string(document), "\n") {
		if strings.TrimSpace(line) == "" {
			if control.Package != "" {
				break
			}
			continue
		}
		applyControlLine(&control, line)
	}
	if control.Package == "" || control.Version == "" || control.Architecture == "" {
		return control, errors.New("control stanza lacks Package, Version, or Architecture")
	}
	return control, nil
}

// applyControlLine records one control header line into control.
func applyControlLine(control *debControl, line string) {
	field, value, found := strings.Cut(line, ":")
	if !found {
		return
	}
	value = strings.TrimSpace(value)
	switch strings.TrimSpace(field) {
	case "Package":
		control.Package = value
	case "Version":
		control.Version = value
	case "Architecture":
		control.Architecture = value
	}
}

// inspectDebNative validates one Debian package without dpkg-deb.
func (validator *Validator) inspectDebNative(ctx context.Context, path string) (debIntegrity, error) {
	data, err := readFileBounded(path)
	if err != nil {
		return debIntegrity{}, err
	}
	archive, err := newArReader(data)
	if err != nil {
		return debIntegrity{}, err
	}
	integrity := debIntegrity{}
	seenBinary, seenControl, seenData := false, false, false
	for {
		member, payload, memberErr := archive.next()
		if errors.Is(memberErr, io.EOF) {
			break
		}
		if memberErr != nil {
			return debIntegrity{}, memberErr
		}
		switch {
		case member == "debian-binary":
			seenBinary = true
			if strings.TrimSpace(string(payload)) != "2.0" {
				return debIntegrity{}, fmt.Errorf("unsupported debian-binary version %q", strings.TrimSpace(string(payload)))
			}
		case strings.HasPrefix(member, "control.tar"):
			seenControl = true
			control, postinst, controlErr := validator.readControlTar(ctx, member, payload)
			if controlErr != nil {
				return debIntegrity{}, controlErr
			}
			integrity.Control = control
			integrity.DepmodPostinst = postinst
		case strings.HasPrefix(member, "data.tar"):
			seenData = true
			count, dataErr := validator.inspectDataTar(ctx, member, payload, &integrity)
			if dataErr != nil {
				return debIntegrity{}, dataErr
			}
			integrity.DataEntryCount = count
		}
	}
	if !seenBinary || !seenControl || !seenData {
		return debIntegrity{}, errors.New("missing debian-binary, control.tar, or data.tar member")
	}
	if integrity.Control.Package == "" || integrity.Control.Version == "" || integrity.Control.Architecture == "" {
		return debIntegrity{}, errors.New("control stanza lacks Package, Version, or Architecture")
	}
	return integrity, nil
}

// readControlTar extracts the control stanza and depmod maintainer-script
// evidence from one control.tar member.
func (validator *Validator) readControlTar(ctx context.Context, member string, payload []byte) (debControl, bool, error) {
	data, err := validator.decompressMember(ctx, member, payload)
	if err != nil {
		return debControl{}, false, err
	}
	reader := tar.NewReader(bytes.NewReader(data))
	control := debControl{}
	haveControl := false
	depmodPostinst := false
	for {
		header, headerErr := reader.Next()
		if errors.Is(headerErr, io.EOF) {
			break
		}
		if headerErr != nil {
			return debControl{}, false, fmt.Errorf("read control tar header: %w", headerErr)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(header.Name)
		isControl := base == "control"
		isScript := base == "postinst" || base == "postrm"
		if !isControl && !isScript {
			continue
		}
		if header.Size > maximumControlMemberBytes {
			return debControl{}, false, fmt.Errorf("control member %q exceeds %d bytes", header.Name, maximumControlMemberBytes)
		}
		document, readErr := io.ReadAll(io.LimitReader(reader, maximumControlMemberBytes+1))
		if readErr != nil {
			return debControl{}, false, fmt.Errorf("read control member %q: %w", header.Name, readErr)
		}
		switch base {
		case "control":
			parsed, parseErr := parseControlStanza(document)
			if parseErr != nil {
				return debControl{}, false, parseErr
			}
			control = parsed
			haveControl = true
		case "postinst":
			depmodPostinst = strings.Contains(string(document), "depmod")
		}
	}
	if !haveControl {
		return debControl{}, false, errors.New("control archive has no control member")
	}
	return control, depmodPostinst, nil
}

// inspectDataTar walks one data.tar member and records bundle-relevant paths.
func (validator *Validator) inspectDataTar(ctx context.Context, member string, payload []byte, integrity *debIntegrity) (int, error) {
	data, err := validator.decompressMember(ctx, member, payload)
	if err != nil {
		return 0, err
	}
	reader := tar.NewReader(bytes.NewReader(data))
	count := 0
	for {
		header, headerErr := reader.Next()
		if errors.Is(headerErr, io.EOF) {
			break
		}
		if headerErr != nil {
			return 0, fmt.Errorf("read data tar header: %w", headerErr)
		}
		count++
		if header.Typeflag != tar.TypeReg {
			continue
		}
		integrity.record(strings.TrimPrefix(header.Name, "./"))
	}
	return count, nil
}

// record updates integrity from one payload entry path.
func (integrity *debIntegrity) record(entry string) {
	trimmed := strings.TrimPrefix(entry, "./")
	if trimmed == "" {
		return
	}
	if rest, ok := strings.CutPrefix(trimmed, "usr/lib/modules/"); ok {
		directory, file, found := strings.Cut(rest, "/")
		if found && directory != "" {
			integrity.ModulesDirectory = directory
			if file == "modules.dep" {
				integrity.ModulesDependPresent = true
			}
		}
	}
	if rest, ok := strings.CutPrefix(trimmed, "boot/vmlinuz-"); ok && !strings.Contains(rest, "/") {
		integrity.BootImage = rest
	}
	if rest, ok := strings.CutPrefix(trimmed, "usr/src/linux-headers-"); ok {
		directory, _, found := strings.Cut(rest, "/")
		if found && directory != "" {
			integrity.HeaderDirectory = directory
		}
	}
}

// dpkgDebAvailable reports whether dpkg-deb exists on the host PATH.
func (validator *Validator) dpkgDebAvailable() bool {
	if validator.DisableExternalTools {
		return false
	}
	if validator.dpkgDebPresent == nil {
		available := toolAvailable("dpkg-deb")
		validator.dpkgDebPresent = &available
	}
	return *validator.dpkgDebPresent
}

// toolAvailable reports whether one executable exists on the host PATH.
func toolAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
