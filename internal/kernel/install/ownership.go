package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/bootidentity"
)

// GRUBOwnership describes whether boot evidence belongs to the selected root.
type GRUBOwnership string

const (
	// GRUBOwned has matching mounted boot and Linux-root identities.
	GRUBOwned GRUBOwnership = "proven-owned"
	// GRUBForeign names another Linux installation, not a verified local ABI.
	GRUBForeign GRUBOwnership = "proven-foreign"
	// GRUBUnresolved cannot establish ownership from the supported static subset.
	GRUBUnresolved GRUBOwnership = "unresolved"
)

// grubIdentityValue bounds literal identifiers without retaining other arguments.
var grubIdentityValue = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// grubVariableName admits only ordinary GRUB variable names.
var grubVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// grubTestConditional recognises a pure test, not executable condition code.
var grubTestConditional = regexp.MustCompile(`^if \[ [^;]* \] ?; then$`)

// grubConditional tracks both outcomes without choosing a runtime branch.
type grubConditional struct {
	before   map[string]string
	first    map[string]string
	elseSeen bool
}

// grubIdentityContext retains only selectors, never unrelated kernel arguments.
type grubIdentityContext struct {
	variables      map[string]string
	branches       []grubConditional
	root           string
	subvol         string
	subvolID       string
	linuxCount     int
	rootCount      int
	rootFlagsCount int
	opaque         bool
	sources        []string
}

// newGRUBIdentityContext starts with no assumption about an inherited GRUB root.
func newGRUBIdentityContext() *grubIdentityContext {
	return &grubIdentityContext{variables: make(map[string]string)}
}

// copyGRUBVariables isolates conditional branches from one another.
func copyGRUBVariables(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

// mergeGRUBVariables retains only identities common to every possible branch.
func mergeGRUBVariables(left, right map[string]string) map[string]string {
	result := make(map[string]string)
	for key, value := range left {
		if value != "" && right[key] == value {
			result[key] = value
		}
	}
	return result
}

// observe records bounded identity effects and rejects unsupported control flow.
func (state *grubIdentityContext) observe(line string) {
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	normalised := strings.Join(strings.Fields(line), " ")
	// Ubuntu's generated one-line Xen module probe cannot change root selection.
	if normalised == "if [ x$grub_platform = xxen ]; then insmod xzio; insmod lzopio; fi" {
		return
	}
	if strings.Contains(line, "$(") || strings.ContainsAny(line, "`\\") || (strings.Contains(line, "'") && strings.Contains(line, "$")) {
		state.opaque = true
		return
	}
	if strings.HasPrefix(line, "if ") {
		if !grubTestConditional.MatchString(normalised) || strings.Contains(normalised, "$(") || len(state.branches) >= maximumGRUBMenuDepth {
			state.opaque = true
			return
		}
		state.branches = append(state.branches, grubConditional{before: copyGRUBVariables(state.variables)})
		return
	}
	if line == "else" {
		if len(state.branches) == 0 || state.branches[len(state.branches)-1].elseSeen {
			state.opaque = true
			return
		}
		branch := &state.branches[len(state.branches)-1]
		branch.first = copyGRUBVariables(state.variables)
		branch.elseSeen = true
		state.variables = copyGRUBVariables(branch.before)
		return
	}
	if line == "fi" {
		if len(state.branches) == 0 {
			state.opaque = true
			return
		}
		branch := state.branches[len(state.branches)-1]
		other := branch.before
		if branch.elseSeen {
			other = branch.first
		}
		state.variables = mergeGRUBVariables(other, state.variables)
		state.branches = state.branches[:len(state.branches)-1]
		return
	}
	// A command separator is control flow, never an ignored kernel argument
	// or harmless presentation-command suffix in the supported static subset.
	if strings.ContainsAny(line, ";|&<>") {
		state.opaque = true
		return
	}
	fields, valid := splitGRUBFields(line)
	if !valid || len(fields) == 0 {
		state.opaque = true
		return
	}
	switch fields[0] {
	case "set":
		if len(fields) != 2 {
			state.opaque = true
			return
		}
		name, value, found := strings.Cut(fields[1], "=")
		if !found || !grubVariableName.MatchString(name) || len(state.variables) >= 64 {
			state.opaque = true
			return
		}
		// Positional disks and arbitrary substitutions are not stable identities.
		resolved := ""
		if strings.HasPrefix(value, "$") {
			variable := strings.Trim(strings.TrimPrefix(value, "$"), "{}")
			if grubVariableName.MatchString(variable) {
				resolved = state.variables[variable]
			}
		}
		state.variables[name] = resolved
	case "search", "search.fs_uuid":
		name, selector, ok := parseGRUBSearch(fields)
		if !ok || len(state.variables) >= 64 {
			state.opaque = true
			return
		}
		state.variables[name] = selector
	case "linux", "linuxefi":
		state.linuxCount++
		if len(fields) < 2 {
			state.opaque = true
			return
		}
		if len(state.branches) != 0 {
			state.opaque = true
		}
		for _, argument := range fields[2:] {
			if strings.HasPrefix(argument, "root=") {
				state.rootCount++
				state.root = canonicalRootSelector(strings.TrimPrefix(argument, "root="))
			}
			if strings.HasPrefix(argument, "rootflags=") {
				state.rootFlagsCount++
				if state.rootFlagsCount > 1 {
					state.opaque = true
				}
				for _, flag := range strings.Split(strings.TrimPrefix(argument, "rootflags="), ",") {
					if strings.HasPrefix(flag, "subvol=") {
						if state.subvol != "" {
							state.opaque = true
						}
						state.subvol = "/" + strings.TrimPrefix(strings.TrimPrefix(flag, "subvol="), "/")
					}
					if strings.HasPrefix(flag, "subvolid=") {
						if state.subvolID != "" {
							state.opaque = true
						}
						state.subvolID = strings.TrimPrefix(flag, "subvolid=")
					}
				}
			}
		}
	case "initrd", "initrdefi", "devicetree":
		if len(state.branches) != 0 {
			state.opaque = true
		}
	case "recordfail", "load_video", "gfxmode", "insmod", "echo", "terminal_output", "savedefault":
		// Standard generated presentation/module helpers do not select a root.
	case "chainloader", "fwsetup", "blscfg", "configfile", "source":
		state.opaque = true
	default:
		state.opaque = true
	}
}

// parseGRUBSearch accepts literal filesystem UUID searches and harmless hints.
func parseGRUBSearch(fields []string) (string, string, bool) {
	if fields[0] == "search.fs_uuid" {
		if len(fields) < 3 || len(fields) > 4 || !grubIdentityValue.MatchString(fields[1]) || !grubVariableName.MatchString(fields[2]) {
			return "", "", false
		}
		return fields[2], "UUID=" + strings.ToLower(fields[1]), true
	}
	variable, value := "root", ""
	fsUUID := false
	setVariable := false
	for index := 1; index < len(fields); index++ {
		field := fields[index]
		switch {
		case field == "--fs-uuid" || field == "-u":
			fsUUID = true
		case field == "--no-floppy":
		case field == "--set":
			setVariable = true
		case strings.HasPrefix(field, "--set="):
			if setVariable {
				return "", "", false
			}
			setVariable = true
			variable = strings.TrimPrefix(field, "--set=")
		case strings.HasPrefix(field, "--hint") && strings.Contains(field, "="):
		case field == "--hint" || strings.HasPrefix(field, "--hint-"):
			index++
			if index >= len(fields) {
				return "", "", false
			}
		case strings.HasPrefix(field, "-") || value != "":
			return "", "", false
		default:
			value = field
		}
	}
	if !fsUUID || !setVariable || !grubVariableName.MatchString(variable) || !grubIdentityValue.MatchString(value) {
		return "", "", false
	}
	return variable, "UUID=" + strings.ToLower(value), true
}

// canonicalRootSelector retains only supported literal root identity forms.
func canonicalRootSelector(value string) string {
	for _, prefix := range []string{"UUID=", "PARTUUID="} {
		if strings.HasPrefix(value, prefix) && grubIdentityValue.MatchString(strings.TrimPrefix(value, prefix)) {
			return prefix + strings.ToLower(strings.TrimPrefix(value, prefix))
		}
	}
	for _, pair := range [][2]string{{"/dev/disk/by-uuid/", "UUID="}, {"/dev/disk/by-partuuid/", "PARTUUID="}} {
		if strings.HasPrefix(value, pair[0]) {
			return canonicalRootSelector(pair[1] + strings.TrimPrefix(value, pair[0]))
		}
	}
	if strings.HasPrefix(value, "/dev/") && path.Clean(value) == value && !strings.ContainsAny(value, "$(){}; \\`\t\r\n") {
		return value
	}
	return ""
}

// artifact records the exact filesystem selector at each artefact command.
func (state *grubIdentityContext) artifact(command, value string) (GRUBPathToken, bool) {
	variable := "root"
	if strings.HasPrefix(value, "($") {
		end := strings.IndexByte(value, ')')
		if end > 2 {
			variable = strings.Trim(value[2:end], "{}")
			value = value[end+1:]
		}
	}
	selector := state.variables[variable]
	state.sources = append(state.sources, selector)
	if strings.HasPrefix(value, "$") {
		state.opaque = true
	}
	if !grubVariableName.MatchString(variable) || strings.HasPrefix(value, "(") || strings.HasPrefix(value, "$") {
		return GRUBPathToken{}, false
	}
	value, valid := normaliseGRUBToken(value)
	return GRUBPathToken{Command: command, Path: value, filesystem: selector}, valid
}

// selectorMatches compares only authoritative, mutually comparable identifiers.
func selectorMatches(selector string, fs bootidentity.Filesystem) (matches, known bool) {
	if strings.HasPrefix(selector, "UUID=") {
		return strings.EqualFold(strings.TrimPrefix(selector, "UUID="), fs.UUID), fs.UUID != ""
	}
	if strings.HasPrefix(selector, "PARTUUID=") {
		return strings.EqualFold(strings.TrimPrefix(selector, "PARTUUID="), fs.PartUUID), fs.PartUUID != ""
	}
	if selector != "" && selector == fs.Device {
		return true, true
	}
	// A different /dev spelling could be an alias; never call it foreign by name.
	return false, false
}

// rootMatches includes explicit btrfs subvolume selection in root ownership.
func (state *grubIdentityContext) rootMatches(fs bootidentity.Filesystem) (bool, bool) {
	match, known := selectorMatches(state.root, fs)
	if !known || !match {
		return match, known
	}
	if fs.FSType != "btrfs" {
		return true, state.subvol == "" && state.subvolID == "" && fs.FSRoot == "/"
	}
	if state.subvolID != "" && (state.subvolID != "5" || state.subvol != "" || fs.FSRoot != "/") {
		return false, false
	}
	if state.subvol == "" {
		return fs.FSRoot == "/", fs.FSRoot == "/" && state.subvolID == "5"
	}
	if path.Clean(state.subvol) != state.subvol || strings.ContainsAny(state.subvol, "$(){}\\;") {
		return false, false
	}
	return state.subvol == fs.FSRoot, true
}

// mapBootToken resolves GRUB's filesystem-relative path without root/boot guessing.
func mapBootToken(root string, fs bootidentity.Filesystem, token GRUBPathToken) (string, bool) {
	match, known := selectorMatches(token.filesystem, fs)
	if !known || !match {
		return "", false
	}
	value, valid := normaliseGRUBToken(token.Path)
	if !valid {
		return "", false
	}
	if fs.FSRoot != "/" {
		if !strings.HasPrefix(value, fs.FSRoot+"/") {
			return "", false
		}
		value = strings.TrimPrefix(value, fs.FSRoot)
	}
	absolute := filepath.Join(fs.Mountpoint, filepath.FromSlash(strings.TrimPrefix(value, "/")))
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}

// classifyGRUBOwnership seals local path evidence to fresh mounted identities.
func classifyGRUBOwnership(root string, entries []GRUBEntry, identity bootidentity.Identity, identityErr error) {
	for index := range entries {
		entry := &entries[index]
		entry.Ownership = GRUBUnresolved
		entry.OwnershipReason = "mounted root and boot identity could not be established"
		if identityErr != nil {
			continue
		}
		state := entry.identityContext
		if state == nil || state.opaque || len(state.branches) != 0 || state.linuxCount != 1 || state.rootCount != 1 || state.root == "" {
			entry.OwnershipReason = "entry has missing, ambiguous or unsupported root selection"
			continue
		}
		match, known := state.rootMatches(identity.Root)
		if !known {
			entry.OwnershipReason = "Linux root selection could not be attributed"
			continue
		}
		bootKnown, bootMatches := len(state.sources) > 0, true
		for _, selector := range state.sources {
			matches, known := selectorMatches(selector, identity.Boot)
			bootKnown = bootKnown && known
			bootMatches = bootMatches && matches && known
		}
		if !bootKnown {
			entry.OwnershipReason = "boot filesystem selection could not be attributed"
			continue
		}
		if !match {
			entry.Ownership = GRUBForeign
			entry.OwnershipReason = "entry selects another Linux installation; its bootability is not checked"
		} else if !bootMatches {
			entry.OwnershipReason = "entry selects the local Linux root but a different boot filesystem"
			continue
		} else {
			entry.Ownership = GRUBOwned
			entry.OwnershipReason = "boot filesystem and Linux root match the selected installation"
		}
		entry.boundRoot = root
		entry.boundIdentity = identity
		for _, tokens := range [][]GRUBPathToken{entry.Linux, entry.Initrd, entry.DeviceTrees} {
			for tokenIndex := range tokens {
				relative, mapped := mapBootToken(root, identity.Boot, tokens[tokenIndex])
				if mapped {
					tokens[tokenIndex].relative = relative
				} else if entry.Ownership == GRUBOwned {
					entry.Ownership = GRUBUnresolved
					entry.OwnershipReason = "artifact is outside the selected mounted boot view"
				}
			}
		}
	}
}

// ownedABIEntries keeps foreign installations outside local validation, while
// rejecting unresolved entries that might be the required local boot path.
func ownedABIEntries(root string, entries []GRUBEntry, abi string) ([]GRUBEntry, error) {
	owned := make([]GRUBEntry, 0)
	for _, entry := range entries {
		if entry.Ownership == GRUBForeign {
			continue
		}
		if GRUBEntryHasUnsafeBootArtifacts(entry) {
			return nil, errors.New("GRUB contains an unsafe kernel or initramfs path whose foreign ownership is not proven")
		}
		if !entryHasArtefact(entry.Linux, "vmlinuz-"+abi) {
			continue
		}
		if entry.Ownership != GRUBOwned || entry.boundRoot != root {
			return nil, fmt.Errorf("GRUB entry for ABI %s has unresolved installation ownership: %s", abi, entry.OwnershipReason)
		}
		owned = append(owned, entry)
	}
	return owned, nil
}

// InspectGRUBArtifact checks one token only in a proven-owned entry. It never
// opens foreign filesystems or substitutes an identically named host file.
func InspectGRUBArtifact(ctx context.Context, root string, entry GRUBEntry, token GRUBPathToken) (FileEvidence, GRUBPathAvailability, error) {
	if entry.Ownership != GRUBOwned || entry.boundRoot != filepath.Clean(root) || token.relative == "" {
		return FileEvidence{}, "", errors.New("GRUB artifact has no proven local installation ownership")
	}
	ctx = bootidentity.WithExpected(ctx, entry.boundIdentity)
	if _, err := bootidentity.Resolve(ctx, root); err != nil {
		return FileEvidence{}, "", err
	}
	evidence, err := HashRootFile(ctx, root, token.relative, "GRUB boot artifact")
	if _, identityErr := bootidentity.Resolve(ctx, root); identityErr != nil {
		return FileEvidence{}, "", identityErr
	}
	switch {
	case err == nil:
		return evidence, GRUBPathPresent, nil
	case errors.Is(err, context.Canceled):
		return FileEvidence{}, "", err
	default:
		return evidence, rootArtifactAvailability(err), err
	}
}

// rootArtifactAvailability preserves missing-versus-inaccessible diagnostics.
func rootArtifactAvailability(err error) GRUBPathAvailability {
	switch {
	case err == nil:
		return GRUBPathPresent
	case errors.Is(err, os.ErrNotExist):
		return GRUBPathMissing
	case errors.Is(err, os.ErrPermission):
		return GRUBPathInaccessible
	default:
		return ""
	}
}
