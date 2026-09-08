// Package bootmenu registers an installed Surface loader with an existing GRUB.
// It never mounts filesystems, regenerates menus or changes firmware variables.
package bootmenu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// ArchOptions explicitly identifies mounted filesystems and the menu to extend.
type ArchOptions struct {
	ArchRoot, GRUBDirectory, ESP string
	DryRun                       bool
}

// Result describes the one menu addition and its recovery copy.
type Result struct {
	Path, Entry, Backup string
	AlreadyPresent      bool
}

// receipt reads only the identity fields needed from the installed boot record.
type receipt struct {
	Schema      int    `json:"schema"`
	ABI         string `json:"abi"`
	RootUUID    string `json:"root_uuid"`
	ESPPartUUID string `json:"esp_partuuid"`
	GRUBSHA256  string `json:"grub_sha256"`
}

// mount is the mounted filesystem identity reported by findmnt.
type mount struct {
	Target   string `json:"target"`
	Source   string `json:"source"`
	FSType   string `json:"fstype"`
	UUID     string `json:"uuid"`
	PartUUID string `json:"partuuid"`
}

var (
	// uuidPattern accepts the canonical ext4 and GPT UUID forms in the receipt.
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)
	// fatPattern bounds the FAT volume identifier used in the GRUB entry.
	fatPattern = regexp.MustCompile(`^[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}$`)
	// abiPattern confines kernel names before constructing installed asset paths.
	abiPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,126}$`)
)

// entryID identifies the one managed menu item without changing other entries.
const entryID = "lexr-arch-installed"

// loaderPath is the dedicated EFI executable written by the Arch installer.
const loaderPath = "EFI/LexrArch/grubaa64.efi"

// RegisterArch verifies an existing installation, then appends its EFI loader
// to custom.cfg. Dry runs also check syntax but create no files. Unknown GRUB
// inclusion layouts are refused rather than regenerating another OS's menu.
func RegisterArch(ctx context.Context, options ArchOptions, runner platform.Runner) (Result, error) {
	var result Result
	for _, path := range []string{options.ArchRoot, options.GRUBDirectory, options.ESP} {
		if err := safeDirectory(path); err != nil {
			return result, err
		}
	}
	if filepath.Clean(options.GRUBDirectory) == filepath.Join(options.ArchRoot, "boot/grub") {
		return result, errors.New("select the existing OS's GRUB directory, not Arch's own menu")
	}
	receiptPath := filepath.Join(options.ArchRoot, "etc/lexr/install-receipt.json")
	receiptBytes, _, err := readRegular(receiptPath, 1<<20)
	if err != nil {
		return result, err
	}
	var installed receipt
	if err = json.Unmarshal(receiptBytes, &installed); err != nil || installed.Schema != 1 ||
		!abiPattern.MatchString(installed.ABI) || !uuidPattern.MatchString(installed.RootUUID) ||
		!uuidPattern.MatchString(installed.ESPPartUUID) {
		return result, errors.New("invalid Lexr Arch installation receipt")
	}
	root, err := inspectMount(ctx, runner, options.ArchRoot, true)
	if err != nil || root.FSType != "ext4" || root.UUID != installed.RootUUID {
		return result, fmt.Errorf("--arch-root must be the mounted ext4 root named in its receipt: %v", err)
	}
	esp, err := inspectMount(ctx, runner, options.ESP, true)
	if err != nil || esp.FSType != "vfat" || !fatPattern.MatchString(esp.UUID) || esp.PartUUID != installed.ESPPartUUID {
		return result, fmt.Errorf("--esp must be the mounted FAT ESP named in the Arch receipt: %v", err)
	}
	owner, err := inspectMount(ctx, runner, options.GRUBDirectory, false)
	if err != nil {
		return result, err
	}
	if owner.UUID == root.UUID || owner.Source == root.Source {
		return result, errors.New("the selected menu belongs to Arch; registering it would create a boot loop")
	}
	loaderName := filepath.Join(options.ESP, loaderPath)
	loader, _, err := readRegular(loaderName, 32<<20)
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(loader)
	if hex.EncodeToString(digest[:]) != installed.GRUBSHA256 || !arm64EFI(loader) {
		return result, errors.New("Arch EFI loader does not match its ARM64 installation receipt; inspect any later GRUB update first")
	}
	for _, name := range []string{"vmlinuz-" + installed.ABI, "initramfs-" + installed.ABI + ".img"} {
		if _, err = regularInfo(filepath.Join(options.ArchRoot, "boot", name)); err != nil {
			return result, fmt.Errorf("installed boot asset: %w", err)
		}
	}
	mainPath := filepath.Join(options.GRUBDirectory, "grub.cfg")
	main, _, err := readRegular(mainPath, 4<<20)
	if err != nil {
		return result, err
	}
	if !loadsCustom(main) {
		return result, errors.New("existing grub.cfg does not contain the supported 41_custom loader; configure custom.cfg loading in that OS first (no menu was regenerated)")
	}
	// Sharing Arch's actual config through another mount must also be refused.
	if bytes.Contains(main, []byte(installed.RootUUID)) && bytes.Contains(main, []byte("Arch Linux ARM for Surface Pro 11")) {
		return result, errors.New("selected GRUB already directly boots this Arch installation; inspect its existing entries")
	}
	result.Path = filepath.Join(options.GRUBDirectory, "custom.cfg")
	result.Entry = renderEntry(strings.ToUpper(esp.UUID))
	old, mode, exists, err := readCustom(result.Path)
	if err != nil {
		return result, err
	}
	if bytes.Contains(main, []byte(entryID)) {
		return result, errors.New("a Lexr Arch entry already exists in grub.cfg; inspect it before registering another")
	}
	if bytes.Count(old, []byte(result.Entry)) == 1 && bytes.Count(old, []byte(entryID)) == 1 {
		result.AlreadyPresent = true
	} else if bytes.Contains(old, []byte(entryID)) || bytes.Contains(old, []byte(loaderPath)) {
		return result, errors.New("custom.cfg has a different Arch loader entry; preserve and review it before registering")
	}
	next := append([]byte(nil), old...)
	if !result.AlreadyPresent {
		if len(next) > 0 && next[len(next)-1] != '\n' {
			next = append(next, '\n')
		}
		next = append(next, []byte("\n"+result.Entry)...)
	}
	if err = runner.Run(ctx, platform.Command{Name: "grub-script-check", Stdin: bytes.NewReader(next)}); err != nil {
		return result, fmt.Errorf("custom menu syntax validation (grub-script-check is required): %w", err)
	}
	if options.DryRun || result.AlreadyPresent {
		return result, nil
	}
	// Recheck the mounted identities and all inputs immediately before changing
	// the selected menu. Do not run this alongside another GRUB update.
	for _, check := range []struct {
		path   string
		before []byte
	}{{mainPath, main}, {receiptPath, receiptBytes}, {loaderName, loader}} {
		current, _, readErr := readRegular(check.path, 32<<20)
		if readErr != nil || !bytes.Equal(current, check.before) {
			return result, errors.New("boot inputs changed during registration; rerun the preview")
		}
	}
	for _, check := range []struct {
		path   string
		before mount
	}{{options.ArchRoot, root}, {options.ESP, esp}} {
		current, mountErr := inspectMount(ctx, runner, check.path, true)
		if mountErr != nil || current != check.before {
			return result, errors.New("mounted boot filesystems changed during registration")
		}
	}
	if err = unchangedCustom(result.Path, old, exists); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	published := false
	defer func() {
		if !published && result.Backup != "" {
			_ = os.Remove(result.Backup)
		}
	}()
	if exists {
		backup, backupErr := os.CreateTemp(options.GRUBDirectory, "custom.cfg.lexr-backup-*")
		if backupErr != nil {
			return result, backupErr
		}
		result.Backup = backup.Name()
		if err = writeSyncClose(backup, old, mode); err != nil {
			return result, err
		}
	}
	staged, err := os.CreateTemp(options.GRUBDirectory, ".lexr-custom-*")
	if err != nil {
		return result, err
	}
	defer os.Remove(staged.Name())
	if err = writeSyncClose(staged, next, mode); err != nil {
		return result, err
	}
	if err = unchangedCustom(result.Path, old, exists); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = os.Rename(staged.Name(), result.Path); err != nil {
		return result, err
	}
	published = true
	directory, err := os.Open(options.GRUBDirectory)
	if err != nil {
		return result, err
	}
	defer directory.Close()
	return result, directory.Sync()
}

// renderEntry leaves all Surface kernel and DTB choices to Arch's own loader.
func renderEntry(espUUID string) string {
	return fmt.Sprintf(`menuentry 'Arch Linux ARM (Surface Pro 11)' --id '%s' {
    insmod part_gpt
    insmod fat
    insmod chain
    search --no-floppy --fs-uuid --set=lexr_arch_esp %s
    chainloader ($lexr_arch_esp)/%s
}
`, entryID, espUUID, loaderPath)
}

// loadsCustom accepts the standard runtime conditional emitted by 41_custom,
// not a commented occurrence of a source command or an unrelated filename.
func loadsCustom(main []byte) bool {
	const stanza = `if [ -f ${config_directory}/custom.cfg ]; then source ${config_directory}/custom.cfg elif [ -z "${config_directory}" -a -f $prefix/custom.cfg ]; then source $prefix/custom.cfg fi`
	start := bytes.Index(main, []byte("### BEGIN /etc/grub.d/41_custom ###"))
	end := bytes.Index(main, []byte("### END /etc/grub.d/41_custom ###"))
	if start < 0 || end <= start {
		return false
	}
	section := main[start+len("### BEGIN /etc/grub.d/41_custom ###") : end]
	return strings.Join(strings.Fields(string(section)), " ") == stanza
}

// inspectMount uses exact mounts for the installation and ESP, and resolves the
// menu's containing filesystem to prevent an accidental self-chainloader.
func inspectMount(ctx context.Context, runner platform.Runner, path string, exact bool) (mount, error) {
	flag := "--target"
	if exact {
		flag = "--mountpoint"
	}
	output, err := runner.Capture(ctx, platform.Command{Name: "findmnt", Args: []string{"--json", flag, path, "--output", "TARGET,SOURCE,FSTYPE,UUID,PARTUUID"}})
	if err != nil {
		return mount{}, err
	}
	var record struct {
		Filesystems []mount `json:"filesystems"`
	}
	if err = json.Unmarshal(output, &record); err != nil || len(record.Filesystems) != 1 {
		return mount{}, errors.New("cannot identify mounted filesystem")
	}
	m := record.Filesystems[0]
	if m.Source == "" || m.UUID == "" || (exact && m.Target != filepath.Clean(path)) {
		return mount{}, errors.New("path is not the required mounted filesystem")
	}
	return m, nil
}

// safeDirectory rejects aliases so every inspected and written path is explicit.
func safeDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("boot registration paths must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if resolved != filepath.Clean(path) || !info.IsDir() {
		return fmt.Errorf("expected a directory without symlinks: %s", path)
	}
	return nil
}

// regularInfo refuses symlinked components, directories and empty boot files.
func regularInfo(path string) (os.FileInfo, error) {
	if err := safeDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("expected nonempty regular file: %s", path)
	}
	return info, nil
}

// readRegular bounds reads and checks that opening did not substitute a link.
func readRegular(path string, limit int64) ([]byte, os.FileMode, error) {
	info, err := regularInfo(path)
	if err != nil {
		return nil, 0, err
	}
	if info.Size() > limit {
		return nil, 0, fmt.Errorf("boot file is too large: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, 0, errors.New("boot file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if int64(len(data)) > limit {
		return nil, 0, errors.New("boot file grew while reading")
	}
	return data, info.Mode().Perm(), err
}

// readCustom allows a missing or empty file, while retaining regular-file rules.
func readCustom(path string) ([]byte, os.FileMode, bool, error) {
	if err := safeDirectory(filepath.Dir(path)); err != nil {
		return nil, 0, false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0644, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, errors.New("custom.cfg must be a regular file, not a symlink")
	}
	if info.Size() == 0 {
		return []byte{}, info.Mode().Perm(), true, nil
	}
	data, mode, err := readRegular(path, 4<<20)
	return data, mode, true, err
}

// unchangedCustom refuses replacing a menu changed since the preview.
func unchangedCustom(path string, before []byte, existed bool) error {
	now, _, exists, err := readCustom(path)
	if err != nil || exists != existed || !bytes.Equal(now, before) {
		return errors.New("custom.cfg changed during registration; rerun the preview")
	}
	return nil
}

// writeSyncClose makes each staged or backup file durable before publication.
func writeSyncClose(file *os.File, data []byte, mode os.FileMode) error {
	defer file.Close()
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

// arm64EFI checks the DOS/PE header and AArch64 machine field without execution.
func arm64EFI(data []byte) bool {
	if len(data) < 64 || string(data[:2]) != "MZ" {
		return false
	}
	offset := uint64(binary.LittleEndian.Uint32(data[60:64]))
	return offset+6 <= uint64(len(data)) && bytes.Equal(data[offset:offset+6], []byte{'P', 'E', 0, 0, 0x64, 0xaa})
}
