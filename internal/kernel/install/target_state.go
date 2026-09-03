package install

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

const (
	// maximumDPkgStatusBytes bounds the package database read during target
	// classification so a corrupt or hostile status file cannot exhaust memory.
	maximumDPkgStatusBytes int64 = 32 << 20
	// maximumDPkgStatusLineBytes bounds one line of the package database.
	maximumDPkgStatusLineBytes = 64 << 10
	// dpkgInstalledStatus is the only package state that counts as installed.
	dpkgInstalledStatus = "install ok installed"
	// dpkgInstallWant is the want word used by pending transactions.
	dpkgInstallWant = "install"
)

// TargetStateClassification is the explicit blocker class for a target ABI.
type TargetStateClassification string

const (
	// TargetStateAbsent reports a fresh target with no artefacts or records.
	TargetStateAbsent TargetStateClassification = "absent-and-eligible"
	// TargetStateComplete reports a fully installed, bootable target ABI.
	TargetStateComplete TargetStateClassification = "already-installed-complete"
	// TargetStatePartial reports leftover, half-configured, or inconsistent
	// target evidence that must be resolved before a fresh installation.
	TargetStatePartial TargetStateClassification = "partial-or-inconsistent"
)

// ArtefactFinding records the presence of one expected target-ABI artefact.
type ArtefactFinding struct {
	// Category names the closed evidence role: boot-file, module-tree,
	// firmware, device-tree, or headers.
	Category string `json:"category"`
	// Path is the absolute path beneath the selected target root.
	Path string `json:"path"`
	// Present reports whether the artefact currently exists.
	Present bool `json:"present"`
}

// PackageStateFinding records one package database record relevant to the
// target ABI without interpreting anything beyond its status tuple.
type PackageStateFinding struct {
	// Package is the exact Debian package name in the database.
	Package string `json:"package"`
	// Status is the verbatim three-word dpkg status tuple.
	Status string `json:"status"`
	// Installed reports the tuple "install ok installed".
	Installed bool `json:"installed"`
	// HalfConfigured reports a pending transaction state such as
	// "install ok unpacked" or "install ok half-configured".
	HalfConfigured bool `json:"half_configured"`
}

// TargetStateEvidence is the complete bounded read-only classification of one
// target ABI. Detection performs no mutation and executes no commands.
type TargetStateEvidence struct {
	// ABI is the exact target kernel ABI that was classified.
	ABI string `json:"abi"`
	// Classification is the explicit blocker class for the fresh-target gate.
	Classification TargetStateClassification `json:"classification"`
	// BootFiles records the kernel image, initramfs, symbol map, and config.
	BootFiles []ArtefactFinding `json:"boot_files"`
	// ModuleTrees records each canonical module tree candidate.
	ModuleTrees []ArtefactFinding `json:"module_trees"`
	// Firmware records the per-ABI firmware directory.
	Firmware []ArtefactFinding `json:"firmware"`
	// FirmwareDeviceTrees records each required device-tree blob.
	FirmwareDeviceTrees []ArtefactFinding `json:"firmware_device_trees"`
	// Headers records both development-header source trees.
	Headers []ArtefactFinding `json:"headers"`
	// GRUBEntries counts ABI-labelled non-recovery GRUB entries, matching the
	// post-install verification contract of exactly one such entry.
	GRUBEntries int `json:"grub_entries"`
	// GRUBDeviceTreeBindingProblem records why the target GRUB entry failed
	// the same device-tree binding verification applied after installation.
	GRUBDeviceTreeBindingProblem string `json:"grub_device_tree_binding_problem,omitempty"`
	// Packages records every target-relevant package database record.
	Packages []PackageStateFinding `json:"packages"`
	// PackageDatabasePresent reports that var/lib/dpkg/status was readable.
	PackageDatabasePresent bool `json:"package_database_present"`
	// Reasons explains every observation that blocked a fresh installation.
	Reasons []string `json:"reasons,omitempty"`
	// Recommendations lists safe next steps scoped to the target ABI only.
	Recommendations []string `json:"recommendations,omitempty"`
}

// TargetStateError blocks the fresh-target gate while carrying the complete
// read-only evidence so receipts can expose structured diagnostics.
type TargetStateError struct {
	// Evidence is the complete target classification behind the blocker.
	Evidence TargetStateEvidence `json:"target_state"`
}

// Error renders the classification with bounded reasons and next steps.
func (e *TargetStateError) Error() string {
	lines := append([]string(nil), e.Evidence.reasons()...)
	builder := &strings.Builder{}
	fmt.Fprintf(builder, "target ABI %s is %s: a fresh installation is blocked", e.Evidence.ABI, e.Evidence.Classification)
	for _, reason := range lines {
		builder.WriteString("\n  ")
		builder.WriteString(reason)
	}
	builder.WriteString("\nsafe next steps (nothing is changed automatically):\n")
	for _, recommendation := range e.Evidence.recommendations() {
		builder.WriteString("  - ")
		builder.WriteString(recommendation)
		builder.WriteString("\n")
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

// reasons returns the bounded human-readable blocker explanations.
func (e *TargetStateEvidence) reasons() []string {
	if len(e.Reasons) != 0 {
		return e.Reasons
	}
	return []string{fmt.Sprintf("classification %s", e.Classification)}
}

// recommendations returns the safe next steps scoped to the target ABI.
func (e *TargetStateEvidence) recommendations() []string {
	if len(e.Recommendations) != 0 {
		return e.Recommendations
	}
	return []string{"review the recorded evidence, then retry once the target root is fresh"}
}

// DiagnosticLines renders one bounded line per evidence category for stderr.
func (e *TargetStateEvidence) DiagnosticLines() []string {
	lines := make([]string, 0, 16)
	appendFindings := func(findings []ArtefactFinding) {
		for _, finding := range findings {
			state := "missing"
			if finding.Present {
				state = "present"
			}
			lines = append(lines, fmt.Sprintf("%s %s: %s", finding.Category, state, finding.Path))
		}
	}
	appendFindings(e.BootFiles)
	appendFindings(e.ModuleTrees)
	appendFindings(e.Firmware)
	appendFindings(e.FirmwareDeviceTrees)
	appendFindings(e.Headers)
	lines = append(lines, fmt.Sprintf("grub entries for target ABI: %d", e.GRUBEntries))
	if !e.PackageDatabasePresent {
		lines = append(lines, "package database var/lib/dpkg/status: absent")
	}
	for _, item := range e.Packages {
		lines = append(lines, fmt.Sprintf("package %s: %s", item.Package, item.Status))
	}
	return append(append(lines, e.reasons()...), e.recommendations()...)
}

// targetStatePaths bundles the closed path set inspected during classification.
type targetStatePaths struct {
	// boot files use the canonical /boot naming for the ABI.
	boot []string
	// module trees are the usr-merged and legacy candidates.
	moduleTrees []string
	// firmware is the per-ABI firmware directory.
	firmware string
	// deviceTrees are the required DTB paths.
	deviceTrees []string
	// headers are the flavour and common header source trees.
	headers []string
}

// classifyTargetState collects every target-ABI artefact and package record
// with read-only filesystem access and returns the explicit classification.
func classifyTargetState(ctx context.Context, root, abi string, packages []Package) (TargetStateEvidence, error) {
	paths, err := targetStatePathSet(root, abi)
	if err != nil {
		return TargetStateEvidence{}, err
	}
	evidence := TargetStateEvidence{ABI: abi}
	if evidence.BootFiles, err = artefactFindings(root, "boot-file", paths.boot); err != nil {
		return TargetStateEvidence{}, err
	}
	if evidence.ModuleTrees, err = artefactFindings(root, "module-tree", paths.moduleTrees); err != nil {
		return TargetStateEvidence{}, err
	}
	if evidence.Firmware, err = artefactFindings(root, "firmware", []string{paths.firmware}); err != nil {
		return TargetStateEvidence{}, err
	}
	if evidence.FirmwareDeviceTrees, err = artefactFindings(root, "device-tree", paths.deviceTrees); err != nil {
		return TargetStateEvidence{}, err
	}
	if evidence.Headers, err = artefactFindings(root, "headers", paths.headers); err != nil {
		return TargetStateEvidence{}, err
	}
	grub, err := rootPath(root, "boot/grub/grub.cfg")
	if err != nil {
		return TargetStateEvidence{}, err
	}
	if err := validateTargetRoute(root, grub, false); err != nil {
		return TargetStateEvidence{}, err
	}
	entries, err := countGRUBEntries(ctx, grub, abi, false, false)
	if err != nil {
		return TargetStateEvidence{}, err
	}
	evidence.GRUBEntries = entries
	databasePresent, packageFindings, err := readTargetPackageStates(root, abi)
	if err != nil {
		return TargetStateEvidence{}, err
	}
	evidence.PackageDatabasePresent = databasePresent
	evidence.Packages = packageFindings
	classifyTargetOutcome(ctx, root, abi, packages, &evidence)
	return evidence, nil
}

// targetStatePathSet resolves and route-validates every closed inspection path.
func targetStatePathSet(root, abi string) (targetStatePaths, error) {
	paths := targetStatePaths{
		boot: []string{
			"boot/vmlinuz-" + abi,
			"boot/initrd.img-" + abi,
			"boot/System.map-" + abi,
			"boot/config-" + abi,
		},
		firmware: "usr/lib/firmware/" + abi,
		headers: []string{
			"usr/src/linux-headers-" + abi,
			"usr/src/linux-qcom-x1e-headers-" + strings.TrimSuffix(abi, "-qcom-x1e"),
		},
	}
	candidates, err := moduleTreeCandidates(root, abi)
	if err != nil {
		return targetStatePaths{}, err
	}
	paths.moduleTrees = candidates
	for _, tree := range requiredDeviceTrees {
		target, err := rootPath(root, "usr/lib/firmware/"+abi+"/device-tree/"+tree.Path)
		if err != nil {
			return targetStatePaths{}, err
		}
		paths.deviceTrees = append(paths.deviceTrees, target)
	}
	resolved := targetStatePaths{}
	resolve := func(relative string) (string, error) {
		target, err := rootPath(root, relative)
		if err != nil {
			return "", err
		}
		if err := validateTargetRoute(root, target, true); err != nil {
			return "", err
		}
		return target, nil
	}
	for _, relative := range paths.boot {
		target, err := resolve(relative)
		if err != nil {
			return targetStatePaths{}, err
		}
		resolved.boot = append(resolved.boot, target)
	}
	for _, candidate := range paths.moduleTrees {
		if err := validateTargetRoute(root, candidate, true); err != nil {
			return targetStatePaths{}, err
		}
		resolved.moduleTrees = append(resolved.moduleTrees, candidate)
	}
	firmware, err := resolve(paths.firmware)
	if err != nil {
		return targetStatePaths{}, err
	}
	resolved.firmware = firmware
	for _, target := range paths.deviceTrees {
		if err := validateTargetRoute(root, target, true); err != nil {
			return targetStatePaths{}, err
		}
		resolved.deviceTrees = append(resolved.deviceTrees, target)
	}
	for _, relative := range paths.headers {
		target, err := resolve(relative)
		if err != nil {
			return targetStatePaths{}, err
		}
		resolved.headers = append(resolved.headers, target)
	}
	return resolved, nil
}

// artefactFindings records presence for each resolved path without mutating.
func artefactFindings(root, category string, paths []string) ([]ArtefactFinding, error) {
	findings := make([]ArtefactFinding, 0, len(paths))
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			findings = append(findings, ArtefactFinding{Category: category, Path: path, Present: true})
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("inspect target %s %s: %w", category, path, err)
		}
		findings = append(findings, ArtefactFinding{Category: category, Path: path})
	}
	return findings, nil
}

// classifyTargetOutcome assigns the explicit classification, reasons, and
// recommendations from the collected evidence without touching the filesystem.
func classifyTargetOutcome(ctx context.Context, root, abi string, packages []Package, evidence *TargetStateEvidence) {
	anyArtefact := evidence.GRUBEntries != 0 || evidence.PackageDatabasePresent && len(evidence.Packages) != 0
	for _, findings := range [][]ArtefactFinding{evidence.BootFiles, evidence.ModuleTrees, evidence.Firmware, evidence.FirmwareDeviceTrees, evidence.Headers} {
		for _, finding := range findings {
			anyArtefact = anyArtefact || finding.Present
		}
	}
	if !anyArtefact {
		evidence.Classification = TargetStateAbsent
		return
	}
	if evidence.isComplete(ctx, root, abi, packages) {
		evidence.Classification = TargetStateComplete
		evidence.Recommendations = []string{
			fmt.Sprintf("target ABI %s is already fully installed; remove only the exact target-ABI packages with the package manager before reinstalling", abi),
			"or rerun with --overwrite to replace the existing installation after reviewing its risk",
			"never remove or overwrite the running or fallback ABI; only target-ABI packages are touched by this tool",
		}
		return
	}
	evidence.Classification = TargetStatePartial
	evidence.Reasons = evidence.partialReasons()
	evidence.Recommendations = evidence.partialRecommendations()
}

// isComplete reports that the target has a fully bootable, fully installed
// baseline equivalent to the post-install verification evidence. Every
// selected package must have an installed record and every required device
// tree must be a non-empty regular file.
func (e *TargetStateEvidence) isComplete(ctx context.Context, root, abi string, packages []Package) bool {
	if _, err := verifyBootFiles(ctx, root, abi); err != nil {
		return false
	}
	if len(packages) != 0 {
		if !e.PackageDatabasePresent || !e.selectedPackagesInstalled(packages) {
			return false
		}
	}
	for _, finding := range e.FirmwareDeviceTrees {
		if !finding.Present {
			return false
		}
	}
	if err := verifyDeviceTreeEvidence(e.FirmwareDeviceTrees); err != nil {
		return false
	}
	if e.GRUBEntries != 1 {
		return false
	}
	if problem := e.grubDeviceTreeBindingProblem(ctx, root, abi); problem != "" {
		e.GRUBDeviceTreeBindingProblem = problem
		return false
	}
	if e.hasHeaderRoles(packages) {
		if _, err := verifyInstalledHeaders(ctx, root, abi, packages); err != nil {
			return false
		}
	}
	return true
}

// selectedPackagesInstalled reports that every package selected from the
// reviewed bundle has an installed database record.
func (e *TargetStateEvidence) selectedPackagesInstalled(packages []Package) bool {
	records := make(map[string]PackageStateFinding, len(e.Packages))
	for _, item := range e.Packages {
		records[item.Package] = item
	}
	for _, item := range packages {
		record, found := records[item.DebianPackage]
		if !found || !record.Installed {
			return false
		}
	}
	return true
}

// verifyDeviceTreeEvidence requires each recorded device tree to be a
// non-empty regular file, matching post-install verification strength.
func verifyDeviceTreeEvidence(findings []ArtefactFinding) error {
	for _, finding := range findings {
		if !finding.Present {
			continue
		}
		info, err := os.Lstat(finding.Path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("device tree must be a non-empty regular file: %s", finding.Path)
		}
	}
	return nil
}

// grubDeviceTreeBindingProblem re-runs the GRUB device-tree binding
// verification read-only against the same non-recovery entry set that
// classification counted (identified by target kernel and initramfs basenames,
// independent of title) and returns a bounded explanation when the entry has
// no, ambiguous, or non-matching device-tree binding. An empty result means
// the binding is sound.
func (e *TargetStateEvidence) grubDeviceTreeBindingProblem(ctx context.Context, root, abi string) string {
	grub, err := rootPath(root, "boot/grub/grub.cfg")
	if err != nil {
		return "GRUB device-tree bindings could not be inspected: " + err.Error()
	}
	parsedEntries, err := parseGRUBEntries(ctx, grub)
	if err != nil {
		return "GRUB device-tree bindings could not be inspected: " + err.Error()
	}
	targetEntries := make([]GRUBEntry, 0, 1)
	for _, entry := range parsedEntries {
		if entry.Recovery || !entryHasArtefact(entry.Linux, "vmlinuz-"+abi) ||
			!entryHasArtefact(entry.Initrd, "initrd.img-"+abi) {
			continue
		}
		targetEntries = append(targetEntries, entry)
	}
	if len(targetEntries) != 1 {
		return fmt.Sprintf("expected exactly one bootable GRUB entry for the target ABI, found %d", len(targetEntries))
	}
	// Post-install verification requires the ABI-labelled title, so an
	// unlabelled entry could never pass that contract either; fail closed
	// here rather than let the shared verifier skip it silently.
	if !strings.Contains(targetEntries[0].Title, abi) {
		return "matching GRUB entry does not name the target ABI in its title"
	}
	if slicesContainString(targetEntries[0].UnsafeCommands, "devicetree") {
		return "matching GRUB entry has an unsafe device-tree path"
	}
	if len(targetEntries[0].DeviceTrees) != 1 {
		return fmt.Sprintf("matching GRUB entry has %d device-tree directives, want exactly one", len(targetEntries[0].DeviceTrees))
	}
	if err := verifyGRUBDeviceTreeBindings(ctx, root, abi, targetEntries, nil); err != nil {
		return "GRUB device-tree binding verification failed: " + err.Error()
	}
	return ""
}

// hasHeaderRoles reports whether the reviewed bundle selected header packages.
func (e *TargetStateEvidence) hasHeaderRoles(packages []Package) bool {
	for _, item := range packages {
		if item.Role == kernel.RoleHeaders || item.Role == kernel.RoleCommonHeaders {
			return true
		}
	}
	return false
}

// partialReasons explains every observation that blocks a fresh installation.
func (e *TargetStateEvidence) partialReasons() []string {
	reasons := make([]string, 0, 8)
	if !e.bootComplete() {
		reasons = append(reasons, fmt.Sprintf("target ABI %s has an incomplete boot baseline", e.ABI))
	}
	if e.GRUBEntries != 0 {
		reasons = append(reasons, fmt.Sprintf("target ABI %s already has %d GRUB entries", e.ABI, e.GRUBEntries))
	}
	if e.GRUBDeviceTreeBindingProblem != "" {
		reasons = append(reasons, fmt.Sprintf("target ABI %s has a GRUB device-tree binding problem: %s", e.ABI, e.GRUBDeviceTreeBindingProblem))
	}
	for _, item := range e.Packages {
		if item.HalfConfigured {
			reasons = append(reasons, fmt.Sprintf("package %s is half-configured: %s", item.Package, item.Status))
		} else if !item.Installed {
			reasons = append(reasons, fmt.Sprintf("package %s is recorded but not installed: %s", item.Package, item.Status))
		}
	}
	if e.PackageDatabasePresent && len(e.Packages) == 0 {
		reasons = append(reasons, "target artefacts exist without any matching package records")
	}
	for _, findings := range [][]ArtefactFinding{e.BootFiles, e.ModuleTrees, e.Firmware, e.FirmwareDeviceTrees, e.Headers} {
		for _, finding := range findings {
			if finding.Present {
				reasons = append(reasons, fmt.Sprintf("leftover %s already has an installed artefact: %s", finding.Category, finding.Path))
			}
		}
	}
	return reasons
}

// bootComplete reports whether every boot file and module tree candidate exists.
func (e *TargetStateEvidence) bootComplete() bool {
	complete := true
	for _, finding := range e.BootFiles {
		complete = complete && finding.Present
	}
	for _, finding := range e.ModuleTrees {
		complete = complete && finding.Present
	}
	return complete
}

// partialRecommendations lists safe next steps scoped to the target ABI only.
func (e *TargetStateEvidence) partialRecommendations() []string {
	recommendations := make([]string, 0, 4)
	halfConfigured := make([]string, 0, 2)
	for _, item := range e.Packages {
		if item.HalfConfigured {
			halfConfigured = append(halfConfigured, item.Package)
		}
	}
	if len(halfConfigured) != 0 {
		recommendations = append(recommendations, fmt.Sprintf("repair the interrupted package transaction, for example with dpkg --configure for: %s", strings.Join(halfConfigured, " ")))
	}
	if e.PackageDatabasePresent && len(e.Packages) != 0 {
		names := make([]string, 0, len(e.Packages))
		for _, item := range e.Packages {
			names = append(names, item.Package)
		}
		recommendations = append(recommendations, fmt.Sprintf("if the partial installation is unwanted, purge only the exact target-ABI packages: %s", strings.Join(names, " ")))
	}
	recommendations = append(recommendations,
		"or rerun with --overwrite to replace the partial installation after reviewing its risk",
		"never remove or overwrite the running or fallback ABI; only target-ABI packages are proposed here",
	)
	return recommendations
}

// overwriteTargetWarning is the explicit risk warning emitted whenever a
// caller overrides the fresh-target gate with --overwrite.
func overwriteTargetWarning(classification TargetStateClassification) string {
	return fmt.Sprintf("warning: --overwrite replaces an existing %s target installation; an unsafe overwrite could break the system or prevent returning to the desktop on reboot", classification)
}

// readTargetPackageStates parses the bounded target package database without
// executing any package-manager command.
func readTargetPackageStates(root, abi string) (bool, []PackageStateFinding, error) {
	statusPath, err := rootPath(root, "var/lib/dpkg/status")
	if err != nil {
		return false, nil, err
	}
	if err := validateTargetRoute(root, statusPath, true); err != nil {
		return false, nil, err
	}
	info, err := os.Lstat(statusPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("inspect package database %s: %w", statusPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, nil, fmt.Errorf("package database must be a non-symlink regular file: %s", statusPath)
	}
	if info.Size() > maximumDPkgStatusBytes {
		return false, nil, fmt.Errorf("package database exceeds %d bytes: %s", maximumDPkgStatusBytes, statusPath)
	}
	file, openedInfo, err := openUnchangedRegular(statusPath, info)
	if err != nil {
		return false, nil, fmt.Errorf("read package database %s: %w", statusPath, err)
	}
	defer file.Close()
	if openedInfo.Size() > maximumDPkgStatusBytes {
		return false, nil, fmt.Errorf("package database exceeds %d bytes: %s", maximumDPkgStatusBytes, statusPath)
	}
	scanner := bufio.NewScanner(io.LimitReader(file, maximumDPkgStatusBytes))
	scanner.Buffer(make([]byte, 0, maximumDPkgStatusLineBytes), maximumDPkgStatusLineBytes)
	findings := make([]PackageStateFinding, 0, 4)
	currentPackage := ""
	currentStatus := ""
	flush := func() {
		if currentPackage == "" {
			return
		}
		if !packageRelevantToTarget(currentPackage, abi) {
			currentPackage, currentStatus = "", ""
			return
		}
		findings = append(findings, PackageStateFinding{
			Package:        currentPackage,
			Status:         currentStatus,
			Installed:      currentStatus == dpkgInstalledStatus,
			HalfConfigured: strings.HasPrefix(currentStatus, dpkgInstallWant+" ok ") && currentStatus != dpkgInstalledStatus,
		})
		currentPackage, currentStatus = "", ""
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if value, found := strings.CutPrefix(line, "Package: "); found && currentPackage == "" {
			currentPackage = strings.TrimSpace(value)
		}
		if value, found := strings.CutPrefix(line, "Status: "); found {
			currentStatus = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return false, nil, fmt.Errorf("parse package database %s: %w", statusPath, err)
	}
	flush()
	entry, statErr := os.Lstat(statusPath)
	if statErr != nil || entry.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, entry) {
		return false, nil, fmt.Errorf("package database changed while it was read: %s", statusPath)
	}
	return true, findings, nil
}

// packageRelevantToTarget matches the exact closed package-name set for one ABI.
func packageRelevantToTarget(name, abi string) bool {
	switch name {
	case "linux-image-" + abi, "linux-modules-" + abi, "linux-headers-" + abi,
		"linux-qcom-x1e-headers-" + strings.TrimSuffix(abi, "-qcom-x1e"):
		return true
	}
	return strings.HasSuffix(name, "-"+abi)
}
