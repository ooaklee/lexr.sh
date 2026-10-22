package install

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/userspace/assessment"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// assessCompatibility authenticates a manifest before privilege or target
// mutation. Only an exact compiled legacy input may omit the new contract.
func assessCompatibility(options Options, component string) (*assessment.Record, error) {
	if options.Compatibility == nil {
		if _, err := os.Lstat(filepath.Join(options.BundleDir, compatibility.Filename)); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("compatibility manifest has no independently pinned catalogue authority")
		}
		return nil, nil
	}
	target, err := assessment.ObserveRoot(options.Root, options.CompatibilityTarget)
	if err != nil {
		return nil, err
	}
	record, err := assessment.Evaluate(options.BundleDir, *options.Compatibility, component, options.CompatibilityRelease, target, options.AllowUnverifiedCompatibility)
	if err != nil {
		return nil, err
	}
	if err := compatibility.RequireAllowed(record.Decision, options.AllowUnverifiedCompatibility); err != nil {
		return &record, err
	}
	return &record, nil
}

// revalidateCompatibility repeats authentication and target inspection at the
// mutation boundary, rejecting even a still-compatible changed target tuple.
func revalidateCompatibility(options Options, component string, planned *assessment.Record) error {
	current, err := assessCompatibility(options, component)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, planned) {
		return errors.New("component compatibility evidence changed after planning")
	}
	return nil
}

// compatibilitySpec adds only authenticated metadata to a fixed payload set.
// New payload bytes still require a reviewed compiled adapter/pin update.
func compatibilitySpec(base releaseSpec, options Options) releaseSpec {
	if options.Compatibility == nil {
		return base
	}
	result := releaseSpec{component: base.component, tag: options.CompatibilityRelease, files: append([]immutableFile(nil), base.files...)}
	result.files = append(result.files, immutableFile{name: compatibility.Filename, sha256: options.Compatibility.SHA256, size: options.Compatibility.Size})
	var names []string
	byName := map[string]immutableFile{}
	for _, file := range result.files {
		if file.name != "SHA256SUMS" {
			names = append(names, file.name)
			byName[file.name] = file
		}
	}
	sort.Strings(names)
	var checksums strings.Builder
	for _, name := range names {
		fmt.Fprintf(&checksums, "%s  %s\n", byName[name].sha256, name)
	}
	digest := sha256.Sum256([]byte(checksums.String()))
	for index := range result.files {
		if result.files[index].name == "SHA256SUMS" {
			result.files[index].sha256 = hex.EncodeToString(digest[:])
			result.files[index].size = int64(checksums.Len())
		}
	}
	return result
}

// planCompatibilityRecords makes the new fixed diagnostic destinations visible
// in dry runs without creating directories or files.
func planCompatibilityRecords(options Options, component string, result *Result) error {
	if result.Compatibility == nil {
		return nil
	}
	for _, name := range []string{compatibility.Filename, "compatibility-assessment.json"} {
		target, err := resolveTarget(options.Root, "var/lib/lexr/userspace/"+component+"/"+name)
		if err != nil {
			return err
		}
		result.Files = append(result.Files, FileChange{Source: name, Target: target, Action: "record-compatibility"})
	}
	return nil
}

// persistCompatibilityRecords retains authenticated bytes and the decision for
// offline diagnosis. Payload completion remains visible if recording fails.
func persistCompatibilityRecords(options Options, component string, result *Result) error {
	if result.Compatibility == nil {
		return nil
	}
	result.FilesInstalled = true
	if err := revalidateCompatibility(options, component, result.Compatibility); err != nil {
		return err
	}
	directory, err := createPrivateInstallStaging("lexr-compatibility-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	data, err := json.MarshalIndent(result.Compatibility, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	source := filepath.Join(directory, "compatibility-assessment.json")
	digest := sha256.Sum256(data)
	if err := writePrivateFile(source, data, hex.EncodeToString(digest[:]), int64(len(data))); err != nil {
		return err
	}
	for _, file := range []struct {
		name, source, digest string
		size                 int64
	}{
		{compatibility.Filename, filepath.Join(options.BundleDir, compatibility.Filename), result.Compatibility.Manifest.SHA256, result.Compatibility.Manifest.Size},
		{"compatibility-assessment.json", source, hex.EncodeToString(digest[:]), int64(len(data))},
	} {
		destination, err := resolveTarget(options.Root, "var/lib/lexr/userspace/"+component+"/"+file.name)
		if err != nil {
			return err
		}
		if err := atomicCopyVerified(file.source, destination, 0o644, file.digest, file.size); err != nil {
			return fmt.Errorf("payload installed but compatibility record could not be retained: %w", err)
		}
	}
	return nil
}
