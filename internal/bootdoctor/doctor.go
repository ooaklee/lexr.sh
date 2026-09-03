package bootdoctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/kernel/install"
)

const (
	// maximumBootSettingBytes bounds GRUB defaults and environment inspection.
	maximumBootSettingBytes int64 = 1 << 20
	// physicalBootabilityLimitation is the stable qualification disclaimer.
	physicalBootabilityLimitation = "static evidence cannot prove physical bootability"
)

// Inspector coordinates static boot evidence through read-only install boundaries.
type Inspector struct{}

// New constructs a strictly read-only boot inspector.
func New() *Inspector {
	return &Inspector{}
}

// Inspect returns a complete static boot report without executing commands,
// elevating privileges, changing defaults, or rewriting device trees.
func (inspector *Inspector) Inspect(ctx context.Context, options Options) (_ Report, returnErr error) {
	if inspector == nil {
		return Report{}, errors.New("boot doctor is not initialised")
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	root := options.Root
	if strings.TrimSpace(root) == "" {
		root = string(filepath.Separator)
	}
	defer func() {
		returnErr = redactAlternateRootError(root, returnErr)
	}()
	device, err := selectDevice(ctx, root, options.Device)
	if err != nil {
		return Report{}, err
	}
	for label, abi := range map[string]string{"target": options.TargetABI, "fallback": options.FallbackABI} {
		if abi == "" {
			continue
		}
		if _, valid := parseKernelRank(abi); !valid {
			return Report{}, fmt.Errorf("%s ABI is not one canonical qcom kernel identity", label)
		}
	}
	parsed, err := install.InspectGRUB(ctx, root)
	if err != nil {
		return Report{}, fmt.Errorf("inspect boot configuration: %w", err)
	}
	report := Report{
		Ready:               true,
		Device:              device,
		TargetABI:           options.TargetABI,
		FallbackABI:         options.FallbackABI,
		Entries:             make([]Entry, 0, len(parsed)),
		PhysicalBootability: physicalBootabilityLimitation,
	}
	configured, saved, settingChecks := inspectDefaultSettings(ctx, root)
	report.Checks = append(report.Checks, settingChecks...)
	report.Default = resolveDefault(parsed, configured, saved)
	report.Default.Configured = redactAlternateRootValue(root, report.Default.Configured)
	report.Default.SavedEntry = redactAlternateRootValue(root, report.Default.SavedEntry)
	report.Default.Effective = redactAlternateRootValue(root, report.Default.Effective)
	if report.Default.Stale {
		report.add(Check{ID: "grub-default-selection", State: StateFail, Required: true, Detail: "the effective GRUB default does not resolve to an available menu entry"})
	} else {
		report.add(Check{ID: "grub-default-selection", State: StatePass, Required: true, Detail: "the effective GRUB default resolves to an available menu entry"})
	}

	relativeDTB, _ := install.DeviceTreeRelativePath(device)
	candidateABIs := make(map[string]bool)
	firmwareABIs, firmwareErr := install.ListRootDirectories(ctx, root, "usr/lib/firmware", "firmware ABI root", 4096)
	if firmwareErr == nil {
		for _, abi := range firmwareABIs {
			candidateABIs[abi] = true
		}
	} else if !errors.Is(firmwareErr, os.ErrNotExist) {
		report.add(Check{ID: "firmware-abi-enumeration", State: StateWarn, Detail: "installed firmware ABI directories could not be enumerated safely"})
	}
	for _, parsedEntry := range parsed {
		abi := entryABI(parsedEntry)
		if abi != "" {
			candidateABIs[abi] = true
		}
	}
	if options.TargetABI != "" {
		candidateABIs[options.TargetABI] = true
	}
	if options.FallbackABI != "" {
		candidateABIs[options.FallbackABI] = true
	}
	candidates := make([]DTBCandidate, 0, len(candidateABIs))
	for abi := range candidateABIs {
		if _, valid := parseKernelRank(abi); !valid {
			continue
		}
		evidence, hashErr := install.HashRootFile(ctx, root, "usr/lib/firmware/"+abi+"/device-tree/"+relativeDTB, "installed device-tree")
		if hashErr == nil {
			candidates = append(candidates, DTBCandidate{ABI: abi, Device: device, SHA256: evidence.SHA256})
		}
	}

	seenRequired := map[string]bool{options.TargetABI: false, options.FallbackABI: false}
	for _, parsedEntry := range parsed {
		entry := inspectEntry(ctx, root, parsedEntry, relativeDTB)
		report.Entries = append(report.Entries, entry)
		required := report.entryRequired(entry)
		if entry.ABI == options.TargetABI || entry.ABI == options.FallbackABI {
			seenRequired[entry.ABI] = true
		}
		report.addEntryChecks(entry, required)
	}
	for _, abi := range []string{options.TargetABI, options.FallbackABI} {
		if abi != "" && !seenRequired[abi] {
			report.add(Check{ID: "required-grub-entry", State: StateFail, Required: true, ABI: abi, Detail: "the explicitly required ABI has no GRUB menu entry"})
		}
	}

	selected, found, selectErr := SelectDTBCandidate(candidates, device)
	if selectErr != nil {
		report.add(Check{ID: "dtb-candidate-selection", State: StateFail, Required: true, Detail: "installed DTB candidates are ambiguous"})
	} else if found {
		report.Attribution.SelectedABI = selected.ABI
		report.Attribution.InstalledSHA256 = selected.SHA256
		report.add(Check{ID: "dtb-candidate-selection", State: StatePass, Detail: "the patch-line-first installed DTB candidate was selected"})
	} else {
		report.add(Check{ID: "dtb-candidate-selection", State: StateWarn, Detail: "no canonical installed DTB candidate was available for attribution"})
	}
	if report.Default.EntryIndex != nil {
		entry := report.Entries[*report.Default.EntryIndex]
		report.Attribution.BootSHA256 = entry.BootDTBSHA256
		matching := make([]DTBCandidate, 0)
		for _, candidate := range candidates {
			if candidate.SHA256 == entry.BootDTBSHA256 {
				matching = append(matching, candidate)
			}
		}
		attributed, matched, attributionErr := SelectDTBCandidate(matching, device)
		if attributionErr != nil {
			report.add(Check{ID: "boot-dtb-attribution", State: StateFail, Required: true, Detail: "the effective boot DTB has ambiguous ABI attribution"})
		} else if matched {
			report.Attribution.AttributedABI = attributed.ABI
			detail := "the effective boot DTB matches one canonical installed ABI candidate"
			if len(matching) > 1 {
				detail = "the effective boot DTB matches multiple canonical installed ABI candidates; attribution is ambiguous"
			}
			report.add(Check{ID: "boot-dtb-attribution", State: StatePass, Detail: detail})
		} else if entry.BootDTBSHA256 != "" {
			report.add(Check{ID: "boot-dtb-attribution", State: StateFail, Required: true, Detail: "the effective boot DTB matches no canonical installed ABI candidate"})
		}
	}
	report.Hooks = inspectLegacyHooks(ctx, root, &report)
	return report, nil
}

// add appends a check and applies only required failures to report readiness.
func (report *Report) add(check Check) {
	report.Checks = append(report.Checks, check)
	if check.Required && check.State == StateFail {
		report.Ready = false
	}
}

// entryRequired reports whether an entry is effective or names an explicitly
// requested target or fallback ABI.
func (report Report) entryRequired(entry Entry) bool {
	if report.Default.EntryIndex != nil && entry.Index == *report.Default.EntryIndex {
		return true
	}
	return entry.ABI != "" && (entry.ABI == report.TargetABI || entry.ABI == report.FallbackABI)
}

// addEntryChecks classifies stale artefacts and absent or mismatched DTBs for
// one normal or recovery entry according to its required status.
func (report *Report) addEntryChecks(entry Entry, required bool) {
	state := StatePass
	detail := "the kernel and initramfs paths exist as regular files"
	unsafeArtefact := slicesContain(entry.UnsafeCommands, "linux") || slicesContain(entry.UnsafeCommands, "linuxefi") ||
		slicesContain(entry.UnsafeCommands, "initrd") || slicesContain(entry.UnsafeCommands, "initrdefi")
	if !entry.KernelExists || !entry.InitramfsExists || unsafeArtefact {
		state = StateWarn
		detail = "the GRUB entry is stale or has an unsafe kernel or initramfs path token"
		if required {
			state = StateFail
		}
	}
	report.add(Check{ID: "grub-entry-artefacts", State: state, Required: required, ABI: entry.ABI, Detail: detail})

	dtbState := StatePass
	dtbDetail := "the boot-side DTB matches the installed same-ABI firmware DTB"
	if slicesContain(entry.UnsafeCommands, "devicetree") {
		dtbState = StateWarn
		dtbDetail = "the GRUB entry has an unsafe devicetree path token"
		if required {
			dtbState = StateFail
		}
	} else if len(entry.DeviceTrees) == 0 {
		dtbState = StateWarn
		dtbDetail = "the GRUB entry has no devicetree directive"
		if required {
			dtbState = StateFail
		}
	} else if entry.DTBMatches == nil || !*entry.DTBMatches {
		dtbState = StateWarn
		dtbDetail = "the boot-side DTB is missing or does not match the installed same-ABI firmware DTB"
		if required {
			dtbState = StateFail
		}
	}
	report.add(Check{ID: "grub-entry-dtb", State: dtbState, Required: required, ABI: entry.ABI, Detail: dtbDetail})
}

// slicesContain reports whether values contains one exact string.
func slicesContain(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

// selectDevice validates an explicit device or attempts bounded live-root discovery.
func selectDevice(ctx context.Context, root, requested string) (string, error) {
	if requested != "" {
		if _, valid := install.DeviceTreeRelativePath(requested); !valid {
			return "", errors.New("device must be x1e-oled or x1p-lcd")
		}
		if strings.Contains(requested, "x1p") {
			return "x1p-lcd", nil
		}
		return "x1e-oled", nil
	}
	if filepath.Clean(root) != string(filepath.Separator) {
		return "", errors.New("--device is required when --root is not the live root")
	}
	model, modelErr := install.ReadRootFile(ctx, root, "sys/firmware/devicetree/base/model", "live device-tree model", 4096)
	compatible, compatibleErr := install.ReadRootFile(ctx, root, "sys/firmware/devicetree/base/compatible", "live device-tree compatibility", 4096)
	evidence := strings.ToLower(string(model) + " " + strings.ReplaceAll(string(compatible), "\x00", " "))
	if modelErr == nil || compatibleErr == nil {
		switch {
		case strings.Contains(evidence, "x1p64100") || strings.Contains(evidence, "lcd"):
			return "x1p-lcd", nil
		case strings.Contains(evidence, "x1e80100") || strings.Contains(evidence, "oled"):
			return "x1e-oled", nil
		}
	}
	return "", errors.New("--device is required because the live hardware variant could not be proven from static evidence")
}

// inspectEntry checks recognised paths without retaining any other stanza arguments.
func inspectEntry(ctx context.Context, root string, parsed install.GRUBEntry, relativeDTB string) Entry {
	entry := Entry{
		Index:          parsed.Index,
		Depth:          parsed.Depth,
		MenuPath:       append([]int(nil), parsed.MenuPath...),
		Title:          redactAlternateRootValue(root, parsed.Title),
		ID:             redactAlternateRootValue(root, parsed.ID),
		ABI:            entryABI(parsed),
		Recovery:       parsed.Recovery,
		Linux:          parsed.Linux,
		Initrd:         parsed.Initrd,
		DeviceTrees:    parsed.DeviceTrees,
		UnsafeCommands: parsed.UnsafeCommands,
	}
	entry.KernelExists = anyGRUBPathExists(ctx, root, parsed.Linux)
	entry.InitramfsExists = anyGRUBPathExists(ctx, root, parsed.Initrd)
	if len(parsed.DeviceTrees) != 1 || entry.ABI == "" || !entryTokenMatchesDevice(parsed.DeviceTrees[0].Path, parsed.Title, relativeDTB) {
		return entry
	}
	bootSide, bootErr := install.HashGRUBPath(ctx, root, parsed.DeviceTrees[0].Path)
	installed, installedErr := install.HashRootFile(ctx, root, "usr/lib/firmware/"+entry.ABI+"/device-tree/"+relativeDTB, "installed device-tree")
	if bootErr == nil {
		entry.BootDTBSHA256 = bootSide.SHA256
	}
	if installedErr == nil {
		entry.InstalledDTBSHA256 = installed.SHA256
	}
	if bootErr == nil && installedErr == nil {
		matches := bootSide.SHA256 == installed.SHA256
		entry.DTBMatches = &matches
	}
	return entry
}

// redactAlternateRootValue removes a host-side root prefix from scalar report
// fields while leaving ordinary target-system titles and identifiers intact.
func redactAlternateRootValue(root, value string) string {
	if value == "" || filepath.Clean(root) == string(filepath.Separator) {
		return value
	}
	if strings.Contains(value, root) {
		return "[redacted alternate-root value]"
	}
	return value
}

// redactAlternateRootError removes the selected host-side root prefix from a
// fatal inspection error while preserving a useful bounded explanation.
func redactAlternateRootError(root string, err error) error {
	if err == nil || filepath.Clean(root) == string(filepath.Separator) {
		return err
	}
	cleanRoot := filepath.Clean(root)
	message := strings.ReplaceAll(err.Error(), cleanRoot, "[redacted alternate-root value]")
	if message == err.Error() {
		return err
	}
	return errors.New(message)
}

// entryTokenMatchesDevice prevents X1E and X1P DTB paths from being compared,
// using an explicit title variant when the retired helper used a shared name.
func entryTokenMatchesDevice(token, title, relativeDTB string) bool {
	base := strings.ToLower(filepath.Base(token))
	expected := strings.ToLower(filepath.Base(relativeDTB))
	if base == expected {
		return true
	}
	if base != "sp11-denali.dtb" {
		return false
	}
	title = strings.ToLower(title)
	if strings.Contains(expected, "x1p64100") {
		return strings.Contains(title, "x1p") || strings.Contains(title, "lcd")
	}
	return !strings.Contains(title, "x1p") && !strings.Contains(title, "lcd")
}

// anyGRUBPathExists reports whether any recognised path resolves to bounded
// regular evidence through the shared installation verifier.
func anyGRUBPathExists(ctx context.Context, root string, tokens []install.GRUBPathToken) bool {
	for _, token := range tokens {
		if _, err := install.HashGRUBPath(ctx, root, token.Path); err == nil {
			return true
		}
	}
	return false
}

// entryABI infers one canonical ABI only from unambiguous vmlinuz basenames.
func entryABI(entry install.GRUBEntry) string {
	abi := ""
	for _, token := range entry.Linux {
		base := filepath.Base(token.Path)
		if !strings.HasPrefix(base, "vmlinuz-") {
			continue
		}
		candidate := strings.TrimPrefix(base, "vmlinuz-")
		if _, valid := parseKernelRank(candidate); !valid || (abi != "" && abi != candidate) {
			return ""
		}
		abi = candidate
	}
	return abi
}

// inspectDefaultSettings reads only GRUB_DEFAULT and saved_entry through bounded files.
func inspectDefaultSettings(ctx context.Context, root string) (string, string, []Check) {
	configured := "0"
	checks := make([]Check, 0, 2)
	defaults, defaultsErr := install.ReadRootFile(ctx, root, "etc/default/grub", "GRUB defaults", maximumBootSettingBytes)
	if defaultsErr == nil {
		if value, found := assignmentValue(defaults, "GRUB_DEFAULT"); found {
			configured = value
		}
	} else if !errors.Is(defaultsErr, os.ErrNotExist) {
		checks = append(checks, Check{ID: "grub-default-file", State: StateWarn, Detail: "GRUB defaults could not be inspected safely; the implicit first entry is assumed"})
	}
	saved := ""
	environment, environmentErr := install.ReadRootFile(ctx, root, "boot/grub/grubenv", "GRUB environment", maximumBootSettingBytes)
	if environmentErr == nil {
		saved, _ = assignmentValue(environment, "saved_entry")
	} else if !errors.Is(environmentErr, os.ErrNotExist) {
		checks = append(checks, Check{ID: "grub-saved-entry-file", State: StateWarn, Detail: "the GRUB saved-entry environment could not be inspected safely"})
	}
	return configured, saved, checks
}

// assignmentValue extracts one bounded literal assignment without sourcing the file.
func assignmentValue(content []byte, key string) (string, bool) {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || !strings.HasPrefix(line, key+"=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, key+"="))
		if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
			value = value[1 : len(value)-1]
		}
		if len(value) <= 512 && !strings.ContainsAny(value, "\x00\r\n") {
			return value, true
		}
		return "", false
	}
	return "", false
}

// resolveDefault maps a numeric, hierarchical, title, identifier, or saved
// GRUB selection without flattening submenu children into the top-level menu.
func resolveDefault(entries []install.GRUBEntry, configured, saved string) DefaultSelection {
	selection := DefaultSelection{Configured: configured, SavedEntry: saved, Effective: configured}
	value := configured
	if value == "saved" || value == "${saved_entry}" || value == "$saved_entry" {
		value = saved
		selection.Effective = saved
	}
	if strings.Contains(value, ">") {
		parts := strings.Split(value, ">")
		menuPath := make([]int, 0, len(parts))
		for _, part := range parts {
			position, err := strconv.Atoi(part)
			if err != nil || position < 0 {
				selection.Stale = true
				return selection
			}
			menuPath = append(menuPath, position)
		}
		for index := range entries {
			if slices.Equal(entries[index].MenuPath, menuPath) {
				selection.EntryIndex = &entries[index].Index
				selection.Effective = entries[index].Title
				return selection
			}
		}
		selection.Stale = true
		return selection
	}
	if position, err := strconv.Atoi(value); err == nil && position >= 0 {
		for index := range entries {
			if entries[index].Depth == 0 && len(entries[index].MenuPath) == 1 && entries[index].MenuPath[0] == position {
				selection.EntryIndex = &entries[index].Index
				selection.Effective = entries[index].Title
				return selection
			}
		}
		selection.Stale = true
		return selection
	}
	for index := range entries {
		if entries[index].Title == value || entries[index].ID == value {
			selection.EntryIndex = &entries[index].Index
			selection.Effective = value
			return selection
		}
	}
	selection.Stale = true
	return selection
}

// inspectLegacyHooks reports exact retired paths as attribution evidence only.
func inspectLegacyHooks(ctx context.Context, root string, report *Report) HookEvidence {
	paths := []struct {
		// id is the stable check identifier.
		id string
		// relative is the exact retired path beneath root.
		relative string
		// assign records safe regular-file presence.
		assign func(*HookEvidence)
	}{
		{id: "retired-dtb-helper", relative: "usr/local/sbin/sp11-grub-inject-dtb", assign: func(hooks *HookEvidence) { hooks.RetiredHelper = true }},
		{id: "retired-dtb-postinst-hook", relative: "etc/kernel/postinst.d/zzzz-surface-pro-11-dtb", assign: func(hooks *HookEvidence) { hooks.PostInstallHook = true }},
		{id: "retired-dtb-postrm-hook", relative: "etc/kernel/postrm.d/zzzz-surface-pro-11-dtb", assign: func(hooks *HookEvidence) { hooks.PostRemoveHook = true }},
	}
	hooks := HookEvidence{}
	for _, item := range paths {
		_, err := install.ReadRootFile(ctx, root, item.relative, "retired DTB hook", maximumBootSettingBytes)
		switch {
		case err == nil:
			item.assign(&hooks)
			report.add(Check{ID: item.id, State: StateWarn, Detail: "the retired legacy DTB path is present as attribution evidence; it was not executed"})
		case errors.Is(err, os.ErrNotExist):
			report.add(Check{ID: item.id, State: StateNeutral, Detail: "the retired legacy DTB path is absent"})
		default:
			report.add(Check{ID: item.id, State: StateWarn, Detail: "the retired legacy DTB path could not be inspected as a regular non-symlink file"})
		}
	}
	return hooks
}
