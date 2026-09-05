package install

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// abiStampedDTBExpression admits the bounded path-safe identity syntax used by
// generated /dtb-<abi> files. It intentionally does not require the stricter
// package ABI convention: deployed names such as 7.2.2-sp11beta2 use a looser
// generation marker, while digest equality remains the correctness proof.
var abiStampedDTBExpression = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)(?:[.-][A-Za-z0-9+~_]+)*$`)

const (
	// maximumModuleEntries bounds recursive inspection of an installed module tree.
	maximumModuleEntries = 500000
	// maximumGRUBBytes bounds GRUB configuration parsing.
	maximumGRUBBytes int64 = 16 << 20
	// maximumGRUBMenuDepth bounds nested submenu state retained while parsing.
	maximumGRUBMenuDepth = 32
	// maximumBootImageInspectionBytes bounds the in-memory PE section review
	// used only when GRUB supplies no external device tree.
	maximumBootImageInspectionBytes int64 = 512 << 20
	// abiStampedDeviceTreeMarker defers hardware attribution to installed bytes.
	abiStampedDeviceTreeMarker = "abi-stamped"
)

// verifyFallback proves that the running fallback has complete boot artefacts.
func verifyFallback(ctx context.Context, root, abi string) (BootEvidence, error) {
	evidence, err := verifyBootFiles(ctx, root, abi)
	if err != nil {
		return BootEvidence{}, fmt.Errorf("fallback ABI %s: %w", abi, err)
	}
	grub, err := rootPath(root, "boot/grub/grub.cfg")
	if err != nil {
		return BootEvidence{}, err
	}
	if err := validateTargetRoute(root, grub, false); err != nil {
		return BootEvidence{}, err
	}
	parsedEntries, err := parseGRUBEntries(ctx, grub)
	if err != nil {
		return BootEvidence{}, err
	}
	if err := validateMatchingGRUBEntryArtifacts(ctx, root, parsedEntries, abi); err != nil {
		return BootEvidence{}, fmt.Errorf("fallback ABI %s: %w", abi, err)
	}
	entries := countMatchingGRUBEntries(parsedEntries, abi, true, false)
	if entries != 1 {
		return BootEvidence{}, fmt.Errorf("fallback ABI %s requires exactly one ABI-labelled non-recovery GRUB entry; found %d", abi, entries)
	}
	deviceTreeBoot, err := verifyGRUBDeviceTreeBindings(ctx, root, abi, parsedEntries, nil)
	if err != nil {
		return BootEvidence{}, fmt.Errorf("fallback ABI %s: %w", abi, err)
	}
	evidence.GRUBEntryCount = entries
	evidence.DeviceTreeBoot = deviceTreeBoot
	return evidence, nil
}

// verifyInstalled proves that the target ABI has complete boot and DTB artefacts.
func verifyInstalled(ctx context.Context, root, abi string, trees []DeviceTree) (BootEvidence, []FileEvidence, error) {
	evidence, err := verifyBootFiles(ctx, root, abi)
	if err != nil {
		return BootEvidence{}, nil, fmt.Errorf("installed ABI %s: %w", abi, err)
	}
	grub, err := rootPath(root, "boot/grub/grub.cfg")
	if err != nil {
		return BootEvidence{}, nil, err
	}
	if err := validateTargetRoute(root, grub, false); err != nil {
		return BootEvidence{}, nil, err
	}
	parsedEntries, err := parseGRUBEntries(ctx, grub)
	if err != nil {
		return BootEvidence{}, nil, err
	}
	if err := validateMatchingGRUBEntryArtifacts(ctx, root, parsedEntries, abi); err != nil {
		return BootEvidence{}, nil, fmt.Errorf("installed ABI %s: %w", abi, err)
	}
	entries := countMatchingGRUBEntries(parsedEntries, abi, true, false)
	if entries != 1 {
		return BootEvidence{}, nil, fmt.Errorf("installed ABI %s requires exactly one ABI-labelled non-recovery GRUB entry; found %d", abi, entries)
	}
	evidence.GRUBEntryCount = entries
	deviceTrees := make([]FileEvidence, 0, len(trees))
	for _, tree := range trees {
		if err := validateTargetRoute(root, tree.TargetPath, false); err != nil {
			return BootEvidence{}, nil, err
		}
		verified, err := requireRegularEvidence(ctx, "device-tree", tree.TargetPath)
		if err != nil {
			return BootEvidence{}, nil, err
		}
		if tree.ExpectedSHA256 != "" && verified.SHA256 != tree.ExpectedSHA256 {
			return BootEvidence{}, nil, fmt.Errorf("installed device tree %s digest differs from the build inventory", tree.Device)
		}
		deviceTrees = append(deviceTrees, verified)
	}
	deviceTreeBoot, err := verifyGRUBDeviceTreeBindings(ctx, root, abi, parsedEntries, trees)
	if err != nil {
		return BootEvidence{}, nil, fmt.Errorf("installed ABI %s: %w", abi, err)
	}
	evidence.DeviceTreeBoot = deviceTreeBoot
	return evidence, deviceTrees, nil
}

// verifyBootFiles checks the kernel image, initramfs, module index, and one module.
func verifyBootFiles(ctx context.Context, root, abi string) (BootEvidence, error) {
	kernelImage, err := rootPath(root, "boot/vmlinuz-"+abi)
	if err != nil {
		return BootEvidence{}, err
	}
	initramfs, err := rootPath(root, "boot/initrd.img-"+abi)
	if err != nil {
		return BootEvidence{}, err
	}
	systemMap, err := rootPath(root, "boot/System.map-"+abi)
	if err != nil {
		return BootEvidence{}, err
	}
	kernelConfig, err := rootPath(root, "boot/config-"+abi)
	if err != nil {
		return BootEvidence{}, err
	}
	if err := validateTargetRoute(root, kernelImage, false); err != nil {
		return BootEvidence{}, err
	}
	if err := validateTargetRoute(root, initramfs, false); err != nil {
		return BootEvidence{}, err
	}
	if err := validateTargetRoute(root, systemMap, false); err != nil {
		return BootEvidence{}, err
	}
	if err := validateTargetRoute(root, kernelConfig, false); err != nil {
		return BootEvidence{}, err
	}
	imageEvidence, err := requireRegularEvidence(ctx, "kernel-image", kernelImage)
	if err != nil {
		return BootEvidence{}, err
	}
	initramfsEvidence, err := requireRegularEvidence(ctx, "initramfs", initramfs)
	if err != nil {
		return BootEvidence{}, err
	}
	systemMapEvidence, err := requireRegularEvidence(ctx, "system-map", systemMap)
	if err != nil {
		return BootEvidence{}, err
	}
	kernelConfigEvidence, err := requireRegularEvidence(ctx, "kernel-config", kernelConfig)
	if err != nil {
		return BootEvidence{}, err
	}
	moduleTree, moduleFile, dependencyIndex, err := inspectModuleTree(ctx, root, abi)
	if err != nil {
		return BootEvidence{}, err
	}
	return BootEvidence{
		ABI:                    abi,
		KernelImage:            imageEvidence,
		Initramfs:              initramfsEvidence,
		SystemMap:              systemMapEvidence,
		KernelConfig:           kernelConfigEvidence,
		ModulesDependencyIndex: dependencyIndex,
		ModuleTree:             moduleTree,
		ModuleFile:             moduleFile,
	}, nil
}

// inspectModuleTree requires a canonical usr-merged module tree with modules.dep.
func inspectModuleTree(ctx context.Context, root, abi string) (string, FileEvidence, FileEvidence, error) {
	candidates, err := moduleTreeCandidates(root, abi)
	if err != nil {
		return "", FileEvidence{}, FileEvidence{}, err
	}
	moduleTree := ""
	for _, candidate := range candidates {
		if _, err := os.Lstat(candidate); err == nil {
			moduleTree = candidate
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", FileEvidence{}, FileEvidence{}, fmt.Errorf("inspect module tree %s: %w", candidate, err)
		}
	}
	if moduleTree == "" {
		return "", FileEvidence{}, FileEvidence{}, fmt.Errorf("module tree is missing for ABI %s", abi)
	}
	if err := validateTargetRoute(root, moduleTree, false); err != nil {
		return "", FileEvidence{}, FileEvidence{}, err
	}
	info, err := os.Lstat(moduleTree)
	if err != nil {
		return "", FileEvidence{}, FileEvidence{}, fmt.Errorf("inspect module tree %s: %w", moduleTree, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", FileEvidence{}, FileEvidence{}, fmt.Errorf("module tree must be a non-symlink directory: %s", moduleTree)
	}
	dependencyPath := filepath.Join(moduleTree, "modules.dep")
	dependencyEvidence, err := requireRegularEvidence(ctx, "modules-dependency-index", dependencyPath)
	if err != nil {
		return "", FileEvidence{}, FileEvidence{}, err
	}
	moduleFile := ""
	entries := 0
	err = filepath.WalkDir(moduleTree, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > maximumModuleEntries {
			return fmt.Errorf("module tree exceeds %d entries", maximumModuleEntries)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("module tree contains a non-regular entry: %s", path)
		}
		if moduleFile == "" && info.Size() > 0 && isKernelModule(path) {
			moduleFile = path
		}
		return nil
	})
	if err != nil {
		return "", FileEvidence{}, FileEvidence{}, fmt.Errorf("inspect module tree %s: %w", moduleTree, err)
	}
	if moduleFile == "" {
		return "", FileEvidence{}, FileEvidence{}, fmt.Errorf("module tree contains no non-empty kernel module: %s", moduleTree)
	}
	moduleEvidence, err := requireRegularEvidence(ctx, "kernel-module", moduleFile)
	if err != nil {
		return "", FileEvidence{}, FileEvidence{}, err
	}
	return moduleTree, moduleEvidence, dependencyEvidence, nil
}

// isKernelModule recognises the supported plain and compressed module suffixes.
func isKernelModule(path string) bool {
	return strings.HasSuffix(path, ".ko") || strings.HasSuffix(path, ".ko.xz") || strings.HasSuffix(path, ".ko.zst")
}

// verifyInstalledHeaders corroborates a successful package-manager command
// with exact filesystem evidence for every selected development-header role.
func verifyInstalledHeaders(ctx context.Context, root, abi string, packages []Package) ([]HeaderEvidence, error) {
	selected := make(map[kernel.PackageRole]Package, 2)
	for _, item := range packages {
		switch item.Role {
		case kernel.RoleHeaders, kernel.RoleCommonHeaders:
			selected[item.Role] = item
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	if len(selected) != 2 {
		return nil, errors.New("installed kernel header verification requires the complete header package pair")
	}

	base := strings.TrimSuffix(abi, "-qcom-x1e")
	trees := []struct {
		role     kernel.PackageRole
		relative string
	}{
		{role: kernel.RoleCommonHeaders, relative: "usr/src/linux-qcom-x1e-headers-" + base},
		{role: kernel.RoleHeaders, relative: "usr/src/linux-headers-" + abi},
	}
	evidence := make([]HeaderEvidence, 0, len(trees))
	for _, tree := range trees {
		packageItem := selected[tree.role]
		treePath, err := rootPath(root, tree.relative)
		if err != nil {
			return nil, err
		}
		if err := validateTargetRoute(root, treePath, false); err != nil {
			return nil, fmt.Errorf("inspect installed %s header tree %s: %w", tree.role, treePath, err)
		}
		info, err := os.Lstat(treePath)
		if err != nil {
			return nil, fmt.Errorf("inspect installed %s header tree %s: %w", tree.role, treePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("installed %s header tree must be a non-symlink directory: %s", tree.role, treePath)
		}
		markerPath := filepath.Join(treePath, "Makefile")
		if err := validateTargetRoute(root, markerPath, false); err != nil {
			return nil, fmt.Errorf("inspect installed %s header marker %s: %w", tree.role, markerPath, err)
		}
		marker, err := requireRegularEvidence(ctx, string(tree.role)+"-makefile", markerPath)
		if err != nil {
			return nil, err
		}
		evidence = append(evidence, HeaderEvidence{
			Role:          tree.role,
			DebianPackage: packageItem.DebianPackage,
			TreePath:      treePath,
			Marker:        marker,
		})
	}
	return evidence, nil
}

// verifyTargetAbsent requires a fresh ABI before package-manager mutation.
func verifyTargetAbsent(ctx context.Context, root, abi string) error {
	paths := []string{
		"boot/vmlinuz-" + abi,
		"boot/initrd.img-" + abi,
		"boot/System.map-" + abi,
		"boot/config-" + abi,
		"usr/lib/firmware/" + abi,
		"usr/src/linux-headers-" + abi,
		"usr/src/linux-qcom-x1e-headers-" + strings.TrimSuffix(abi, "-qcom-x1e"),
	}
	for _, relative := range paths {
		target, err := rootPath(root, relative)
		if err != nil {
			return err
		}
		if err := validateTargetRoute(root, target, true); err != nil {
			return err
		}
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("target ABI already has an installed artefact: %s", target)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect target ABI path %s: %w", target, err)
		}
	}
	moduleTrees, err := moduleTreeCandidates(root, abi)
	if err != nil {
		return err
	}
	for _, target := range moduleTrees {
		if err := validateTargetRoute(root, target, true); err != nil {
			return err
		}
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("target ABI already has an installed artefact: %s", target)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect target ABI path %s: %w", target, err)
		}
	}
	grub, err := rootPath(root, "boot/grub/grub.cfg")
	if err != nil {
		return err
	}
	if err := validateTargetRoute(root, grub, false); err != nil {
		return err
	}
	entries, err := countGRUBEntries(ctx, grub, abi, false, true)
	if err != nil {
		return err
	}
	if entries != 0 {
		return fmt.Errorf("target ABI %s already has %d GRUB entries", abi, entries)
	}
	return nil
}

// moduleTreeCandidates returns the usr-merged path and any real legacy /lib path.
func moduleTreeCandidates(root, abi string) ([]string, error) {
	usrTree, err := rootPath(root, "usr/lib/modules/"+abi)
	if err != nil {
		return nil, err
	}
	candidates := []string{usrTree}
	legacyBase, err := rootPath(root, "lib")
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(legacyBase)
	if errors.Is(err, os.ErrNotExist) {
		return candidates, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect legacy module root %s: %w", legacyBase, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return candidates, nil
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("legacy module root is not a directory: %s", legacyBase)
	}
	legacyTree, err := rootPath(root, "lib/modules/"+abi)
	if err != nil {
		return nil, err
	}
	return append(candidates, legacyTree), nil
}

// countGRUBEntries counts matching non-recovery menu entries without executing GRUB.
func countGRUBEntries(ctx context.Context, path, abi string, requireTitle, includeRecovery bool) (int, error) {
	entries, err := parseGRUBEntries(ctx, path)
	if err != nil {
		return 0, err
	}
	return countMatchingGRUBEntries(entries, abi, requireTitle, includeRecovery), nil
}

// parseGRUBEntries reads one pinned, bounded GRUB configuration and retains
// only menu hierarchy, titles, identifiers, recovery state, and recognised
// path tokens.
func parseGRUBEntries(ctx context.Context, path string) ([]GRUBEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect GRUB configuration %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumGRUBBytes {
		return nil, fmt.Errorf("GRUB configuration must be a non-empty regular file no larger than %d bytes: %s", maximumGRUBBytes, path)
	}
	file, opened, err := openUnchangedRegular(path, info)
	if err != nil {
		return nil, fmt.Errorf("open GRUB configuration: %w", err)
	}
	defer file.Close()
	entries := make([]GRUBEntry, 0)
	var active *GRUBEntry
	menuPaths := [][]int{nil}
	menuTitlePaths := [][]string{nil}
	menuPositions := []int{0}
	menuBlocks := make([]bool, 0)
	scanner := bufio.NewScanner(io.LimitReader(file, maximumGRUBBytes+1))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "}") {
			active = nil
			if len(menuBlocks) > 0 {
				last := len(menuBlocks) - 1
				if menuBlocks[last] && len(menuPaths) > 1 {
					menuPaths = menuPaths[:len(menuPaths)-1]
					menuTitlePaths = menuTitlePaths[:len(menuTitlePaths)-1]
					menuPositions = menuPositions[:len(menuPositions)-1]
				}
				menuBlocks = menuBlocks[:last]
			}
			continue
		}
		if strings.HasPrefix(line, "submenu ") {
			active = nil
			if len(menuPaths) >= maximumGRUBMenuDepth+1 {
				return nil, fmt.Errorf("GRUB submenu nesting exceeds %d levels", maximumGRUBMenuDepth)
			}
			level := len(menuPaths) - 1
			position := menuPositions[level]
			menuPositions[level]++
			submenuPath := append(append([]int(nil), menuPaths[level]...), position)
			submenuTitle, valid := grubSubmenuTitle(line)
			if !valid || len(submenuTitle) > 512 || strings.ContainsAny(submenuTitle, "\x00\r\n") {
				submenuTitle = ""
			}
			submenuTitlePath := append(append([]string(nil), menuTitlePaths[level]...), submenuTitle)
			menuPaths = append(menuPaths, submenuPath)
			menuTitlePaths = append(menuTitlePaths, submenuTitlePath)
			menuPositions = append(menuPositions, 0)
			menuBlocks = append(menuBlocks, true)
			continue
		}
		if strings.HasPrefix(line, "menuentry ") {
			active = nil
			level := len(menuPaths) - 1
			position := menuPositions[level]
			menuPositions[level]++
			menuPath := append(append([]int(nil), menuPaths[level]...), position)
			menuBlocks = append(menuBlocks, false)
			title, valid := grubMenuTitle(line)
			if !valid || len(title) > 512 || strings.ContainsAny(title, "\x00\r\n") {
				continue
			}
			entries = append(entries, GRUBEntry{
				Index:         len(entries),
				Depth:         level,
				MenuPath:      menuPath,
				MenuTitlePath: append(append([]string(nil), menuTitlePaths[level]...), title),
				Title:         title,
				ID:            grubMenuID(line),
				Recovery:      strings.Contains(strings.ToLower(title), "recovery"),
			})
			active = &entries[len(entries)-1]
			continue
		}
		if active == nil {
			continue
		}
		fields, valid := splitGRUBFields(line)
		if !valid || len(fields) < 2 {
			continue
		}
		command := fields[0]
		pathFields := fields[1:2]
		if command == "initrd" || command == "initrdefi" {
			pathFields = fields[1:]
		}
		values := make([]GRUBPathToken, 0, len(pathFields))
		for _, field := range pathFields {
			token, valid := normaliseGRUBToken(field)
			if !valid {
				switch command {
				case "linux", "linuxefi", "initrd", "initrdefi", "devicetree":
					active.UnsafeCommands = append(active.UnsafeCommands, command)
				}
				continue
			}
			values = append(values, GRUBPathToken{Command: command, Path: token})
		}
		switch command {
		case "linux", "linuxefi":
			active.Linux = append(active.Linux, values...)
		case "initrd", "initrdefi":
			active.Initrd = append(active.Initrd, values...)
		case "devicetree":
			active.DeviceTrees = append(active.DeviceTrees, values...)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read GRUB configuration: %w", err)
	}
	entry, statErr := os.Lstat(path)
	if statErr != nil || entry.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, entry) {
		return nil, fmt.Errorf("GRUB configuration changed while it was inspected: %s", path)
	}
	return entries, nil
}

// countMatchingGRUBEntries counts complete ABI-labelled entries in already
// bounded parsed evidence.
func countMatchingGRUBEntries(entries []GRUBEntry, abi string, requireTitle, includeRecovery bool) int {
	count := 0
	for _, entry := range entries {
		if (!includeRecovery && entry.Recovery) || (requireTitle && !strings.Contains(entry.Title, abi)) {
			continue
		}
		if grubEntryNamesExactABIArtifacts(entry, abi) {
			count++
		}
	}
	return count
}

// validateMatchingGRUBEntryArtifacts rejects ambiguous entries before title
// and recovery cardinality checks. Any stanza which contains the requested
// kernel token is security-relevant even when it would not count as the one
// canonical normal entry.
func validateMatchingGRUBEntryArtifacts(ctx context.Context, root string, entries []GRUBEntry, abi string) error {
	for _, entry := range entries {
		if GRUBEntryHasUnsafeBootArtifacts(entry) {
			return fmt.Errorf("GRUB entry %q contains an unsafe kernel or initramfs path", entry.Title)
		}
		if !entryHasArtefact(entry.Linux, "vmlinuz-"+abi) {
			continue
		}
		if err := VerifyGRUBEntryABIArtifacts(ctx, root, entry, abi); err != nil {
			return fmt.Errorf("matching GRUB entry %q: %w", entry.Title, err)
		}
	}
	return nil
}

// grubMenuTitle extracts the first quoted or unquoted GRUB menu-entry title.
func grubMenuTitle(line string) (string, bool) {
	return grubCommandTitle(line, "menuentry")
}

// grubSubmenuTitle extracts the first quoted or unquoted GRUB submenu title.
func grubSubmenuTitle(line string) (string, bool) {
	return grubCommandTitle(line, "submenu")
}

// grubCommandTitle extracts one bounded literal title without shell evaluation.
func grubCommandTitle(line, command string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, command+" ") {
		return "", false
	}
	remainder := strings.TrimSpace(strings.TrimPrefix(trimmed, command))
	if remainder == "" {
		return "", false
	}
	quote := remainder[0]
	if quote != '\'' && quote != '"' {
		fields := strings.Fields(remainder)
		if len(fields) == 0 {
			return "", false
		}
		return fields[0], true
	}
	escaped := false
	for index := 1; index < len(remainder); index++ {
		character := remainder[index]
		if quote == '"' && character == '\\' && !escaped {
			escaped = true
			continue
		}
		if character == quote && !escaped {
			return remainder[1:index], true
		}
		escaped = false
	}
	return "", false
}

// grubMenuID returns one bounded literal identifier following --id or the
// generated menuentry identifier option without interpreting shell syntax.
func grubMenuID(line string) string {
	fields, valid := splitGRUBFields(line)
	if !valid {
		return ""
	}
	for index, field := range fields {
		if (field == "--id" || field == "$menuentry_id_option") && index+1 < len(fields) {
			identifier := fields[index+1]
			if len(identifier) <= 512 && !strings.ContainsAny(identifier, "\x00\r\n") {
				return identifier
			}
		}
	}
	return ""
}

// splitGRUBFields separates a bounded GRUB line while retaining quoted path
// tokens and without evaluating substitutions, escapes, or other shell input.
func splitGRUBFields(line string) ([]string, bool) {
	if len(line) > 1<<20 || strings.ContainsRune(line, '\x00') {
		return nil, false
	}
	fields := make([]string, 0, 8)
	var field strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if field.Len() > 0 {
			fields = append(fields, field.String())
			field.Reset()
		}
	}
	for _, character := range line {
		if escaped {
			field.WriteRune(character)
			escaped = false
			continue
		}
		if character == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
			} else {
				field.WriteRune(character)
			}
			continue
		}
		switch character {
		case '\'', '"':
			quote = character
		case ' ', '\t':
			flush()
		default:
			field.WriteRune(character)
		}
	}
	if quote != 0 || escaped {
		return nil, false
	}
	flush()
	return fields, true
}

// normaliseGRUBToken admits only a single absolute, traversal-free path token
// and strips the literal GRUB root-device prefix used by generated entries.
func normaliseGRUBToken(token string) (string, bool) {
	for _, prefix := range []string{"($root)", "${root}", "$root"} {
		if strings.HasPrefix(token, prefix) {
			token = strings.TrimPrefix(token, prefix)
			if !strings.HasPrefix(token, "/") {
				return "", false
			}
			break
		}
	}
	if len(token) == 0 || len(token) > 4096 || !strings.HasPrefix(token, "/") || strings.ContainsAny(token, "\\$(){}\x00\r\n") {
		return "", false
	}
	if path.Clean(token) != token || token == "." || token == ".." || strings.HasPrefix(token, "../") || strings.Contains(token, "//") {
		return "", false
	}
	return token, true
}

// InspectGRUB returns bounded, redacted menu-entry evidence from the selected
// root without executing GRUB or any package hook.
func InspectGRUB(ctx context.Context, root string) ([]GRUBEntry, error) {
	canonical, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}
	grub, err := rootPath(canonical, "boot/grub/grub.cfg")
	if err != nil {
		return nil, err
	}
	if err := validateTargetRoute(canonical, grub, false); err != nil {
		return nil, err
	}
	entries, err := parseGRUBEntries(ctx, grub)
	if err != nil {
		return nil, err
	}
	if canonical != string(filepath.Separator) {
		for index := range entries {
			entries[index].Linux = rejectAlternateRootTokens(entries[index].Linux, canonical, &entries[index].UnsafeCommands)
			entries[index].Initrd = rejectAlternateRootTokens(entries[index].Initrd, canonical, &entries[index].UnsafeCommands)
			entries[index].DeviceTrees = rejectAlternateRootTokens(entries[index].DeviceTrees, canonical, &entries[index].UnsafeCommands)
		}
	}
	return entries, nil
}

// rejectAlternateRootTokens prevents a host-side fixture prefix embedded in
// GRUB evidence from being retained or exposed as a target-system boot path.
func rejectAlternateRootTokens(tokens []GRUBPathToken, root string, unsafe *[]string) []GRUBPathToken {
	filtered := make([]GRUBPathToken, 0, len(tokens))
	for _, token := range tokens {
		if token.Path == root || strings.HasPrefix(token.Path, root+string(filepath.Separator)) {
			*unsafe = append(*unsafe, token.Command)
			continue
		}
		filtered = append(filtered, token)
	}
	return filtered
}

// verifyGRUBDeviceTreeBindings proves one consistent boot-time DTB path across
// every matching normal and recovery entry. An external binding must match a
// required same-ABI firmware DTB. Entries without an external directive are
// valid only when the kernel embeds exactly one recognised same-ABI DTB.
func verifyGRUBDeviceTreeBindings(ctx context.Context, root, abi string, entries []GRUBEntry, trees []DeviceTree) (DeviceTreeBootEvidence, error) {
	var evidence DeviceTreeBootEvidence
	var embedded *DeviceTreeBootEvidence
	if err := validateMatchingGRUBEntryArtifacts(ctx, root, entries, abi); err != nil {
		return DeviceTreeBootEvidence{}, err
	}
	for _, entry := range entries {
		if !entryHasArtefact(entry.Linux, "vmlinuz-"+abi) {
			continue
		}
		if len(entry.UnsafeCommands) != 0 {
			for _, command := range entry.UnsafeCommands {
				if command == "devicetree" {
					return DeviceTreeBootEvidence{}, fmt.Errorf("matching GRUB entry %q contains an unsafe device-tree path", entry.Title)
				}
			}
			return DeviceTreeBootEvidence{}, fmt.Errorf("matching GRUB entry %q contains unsafe boot paths", entry.Title)
		}
		var current DeviceTreeBootEvidence
		if len(entry.DeviceTrees) == 0 {
			if embedded == nil {
				verified, err := verifyEmbeddedDeviceTreeBinding(ctx, root, abi, trees)
				if err != nil {
					return DeviceTreeBootEvidence{}, err
				}
				embedded = &verified
			}
			current = *embedded
		} else {
			if len(entry.DeviceTrees) != 1 {
				return DeviceTreeBootEvidence{}, errors.New("matching GRUB entry has multiple device-tree directives")
			}
			digest, err := verifyExternalDeviceTreeBinding(ctx, root, abi, entry, trees)
			if err != nil {
				return DeviceTreeBootEvidence{}, err
			}
			current = DeviceTreeBootEvidence{Mode: DeviceTreeBootExternal, SHA256: digest, SHA256s: []string{digest}}
		}
		if evidence.Mode == "" {
			evidence = current
		} else if evidence.Mode != current.Mode || evidence.SHA256 != current.SHA256 || !equalStrings(evidence.SHA256s, current.SHA256s) {
			return DeviceTreeBootEvidence{}, errors.New("matching normal and recovery GRUB entries use inconsistent device-tree bindings")
		}
		evidence.GRUBEntryCount++
		if entry.Recovery {
			evidence.RecoveryGRUBEntryCount++
		} else {
			evidence.NormalGRUBEntryCount++
		}
	}
	if evidence.Mode == "" || evidence.GRUBEntryCount == 0 {
		return DeviceTreeBootEvidence{}, fmt.Errorf("ABI %s has no matching GRUB entry with a proven boot-time device tree", abi)
	}
	return evidence, nil
}

// InspectDeviceTreeBootBinding applies the native installer's complete
// embedded-or-external boot-DTB rule to bounded, already parsed GRUB entries.
// Device limits digest authority to one supported hardware variant so a
// diagnostic for one Surface model cannot accept another model's payload.
func InspectDeviceTreeBootBinding(ctx context.Context, root, abi, device string, entries []GRUBEntry) (DeviceTreeBootEvidence, error) {
	if err := validateABI("inspected", abi); err != nil {
		return DeviceTreeBootEvidence{}, err
	}
	relative, valid := DeviceTreeRelativePath(device)
	if !valid {
		return DeviceTreeBootEvidence{}, errors.New("device must be x1e-oled or x1p-lcd")
	}
	var selected kernel.DeviceTree
	for _, required := range requiredDeviceTrees {
		if required.Path == relative {
			selected = required
			break
		}
	}
	if selected.Device == "" {
		return DeviceTreeBootEvidence{}, errors.New("device has no required device-tree contract")
	}
	target, err := rootPath(root, "usr/lib/firmware/"+abi+"/device-tree/"+selected.Path)
	if err != nil {
		return DeviceTreeBootEvidence{}, err
	}
	external := false
	for _, entry := range entries {
		if len(entry.DeviceTrees) == 0 {
			continue
		}
		external = true
		entryDevice, _, recognised := requiredTreeForEntry(entry, abi)
		if !recognised || (entryDevice != abiStampedDeviceTreeMarker && entryDevice != selected.Device) {
			return DeviceTreeBootEvidence{}, errors.New("matching GRUB entry names a different device-tree variant")
		}
	}
	if external {
		// Legacy diagnostics retain their bounded shared-name mapping. Generic
		// schema-2 installation always supplies the signed inventory instead.
		return verifyGRUBDeviceTreeBindings(ctx, root, abi, entries, nil)
	}
	return verifyGRUBDeviceTreeBindings(ctx, root, abi, entries, []DeviceTree{{
		Device: selected.Device, RelativePath: selected.Path, TargetPath: target,
		EmbeddedMatches: 1,
	}})
}

// verifyExternalDeviceTreeBinding checks one recognised GRUB DTB token and
// returns its boot-side digest after same-ABI comparison.
func verifyExternalDeviceTreeBinding(ctx context.Context, root, abi string, entry GRUBEntry, trees []DeviceTree) (string, error) {
	device, relative, valid := declaredTreeForEntry(entry, abi, trees)
	if !valid {
		return "", errors.New("matching GRUB entry has an unrecognised declared device-tree path")
	}
	if device == abiStampedDeviceTreeMarker {
		digest, err := verifyABIStampedDeviceTreeBinding(ctx, root, abi, entry.DeviceTrees[0].Path, trees)
		if err != nil {
			return "", err
		}
		return digest, nil
	}
	installedPath := ""
	for _, tree := range trees {
		if tree.Device == device && tree.RelativePath == relative {
			installedPath = tree.TargetPath
			break
		}
	}
	if installedPath == "" {
		if len(trees) > 0 {
			return "", errors.New("matching GRUB entry names a different declared device-tree variant")
		}
		var err error
		installedPath, err = rootPath(root, "usr/lib/firmware/"+abi+"/device-tree/"+relative)
		if err != nil {
			return "", err
		}
	}
	if err := validateTargetRoute(root, installedPath, false); err != nil {
		return "", err
	}
	installed, err := requireRegularEvidence(ctx, "installed device-tree", installedPath)
	if err != nil {
		return "", err
	}
	bootSide, err := hashGRUBPath(ctx, root, entry.DeviceTrees[0].Path)
	if err != nil {
		return "", fmt.Errorf("inspect GRUB device-tree for %s: %w", device, err)
	}
	if bootSide.SHA256 != installed.SHA256 {
		return "", fmt.Errorf("GRUB device-tree for %s does not match installed ABI %s", device, abi)
	}
	return bootSide.SHA256, nil
}

// verifyEmbeddedDeviceTreeBinding accepts a missing external GRUB directive
// only when the bounded AArch64 PE image carries exactly one .dtbauto payload
// matching a required same-ABI firmware DTB.
func verifyEmbeddedDeviceTreeBinding(ctx context.Context, root, abi string, trees []DeviceTree) (DeviceTreeBootEvidence, error) {
	image, err := ReadRootFile(ctx, root, "boot/vmlinuz-"+abi, "kernel image", maximumBootImageInspectionBytes)
	if err != nil {
		return DeviceTreeBootEvidence{}, fmt.Errorf("inspect kernel image for embedded device tree: %w", err)
	}
	file, err := pe.NewFile(bytes.NewReader(image))
	if err != nil {
		return DeviceTreeBootEvidence{}, fmt.Errorf("matching GRUB entry has no devicetree directive and kernel image has no inspectable embedded device tree: %w", err)
	}
	defer file.Close()
	if file.FileHeader.Machine != pe.IMAGE_FILE_MACHINE_ARM64 {
		return DeviceTreeBootEvidence{}, errors.New("matching GRUB entry has no devicetree directive and kernel image is not AArch64 PE")
	}
	installed, err := installedDeviceTreeCandidates(ctx, root, abi, trees, true)
	if err != nil {
		return DeviceTreeBootEvidence{}, err
	}
	if len(installed) == 0 {
		return DeviceTreeBootEvidence{}, fmt.Errorf("embedded device-tree verification found no installed same-ABI variant for ABI %s", abi)
	}
	sections := 0
	matchCounts := make(map[string]int, len(installed))
	for _, section := range file.Sections {
		if err := ctx.Err(); err != nil {
			return DeviceTreeBootEvidence{}, err
		}
		if section.Name != ".dtbauto" {
			continue
		}
		sections++
		if section.VirtualSize == 0 || section.VirtualSize > section.Size {
			return DeviceTreeBootEvidence{}, errors.New("embedded device-tree section has an invalid bounded payload size")
		}
		data, err := section.Data()
		if err != nil {
			return DeviceTreeBootEvidence{}, fmt.Errorf("read embedded device-tree section: %w", err)
		}
		if uint64(len(data)) < uint64(section.VirtualSize) {
			return DeviceTreeBootEvidence{}, errors.New("embedded device-tree section is shorter than its declared virtual payload")
		}
		payload := data[:section.VirtualSize]
		digestBytes := sha256.Sum256(payload)
		digest := hex.EncodeToString(digestBytes[:])
		sectionMatches := 0
		for _, candidate := range installed {
			if candidate.Size == int64(len(payload)) && candidate.SHA256 == digest {
				sectionMatches++
				matchCounts[digest]++
			}
		}
		if sectionMatches != 1 {
			return DeviceTreeBootEvidence{}, fmt.Errorf("embedded device-tree section has %d attributable same-ABI matches; expected exactly one", sectionMatches)
		}
	}
	if sections == 0 {
		return DeviceTreeBootEvidence{}, errors.New("matching GRUB entry has no devicetree directive and kernel image has no .dtbauto section")
	}
	closedInventory := len(trees) != 0
	if closedInventory && sections != len(installed) {
		return DeviceTreeBootEvidence{}, fmt.Errorf("kernel image has %d attributable .dtbauto sections for %d required same-ABI DTBs", sections, len(installed))
	}
	digests := make([]string, 0, len(installed))
	for _, candidate := range installed {
		if matchCounts[candidate.SHA256] > 1 {
			return DeviceTreeBootEvidence{}, fmt.Errorf("same-ABI DTB %s has %d embedded matches; expected at most one", candidate.Path, matchCounts[candidate.SHA256])
		}
		if closedInventory && matchCounts[candidate.SHA256] != 1 {
			return DeviceTreeBootEvidence{}, fmt.Errorf("required same-ABI DTB %s has %d embedded matches; expected exactly one", candidate.Path, matchCounts[candidate.SHA256])
		}
		if matchCounts[candidate.SHA256] == 1 {
			digests = append(digests, candidate.SHA256)
		}
	}
	sort.Strings(digests)
	if len(digests) == 0 {
		return DeviceTreeBootEvidence{}, errors.New("embedded device-tree verification found no uniquely attributable same-ABI payload")
	}
	matchedDigest := digests[0]
	if len(digests) > 1 {
		aggregate := sha256.Sum256([]byte(strings.Join(digests, "\x00")))
		matchedDigest = hex.EncodeToString(aggregate[:])
	}
	return DeviceTreeBootEvidence{Mode: DeviceTreeBootEmbedded, SHA256: matchedDigest, SHA256s: digests}, nil
}

// installedDeviceTreeCandidates returns only required, same-ABI DTBs selected
// by the caller. An empty selection retains the installer's all-device policy.
func installedDeviceTreeCandidates(ctx context.Context, root, abi string, trees []DeviceTree, embeddedOnly bool) ([]FileEvidence, error) {
	selected := trees
	if len(selected) == 0 {
		selected = make([]DeviceTree, 0, len(requiredDeviceTrees))
		for _, tree := range requiredDeviceTrees {
			target, err := rootPath(root, "usr/lib/firmware/"+abi+"/device-tree/"+tree.Path)
			if err != nil {
				return nil, err
			}
			selected = append(selected, DeviceTree{Device: tree.Device, RelativePath: tree.Path, TargetPath: target})
		}
	}
	installed := make([]FileEvidence, 0, len(selected))
	for _, tree := range selected {
		if len(trees) != 0 && embeddedOnly && tree.EmbeddedMatches != 1 {
			continue
		}
		if err := validateSelectedDeviceTree(tree); err != nil {
			return nil, err
		}
		expected, err := rootPath(root, "usr/lib/firmware/"+abi+"/device-tree/"+tree.RelativePath)
		if err != nil {
			return nil, err
		}
		if filepath.Clean(tree.TargetPath) != expected {
			return nil, errors.New("selected device-tree target differs from its same-ABI firmware path")
		}
		if _, err := os.Lstat(tree.TargetPath); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		if err := validateTargetRoute(root, tree.TargetPath, false); err != nil {
			return nil, err
		}
		evidence, err := requireRegularEvidence(ctx, "installed device-tree", tree.TargetPath)
		if err != nil {
			return nil, err
		}
		if tree.ExpectedSHA256 != "" && evidence.SHA256 != tree.ExpectedSHA256 {
			return nil, fmt.Errorf("installed device tree %s digest differs from the build inventory", tree.Device)
		}
		installed = append(installed, evidence)
	}
	return installed, nil
}

// validateSelectedDeviceTree keeps caller-selected inventory within one
// portable vendor-relative DTB path. The signed bundle supplies the closed
// platform set; this layer supplies the filesystem boundary.
func validateSelectedDeviceTree(tree DeviceTree) error {
	if !packageNameExpression.MatchString(tree.Device) {
		return errors.New("selected device tree has an unsafe platform identifier")
	}
	if tree.RelativePath == "" || path.IsAbs(tree.RelativePath) || path.Clean(tree.RelativePath) != tree.RelativePath ||
		strings.Contains(tree.RelativePath, "\\") || strings.Contains(tree.RelativePath, "..") ||
		!strings.Contains(tree.RelativePath, "/") || !strings.HasSuffix(tree.RelativePath, ".dtb") {
		return errors.New("selected device tree has an unsafe package-relative path")
	}
	return nil
}

// verifyABIStampedDeviceTreeBinding attributes the physical hardware variant
// before digest comparison, mirroring the bootdoctor digest-filtering used by
// SelectDTBCandidate: installed same-ABI variant candidates are filtered by
// the boot-side digest so coexisting OLED and LCD variants remain the normal
// healthy state. The binding fails closed when the boot bytes match no
// installed variant; byte-identical variant matches are interchangeable.
func verifyABIStampedDeviceTreeBinding(ctx context.Context, root, abi, token string, trees []DeviceTree) (string, error) {
	bootSide, err := hashGRUBPath(ctx, root, token)
	if err != nil {
		return "", fmt.Errorf("inspect ABI-stamped GRUB device-tree: %w", err)
	}
	installed, err := installedDeviceTreeCandidates(ctx, root, abi, trees, false)
	if err != nil {
		return "", err
	}
	if len(installed) == 0 {
		return "", fmt.Errorf("ABI-stamped GRUB device-tree has no installed variant for ABI %s", abi)
	}
	attributed := 0
	for _, candidate := range installed {
		if candidate.SHA256 == bootSide.SHA256 {
			attributed++
		}
	}
	if attributed == 0 {
		return "", fmt.Errorf("ABI-stamped GRUB device-tree for ABI %s does not match any installed variant", abi)
	}
	return bootSide.SHA256, nil
}

// slicesContainString reports whether values contains one exact string.
func slicesContainString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

// equalStrings reports whether two ordered string collections are identical.
func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// entryHasArtefact reports whether one recognised command list names the exact
// kernel or initramfs basename.
func entryHasArtefact(tokens []GRUBPathToken, basename string) bool {
	for _, token := range tokens {
		if pathTokenMatches([]string{token.Path}, basename) {
			return true
		}
	}
	return false
}

// grubEntryNamesExactABIArtifacts reports whether an entry has exactly one
// kernel token and exactly one initramfs token with the requested basenames.
// Byte identity is proved separately before callers use this for cardinality.
func grubEntryNamesExactABIArtifacts(entry GRUBEntry, abi string) bool {
	return len(entry.Linux) == 1 && entryHasArtefact(entry.Linux, "vmlinuz-"+abi) &&
		len(entry.Initrd) == 1 && entryHasArtefact(entry.Initrd, "initrd.img-"+abi)
}

// GRUBEntryHasUnsafeBootArtifacts reports whether parsing rejected any kernel
// or initramfs token. The raw value is deliberately not retained, so callers
// must fail closed rather than attempting to attribute it to a different ABI.
func GRUBEntryHasUnsafeBootArtifacts(entry GRUBEntry) bool {
	for _, command := range entry.UnsafeCommands {
		switch command {
		case "linux", "linuxefi", "initrd", "initrdefi":
			return true
		}
	}
	return false
}

// VerifyGRUBEntryABIArtifacts resolves and hashes the actual GRUB tokens and
// requires byte identity with the canonical exact-ABI kernel and initramfs.
// HashGRUBPath handles both /boot-prefixed and separate-/boot token forms.
func VerifyGRUBEntryABIArtifacts(ctx context.Context, root string, entry GRUBEntry, abi string) error {
	if len(entry.Linux) != 1 || !entryHasArtefact(entry.Linux, "vmlinuz-"+abi) {
		return errors.New("does not name exactly one exact-ABI kernel")
	}
	if len(entry.Initrd) != 1 || !entryHasArtefact(entry.Initrd, "initrd.img-"+abi) {
		return errors.New("does not name exactly one exact-ABI initramfs")
	}
	wantedKernel, err := HashGRUBPath(ctx, root, "/boot/vmlinuz-"+abi)
	if err != nil {
		return fmt.Errorf("verify canonical exact-ABI kernel: %w", err)
	}
	actualKernel, err := HashGRUBPath(ctx, root, entry.Linux[0].Path)
	if err != nil {
		return fmt.Errorf("verify GRUB kernel token: %w", err)
	}
	if actualKernel.SHA256 != wantedKernel.SHA256 || actualKernel.Size != wantedKernel.Size {
		return errors.New("GRUB kernel token differs from the canonical exact-ABI kernel")
	}
	wantedInitrd, err := HashGRUBPath(ctx, root, "/boot/initrd.img-"+abi)
	if err != nil {
		return fmt.Errorf("verify canonical exact-ABI initramfs: %w", err)
	}
	actualInitrd, err := HashGRUBPath(ctx, root, entry.Initrd[0].Path)
	if err != nil {
		return fmt.Errorf("verify GRUB initramfs token: %w", err)
	}
	if actualInitrd.SHA256 != wantedInitrd.SHA256 || actualInitrd.Size != wantedInitrd.Size {
		return errors.New("GRUB initramfs token differs from the canonical exact-ABI initramfs")
	}
	return nil
}

// declaredTreeForEntry resolves the generic boot-support package's only
// permitted external path forms against the signed bundle inventory. A nil
// inventory retains the bounded legacy fallback mapping used by diagnostics.
func declaredTreeForEntry(entry GRUBEntry, abi string, trees []DeviceTree) (string, string, bool) {
	if len(trees) == 0 {
		return requiredTreeForEntry(entry, abi)
	}
	token := entry.DeviceTrees[0].Path
	if _, valid := ABIStampedDTBIdentity(token); valid && ABIStampedDTBMatchesABI(token, abi) {
		return abiStampedDeviceTreeMarker, "", true
	}
	for _, tree := range trees {
		basename := path.Base(tree.RelativePath)
		if token == "/"+basename || token == "/boot/"+basename ||
			token == "/"+tree.RelativePath || token == "/boot/"+tree.RelativePath {
			return tree.Device, tree.RelativePath, true
		}
	}
	var relative string
	for _, prefix := range []string{"/dtbs/" + abi + "/", "/boot/dtbs/" + abi + "/"} {
		if value, found := strings.CutPrefix(token, prefix); found {
			relative = value
			break
		}
	}
	if relative == "" || path.Clean(relative) != relative || strings.Contains(relative, "..") {
		return "", "", false
	}
	for _, tree := range trees {
		if tree.RelativePath == relative {
			return tree.Device, tree.RelativePath, true
		}
	}
	return "", "", false
}

// requiredTreeForEntry maps canonical and legacy shared names to one compiled
// Surface hardware variant. ABI-stamped names return a marker so the caller
// can derive the variant from installed digest evidence rather than the title.
func requiredTreeForEntry(entry GRUBEntry, abi string) (string, string, bool) {
	token := strings.ToLower(entry.DeviceTrees[0].Path)
	if _, valid := ABIStampedDTBIdentity(token); valid && ABIStampedDTBMatchesABI(token, abi) {
		return abiStampedDeviceTreeMarker, "", true
	}
	title := strings.ToLower(entry.Title)
	titleX1P := strings.Contains(title, "x1p") || strings.Contains(title, "lcd")
	titleX1E := strings.Contains(title, "x1e") || strings.Contains(title, "oled")
	for _, tree := range requiredDeviceTrees {
		if filepath.Base(token) == strings.ToLower(filepath.Base(tree.Path)) {
			if (strings.Contains(tree.Device, "x1p") && titleX1E) || (strings.Contains(tree.Device, "x1e") && titleX1P) {
				return "", "", false
			}
			return tree.Device, tree.Path, true
		}
	}
	if filepath.Base(token) == "sp11-denali.dtb" && titleX1P && !titleX1E {
		return requiredDeviceTrees[1].Device, requiredDeviceTrees[1].Path, true
	}
	if filepath.Base(token) == "sp11-denali.dtb" && !titleX1P {
		return requiredDeviceTrees[0].Device, requiredDeviceTrees[0].Path, true
	}
	return "", "", false
}

// ABIStampedDTBIdentity returns the bounded identity embedded in one safe
// dtb-<identity> basename in any normalised directory. The suffix grammar is
// deliberately looser than package ABI validation because deployed GRUB tokens
// use markers such as sp11beta2.
func ABIStampedDTBIdentity(token string) (string, bool) {
	normalised, valid := normaliseGRUBToken(token)
	if !valid {
		return "", false
	}
	base := strings.ToLower(filepath.Base(normalised))
	if !strings.HasPrefix(base, "dtb-") {
		return "", false
	}
	identity := strings.TrimPrefix(base, "dtb-")
	if len(identity) == 0 || len(identity) > maximumABIBytes || !abiStampedDTBExpression.MatchString(identity) {
		return "", false
	}
	return identity, true
}

// ABIStampedDTBMatchesABI cross-checks the unambiguous numeric kernel version
// in a stamped token against the entry's vmlinuz ABI. Local suffix conventions
// may differ; byte-for-byte digest verification proves the actual DTB binding.
func ABIStampedDTBMatchesABI(token, abi string) bool {
	identity, valid := ABIStampedDTBIdentity(token)
	if !valid || len(abi) == 0 || len(abi) > maximumABIBytes {
		return false
	}
	tokenVersion := abiStampedDTBExpression.FindStringSubmatch(identity)
	abiVersion := abiStampedDTBExpression.FindStringSubmatch(strings.ToLower(abi))
	if len(tokenVersion) != 4 || len(abiVersion) != 4 {
		return false
	}
	for index := 1; index <= 3; index++ {
		if tokenVersion[index] != abiVersion[index] {
			return false
		}
	}
	return true
}

// hashGRUBPath resolves a GRUB path below the selected target root or its
// mounted boot directory and returns bounded regular-file evidence.
func hashGRUBPath(ctx context.Context, root, token string) (FileEvidence, error) {
	normalised, valid := normaliseGRUBToken(token)
	if !valid {
		return FileEvidence{}, errors.New("GRUB boot-artifact token is not a safe absolute path")
	}
	relative := strings.TrimPrefix(normalised, "/")
	candidates := []string{relative}
	if !strings.HasPrefix(normalised, "/boot/") {
		candidates = []string{"boot/" + relative, relative}
	}
	var missing error
	available := make([]FileEvidence, 0, len(candidates))
	for _, candidate := range candidates {
		target, err := rootPath(root, candidate)
		if err != nil {
			return FileEvidence{}, err
		}
		if err := validateTargetRoute(root, target, false); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				missing = err
				continue
			}
			return FileEvidence{}, err
		}
		evidence, err := requireRegularEvidence(ctx, "GRUB boot-artifact", target)
		if err == nil {
			available = append(available, evidence)
			continue
		}
		if errors.Is(err, os.ErrNotExist) {
			missing = err
			continue
		}
		return FileEvidence{}, err
	}
	if len(available) > 1 {
		return FileEvidence{}, errors.New("GRUB boot-artifact path has ambiguous root and boot-directory resolutions")
	}
	if len(available) == 1 {
		return available[0], nil
	}
	if missing != nil {
		return FileEvidence{}, missing
	}
	return FileEvidence{}, errors.New("GRUB boot-artifact path is unavailable")
}

// HashGRUBPath resolves and hashes one recognised GRUB path through the same
// bounded, symlink-rejecting evidence boundary used by installation.
func HashGRUBPath(ctx context.Context, root, token string) (FileEvidence, error) {
	canonical, err := canonicalRoot(root)
	if err != nil {
		return FileEvidence{}, err
	}
	return hashGRUBPath(ctx, canonical, token)
}

// InspectGRUBPath resolves and hashes one recognised path while exposing the
// missing-versus-permission distinction needed by read-only diagnostics. Other
// safety errors retain an empty availability and must continue to fail closed.
func InspectGRUBPath(ctx context.Context, root, token string) (FileEvidence, GRUBPathAvailability, error) {
	evidence, err := HashGRUBPath(ctx, root, token)
	switch {
	case err == nil:
		return evidence, GRUBPathPresent, nil
	case errors.Is(err, os.ErrPermission):
		return FileEvidence{}, GRUBPathInaccessible, err
	case errors.Is(err, os.ErrNotExist):
		return FileEvidence{}, GRUBPathMissing, err
	default:
		return FileEvidence{}, "", err
	}
}

// pathTokenMatches reports whether any GRUB path token ends in the exact artefact.
func pathTokenMatches(tokens []string, basename string) bool {
	for _, token := range tokens {
		token = strings.Trim(token, "'\"")
		if token == basename || strings.HasSuffix(token, "/"+basename) {
			return true
		}
	}
	return false
}

// fallbackUnchanged compares the safety-critical fallback identities before and after.
func fallbackUnchanged(before, after BootEvidence) error {
	if before.ABI != after.ABI || before.KernelImage.SHA256 != after.KernelImage.SHA256 ||
		before.Initramfs.SHA256 != after.Initramfs.SHA256 ||
		before.SystemMap.SHA256 != after.SystemMap.SHA256 ||
		before.KernelConfig.SHA256 != after.KernelConfig.SHA256 ||
		before.ModulesDependencyIndex.SHA256 != after.ModulesDependencyIndex.SHA256 ||
		before.ModuleFile.SHA256 != after.ModuleFile.SHA256 ||
		before.DeviceTreeBoot.Mode != after.DeviceTreeBoot.Mode ||
		before.DeviceTreeBoot.SHA256 != after.DeviceTreeBoot.SHA256 ||
		before.DeviceTreeBoot.GRUBEntryCount != after.DeviceTreeBoot.GRUBEntryCount ||
		before.DeviceTreeBoot.NormalGRUBEntryCount != after.DeviceTreeBoot.NormalGRUBEntryCount ||
		before.DeviceTreeBoot.RecoveryGRUBEntryCount != after.DeviceTreeBoot.RecoveryGRUBEntryCount ||
		!equalStrings(before.DeviceTreeBoot.SHA256s, after.DeviceTreeBoot.SHA256s) ||
		after.GRUBEntryCount != 1 {
		return fmt.Errorf("fallback ABI %s changed or became unbootable during installation", before.ABI)
	}
	return nil
}
