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
}

func (v *VerifiedArtifacts) Files() []Artifact {
	if v == nil || !v.valid {
		return nil
	}
	return append([]Artifact(nil), v.artifacts...)
}
func (v *VerifiedArtifacts) Links() []Link {
	if v == nil || !v.valid {
		return nil
	}
	return append([]Link(nil), v.links...)
}
func (v *VerifiedArtifacts) Data(name string) ([]byte, bool) {
	if v == nil || !v.valid {
		return nil, false
	}
	data, ok := v.files[name]
	return bytes.Clone(data), ok
}
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

func bytesDigest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

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
// artifact contract. No pathname from either stream is extracted or followed.
func DecodeDockerCopy(data []byte, expectedJobs int) (*VerifiedArtifacts, error) {
	return decodeDockerCopy(data, expectedJobs, expectedFiles())
}

func decodeDockerCopy(data []byte, expectedJobs int, rules map[string]fileRule) (*VerifiedArtifacts, error) {
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
	return decodeArtifactArchive(inner, expectedJobs, rules)
}

// DecodeArtifactArchive requires the compiled roster, not merely the roster in
// the recipe's descriptive report. It independently hashes and inspects all
// received bytes. This does not construct release or installation authority.
func DecodeArtifactArchive(data []byte, expectedJobs int) (*VerifiedArtifacts, error) {
	return decodeArtifactArchive(data, expectedJobs, expectedFiles())
}

// Only package tests substitute licence bytes. Exported production entry points
// always use expectedFiles' compiled licence authorities and the real ELF check.
func decodeArtifactArchive(data []byte, expectedJobs int, rules map[string]fileRule) (*VerifiedArtifacts, error) {
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
	if err := validateNativeELFs(files); err != nil {
		return nil, err
	}
	report, err := validateEvidence(files, expectedJobs)
	if err != nil {
		return nil, err
	}
	artifacts := make([]Artifact, 0, len(files))
	for name, data := range files {
		artifacts = append(artifacts, Artifact{name, int64(len(data)), bytesDigest(data), rules[name].mode})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Name < artifacts[j].Name })
	return &VerifiedArtifacts{files: files, artifacts: artifacts, links: links, report: report, valid: true}, nil
}

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

func validateEvidence(files map[string][]byte, jobs int) (BuildReport, error) {
	var report BuildReport
	for _, name := range []string{ReportName, "testlog.json", "testlog.txt", "qmi-dependencies.json", "ssc-dependencies.json", "proxy-dependencies.json", "loader-evidence.txt"} {
		data := files[name]
		if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 || !bytes.HasSuffix(data, []byte("\n")) {
			return report, fmt.Errorf("evidence text differs: %s", name)
		}
	}
	if err := decodeJSON(files[ReportName], &report, true); err != nil {
		return report, fmt.Errorf("build report: %w", err)
	}
	if report.Schema != "lexr-sensors-source-build-v1" || report.SourceCommit != SourceCommit || report.Jobs != jobs || !reflect.DeepEqual(report.Fixtures, FixtureTargets()) || !reflect.DeepEqual(report.ProxyInstallTargets, ProxyInstallTargets()) {
		return report, errors.New("build report contract differs")
	}
	if report.VolumeFreeBeforeBytes < 6<<30 || report.JobApparentBeforeArchiveBytes <= 0 || report.MemoryPeakBytes != nil && *report.MemoryPeakBytes <= 0 {
		return report, errors.New("build resource observations differ")
	}
	seen := map[string]bool{}
	var selectedBytes int64
	for _, a := range report.Products {
		data, ok := files[a.Name]
		if !ok || a.Name == ReportName || seen[a.Name] || a.Bytes != int64(len(data)) || a.SHA256 != bytesDigest(data) || a.Mode != expectedFiles()[a.Name].mode {
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
		"libssc":   {"libssc", "0.4.7", ProviderDirectory + "/lib/pkgconfig", ProviderDirectory + "/lib/libssc.so.2", bytesDigest(files["libssc.so.2"])},
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
	if err := validateDependencies(files); err != nil {
		return report, err
	}
	if err := validateLoaders(files, report.Loaders); err != nil {
		return report, err
	}
	return report, nil
}

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

type dependencyEvidence struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	CompileArgs []string `json:"compile_args"`
	LinkArgs    []string `json:"link_args"`
}

func validateDependencies(files map[string][]byte) error {
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
				expectedVersion = "0.4.7"
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

var loaderResolved = regexp.MustCompile(`^([^[:space:]]+) => (/[^[:space:]]+) \(0x[0-9a-fA-F]+\)$`)
var loaderAbsolute = regexp.MustCompile(`^(/[^[:space:]]+) \(0x[0-9a-fA-F]+\)$`)
var loaderVirtual = regexp.MustCompile(`^linux-vdso\.so\.1 \(0x[0-9a-fA-F]+\)$`)

func expectedLoaderProviders() map[string]map[string]string {
	qmi := ProviderDirectory + "/lib/libqmi-glib.so.5"
	ssc := ProviderDirectory + "/lib/libssc.so.2"
	return map[string]map[string]string{"libqmi-glib.so.5.12.0": {}, "libssc.so.2": {"libqmi-glib.so.5": qmi}, "ssccli": {"libqmi-glib.so.5": qmi, "libssc.so.2": ssc}, "iio-sensor-proxy": {"libqmi-glib.so.5": qmi, "libssc.so.2": ssc}, "monitor-sensor": {}}
}

func parseLoader(text string) (map[string]string, error) {
	observed, err := parseLoaderObservations(text)
	if err != nil {
		return nil, err
	}
	return observed.providers, nil
}

type loaderObservations struct {
	providers map[string]string
	resolved  map[string]string
}

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

func validateLoaders(files map[string][]byte, reported []Loader) error {
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
		object, err := inspectNativeELF(record.Artifact, files[record.Artifact])
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
