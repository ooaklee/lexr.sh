package install

import (
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// maximumCompanionModuleBytes bounds one out-of-tree module inspected for
	// vermagic so a corrupted tree cannot exhaust preflight memory.
	maximumCompanionModuleBytes int64 = 64 << 20
	// maximumCompanionModuleFiles bounds the total out-of-tree module files
	// retained in one warning so an adversarial tree cannot flood receipts.
	maximumCompanionModuleFiles = 512
)

// companionModuleDirectories are the module-tree locations an out-of-tree
// companion package installs into. The literal `lib` form is only inspected
// when it is not the merged-usr symlink of `usr/lib`, which the caller
// de-duplicates.
var companionModuleDirectories = []string{"usr/lib/modules", "lib/modules"}

// companionModuleSuffixes are the kmod installation forms of built modules.
var companionModuleSuffixes = []string{".ko", ".ko.zst", ".ko.xz", ".ko.gz"}

// companionModuleABIWarning returns one explicit non-fatal warning per other
// kernel ABI whose out-of-tree companion modules under .../<abi>/updates will
// have no matching ABI after installing the target ABI, or "" when the target
// root has no such modules. Unreadable module metadata never fails preflight:
// the module-tree ABI directory is the authoritative fallback signal.
func companionModuleABIWarning(root, targetABI string) string {
	trees := companionModuleTrees(root)
	if len(trees) == 0 {
		return ""
	}
	affected := make(map[string][]string)
	for _, tree := range trees {
		abi := filepath.Base(tree)
		if abi == targetABI {
			continue
		}
		for _, module := range companionModulesInTree(tree) {
			vermagic, ok := moduleVermagic(module)
			if !ok || vermagic == "" {
				// Compressed modules and unreadable metadata still carry the
				// ABI of the module tree they were installed for.
				vermagic = abi
			}
			affected[abi] = append(affected[abi],
				fmt.Sprintf("%s (vermagic %s)", module, vermagicFirstField(vermagic)))
		}
	}
	if len(affected) == 0 {
		return ""
	}
	abis := make([]string, 0, len(affected))
	for abi := range affected {
		abis = append(abis, abi)
	}
	sort.Strings(abis)
	var builder strings.Builder
	fmt.Fprintf(&builder,
		"warning: companion out-of-tree modules on this root were built for other kernel ABIs and will have no matching ABI after installing %s; rebuild them against the new headers before relying on the device (see the kernel management guide's companion module section):",
		targetABI)
	for _, abi := range abis {
		list := affected[abi]
		sort.Strings(list)
		fmt.Fprintf(&builder, "\n  %s: %s", abi, strings.Join(list, ", "))
	}
	return builder.String()
}

// companionModuleTrees returns the de-duplicated existing module trees of the
// target root. A merged-usr `lib` symlink resolves onto `usr/lib` and is
// skipped so its modules are never counted twice.
func companionModuleTrees(root string) []string {
	var trees []string
	seen := make(map[string]bool)
	for _, relative := range companionModuleDirectories {
		base := filepath.Join(root, filepath.FromSlash(relative))
		resolved, err := filepath.EvalSymlinks(base)
		if err != nil || seen[resolved] {
			continue
		}
		seen[resolved] = true
		entries, err := os.ReadDir(resolved)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			trees = append(trees, filepath.Join(resolved, entry.Name()))
		}
	}
	return trees
}

// companionModulesInTree lists the installed out-of-tree module files under
// one module tree's updates directory as `updates/...` paths relative to the
// tree, bounded in count. Paths are reported relative so warnings remain
// portable across hosts and receipts stay short.
func companionModulesInTree(tree string) []string {
	var modules []string
	_ = filepath.WalkDir(filepath.Join(tree, "updates"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || len(modules) >= maximumCompanionModuleFiles {
			return filepath.SkipAll
		}
		if entry.IsDir() {
			return nil
		}
		for _, suffix := range companionModuleSuffixes {
			if strings.HasSuffix(entry.Name(), suffix) {
				if relative, relErr := filepath.Rel(tree, path); relErr == nil {
					modules = append(modules, filepath.ToSlash(relative))
				}
				return nil
			}
		}
		return nil
	})
	return modules
}

// moduleVermagic reads one module's .modinfo vermagic line when the module is
// an uncompressed, bounded ELF object.
func moduleVermagic(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > maximumCompanionModuleBytes {
		return "", false
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	parsed, err := elf.NewFile(file)
	if err != nil {
		return "", false
	}
	defer parsed.Close()
	section := parsed.Section(".modinfo")
	if section == nil || section.Size > uint64(maximumCompanionModuleBytes) {
		return "", false
	}
	data, err := section.Data()
	if err != nil {
		return "", false
	}
	for _, entry := range strings.Split(strings.TrimRight(string(data), "\x00"), "\x00") {
		if value, found := strings.CutPrefix(entry, "vermagic="); found {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// vermagicFirstField keeps warning lines short by retaining only the kernel
// version field of a vermagic line, e.g. `6.11.0-foo SMP preempt ... aarch64`.
func vermagicFirstField(vermagic string) string {
	fields := strings.Fields(vermagic)
	if len(fields) == 0 {
		return "unreadable"
	}
	return fields[0]
}
