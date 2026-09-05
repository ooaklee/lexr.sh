package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// WiFi derives the qualified SP11 board fallback from the target's distribution
// firmware. It never reads an installed neighbour root or downloads executable
// helpers. Activation is explicit and limited to the running Surface's radio.
func (installer *Installer) WiFi(ctx context.Context, options Options) (Result, error) {
	if options.BundleDir != "" || options.RepositoryRoot != "" || options.CameraAuthoritySHA256 != "" {
		return Result{}, errors.New("Wi-Fi uses the selected root's distribution firmware; release and camera inputs do not apply")
	}
	root := options.Root
	if root == "" {
		root = "/"
	}
	root, err := filepath.Abs(root)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		return Result{}, fmt.Errorf("resolve Wi-Fi target root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return Result{}, errors.New("Wi-Fi target root must be a directory")
	}
	result := Result{Component: WiFiComponent, Root: root, DryRun: options.DryRun}
	if options.Activate {
		result.Commands, err = installer.planWiFiActivation(root)
		if err != nil {
			return result, err
		}
		result.ActivationRequired = true
	}
	database, source, err := installer.readWiFiDatabase(ctx, root)
	if err != nil {
		return result, err
	}
	selection, err := selectWiFiBoard(database)
	if err != nil {
		return result, err
	}
	if selection.name == wifiSurfaceBoard {
		// The kernel prefers a matching API-2 entry; no fallback is needed.
		result.Files = []FileChange{{Source: source, Target: source, Action: "retain-native-board"}}
		if options.DryRun {
			return result, nil
		}
		result.FilesInstalled = true
		result.RebootRequired = !options.Activate
		return installer.finishWiFiActivation(ctx, result)
	}
	target, err := resolveTarget(root, wifiFirmwareDirectory+"board.bin")
	if err != nil {
		return result, err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(selection.data))
	plan := audioChangePlan{
		change:       FileChange{Source: source + "#" + selection.name, Target: target, Action: "create"},
		sourceDigest: digest, sourceSize: int64(len(selection.data)), mode: 0o644,
	}
	if existing, statErr := os.Lstat(target); statErr == nil {
		if !existing.Mode().IsRegular() {
			return result, errors.New("refusing non-regular Wi-Fi board target")
		}
		plan.originalHash, existing, err = hashRegularNoFollowBounded(target, maximumWiFiBoardBytes)
		if err != nil {
			return result, err
		}
		plan.originalMode, plan.originalSize = existing.Mode().Perm(), existing.Size()
		plan.change.Replaced, plan.change.Action = true, "replace"
		if plan.originalHash == digest {
			plan.change.Action = "retain"
			result.Files = []FileChange{plan.change}
			if options.DryRun {
				return result, nil
			}
			result.FilesInstalled = true
			result.RebootRequired = !options.Activate
			return installer.finishWiFiActivation(ctx, result)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return result, statErr
	}
	stamp := installer.now().UTC().Format("20060102T150405.000000000Z")
	backupRelative := "var/lib/lexr/backups/userspace/" + stamp + "/wifi/"
	receipt, err := resolveTarget(root, backupRelative+"receipt.json")
	if err != nil {
		return result, err
	}
	result.Receipt, result.BackupDirectory = receipt, filepath.Dir(receipt)
	if plan.change.Replaced {
		plan.change.Backup = filepath.Join(result.BackupDirectory, "board.bin")
	}
	result.Files = []FileChange{plan.change}
	result.RebootRequired = !options.Activate
	if options.DryRun {
		return result, nil
	}
	if err := installer.requireRoot(false); err != nil {
		return result, err
	}
	stage, err := createPrivateInstallStaging("lexr-wifi-install-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(stage)
	staged := filepath.Join(stage, "board.bin")
	if err := os.WriteFile(staged, selection.data, 0o600); err != nil {
		return result, err
	}
	if checked, err := resolveTarget(root, backupRelative+"receipt.json"); err != nil || checked != receipt {
		return result, errors.New("Wi-Fi receipt path changed after planning")
	}
	if err := os.MkdirAll(filepath.Dir(result.BackupDirectory), 0o700); err != nil {
		return result, err
	}
	if err := os.Mkdir(result.BackupDirectory, 0o700); err != nil {
		return result, err
	}
	if plan.change.Replaced {
		if err := atomicCopyVerified(target, plan.change.Backup, plan.originalMode, plan.originalHash, plan.originalSize); err != nil {
			return result, err
		}
	}
	writeReceipt := func() error {
		// A prepared receipt is a write-ahead checkpoint. If interrupted, its
		// recorded digests and backup permit inspection even when publication
		// completed before the next checkpoint could be recorded.
		phase := "prepared"
		if result.FilesInstalled {
			phase = "files-installed"
		}
		if result.ActivationComplete {
			phase = "activation-complete"
		}
		if result.ActivationError != "" {
			phase = "activation-incomplete"
		}
		data, err := json.MarshalIndent(struct {
			SchemaVersion  int    `json:"schema_version"`
			Phase          string `json:"phase"`
			SourceSHA256   string `json:"source_sha256"`
			BoardSHA256    string `json:"board_sha256"`
			PreviousSHA256 string `json:"previous_sha256,omitempty"`
			BoardName      string `json:"board_name"`
			Result         Result `json:"result"`
		}{1, phase, fmt.Sprintf("%x", sha256.Sum256(database)), digest, plan.originalHash, selection.name, result}, "", "  ")
		if err != nil {
			return err
		}
		stagedReceipt := filepath.Join(stage, "receipt.json")
		if err := os.WriteFile(stagedReceipt, data, 0o600); err != nil {
			return err
		}
		return atomicCopyVerified(stagedReceipt, receipt, 0o600, fmt.Sprintf("%x", sha256.Sum256(data)), int64(len(data)))
	}
	if err := writeReceipt(); err != nil {
		return result, err
	}
	if checked, err := resolveTarget(root, wifiFirmwareDirectory+"board.bin"); err != nil || checked != target {
		return result, errors.New("Wi-Fi target path changed after planning")
	}
	if plan.change.Replaced {
		current, currentInfo, err := hashRegularNoFollowBounded(target, maximumWiFiBoardBytes)
		if err != nil || current != plan.originalHash || currentInfo.Mode().Perm() != plan.originalMode || currentInfo.Size() != plan.originalSize {
			return result, errors.New("Wi-Fi target changed after backup")
		}
	} else if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return result, errors.New("Wi-Fi target appeared after planning")
	}
	if err := atomicCopyVerified(staged, target, 0o644, digest, plan.sourceSize); err != nil {
		if current, _, readErr := hashRegularNoFollowBounded(target, maximumWiFiBoardBytes); readErr == nil && current == digest {
			rollbackErr := rollbackAudio([]audioChangePlan{plan})
			result.FilesInstalled = rollbackErr != nil
			return result, errors.Join(err, rollbackErr)
		}
		return result, err
	}
	result.FilesInstalled = true
	if err := writeReceipt(); err != nil {
		rollbackErr := rollbackAudio([]audioChangePlan{plan})
		result.FilesInstalled = rollbackErr != nil
		return result, errors.Join(err, rollbackErr)
	}
	result, err = installer.finishWiFiActivation(ctx, result)
	return result, errors.Join(err, writeReceipt())
}

// planSurfaceWiFiActivation confines an explicit radio restart to the running
// Surface Pro 11 X1E/OLED and a recognised driver layout, without an ABI gate.
// It cannot activate an alternate filesystem root.
func planSurfaceWiFiActivation(root string) ([]Command, error) {
	if runtime.GOOS != "linux" || root != "/" {
		return nil, errors.New("Wi-Fi activation requires the running Linux root; omit --activate for a mounted target")
	}
	return planWiFiActivationFromSysfs("/sys")
}

// planWiFiActivationFromSysfs inspects the hardware and loaded module state.
// Its filesystem argument permits fixture tests without probing the test host.
func planWiFiActivationFromSysfs(sysRoot string) ([]Command, error) {
	compatible, err := os.ReadFile(filepath.Join(sysRoot, "firmware/devicetree/base/compatible"))
	if err != nil || !isWiFiSurfaceX1EOLED(compatible) {
		return nil, errors.New("Wi-Fi activation requires Surface Pro 11 X1E/OLED")
	}
	devices, err := filepath.Glob(filepath.Join(sysRoot, "bus/pci/devices/*"))
	if err != nil {
		return nil, err
	}
	matches := 0
	driverName := ""
	radioPath := ""
	for _, device := range devices {
		vendor, _ := os.ReadFile(filepath.Join(device, "vendor"))
		product, _ := os.ReadFile(filepath.Join(device, "device"))
		if strings.TrimSpace(string(vendor)) != "0x17cb" || strings.TrimSpace(string(product)) != "0x1107" {
			continue
		}
		driver, err := os.Readlink(filepath.Join(device, "driver"))
		if err != nil {
			return nil, errors.New("Wi-Fi activation requires a bound ath12k PCI driver")
		}
		driverName = filepath.Base(driver)
		radioPath = device
		matches++
	}
	if matches != 1 {
		return nil, errors.New("Wi-Fi activation requires exactly one built-in WCN7850")
	}
	module := ""
	switch driverName {
	case "ath12k_pci":
		module = "ath12k"
	case "ath12k_wifi7_pci":
		// Physical SP11 testing found an MHI/QMI kernel Oops on reload after
		// missing board data. A loaded module does not prove safe teardown.
		// Keep this driver-layout guard until reload is separately qualified.
		return nil, errors.New("live Wi-Fi restart is not qualified for the split ath12k driver: reloading it has crashed the kernel; omit --activate, install the board data before boot, and reboot the installed system or use a rebuilt live ISO")
	default:
		return nil, errors.New("Wi-Fi activation requires a recognised ath12k PCI driver layout")
	}
	state, err := os.ReadFile(filepath.Join(sysRoot, "module", module, "initstate"))
	if err != nil || strings.TrimSpace(string(state)) != "live" {
		return nil, fmt.Errorf("Wi-Fi activation requires the loaded %s module; built-in or unavailable drivers cannot be reloaded, so omit --activate and reboot", module)
	}
	referenceData, err := os.ReadFile(filepath.Join(sysRoot, "module", module, "refcnt"))
	references, parseErr := strconv.Atoi(strings.TrimSpace(string(referenceData)))
	if err != nil || parseErr != nil || references < 0 {
		return nil, errors.New("Wi-Fi module state is unavailable or teardown is incomplete; do not retry activation, reboot with the board data installed")
	}
	phys, err := os.ReadDir(filepath.Join(radioPath, "ieee80211"))
	if err != nil || len(phys) == 0 {
		return nil, errors.New("Wi-Fi initialisation has not produced a radio; do not reload a failed probe, install board data without --activate and boot with it already present")
	}
	for _, device := range devices {
		driver, err := os.Readlink(filepath.Join(device, "driver"))
		if err != nil || filepath.Base(driver) != driverName {
			continue
		}
		vendor, _ := os.ReadFile(filepath.Join(device, "vendor"))
		product, _ := os.ReadFile(filepath.Join(device, "device"))
		if strings.TrimSpace(string(vendor)) != "0x17cb" || strings.TrimSpace(string(product)) != "0x1107" {
			return nil, errors.New("Wi-Fi activation would also restart another PCI device using this driver; omit --activate")
		}
	}
	return []Command{
		{Name: "/usr/sbin/modprobe", Args: []string{"-r", module}},
		{Name: "/usr/sbin/modprobe", Args: []string{module}},
	}, nil
}

// isWiFiSurfaceX1EOLED matches a complete token anywhere in a terminated DT
// compatible list. The shared Denali token alone cannot qualify the X1P model.
func isWiFiSurfaceX1EOLED(compatible []byte) bool {
	if len(compatible) == 0 || len(compatible) > 4096 || compatible[len(compatible)-1] != 0 {
		return false
	}
	for _, token := range bytes.Split(compatible, []byte{0}) {
		if bytes.Equal(token, []byte("microsoft,denali-oled")) {
			return true
		}
	}
	return false
}

// finishWiFiActivation reports filesystem publication separately from an
// explicit wireless restart. It never restarts DSP, USB or NetworkManager.
func (installer *Installer) finishWiFiActivation(ctx context.Context, result Result) (Result, error) {
	if !result.ActivationRequired {
		return result, nil
	}
	if err := installer.requireRoot(false); err != nil {
		return result, err
	}
	commands, err := installer.planWiFiActivation(result.Root)
	if err == nil && !slices.EqualFunc(commands, result.Commands, func(current, planned Command) bool {
		return current.Name == planned.Name && slices.Equal(current.Args, planned.Args)
	}) {
		err = errors.New("Wi-Fi activation plan changed after inspection; review a new dry run")
	}
	if err == nil {
		// Unlike independent service operations, a reload must stop if its
		// prerequisite fails. Never load after a failed or cancelled unload.
		for _, command := range result.Commands {
			if err = installer.runActivationCommands(ctx, []Command{command}); err != nil {
				break
			}
		}
	}
	if err != nil {
		result.ActivationError = err.Error()
		result.RebootRequired = true
		return result, fmt.Errorf("Wi-Fi board data is installed but radio restart failed: %w", err)
	}
	result.ActivationComplete = true
	return result, nil
}
