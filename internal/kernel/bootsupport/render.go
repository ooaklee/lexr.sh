// Package bootsupport renders the deterministic filesystem payload for the
// generic Debian boot-support package used by raw kernel images.
package bootsupport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// PackageName is the stable Debian binary package identity.
	PackageName = "lexr-kernel-boot-support"
	// Architecture reflects that the payload contains portable data and POSIX shell.
	Architecture = "all"

	// controlPath is the Debian binary-package control file location.
	controlPath = "DEBIAN/control"
	// buildRoot contains immutable metadata for the ABI that emitted the package.
	buildRoot = "usr/lib/lexr/kernel-build"
	// platformRoot contains declarative records consumed by the generic helper.
	platformRoot = "usr/lib/lexr/kernel-platforms"
	// helperPath is the installed exact-ABI boot materialisation helper.
	helperPath = "usr/libexec/lexr/kernel-boot-refresh"
	// postInstallPath is the kernel post-install hook location.
	postInstallPath = "etc/kernel/postinst.d/05-lexr-kernel-boot"
	// postRemovePath is the kernel post-removal hook location.
	postRemovePath = "etc/kernel/postrm.d/05-lexr-kernel-boot"
)

var (
	// abiExpression accepts the closed safe character set used by kernel ABIs.
	abiExpression = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,126}$`)
	// versionExpression accepts bounded Debian version identifiers.
	versionExpression = regexp.MustCompile(`^[0-9][A-Za-z0-9.+:~-]{0,126}$`)
	// platformIDExpression accepts safe platform directory names.
	platformIDExpression = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	// pathPartExpression accepts safe relative device-tree path components.
	pathPartExpression = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~-]*$`)
	// digestExpression recognises lower-case SHA-256 digests.
	digestExpression = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Platform declares one safely detectable platform and the one DTB that a raw
// image may bind for it. Selection values are exact, not substring patterns.
type Platform struct {
	ID                string
	Title             string
	DeviceTreePath    string
	DeviceTreeSHA256  string
	MachineIdentities []string
	Compatibles       []string
}

// Request supplies the immutable build identity and declarative platform set.
type Request struct {
	ABI        string
	Version    string
	Maintainer string
	Platforms  []Platform
}

// File is one relative staging-tree file with normalised content and mode.
type File struct {
	Path string
	Mode fs.FileMode
	Data []byte
}

// Payload is a complete, deterministically ordered Debian package staging tree.
type Payload struct {
	Name         string
	Version      string
	Architecture string
	Files        []File
}

// DebianPackageBuildScript returns the fixed Linux-container script which
// converts a materialised Payload into a reproducible .deb. Keeping archive
// creation in the pinned build container avoids a host dpkg-deb dependency.
func DebianPackageBuildScript() string {
	return containerPackageBuildScript
}

// File returns a defensive copy of the named staging file.
func (payload Payload) File(name string) (File, bool) {
	index := sort.Search(len(payload.Files), func(index int) bool {
		return payload.Files[index].Path >= name
	})
	if index == len(payload.Files) || payload.Files[index].Path != name {
		return File{}, false
	}
	item := payload.Files[index]
	item.Data = append([]byte(nil), item.Data...)
	return item, true
}

// Render validates request and returns the complete package staging content.
// It performs no filesystem writes and preserves no caller-owned slices.
func Render(request Request) (Payload, error) {
	platforms, err := validateRequest(request)
	if err != nil {
		return Payload{}, err
	}

	files := []File{
		textFile(controlPath, 0o644, renderControl(request)),
		textFile("DEBIAN/postinst", 0o755, renderPackagePostInstall(request.ABI)),
		textFile("DEBIAN/prerm", 0o755, packagePreRemove),
		textFile("DEBIAN/postrm", 0o755, packagePostRemove),
		textFile("DEBIAN/triggers", 0o644, "interest-noawait linux-update-"+request.ABI+"\n"),
		textFile(helperPath, 0o755, bootRefreshHelper),
		textFile(postInstallPath, 0o755, renderKernelPostInstall(request.ABI)),
		textFile(postRemovePath, 0o755, renderKernelPostRemove(request.ABI)),
		textFile(path.Join(buildRoot, "abi"), 0o644, request.ABI+"\n"),
		textFile(path.Join(buildRoot, "version"), 0o644, request.Version+"\n"),
	}

	platformIDs := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		platformIDs = append(platformIDs, platform.ID)
		root := path.Join(platformRoot, platform.ID)
		files = append(files,
			textFile(path.Join(root, "title"), 0o644, platform.Title+"\n"),
			textFile(path.Join(root, "dtb-path"), 0o644, platform.DeviceTreePath+"\n"),
			textFile(path.Join(root, "dtb-sha256"), 0o644, platform.DeviceTreeSHA256+"\n"),
			textFile(path.Join(root, "machine-identities"), 0o644, lines(platform.MachineIdentities)),
			textFile(path.Join(root, "compatibles"), 0o644, lines(platform.Compatibles)),
		)
	}
	files = append(files, textFile(path.Join(buildRoot, "platforms"), 0o644, lines(platformIDs)))
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })

	return Payload{
		Name:         PackageName,
		Version:      request.Version,
		Architecture: Architecture,
		Files:        files,
	}, nil
}

// validateRequest checks and defensively orders every untrusted request field.
func validateRequest(request Request) ([]Platform, error) {
	var problems []error
	if !abiExpression.MatchString(request.ABI) {
		problems = append(problems, fmt.Errorf("unsafe kernel ABI %q", request.ABI))
	}
	if !versionExpression.MatchString(request.Version) {
		problems = append(problems, fmt.Errorf("unsafe Debian version %q", request.Version))
	}
	if err := validateLine("maintainer", request.Maintainer, 256, false); err != nil {
		problems = append(problems, err)
	}
	if len(request.Platforms) == 0 {
		problems = append(problems, errors.New("at least one platform record is required"))
	}
	if len(request.Platforms) > 128 {
		problems = append(problems, errors.New("platform record count exceeds 128"))
	}

	platforms := append([]Platform(nil), request.Platforms...)
	seenIDs := make(map[string]bool, len(platforms))
	seenSelection := make(map[string]string)
	for index := range platforms {
		platform := &platforms[index]
		platform.MachineIdentities = sortedUnique(platform.MachineIdentities)
		platform.Compatibles = sortedUnique(platform.Compatibles)
		if !platformIDExpression.MatchString(platform.ID) {
			problems = append(problems, fmt.Errorf("unsafe platform ID %q", platform.ID))
		} else if seenIDs[platform.ID] {
			problems = append(problems, fmt.Errorf("duplicate platform ID %q", platform.ID))
		}
		seenIDs[platform.ID] = true
		if err := validateLine("platform title", platform.Title, 160, false); err != nil {
			problems = append(problems, fmt.Errorf("platform %q: %w", platform.ID, err))
		}
		if err := validateDTBPath(platform.DeviceTreePath); err != nil {
			problems = append(problems, fmt.Errorf("platform %q: %w", platform.ID, err))
		}
		if !digestExpression.MatchString(platform.DeviceTreeSHA256) {
			problems = append(problems, fmt.Errorf("platform %q has malformed DTB SHA-256", platform.ID))
		}
		if len(platform.MachineIdentities) == 0 && len(platform.Compatibles) == 0 {
			problems = append(problems, fmt.Errorf("platform %q has no exact selection identity", platform.ID))
		}
		for _, identity := range platform.MachineIdentities {
			if err := validateLine("machine identity", identity, 256, false); err != nil {
				problems = append(problems, fmt.Errorf("platform %q: %w", platform.ID, err))
			}
			key := "machine\x00" + identity
			if owner, exists := seenSelection[key]; exists && owner != platform.ID {
				problems = append(problems, fmt.Errorf("machine identity %q is shared by platforms %q and %q", identity, owner, platform.ID))
			}
			seenSelection[key] = platform.ID
		}
		for _, compatible := range platform.Compatibles {
			if err := validateLine("compatible", compatible, 256, false); err != nil {
				problems = append(problems, fmt.Errorf("platform %q: %w", platform.ID, err))
			}
			key := "compatible\x00" + compatible
			if owner, exists := seenSelection[key]; exists && owner != platform.ID {
				problems = append(problems, fmt.Errorf("compatible %q is shared by platforms %q and %q", compatible, owner, platform.ID))
			}
			seenSelection[key] = platform.ID
		}
	}
	sort.Slice(platforms, func(left, right int) bool { return platforms[left].ID < platforms[right].ID })
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return platforms, nil
}

// validateLine rejects unsafe control characters and ambiguous surrounding space.
func validateLine(name, value string, maximum int, allowEmpty bool) error {
	if (!allowEmpty && value == "") || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("unsafe %s %q", name, value)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("unsafe %s %q", name, value)
		}
	}
	return nil
}

// validateDTBPath accepts only bounded vendor-relative DTB paths.
func validateDTBPath(value string) error {
	if value == "" || len(value) > 512 || path.IsAbs(value) || path.Clean(value) != value || strings.Contains(value, "..") || !strings.HasSuffix(value, ".dtb") {
		return fmt.Errorf("unsafe relative DTB path %q", value)
	}
	parts := strings.Split(value, "/")
	if len(parts) < 2 {
		return fmt.Errorf("DTB path %q must contain vendor and file components", value)
	}
	for _, part := range parts {
		if !pathPartExpression.MatchString(part) || part == "." || part == ".." {
			return fmt.Errorf("unsafe relative DTB path %q", value)
		}
	}
	return nil
}

// sortedUnique returns a sorted defensive copy with duplicates removed.
func sortedUnique(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	if len(result) < 2 {
		return result
	}
	write := 1
	for read := 1; read < len(result); read++ {
		if result[read] == result[write-1] {
			continue
		}
		result[write] = result[read]
		write++
	}
	return result[:write]
}

// lines renders zero or more values as a newline-terminated record file.
func lines(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.Join(values, "\n") + "\n"
}

// textFile constructs one text entry for the deterministic staging payload.
func textFile(name string, mode fs.FileMode, content string) File {
	return File{Path: name, Mode: mode, Data: []byte(content)}
}

// renderControl emits deterministic Debian binary-package metadata.
func renderControl(request Request) string {
	var control bytes.Buffer
	fmt.Fprintf(&control, "Package: %s\n", PackageName)
	fmt.Fprintf(&control, "Version: %s\n", request.Version)
	control.WriteString("Section: admin\nPriority: optional\nArchitecture: all\n")
	fmt.Fprintf(&control, "Maintainer: %s\n", request.Maintainer)
	control.WriteString("Depends: coreutils, grub-common, grub2-common, util-linux\n")
	fmt.Fprintf(&control, "Recommends: linux-image-%s (= %s), linux-modules-%s (= %s)\n", request.ABI, request.Version, request.ABI, request.Version)
	fmt.Fprintf(&control, "Enhances: linux-image-%s, linux-modules-%s\n", request.ABI, request.ABI)
	control.WriteString("Description: exact-ABI external device-tree boot support for Lexr kernels\n")
	control.WriteString(" Materialises one verified ABI-scoped device tree for the stock GRUB generator.\n")
	return control.String()
}

// Digest returns a stable SHA-256 over path, mode, size, and content for every
// staged file. It is useful for testing and provenance before archive creation.
func (payload Payload) Digest() string {
	hasher := sha256.New()
	for _, item := range payload.Files {
		fmt.Fprintf(hasher, "%s\x00%04o\x00%d\x00", item.Path, item.Mode.Perm(), len(item.Data))
		_, _ = hasher.Write(item.Data)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}
