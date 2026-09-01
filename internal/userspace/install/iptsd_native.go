package install

import (
	debugelf "debug/elf"
	"errors"
	"fmt"
	"os"

	userspaceiptsd "github.com/ooaklee/lexr.sh/internal/userspace/iptsd"
)

// maximumNativeIPTSDBinaryBytes matches the status inspector's per-object
// ceiling and prevents native-layout recognition from parsing an unbounded file.
const maximumNativeIPTSDBinaryBytes int64 = 64 << 20

// fedoraNativeIPTSDState distinguishes an absent native layout from a complete
// exact package and from any partial or incompatible collision.
type fedoraNativeIPTSDState uint8

const (
	// fedoraNativeIPTSDAbsent means no unambiguously native path was observed.
	fedoraNativeIPTSDAbsent fedoraNativeIPTSDState = iota
	// fedoraNativeIPTSDComplete means every exact static and ELF member passed.
	fedoraNativeIPTSDComplete
	// fedoraNativeIPTSDPartial means at least one native path exists but the
	// complete exact package contract was not satisfied.
	fedoraNativeIPTSDPartial
)

// fedoraNativeIPTSDInstalled distinguishes the complete exact native RPM from
// any partial collision. It deliberately does not parse the RPM database or
// invoke a shell: deterministic integration bytes provide the package marker,
// while both rebuilt executables must be regular executable AArch64 ELF files.
func fedoraNativeIPTSDInstalled(root string) (fedoraNativeIPTSDState, error) {
	anyPresent := false
	complete := true
	for _, expected := range userspaceiptsd.FedoraNativeRPMStaticFiles() {
		path, err := resolveTarget(root, expected.Path)
		if err != nil {
			return fedoraNativeIPTSDPartial, fmt.Errorf("resolve Fedora-native IPTSD marker /%s: %w", expected.Path, err)
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			complete = false
			continue
		}
		if err != nil {
			return fedoraNativeIPTSDPartial, fmt.Errorf("inspect Fedora-native IPTSD marker /%s: %w", expected.Path, err)
		}
		distinctNativePath := expected.Path != userspaceiptsd.FedoraNativeRPMSharedSleepHookPath
		if !info.Mode().IsRegular() || info.Size() != expected.Size || expected.Executable && info.Mode().Perm()&0o111 == 0 {
			if distinctNativePath {
				anyPresent = true
			}
			complete = false
			continue
		}
		digest, _, err := hashRegularNoFollowBounded(path, expected.Size)
		if err != nil {
			return fedoraNativeIPTSDPartial, fmt.Errorf("hash Fedora-native IPTSD marker /%s: %w", expected.Path, err)
		}
		matchesNativeBytes := digest == expected.SHA256
		if distinctNativePath || matchesNativeBytes {
			anyPresent = true
		}
		if !matchesNativeBytes {
			complete = false
		}
	}

	for _, expected := range userspaceiptsd.FedoraNativeRPMBinaries() {
		path, err := resolveTarget(root, expected.Path)
		if err != nil {
			return fedoraNativeIPTSDPartial, fmt.Errorf("resolve Fedora-native IPTSD binary /%s: %w", expected.Path, err)
		}
		valid, err := isBoundedAArch64ELF(path, expected.Executable)
		if errors.Is(err, os.ErrNotExist) {
			complete = false
			continue
		}
		if err != nil {
			return fedoraNativeIPTSDPartial, fmt.Errorf("inspect Fedora-native IPTSD binary /%s: %w", expected.Path, err)
		}
		anyPresent = true
		if !valid {
			complete = false
		}
	}
	if !anyPresent {
		return fedoraNativeIPTSDAbsent, nil
	}
	if complete {
		return fedoraNativeIPTSDComplete, nil
	}
	return fedoraNativeIPTSDPartial, nil
}

// isBoundedAArch64ELF validates architecture on the same no-follow descriptor
// whose mode and length established the fixed inspection boundary.
func isBoundedAArch64ELF(path string, executable bool) (bool, error) {
	file, info, err := openRegularNoFollow(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	if info.Size() < 1 || info.Size() > maximumNativeIPTSDBinaryBytes || executable && info.Mode().Perm()&0o111 == 0 {
		return false, nil
	}
	object, err := debugelf.NewFile(file)
	if err != nil {
		return false, nil
	}
	defer object.Close()
	return object.Class == debugelf.ELFCLASS64 && object.Data == debugelf.ELFDATA2LSB && object.Machine == debugelf.EM_AARCH64, nil
}
