// Package relocation checks data captured by a separately reviewed native
// observer. It neither runs a program nor grants installation or activation
// authority. A successful check is software-only, pinned-image evidence, not
// physical-sensor or general distribution qualification.
package relocation

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ooaklee/lexr.sh/internal/sensors/packageplan"
	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
)

const (
	// Schema identifies the closed, software-only relocation observation format.
	Schema = "lexr-sensors-relocation-observation-v1"
	// MaximumObservationBytes bounds the untrusted JSON before decoding.
	MaximumObservationBytes = 2 << 20
	// maximumTextBytes bounds each raw loader listing or CLI output stream.
	maximumTextBytes = 64 << 10
	// interpreter fixes the native ARM64 loader used by the reviewed observer.
	interpreter = "/lib/ld-linux-aarch64.so.1"
	// MissingProviderDirectory must be a fresh, empty directory in the reviewed
	// observer's private tmpfs. This string alone does not prove that property.
	MissingProviderDirectory = "/tmp/lexr-empty-providers"
)

// Observation contains raw, untrimmed native output. The root proof must also
// authenticate the observer, image, arguments, environment, mounts and lifecycle;
// an editable JSON observation is not a native execution receipt by itself.
type Observation struct {
	Schema                 string                 `json:"schema"`
	Architecture           string                 `json:"architecture"`
	Prefix                 string                 `json:"prefix"`
	FileSHA256             map[string]string      `json:"fileSHA256"`
	Links                  map[string]string      `json:"links"`
	Listings               map[string]string      `json:"listings"`
	Smokes                 []Smoke                `json:"smokes"`
	MissingProviderControl MissingProviderControl `json:"missingProviderControl"`
}

// Smoke is one of four fixed, early-exit CLI invocations. No general-purpose
// caller-selected execution interface is provided by this package.
type Smoke struct {
	Path     string   `json:"path"`
	Args     []string `json:"args"`
	ExitCode int      `json:"exitCode"`
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
}

// MissingProviderControl records a fixed-loader --list observation of ssccli
// with the private provider directory removed from the loader search path.
type MissingProviderControl struct {
	Path        string `json:"path"`
	LibraryPath string `json:"libraryPath"`
	ExitCode    int    `json:"exitCode"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
}

// Decode rejects duplicate, missing, unknown and incorrectly cased fields,
// excessive nesting, oversized input and trailing JSON. Verify must still be
// called with an opaque verified package after this structural decoding.
func Decode(data []byte) (Observation, error) {
	var result Observation
	if len(data) == 0 || len(data) > MaximumObservationBytes || !utf8.Valid(data) {
		return result, errors.New("relocation observation size or UTF-8 differs")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSON(decoder, 0, new(int)); err != nil {
		return result, fmt.Errorf("relocation observation JSON: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return result, errors.New("relocation observation has trailing JSON")
	}
	root, err := closedObject(data, "schema", "architecture", "prefix", "fileSHA256", "links", "listings", "smokes", "missingProviderControl")
	if err != nil {
		return result, err
	}
	var smokes []json.RawMessage
	if err := json.Unmarshal(root["smokes"], &smokes); err != nil || len(smokes) != 4 {
		return result, errors.New("relocation observation smoke structure differs")
	}
	for _, smoke := range smokes {
		if _, err := closedObject(smoke, "path", "args", "exitCode", "stdout", "stderr"); err != nil {
			return result, err
		}
	}
	if _, err := closedObject(root["missingProviderControl"], "path", "libraryPath", "exitCode", "stdout", "stderr"); err != nil {
		return result, err
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Observation{}, fmt.Errorf("relocation observation typed JSON: %w", err)
	}
	return result, nil
}

// closedObject requires every named JSON property and rejects extras or nulls.
func closedObject(data []byte, names ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || len(object) != len(names) {
		return nil, errors.New("relocation observation object fields differ")
	}
	for _, name := range names {
		value, found := object[name]
		if !found || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("relocation observation field is absent or null: %s", name)
		}
	}
	return object, nil
}

// walkJSON checks duplicates before encoding/json can silently overwrite them.
func walkJSON(decoder *json.Decoder, depth int, count *int) error {
	*count++
	if depth > 8 || *count > 4096 {
		return errors.New("JSON complexity exceeds policy")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return errors.New("duplicate or malformed JSON key")
			}
			seen[name] = true
			if err := walkJSON(decoder, depth+1, count); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("malformed JSON object")
		}
	case '[':
		for decoder.More() {
			if err := walkJSON(decoder, depth+1, count); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("malformed JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

// elfPaths fixes the five executable objects requiring relocated loader proof.
func elfPaths() []string {
	return []string{"lib/libqmi-glib.so.5.12.0", "lib/libssc.so.2", "bin/ssccli", "libexec/iio-sensor-proxy", "bin/monitor-sensor"}
}

// expectedFiles closes the nine runtime payload destinations and their modes.
func expectedFiles() map[string]int64 {
	return map[string]int64{
		"lib/libqmi-glib.so.5.12.0": 0o755, "lib/libssc.so.2": 0o755,
		"bin/ssccli": 0o755, "libexec/iio-sensor-proxy": 0o755, "bin/monitor-sensor": 0o755,
		"share/licenses/COPYING.libqmi": 0o644, "share/licenses/COPYING.LIB.libqmi": 0o644,
		"share/licenses/LICENSE.libssc": 0o644, "share/licenses/COPYING.iio-sensor-proxy": 0o644,
	}
}

// expectedLinks closes the three relative private-provider library aliases.
func expectedLinks() map[string]string {
	return map[string]string{"lib/libqmi-glib.so": "libqmi-glib.so.5", "lib/libqmi-glib.so.5": "libqmi-glib.so.5.12.0", "lib/libssc.so": "libssc.so.2"}
}

// Verify checks an observation against every byte and alias of a verified,
// inactive package. It does not open files, execute code or modify a target.
func Verify(pkg *runtimepkg.VerifiedPackage, observation Observation) error {
	return verifyPackageProfile(pkg, observation, false)
}

// Verify048 verifies only observations of the sealed metadata0.4.8 package,
// including its exact early-exit CLI version. It grants no execution authority.
func Verify048(pkg *runtimepkg.VerifiedPackage, observation Observation) error {
	return verifyPackageProfile(pkg, observation, true)
}

// verifyPackageProfile never derives a profile from editable observations.
func verifyPackageProfile(pkg *runtimepkg.VerifiedPackage, observation Observation, metadata048 bool) error {
	if pkg == nil || pkg.SourceLineage() == "" || pkg.IsMetadata048() != metadata048 {
		return errors.New("relocation requires an opaque verified package")
	}
	files, links := pkg.Files(), pkg.Links()
	wantedFiles := expectedFiles()
	if len(files) != len(wantedFiles) || len(links) != len(expectedLinks()) {
		return errors.New("relocation package payload roster differs")
	}
	fileBytes := make(map[string][]byte, len(files))
	for _, file := range files {
		mode, found := wantedFiles[file.Path]
		if !found || fileBytes[file.Path] != nil || file.Mode != mode || len(file.Data) == 0 {
			return errors.New("relocation package regular-file metadata differs")
		}
		fileBytes[file.Path] = file.Data
	}
	actualLinks := make(map[string]string, len(links))
	for _, link := range links {
		if _, duplicate := actualLinks[link.Path]; duplicate {
			return errors.New("relocation package aliases are duplicated")
		}
		actualLinks[link.Path] = link.Target
	}
	if !reflect.DeepEqual(actualLinks, expectedLinks()) {
		return errors.New("relocation package alias roster differs")
	}
	return verifyPayloadProfile(fileBytes, actualLinks, observation, metadata048)
}

// verifyPayload binds complete observer data to authenticated package members.
func verifyPayload(files map[string][]byte, links map[string]string, observation Observation) error {
	return verifyPayloadProfile(files, links, observation, false)
}

// verifyPayloadProfile preserves full byte/listing checks and exact CLI ABI.
func verifyPayloadProfile(files map[string][]byte, links map[string]string, observation Observation, metadata048 bool) error {
	if observation.Schema != Schema || observation.Architecture != "aarch64" || observation.Prefix != packageplan.PrivatePrefix {
		return errors.New("relocation observation identity differs")
	}
	if len(observation.FileSHA256) != len(files) || !reflect.DeepEqual(observation.Links, links) || len(observation.Listings) != len(elfPaths()) {
		return errors.New("relocation observation payload or listing roster differs")
	}
	for name, data := range files {
		digest := sha256.Sum256(data)
		if observation.FileSHA256[name] != hex.EncodeToString(digest[:]) {
			return fmt.Errorf("relocation payload digest differs: %s", name)
		}
	}
	contexts := make(map[string]elfContext, len(elfPaths()))
	for _, name := range elfPaths() {
		context, err := inspectContext(name, files[name])
		if err != nil {
			return err
		}
		contexts[name] = context
		listing, found := observation.Listings[name]
		if !found {
			return fmt.Errorf("relocation listing is absent: %s", name)
		}
		if err := verifyListing(name, context, listing); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if err := verifySmokesProfile(observation.Smokes, metadata048); err != nil {
		return err
	}
	return verifyMissingProvider(contexts["bin/ssccli"], observation.MissingProviderControl)
}

// elfContext preserves the direct dependencies and identity of one target ELF.
type elfContext struct {
	needed []string
	soname string
}

// inspectContext reads only bytes already authenticated by runtimepkg. The
// unchanged source-artefact verifier owns bounded full ELF validation; here we
// recheck the exact dynamic context used for interpreting native observations.
func inspectContext(name string, data []byte) (elfContext, error) {
	var context elfContext
	object, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return context, fmt.Errorf("%s: invalid verified-package ELF: %w", name, err)
	}
	defer object.Close()
	if object.Machine != elf.EM_AARCH64 || object.Class != elf.ELFCLASS64 || object.Data != elf.ELFDATA2LSB {
		return context, fmt.Errorf("%s: ELF architecture differs", name)
	}
	context.needed, err = object.DynString(elf.DT_NEEDED)
	if err != nil || len(context.needed) == 0 || len(context.needed) > 128 {
		return context, fmt.Errorf("%s: ELF dependency context differs", name)
	}
	seen := map[string]bool{}
	for _, dependency := range context.needed {
		if !safeName(dependency) || seen[dependency] {
			return context, fmt.Errorf("%s: unsafe or duplicate ELF dependency", name)
		}
		seen[dependency] = true
	}
	sonames, err := object.DynString(elf.DT_SONAME)
	wanted := ""
	if name == "lib/libqmi-glib.so.5.12.0" {
		wanted = "libqmi-glib.so.5"
	} else if name == "lib/libssc.so.2" {
		wanted = "libssc.so.2"
	}
	if err != nil || wanted == "" && len(sonames) != 0 || wanted != "" && (len(sonames) != 1 || sonames[0] != wanted) {
		return context, fmt.Errorf("%s: ELF SONAME context differs", name)
	}
	context.soname = wanted
	for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
		values, err := object.DynString(tag)
		if err != nil || len(values) != 0 {
			return context, fmt.Errorf("%s: ELF contains a private search override", name)
		}
	}
	return context, nil
}

// validText bounds raw native output and refuses hidden control characters.
func validText(text string) bool {
	if len(text) > maximumTextBytes || !utf8.ValidString(text) {
		return false
	}
	for _, value := range text {
		if unicode.IsControl(value) && value != '\n' && value != '\t' {
			return false
		}
	}
	return true
}

// verifySmokes enforces the exact four safe invocations and meaningful output.
func verifySmokes(smokes []Smoke) error {
	return verifySmokesProfile(smokes, false)
}

// verifySmokesProfile binds version output to the selected package's exact ABI.
func verifySmokesProfile(smokes []Smoke, metadata048 bool) error {
	expected := []struct{ path, argument string }{{"bin/ssccli", "--help"}, {"bin/ssccli", "--version"}, {"libexec/iio-sensor-proxy", "--help"}, {"bin/monitor-sensor", "--help"}}
	if len(smokes) != len(expected) {
		return errors.New("relocation smoke roster differs")
	}
	for index, wanted := range expected {
		smoke := smokes[index]
		if smoke.Path != wanted.path || !reflect.DeepEqual(smoke.Args, []string{wanted.argument}) || smoke.ExitCode != 0 || smoke.Stderr != "" || !validText(smoke.Stdout) {
			return errors.New("relocation smoke arguments, status or output differ")
		}
		if wanted.argument == "--version" {
			version := "libssc version 0.4.7\n"
			if metadata048 {
				version = "libssc version 0.4.8\n"
			}
			if smoke.Stdout != version {
				return errors.New("relocation ssccli version differs")
			}
			continue
		}
		if len(smoke.Stdout) < 100 || !strings.HasPrefix(smoke.Stdout, "Usage:\n") || !strings.Contains(smoke.Stdout, path.Base(wanted.path)) || !strings.Contains(smoke.Stdout, "Help Options:") || !strings.Contains(smoke.Stdout, "Application Options:") || !strings.Contains(smoke.Stdout, "--help") {
			return errors.New("relocation help output is absent or differs")
		}
		usage := strings.Fields(strings.SplitN(smoke.Stdout, "\n", 3)[1])
		if len(usage) < 2 || usage[0] != path.Base(wanted.path) && usage[0] != packageplan.PrivatePrefix+"/"+wanted.path || !strings.HasPrefix(usage[1], "[OPTION") {
			return errors.New("relocation help executable identity differs")
		}
		marker := map[string]string{"bin/ssccli": "--version", "libexec/iio-sensor-proxy": "--replace", "bin/monitor-sensor": "--accel"}[wanted.path]
		if !strings.Contains(smoke.Stdout, marker) {
			return errors.New("relocation help output lacks the expected component option")
		}
	}
	return nil
}

// verifyMissingProvider requires the expected loader failure without providers.
func verifyMissingProvider(context elfContext, control MissingProviderControl) error {
	missing := ""
	for _, dependency := range context.needed {
		if dependency == "libssc.so.2" || dependency == "libqmi-glib.so.5" {
			missing = dependency
			break
		}
	}
	if missing == "" || control.Path != "bin/ssccli" || control.LibraryPath != MissingProviderDirectory || control.ExitCode != 127 || control.Stdout != "" {
		return errors.New("relocation missing-provider inverse control differs")
	}
	wanted := packageplan.PrivatePrefix + "/bin/ssccli: error while loading shared libraries: " + missing + ": cannot open shared object file: No such file or directory\n"
	if control.Stderr != wanted {
		return errors.New("relocation missing-provider failure text differs")
	}
	return nil
}
