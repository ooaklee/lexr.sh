package build

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ooaklee/lexr.sh/internal/camera/jsonstrict"
)

// VerifiedArtifacts can only be produced by the complete byte verifier. It has
// no filesystem or execution methods, and its accessors return defensive copies.
type VerifiedArtifacts struct {
	files     map[string][]byte
	artifacts []Artifact
	links     []Link
	report    BuildReport
	valid     bool
	profile   artifactProfile
}

// artifactProfile selects one closed contract before any untrusted data is read.
type artifactProfile uint8

// The default profile remains 0.4.7; only explicit successor entry points select 0.4.8.
const (
	artifact047 artifactProfile = iota
	artifact048
)

// IsMetadata048 identifies only a valid handle from an explicit 0.4.8 decoder.
func (v *VerifiedArtifacts) IsMetadata048() bool {
	return v != nil && v.valid && v.profile == artifact048
}

// Files returns a copy of the verified regular-member roster, or nil if invalid.
func (v *VerifiedArtifacts) Files() []Artifact {
	if v == nil || !v.valid {
		return nil
	}
	return append([]Artifact(nil), v.artifacts...)
}

// Links returns a copy of the verified symlink roster, or nil if invalid.
func (v *VerifiedArtifacts) Links() []Link {
	if v == nil || !v.valid {
		return nil
	}
	return append([]Link(nil), v.links...)
}

// Data copies one verified member's bytes without exposing internal storage.
func (v *VerifiedArtifacts) Data(name string) ([]byte, bool) {
	if v == nil || !v.valid {
		return nil, false
	}
	data, ok := v.files[name]
	return bytes.Clone(data), ok
}

// Report returns a deep copy of validated observations, not installation policy.
func (v *VerifiedArtifacts) Report() BuildReport {
	if v == nil || !v.valid {
		return BuildReport{}
	}
	r := v.report
	r.Products = append([]Artifact(nil), r.Products...)
	r.Links = append([]Link(nil), r.Links...)
	r.Providers = append([]Provider(nil), r.Providers...)
	r.Fixtures = append([]string(nil), r.Fixtures...)
	r.ProxyInstallTargets = append([]string(nil), r.ProxyInstallTargets...)
	r.Loaders = append([]Loader(nil), r.Loaders...)
	for i := range r.Loaders {
		values := map[string]string{}
		for k, value := range r.Loaders[i].Providers {
			values[k] = value
		}
		r.Loaders[i].Providers = values
	}
	if r.MemoryPeakBytes != nil {
		peak := *r.MemoryPeakBytes
		r.MemoryPeakBytes = &peak
	}
	return r
}

// bytesDigest hashes received bytes using the contract's lowercase SHA-256 form.
func bytesDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// safeFlatName permits only bounded ASCII basenames, never archive path traversal.
func safeFlatName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 128 {
		return false
	}
	for _, c := range name {
		if c > 127 || !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '+') {
			return false
		}
	}
	return true
}

// checkRosterNames rejects unsafe names and case aliases in the compiled roster.
func checkRosterNames(files map[string]fileRule, links []Link) error {
	seen := map[string]bool{}
	for name := range files {
		if !safeFlatName(name) || seen[strings.ToLower(name)] {
			return errors.New("compiled artifact roster has unsafe/case-colliding names")
		}
		seen[strings.ToLower(name)] = true
	}
	for _, link := range links {
		if !safeFlatName(link.Name) || !safeFlatName(link.Target) || seen[strings.ToLower(link.Name)] {
			return errors.New("compiled alias roster has unsafe/case-colliding names")
		}
		seen[strings.ToLower(link.Name)] = true
	}
	return nil
}

// validHeader bounds common tar metadata and optionally requires plain USTAR.
func validHeader(h *tar.Header, ustar bool) bool {
	return h != nil && (!ustar || h.Format == tar.FormatUSTAR) && len(h.PAXRecords) == 0 && len(h.Xattrs) == 0 && safeFlatName(h.Name) && h.Mode >= 0 && h.Mode&^int64(0o777) == 0
}

// endOfArchive refuses missing end blocks, nonzero trailers and truncated
// streams. archive/tar otherwise accepts an EOF without the two zero records.
func endOfArchive(reader *bytes.Reader, before int) error {
	if before-reader.Len() < 1024 {
		return errors.New("archive is missing complete end records")
	}
	for reader.Len() > 0 {
		value, err := reader.ReadByte()
		if err != nil || value != 0 {
			return errors.New("archive has nonzero trailing bytes")
		}
	}
	return nil
}

// DecodeDockerCopy validates Docker's transport tar, then the independent inner
// artefact contract. No pathname from either stream is extracted or followed.
func DecodeDockerCopy(data []byte, expectedJobs int) (*VerifiedArtifacts, error) {
	return decodeDockerCopy(data, expectedJobs, expectedFiles())
}

// DecodeDockerCopy048 validates the fixed successor transport and 0.4.8 contract.
func DecodeDockerCopy048(data []byte, expectedJobs int) (*VerifiedArtifacts, error) {
	return decodeDockerCopyProfile(data, expectedJobs, expectedFiles048(), artifact048)
}

// decodeDockerCopy requires exactly one fixed inner archive and complete endings.
// Only package fixtures substitute the regular-member licence rules.
func decodeDockerCopy(data []byte, expectedJobs int, rules map[string]fileRule) (*VerifiedArtifacts, error) {
	return decodeDockerCopyProfile(data, expectedJobs, rules, artifact047)
}

// decodeDockerCopyProfile retains the selected contract through both tar layers.
func decodeDockerCopyProfile(data []byte, expectedJobs int, rules map[string]fileRule, profile artifactProfile) (*VerifiedArtifacts, error) {
	if len(data) == 0 || len(data) > MaximumArchiveBytes+(64<<10) || len(data)%512 != 0 {
		return nil, errors.New("Docker copy archive size/alignment differs")
	}
	reader := bytes.NewReader(data)
	archive := tar.NewReader(reader)
	h, err := archive.Next()
	if err != nil || !validHeader(h, false) || h.Name != ArchiveName || h.Typeflag != tar.TypeReg || h.Linkname != "" || h.Size <= 0 || h.Size > MaximumArchiveBytes {
		return nil, errors.New("Docker copy must contain the fixed regular archive only")
	}
	inner := make([]byte, int(h.Size))
	if _, err := io.ReadFull(archive, inner); err != nil {
		return nil, errors.New("Docker copy member is truncated")
	}
	before := reader.Len()
	if _, err := archive.Next(); err != io.EOF {
		return nil, errors.New("Docker copy contains an extra or invalid member")
	}
	if err := endOfArchive(reader, before); err != nil {
		return nil, err
	}
	return decodeArtifactArchiveProfile(inner, expectedJobs, rules, profile)
}

// DecodeArtifactArchive requires the compiled roster, not merely the roster in
// the recipe's descriptive report. It independently hashes and inspects all
// received bytes. This does not construct release or installation authority.
func DecodeArtifactArchive(data []byte, expectedJobs int) (*VerifiedArtifacts, error) {
	return decodeArtifactArchive(data, expectedJobs, expectedFiles())
}

// DecodeArtifactArchive048 requires the complete fixed 0.4.8 artefact contract.
func DecodeArtifactArchive048(data []byte, expectedJobs int) (*VerifiedArtifacts, error) {
	return decodeArtifactArchiveProfile(data, expectedJobs, expectedFiles048(), artifact048)
}

// Only package tests substitute licence bytes. Exported production entry points
// always use expectedFiles' compiled licence authorities and the real ELF check.
func decodeArtifactArchive(data []byte, expectedJobs int, rules map[string]fileRule) (*VerifiedArtifacts, error) {
	return decodeArtifactArchiveProfile(data, expectedJobs, rules, artifact047)
}

// decodeArtifactArchiveProfile never discovers its profile from archive contents.
func decodeArtifactArchiveProfile(data []byte, expectedJobs int, rules map[string]fileRule, profile artifactProfile) (*VerifiedArtifacts, error) {
	if profile != artifact047 && profile != artifact048 {
		return nil, errors.New("unknown compiled artefact profile")
	}
	if expectedJobs < 1 || expectedJobs > 8 {
		return nil, errors.New("expected jobs must be between one and eight")
	}
	if len(data) == 0 || len(data) > MaximumArchiveBytes || len(data)%512 != 0 {
		return nil, errors.New("artifact archive size/alignment differs")
	}
	links := ArtifactLinks()
	if err := checkRosterNames(rules, links); err != nil {
		return nil, err
	}
	wantedLinks := map[string]string{}
	for _, link := range links {
		wantedLinks[link.Name] = link.Target
	}
	files := map[string][]byte{}
	seen := map[string]bool{}
	var total int64
	reader := bytes.NewReader(data)
	archive := tar.NewReader(reader)
	for {
		before := reader.Len()
		h, err := archive.Next()
		if err == io.EOF {
			if err := endOfArchive(reader, before); err != nil {
				return nil, err
			}
			break
		}
		if err != nil || !validHeader(h, true) || seen[strings.ToLower(h.Name)] {
			return nil, errors.New("artifact archive has an invalid/duplicate header")
		}
		seen[strings.ToLower(h.Name)] = true
		if !h.ModTime.Equal(time.Unix(0, 0)) {
			return nil, errors.New("artifact modification time differs from compiled epoch")
		}
		if h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" {
			return nil, errors.New("artifact header carries unexpected ownership metadata")
		}
		if target, ok := wantedLinks[h.Name]; ok {
			if h.Typeflag != tar.TypeSymlink || h.Linkname != target || h.Mode != 0o777 || h.Size != 0 {
				return nil, errors.New("artifact alias differs from compiled route")
			}
			continue
		}
		rule, ok := rules[h.Name]
		if !ok || h.Typeflag != tar.TypeReg || h.Linkname != "" || h.Mode != rule.mode || h.Size <= 0 || h.Size > rule.maximum || h.Size > MaximumArchiveBytes-total {
			return nil, errors.New("artifact regular member differs from compiled roster")
		}
		total += h.Size
		member := make([]byte, int(h.Size))
		if _, err := io.ReadFull(archive, member); err != nil {
			return nil, errors.New("artifact member is truncated")
		}
		if rule.licenseSHA256 != "" && bytesDigest(member) != rule.licenseSHA256 {
			return nil, fmt.Errorf("licence identity differs: %s", h.Name)
		}
		files[h.Name] = member
	}
	if len(files) != len(rules) || len(seen) != len(rules)+len(wantedLinks) {
		return nil, errors.New("artifact archive is incomplete")
	}
	var elfErr error
	if profile == artifact048 {
		elfErr = validateNativeELFs048(files)
	} else {
		elfErr = validateNativeELFs(files)
	}
	if elfErr != nil {
		return nil, elfErr
	}
	report, err := validateEvidenceProfile(files, expectedJobs, profile)
	if err != nil {
		return nil, err
	}
	artifacts := make([]Artifact, 0, len(files))
	for name, data := range files {
		artifacts = append(artifacts, Artifact{name, int64(len(data)), bytesDigest(data), rules[name].mode})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Name < artifacts[j].Name })
	return &VerifiedArtifacts{files: files, artifacts: artifacts, links: links, report: report, valid: true, profile: profile}, nil
}

// decodeJSON requires UTF-8 evidence without NULs or duplicate member names and
// optionally refuses fields outside the destination schema.
func decodeJSON(data []byte, into any, strict bool) error {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return errors.New("evidence is not valid UTF-8 text")
	}
	if err := jsonstrict.RejectDuplicateNames(data); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if strict {
		d.DisallowUnknownFields()
	}
	if err := d.Decode(into); err != nil {
		return err
	}
	return nil
}

// validateEvidence cross-checks reported identities, resources and observations
// against compiled authority and the actual received output bytes.
func validateEvidence(files map[string][]byte, jobs int) (BuildReport, error) {
	return validateEvidenceProfile(files, jobs, artifact047)
}

// validateEvidenceProfile applies exactly one source, dependency and test policy.
func validateEvidenceProfile(files map[string][]byte, jobs int, profile artifactProfile) (BuildReport, error) {
	var report BuildReport
	commit, version, rules := SourceCommit, "0.4.7", expectedFiles()
	textFiles := []string{ReportName, "testlog.json", "testlog.txt", "qmi-dependencies.json", "ssc-dependencies.json", "proxy-dependencies.json", "loader-evidence.txt"}
	if profile == artifact048 {
		commit, version, rules = SourceCommit048, "0.4.8", expectedFiles048()
		textFiles = append(textFiles, "ssc-metadata-testlog.json", "ssc-metadata-testlog.txt")
	} else if profile != artifact047 {
		return report, errors.New("unknown compiled evidence profile")
	}
	for _, name := range textFiles {
		data := files[name]
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 || !bytes.HasSuffix(data, []byte("\n")) {
			return report, fmt.Errorf("evidence text differs: %s", name)
		}
	}
	if err := decodeJSON(files[ReportName], &report, true); err != nil {
		return report, fmt.Errorf("build report: %w", err)
	}
	if report.Schema != "lexr-sensors-source-build-v1" || report.SourceCommit != commit || report.Jobs != jobs || !reflect.DeepEqual(report.Fixtures, FixtureTargets()) || !reflect.DeepEqual(report.ProxyInstallTargets, ProxyInstallTargets()) {
		return report, errors.New("build report contract differs")
	}
	if report.VolumeFreeBeforeBytes < 6<<30 || report.JobApparentBeforeArchiveBytes <= 0 || report.MemoryPeakBytes != nil && *report.MemoryPeakBytes <= 0 {
		return report, errors.New("build resource observations differ")
	}
	seen := map[string]bool{}
	var selectedBytes int64
	for _, a := range report.Products {
		data, ok := files[a.Name]
		if !ok || a.Name == ReportName || seen[a.Name] || a.Bytes != int64(len(data)) || a.SHA256 != bytesDigest(data) || a.Mode != rules[a.Name].mode {
			return report, errors.New("reported product identity differs")
		}
		seen[a.Name] = true
		selectedBytes += a.Bytes
	}
	if len(seen) != len(files)-1 || report.JobApparentBeforeArchiveBytes < selectedBytes {
		return report, errors.New("reported product set/accounting differs")
	}
	if !reflect.DeepEqual(report.Links, ArtifactLinks()) {
		return report, errors.New("reported library aliases differ")
	}
	expectedProviders := map[string]Provider{
		"qmi-glib": {"qmi-glib", "1.39.1", ProviderDirectory + "/lib/pkgconfig", ProviderDirectory + "/lib/libqmi-glib.so.5.12.0", bytesDigest(files["libqmi-glib.so.5.12.0"])},
		"libssc":   {"libssc", version, ProviderDirectory + "/lib/pkgconfig", ProviderDirectory + "/lib/libssc.so.2", bytesDigest(files["libssc.so.2"])},
	}
	seen = map[string]bool{}
	for _, provider := range report.Providers {
		expected, ok := expectedProviders[provider.Name]
		if !ok || seen[provider.Name] || provider != expected {
			return report, errors.New("reported private provider differs")
		}
		seen[provider.Name] = true
	}
	if len(seen) != 2 {
		return report, errors.New("reported private provider set is incomplete")
	}
	if err := validateTestLog(files["testlog.json"], files["testlog.txt"]); err != nil {
		return report, err
	}
	if err := validateDependenciesProfile(files, profile); err != nil {
		return report, err
	}
	if profile == artifact048 {
		if err := validateMetadataTestLog048(files["ssc-metadata-testlog.json"], files["ssc-metadata-testlog.txt"]); err != nil {
			return report, err
		}
	}
	if err := validateLoadersProfile(files, report.Loaders, profile); err != nil {
		return report, err
	}
	return report, nil
}

// validateTestLog requires every selected Meson fixture to finish successfully
// with recorded output and a matching entry in the retained text log.
func validateTestLog(data, text []byte) error {
	wanted := map[string]bool{}
	for _, name := range FixtureTargets() {
		wanted[name] = true
	}
	seen := map[string]bool{}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			return errors.New("test evidence has an empty record")
		}
		var entry struct {
			Name       string `json:"name"`
			Result     string `json:"result"`
			ReturnCode *int   `json:"returncode"`
			Stdout     string `json:"stdout"`
		}
		if err := decodeJSON(line, &entry, false); err != nil {
			return fmt.Errorf("invalid Meson test record: %w", err)
		}
		name := strings.TrimPrefix(entry.Name, "iio-sensor-proxy:")
		if !wanted[name] || seen[name] || entry.Result != "OK" || entry.ReturnCode == nil || *entry.ReturnCode != 0 || strings.TrimSpace(entry.Stdout) == "" || !bytes.Contains(text, []byte(name)) {
			return errors.New("Meson test selection/result differs")
		}
		seen[name] = true
	}
	if len(seen) != len(wanted) {
		return errors.New("Meson test evidence is incomplete")
	}
	return nil
}

// metadataTestCases048 fixes every required helper and real-parser TAP case.
func metadataTestCases048() map[string][]string {
	return map[string][]string{
		"test-metadata-diagnostics": {
			"/metadata/owned-slots", "/metadata/duplicates-invalid", "/metadata/attribute-bound",
			"/metadata/matrix-provenance", "/metadata/boxed-copy-lifetime", "/metadata/matrix-representation-copies",
		},
		"test-metadata-diagnostics-integration": {
			"/metadata-native/opaque-commit", "/metadata-native/diagnostics-preserve-legacy",
			"/metadata-native/matrix-legacy-provenance", "/metadata-native/matrix-all-rows", "/metadata-native/getter-admission",
		},
	}
}

// metadataTAPDirective refuses skipped, deferred or aborted case evidence.
var metadataTAPDirective = regexp.MustCompile(`(?i)\b(skip|todo)\b|bail out`)

// validateMetadataTAP048 requires one exact plan and all ordered non-skipped cases.
func validateMetadataTAP048(output string, cases []string) error {
	if output == "" || len(output) > 1<<20 || strings.ContainsRune(output, '\r') || metadataTAPDirective.MatchString(output) {
		return errors.New("metadata TAP output has invalid bounds or directives")
	}
	header, plan := 0, 0
	var passed []string
	for _, line := range strings.Split(output, "\n") {
		switch {
		case line == "TAP version 14":
			header++
		case line == fmt.Sprintf("1..%d", len(cases)):
			plan++
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "ok "):
			passed = append(passed, line)
		default:
			return errors.New("metadata TAP has an unexpected result or plan")
		}
	}
	if header != 1 || plan != 1 || len(passed) != len(cases) {
		return errors.New("metadata TAP header, plan or case count differs")
	}
	for n, name := range cases {
		if passed[n] != fmt.Sprintf("ok %d %s", n+1, name) {
			return errors.New("metadata TAP case identity or order differs")
		}
	}
	return nil
}

// validateMetadataTestLog048 binds two exact executables to complete 6+5 TAP runs.
func validateMetadataTestLog048(data, text []byte) error {
	wanted, seen := metadataTestCases048(), map[string]bool{}
	if len(data) > 8<<20 || len(text) > 8<<20 || !utf8.Valid(text) || bytes.IndexByte(text, 0) >= 0 || !bytes.HasSuffix(text, []byte("\n")) || !bytes.HasSuffix(data, []byte("\n")) {
		return errors.New("metadata text log differs")
	}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var entry struct {
			Name       string   `json:"name"`
			Result     string   `json:"result"`
			ReturnCode *int     `json:"returncode"`
			Command    []string `json:"command"`
			Stdout     string   `json:"stdout"`
			Stderr     *string  `json:"stderr"`
		}
		if !json.Valid(line) {
			return errors.New("metadata Meson record is not one JSON value")
		}
		if err := decodeJSON(line, &entry, false); err != nil {
			return fmt.Errorf("metadata Meson test record: %w", err)
		}
		name := strings.TrimPrefix(entry.Name, "metadata-local - libssc:")
		cases, found := wanted[name]
		command := "/work/job/ssc-build/tests/metadata/" + name
		if !found || seen[name] || entry.Name != "metadata-local - libssc:"+name || entry.Result != "OK" || entry.ReturnCode == nil || *entry.ReturnCode != 0 || !reflect.DeepEqual(entry.Command, []string{command}) || entry.Stderr != nil && *entry.Stderr != "" {
			return errors.New("metadata Meson executable, status or selection differs")
		}
		if err := validateMetadataTAP048(entry.Stdout, cases); err != nil {
			return err
		}
		if !bytes.Contains(text, []byte(entry.Name)) || !bytes.Contains(text, []byte(command)) || !bytes.Contains(text, []byte(entry.Stdout)) {
			return errors.New("metadata text log does not contain its exact JSON run")
		}
		seen[name] = true
	}
	if len(seen) != len(wanted) {
		return errors.New("metadata Meson evidence is incomplete")
	}
	return nil
}

// dependencyEvidence retains Meson's selected provider identity and build flags.
type dependencyEvidence struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	CompileArgs []string `json:"compile_args"`
	LinkArgs    []string `json:"link_args"`
}

// validateDependencies requires the fixed private QMI/SSC providers at each
// dependent build stage rather than trusting version strings alone.
func validateDependencies(files map[string][]byte) error {
	return validateDependenciesProfile(files, artifact047)
}

// validateDependenciesProfile selects one compiled private dependency version.
func validateDependenciesProfile(files map[string][]byte, profile artifactProfile) error {
	sscVersion := "0.4.7"
	if profile == artifact048 {
		sscVersion = "0.4.8"
	} else if profile != artifact047 {
		return errors.New("unknown compiled dependency profile")
	}
	for _, component := range []string{"qmi", "ssc", "proxy"} {
		var entries []dependencyEvidence
		if err := decodeJSON(files[component+"-dependencies.json"], &entries, false); err != nil || len(entries) == 0 || len(entries) > 512 {
			return fmt.Errorf("%s dependency evidence differs", component)
		}
		seen := map[string]bool{}
		for _, entry := range entries {
			if !safeFlatName(entry.Name) || entry.Version == "" || strings.ContainsAny(entry.Version, "\x00\r\n\t ") {
				return errors.New("dependency identity is absent")
			}
			if entry.Name != "qmi-glib" && entry.Name != "libssc" {
				continue
			}
			if seen[entry.Name] || component == "qmi" {
				return errors.New("duplicate or unexpected private dependency")
			}
			seen[entry.Name] = true
			expectedVersion := "1.39.1"
			if entry.Name == "libssc" {
				expectedVersion = sscVersion
			}
			if entry.Version != expectedVersion {
				return errors.New("private dependency version differs")
			}
			if err := validateProviderFlags(entry, entry.Name == "libssc"); err != nil {
				return err
			}
		}
		if component == "ssc" && !seen["qmi-glib"] || component == "proxy" && !seen["libssc"] {
			return errors.New("selected private dependency is absent")
		}
	}
	return nil
}

// validateProviderFlags checks private headers and library routes in Meson flags.
func validateProviderFlags(entry dependencyEvidence, includeSSC bool) error {
	includes := map[string]bool{}
	privateLibDir := false
	libraries := map[string]bool{}
	for _, arg := range entry.CompileArgs {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return errors.New("dependency flag has controls")
		}
		if strings.HasPrefix(arg, "-I") {
			name := path.Clean(strings.TrimPrefix(arg, "-I"))
			if strings.Contains(name, "libqmi-glib") || strings.HasSuffix(name, "/libssc") {
				if name != ProviderDirectory+"/include/libqmi-glib" && name != ProviderDirectory+"/include/libssc" {
					return errors.New("provider headers are not private")
				}
				includes[name] = true
			}
		}
	}
	for _, arg := range entry.LinkArgs {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return errors.New("dependency link flag has controls")
		}
		if strings.HasPrefix(arg, "-L") && path.Clean(strings.TrimPrefix(arg, "-L")) == ProviderDirectory+"/lib" {
			privateLibDir = true
		}
		for _, lib := range []string{"qmi-glib", "ssc"} {
			if arg == "-l"+lib {
				libraries[lib] = true
				continue
			}
			if strings.Contains(arg, "lib"+lib+".so") {
				if !strings.HasPrefix(arg, ProviderDirectory+"/lib/") || path.Dir(path.Clean(arg)) != ProviderDirectory+"/lib" {
					return errors.New("provider library is not private")
				}
				libraries[lib+"-absolute"] = true
			}
		}
	}
	for _, lib := range []string{"qmi-glib", "ssc"} {
		if lib == "ssc" && !includeSSC {
			continue
		}
		header := "libqmi-glib"
		if lib == "ssc" {
			header = "libssc"
		}
		if !includes[ProviderDirectory+"/include/"+header] || !libraries[lib+"-absolute"] && !(privateLibDir && libraries[lib]) {
			return errors.New("private provider compile/link selection is incomplete")
		}
	}
	return nil
}

// loaderResolved recognises one named dependency resolved to an absolute path.
var loaderResolved = regexp.MustCompile(`^([^[:space:]]+) => (/[^[:space:]]+) \(0x[0-9a-fA-F]+\)$`)

// loaderAbsolute recognises the loader's absolute self-route with its address.
var loaderAbsolute = regexp.MustCompile(`^(/[^[:space:]]+) \(0x[0-9a-fA-F]+\)$`)

// loaderVirtual recognises the fixed virtual DSO row without inventing a file.
var loaderVirtual = regexp.MustCompile(`^linux-vdso\.so\.1 \(0x[0-9a-fA-F]+\)$`)

// expectedLoaderProviders fixes private runtime routes for every output binary.
func expectedLoaderProviders() map[string]map[string]string {
	qmi := ProviderDirectory + "/lib/libqmi-glib.so.5"
	ssc := ProviderDirectory + "/lib/libssc.so.2"
	return map[string]map[string]string{"libqmi-glib.so.5.12.0": {}, "libssc.so.2": {"libqmi-glib.so.5": qmi}, "ssccli": {"libqmi-glib.so.5": qmi, "libssc.so.2": ssc}, "iio-sensor-proxy": {"libqmi-glib.so.5": qmi, "libssc.so.2": ssc}, "monitor-sensor": {}}
}

// parseLoader returns private providers only after validating every loader row.
func parseLoader(text string) (map[string]string, error) {
	observed, err := parseLoaderObservations(text)
	if err != nil {
		return nil, err
	}
	return observed.providers, nil
}

// loaderObservations separates private providers from all named resolutions.
type loaderObservations struct {
	providers map[string]string
	resolved  map[string]string
}

// parseLoaderObservations refuses ambiguous, unnamed and unexpected loader routes
// while retaining system dependencies needed for direct ELF-need checks.
func parseLoaderObservations(text string) (*loaderObservations, error) {
	if strings.TrimSpace(text) == "" || strings.ContainsAny(text, "\x00\r") {
		return nil, errors.New("empty or malformed loader evidence")
	}
	providers := map[string]string{}
	resolved := map[string]string{}
	seen := map[string]bool{}
	for _, raw := range strings.Split(strings.TrimSpace(text), "\n") {
		line := strings.TrimSpace(raw)
		if loaderVirtual.MatchString(line) {
			if seen["linux-vdso.so.1"] {
				return nil, errors.New("duplicate virtual loader row")
			}
			seen["linux-vdso.so.1"] = true
			continue
		}
		if match := loaderAbsolute.FindStringSubmatch(line); match != nil {
			if match[1] != "/lib/ld-linux-aarch64.so.1" {
				return nil, errors.New("loader absolute self-route differs")
			}
			if seen[match[1]] {
				return nil, errors.New("duplicate absolute loader row")
			}
			seen[match[1]] = true
			// A real DSO may name the loader in DT_NEEDED. Its fixed absolute
			// self-row resolves that SONAME; the virtual DSO row does not.
			resolved["ld-linux-aarch64.so.1"] = match[1]
			continue
		}
		match := loaderResolved.FindStringSubmatch(line)
		if match == nil || !safeFlatName(match[1]) || seen[match[1]] || path.Clean(match[2]) != match[2] {
			return nil, errors.New("loader resolved row differs")
		}
		if match[1] == "ld-linux-aarch64.so.1" {
			return nil, errors.New("loader self-row must use the fixed absolute form")
		}
		seen[match[1]] = true
		if strings.HasPrefix(match[1], "libqmi-glib.so") || strings.HasPrefix(match[1], "libssc.so") {
			if match[1] != "libqmi-glib.so.5" && match[1] != "libssc.so.2" {
				return nil, errors.New("unexpected provider SONAME in loader evidence")
			}
			providers[match[1]] = match[2]
		} else if !strings.HasPrefix(match[2], "/lib/") && !strings.HasPrefix(match[2], "/usr/lib/") {
			return nil, errors.New("system loader dependency has unexpected route")
		}
		resolved[match[1]] = match[2]
	}
	if !seen["/lib/ld-linux-aarch64.so.1"] {
		return nil, errors.New("loader evidence is missing the fixed absolute self-row")
	}
	return &loaderObservations{providers: providers, resolved: resolved}, nil
}

// validateLoaders binds reported private origins and direct ELF needs to the
// independently parsed loader listings for the complete binary roster.
func validateLoaders(files map[string][]byte, reported []Loader) error {
	return validateLoadersProfile(files, reported, artifact047)
}

// validateLoadersProfile uses the previously selected ABI for direct-need checks.
func validateLoadersProfile(files map[string][]byte, reported []Loader, profile artifactProfile) error {
	if profile != artifact047 && profile != artifact048 {
		return errors.New("unknown compiled loader profile")
	}
	var raw map[string]string
	if err := decodeJSON(files["loader-evidence.txt"], &raw, false); err != nil {
		return fmt.Errorf("loader evidence JSON: %w", err)
	}
	expected := expectedLoaderProviders()
	if len(raw) != len(expected) || len(reported) != len(expected) {
		return errors.New("loader evidence set differs")
	}
	seen := map[string]bool{}
	for _, record := range reported {
		wanted, ok := expected[record.Artifact]
		if !ok || seen[record.Artifact] || record.Providers == nil || !reflect.DeepEqual(record.Providers, wanted) {
			return errors.New("reported loader origins differ")
		}
		seen[record.Artifact] = true
		text, ok := raw[record.Artifact]
		if !ok {
			return errors.New("raw loader record is absent")
		}
		observed, err := parseLoaderObservations(text)
		if err != nil || !reflect.DeepEqual(observed.providers, wanted) {
			return errors.New("raw loader origins differ")
		}
		var object nativeELF
		if profile == artifact048 {
			object, err = inspectNativeELF048(record.Artifact, files[record.Artifact])
		} else {
			object, err = inspectNativeELF(record.Artifact, files[record.Artifact])
		}
		if err != nil {
			return err
		}
		for dependency := range object.needed {
			if _, ok := observed.resolved[dependency]; !ok {
				return fmt.Errorf("%s: direct ELF dependency is absent from loader evidence: %s", record.Artifact, dependency)
			}
		}
	}
	return nil
}
