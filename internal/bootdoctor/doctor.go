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
		entry := inspectEntry(ctx, root, parsedEntry, relativeDTB, device)
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

	bindingByABI := make(map[string]install.DeviceTreeBootEvidence)
	seenBindingABI := make(map[string]bool)
	for _, entry := range report.Entries {
		abi := entry.ABI
		if _, valid := parseKernelRank(abi); !valid || seenBindingABI[abi] {
			continue
		}
		seenBindingABI[abi] = true
		required := report.abiRequired(abi)
		binding, bindingValid := report.abiDeviceTreeBinding(abi)
		if !bindingValid {
			state := StateWarn
			detail := "the ABI's normal and recovery entries do not share one verified embedded or external device-tree binding"
			if report.abiBindingInaccessibleOnly(abi) {
				detail = "some device-tree binding evidence is inaccessible; review the per-entry checks and re-run with sudo for complete evidence"
			} else if required {
				state = StateFail
			}
			report.add(Check{ID: "grub-abi-dtb-consistency", State: state, Required: required, ABI: abi,
				Detail: detail})
			continue
		}
		bindingByABI[abi] = binding
		report.add(Check{ID: "grub-abi-dtb-consistency", State: StatePass, Required: required, ABI: abi,
			Detail: fmt.Sprintf("%d normal or recovery entries share one verified %s device-tree binding", binding.GRUBEntryCount, binding.Mode)})
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
		if binding, found := bindingByABI[entry.ABI]; found {
			report.Attribution.DeviceTreeBoot = &binding
			report.Attribution.BootSHA256 = binding.SHA256
		} else {
			report.Attribution.BootSHA256 = entry.BootDTBSHA256
		}
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
		} else if entry.BootDTBSHA256 != "" && entry.InstalledDTBState != install.GRUBPathInaccessible {
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

// abiRequired reports whether any effective or explicitly selected entry uses
// abi, allowing one cross-entry consistency result to retain role severity.
func (report Report) abiRequired(abi string) bool {
	for _, entry := range report.Entries {
		if entry.ABI == abi && report.entryRequired(entry) {
			return true
		}
	}
	return false
}

// abiDeviceTreeBinding combines already verified per-entry evidence without
// rereading a potentially large embedded kernel image for the same ABI.
func (report Report) abiDeviceTreeBinding(abi string) (install.DeviceTreeBootEvidence, bool) {
	var combined install.DeviceTreeBootEvidence
	for _, entry := range report.Entries {
		if entry.ABI != abi {
			continue
		}
		if entry.DeviceTreeBoot == nil {
			return install.DeviceTreeBootEvidence{}, false
		}
		current := *entry.DeviceTreeBoot
		if combined.Mode == "" {
			combined.Mode = current.Mode
			combined.SHA256 = current.SHA256
		} else if combined.Mode != current.Mode || combined.SHA256 != current.SHA256 {
			return install.DeviceTreeBootEvidence{}, false
		}
		combined.GRUBEntryCount++
	}
	return combined, combined.Mode != "" && combined.GRUBEntryCount > 0
}

// abiBindingInaccessibleOnly reports that every unverified entry for one ABI
// is inconclusive solely because required files could not be read. Any
// definitive failure or inconsistent set of individually verified modes wins.
func (report Report) abiBindingInaccessibleOnly(abi string) bool {
	unverified := false
	for _, entry := range report.Entries {
		if entry.ABI != abi || entry.DeviceTreeBoot != nil {
			continue
		}
		unverified = true
		if entry.BootDTBState != install.GRUBPathInaccessible && entry.InstalledDTBState != install.GRUBPathInaccessible &&
			(len(entry.DeviceTrees) != 0 || entry.KernelState != install.GRUBPathInaccessible) {
			return false
		}
	}
	return unverified
}

// addEntryChecks classifies stale artefacts and absent or mismatched DTBs for
// one normal or recovery entry according to its required status.
func (report *Report) addEntryChecks(entry Entry, required bool) {
	state := StatePass
	detail := "the kernel and initramfs paths exist as regular files"
	unsafeArtefact := slicesContain(entry.UnsafeCommands, "linux") || slicesContain(entry.UnsafeCommands, "linuxefi") ||
		slicesContain(entry.UnsafeCommands, "initrd") || slicesContain(entry.UnsafeCommands, "initrdefi") ||
		entry.KernelState == "" || entry.InitramfsState == ""
	inaccessibleArtefact := entry.KernelState == install.GRUBPathInaccessible || entry.InitramfsState == install.GRUBPathInaccessible
	missingArtefact := entry.KernelState == install.GRUBPathMissing || entry.InitramfsState == install.GRUBPathMissing
	if unsafeArtefact || missingArtefact {
		state = StateWarn
		detail = "the GRUB entry is stale or has an unsafe kernel or initramfs path token"
		if required {
			state = StateFail
		}
	} else if inaccessibleArtefact {
		state = StateWarn
		detail = "a kernel or initramfs path is inaccessible; re-run with sudo to verify"
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
	} else if entry.DeviceTreeBoot != nil {
		dtbDetail = fmt.Sprintf("the %s boot DTB matches the installed same-ABI firmware DTB", entry.DeviceTreeBoot.Mode)
	} else if len(entry.DeviceTrees) == 0 && entry.KernelState == install.GRUBPathInaccessible {
		dtbState = StateWarn
		dtbDetail = "the kernel image is inaccessible; re-run with sudo to inspect an embedded device tree"
	} else if len(entry.DeviceTrees) == 0 {
		dtbState = StateWarn
		dtbDetail = "the GRUB entry has neither a verified embedded DTB nor an external devicetree directive"
		if required {
			dtbState = StateFail
		}
	} else if entry.BootDTBState == "" || entry.InstalledDTBState == "" {
		dtbState = StateWarn
		dtbDetail = "the GRUB entry has an unrecognised or unsafe devicetree path"
		if required {
			dtbState = StateFail
		}
	} else if entry.BootDTBState == install.GRUBPathMissing || entry.InstalledDTBState == install.GRUBPathMissing ||
		(entry.DTBMatches != nil && !*entry.DTBMatches) ||
		(entry.BootDTBState == install.GRUBPathPresent && entry.InstalledDTBState == install.GRUBPathPresent && entry.DTBMatches == nil) {
		dtbState = StateWarn
		dtbDetail = "the boot-side DTB is missing or does not match the installed same-ABI firmware DTB"
		if required {
			dtbState = StateFail
		}
	} else if entry.BootDTBState == install.GRUBPathInaccessible || entry.InstalledDTBState == install.GRUBPathInaccessible {
		dtbState = StateWarn
		dtbDetail = "a boot-side or installed DTB path is inaccessible; re-run with sudo to verify"
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
func inspectEntry(ctx context.Context, root string, parsed install.GRUBEntry, relativeDTB, device string) Entry {
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
	entry.KernelState = grubPathsAvailability(ctx, root, parsed.Linux)
	entry.InitramfsState = grubPathsAvailability(ctx, root, parsed.Initrd)
	entry.KernelExists = entry.KernelState == install.GRUBPathPresent
	entry.InitramfsExists = entry.InitramfsState == install.GRUBPathPresent
	if entry.ABI == "" || len(parsed.DeviceTrees) > 1 ||
		(len(parsed.DeviceTrees) == 1 && !entryTokenMatchesDevice(parsed.DeviceTrees[0].Path, parsed.Title, relativeDTB, entry.ABI)) {
		return entry
	}
	installed, installedErr := install.HashRootFile(ctx, root, "usr/lib/firmware/"+entry.ABI+"/device-tree/"+relativeDTB, "installed device-tree")
	entry.InstalledDTBState = rootFileAvailability(installedErr)
	if installedErr == nil {
		entry.InstalledDTBSHA256 = installed.SHA256
	}
	if len(parsed.DeviceTrees) == 1 {
		bootSide, bootState, bootErr := install.InspectGRUBPath(ctx, root, parsed.DeviceTrees[0].Path)
		entry.BootDTBState = bootState
		if bootErr == nil {
			entry.BootDTBSHA256 = bootSide.SHA256
		}
		if bootErr == nil && installedErr == nil {
			matches := bootSide.SHA256 == installed.SHA256
			entry.DTBMatches = &matches
		}
	}
	binding, bindingErr := install.InspectDeviceTreeBootBinding(ctx, root, entry.ABI, device, []install.GRUBEntry{parsed})
	if bindingErr == nil {
		entry.DeviceTreeBoot = &binding
		entry.BootDTBState = install.GRUBPathPresent
		entry.BootDTBSHA256 = binding.SHA256
		matches := installedErr == nil && binding.SHA256 == installed.SHA256
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

// entryTokenMatchesDevice prevents X1E and X1P canonical DTB paths from being
// compared, while accepting a safe ABI-stamped name only when its numeric
// kernel version agrees with the entry ABI. Digest equality then proves bytes.
func entryTokenMatchesDevice(token, title, relativeDTB, abi string) bool {
	base := strings.ToLower(filepath.Base(token))
	expected := strings.ToLower(filepath.Base(relativeDTB))
	if base == expected {
		return true
	}
	if _, valid := install.ABIStampedDTBIdentity(token); valid {
		return install.ABIStampedDTBMatchesABI(token, abi)
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

// grubPathsAvailability inspects every recognised token so an unsafe sibling
// cannot be hidden by present evidence. Without an unsafe token, any present
// path preserves the historical any-token-exists result; otherwise genuine
// missing evidence takes precedence over permission-inaccessible evidence.
func grubPathsAvailability(ctx context.Context, root string, tokens []install.GRUBPathToken) install.GRUBPathAvailability {
	states := make([]install.GRUBPathAvailability, 0, len(tokens))
	for _, token := range tokens {
		_, state, _ := install.InspectGRUBPath(ctx, root, token.Path)
		states = append(states, state)
	}
	return aggregateGRUBPathAvailability(states)
}

// aggregateGRUBPathAvailability applies the fail-closed multi-token ordering
// after every path has been inspected.
func aggregateGRUBPathAvailability(states []install.GRUBPathAvailability) install.GRUBPathAvailability {
	unsafe := false
	present := false
	missing := len(states) == 0
	inaccessible := false
	for _, state := range states {
		switch state {
		case install.GRUBPathPresent:
			present = true
		case install.GRUBPathMissing:
			missing = true
		case install.GRUBPathInaccessible:
			inaccessible = true
		default:
			unsafe = true
		}
	}
	if unsafe {
		return ""
	}
	if present {
		return install.GRUBPathPresent
	}
	if missing {
		return install.GRUBPathMissing
	}
	if inaccessible {
		return install.GRUBPathInaccessible
	}
	return install.GRUBPathMissing
}

// rootFileAvailability applies the same bounded classification to a compiled
// root-relative evidence path.
func rootFileAvailability(err error) install.GRUBPathAvailability {
	switch {
	case err == nil:
		return install.GRUBPathPresent
	case errors.Is(err, os.ErrPermission):
		return install.GRUBPathInaccessible
	case errors.Is(err, os.ErrNotExist):
		return install.GRUBPathMissing
	default:
		return ""
	}
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
