// Package assessment binds authenticated component metadata to explicit target
// evidence without running target binaries or granting installation authority.
package assessment

import (
	"encoding/binary"
	"errors"
	"fmt"
	kernelidentity "github.com/ooaklee/lexr.sh/internal/kernel/identity"
	"github.com/ooaklee/lexr.sh/internal/profile"
	"io"
	"os"
	"path/filepath"

	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	"github.com/ooaklee/lexr.sh/internal/version"
)

// Record is the portable explanation retained by plans and transaction receipts.
type Record struct {
	// SchemaVersion identifies the compatibility assessment record.
	SchemaVersion int `json:"schema_version"`
	// Manifest identifies exact bytes authenticated independently of this record.
	Manifest compatibility.Reference `json:"manifest"`
	// Decision records the observed tuple and matched rule.
	Decision compatibility.Decision `json:"decision"`
	// AllowUnverified records the dedicated operator override.
	AllowUnverified bool `json:"allow_unverified"`
}

// ReadManifest opens one bounded regular asset beneath an explicit directory.
func ReadManifest(directory string) ([]byte, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	info, err := root.Lstat(compatibility.Filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > compatibility.MaxBytes {
		return nil, errors.New("compatibility manifest must be a bounded regular file")
	}
	file, err := compatibility.OpenRegular(root, compatibility.Filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, errors.New("compatibility manifest changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, compatibility.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > compatibility.MaxBytes {
		return nil, errors.New("compatibility manifest exceeds size bound")
	}
	return data, nil
}

// Evaluate authenticates the exact component release before interpreting it.
func Evaluate(directory string, reference compatibility.Reference, component, release string, target compatibility.Target, allowUnverified bool) (Record, error) {
	data, err := ReadManifest(directory)
	if err != nil {
		return Record{}, err
	}
	manifest, err := compatibility.Verify(data, reference, component, release)
	if err != nil {
		return Record{}, err
	}
	decision := compatibility.Evaluate(manifest, target)
	return Record{SchemaVersion: 1, Manifest: reference, Decision: decision, AllowUnverified: allowUnverified}, nil
}

// ObserveRoot reads only the selected root. Explicit architecture, device and
// kernel selections are permitted; the operating system always comes from disk.
// Missing evidence remains empty and is rejected by the shared evaluator.
func ObserveRoot(directory string, selected compatibility.Target) (compatibility.Target, error) {
	if directory == "" {
		directory = "/"
	}
	selected.LexrVersion, _, _ = version.Info()
	id, release, err := compatibility.ReadOSRelease(directory)
	if err != nil {
		return selected, fmt.Errorf("inspect target operating system: %w", err)
	}
	selected.OSID, selected.OSVersion = id, release
	root, err := os.OpenRoot(directory)
	if err != nil {
		return selected, err
	}
	defer root.Close()
	for _, path := range []string{"usr/bin/dash", "usr/bin/bash", "bin/dash", "bin/bash"} {
		file, openErr := compatibility.OpenRegular(root, path)
		if openErr != nil {
			continue
		}
		var header [20]byte
		_, inspectErr := io.ReadFull(file, header[:])
		observed := ""
		if inspectErr == nil && string(header[:4]) == "\x7fELF" && (header[4] == 1 || header[4] == 2) && (header[5] == 1 || header[5] == 2) && header[6] == 1 {
			var order binary.ByteOrder = binary.LittleEndian
			if header[5] == 2 {
				order = binary.BigEndian
			}
			switch order.Uint16(header[18:20]) {
			case 183:
				observed = "arm64"
			case 62:
				observed = "amd64"
			case 3:
				observed = "386"
			case 40:
				observed = "arm"
			}
		}
		file.Close()
		if observed == "" || (observed == "arm64" || observed == "amd64") && header[4] != 2 || (observed == "arm" || observed == "386") && header[4] != 1 {
			return selected, errors.New("target shell has malformed or unregistered ELF architecture evidence")
		}
		if observed != "" {
			if selected.Architecture != "" && selected.Architecture != observed {
				return selected, errors.New("explicit architecture disagrees with target ELF evidence")
			}
			selected.Architecture = observed
			break
		}
	}
	if observed, detectErr := profile.Detect(directory); detectErr == nil {
		if selected.DeviceProfile != "" && selected.DeviceProfile != observed.ID {
			return selected, errors.New("explicit device profile disagrees with target evidence")
		}
		selected.DeviceProfile = observed.ID
	} else if selected.DeviceProfile == "" {
		return selected, fmt.Errorf("target device profile is unavailable: %w; supply an explicit registered profile", detectErr)
	}
	if selected.KernelABI == "" {
		modules, err := root.Open("lib/modules")
		if err == nil {
			names, readErr := modules.Readdirnames(257)
			modules.Close()
			if (readErr == nil || errors.Is(readErr, io.EOF)) && len(names) == 1 {
				selected.KernelABI = names[0]
			}
		}
	}
	if selected.KernelABI != "" {
		if _, err := kernelidentity.Parse(selected.KernelABI); err != nil {
			return selected, err
		}
		info, err := root.Stat(filepath.Join("lib/modules", selected.KernelABI))
		if err != nil || !info.IsDir() {
			return selected, errors.New("selected kernel ABI is not installed in the target root")
		}
	}
	return selected, nil
}
