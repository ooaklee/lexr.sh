package quality

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	// legacyProductSlug is assembled so the quality gate examines its own source
	// without requiring a blanket file exemption.
	legacyProductSlug = "linux" + "-armer"
	// legacyProductTitle is the retired human-facing product name.
	legacyProductTitle = "Linux" + " Armer"
	// legacyModulePath is forbidden even in historical prose because it can
	// silently restore imports from the former nested module.
	legacyModulePath = "github.com/ooaklee/linux-surface-pro-11-oe/cli/" + legacyProductSlug
	// historicalADRPath identifies the immutable decisions written before the
	// standalone repository and product rename.
	historicalADRPath = regexp.MustCompile(`^docs/adr/adr-(?:00[1-9]|01[0-9]|02[0-2])-[^/]+\.md$`)
	// approvedLegacyIdentifier matches only stable protocol, schema-3 media,
	// installed-state, receipt, and OE release identities retained for
	// compatibility. It deliberately excludes transient and product prose.
	approvedLegacyIdentifier = regexp.MustCompile(strings.Join([]string{
		regexp.QuoteMeta(legacyProductSlug) + `\.windows-handoff(?:[A-Za-z0-9._/-]*)?`,
		regexp.QuoteMeta(legacyProductSlug) + `-windows-handoff\.json`,
		`\.` + regexp.QuoteMeta(legacyProductSlug) + `-handoffs`,
		regexp.QuoteMeta(legacyProductSlug) + `-(?:kernel-bundle|kernel-build-provenance|kernel-release-manifest|userspace-bundle)\.json`,
		regexp.QuoteMeta(legacyProductSlug) + `-manifest\.json`,
		`(?:etc|usr/lib|usr/libexec|var/lib)/` + regexp.QuoteMeta(legacyProductSlug) + `(?:[A-Za-z0-9._/-]*)?`,
		`(?:05-)?` + regexp.QuoteMeta(legacyProductSlug) + `-(?:refresh-sp11-boot|sp11-dtb(?:-remove)?|sp11-[A-Za-z0-9*._-]+)`,
		`bluetooth-` + regexp.QuoteMeta(legacyProductSlug) + `-binary`,
		`bin/linux-arm64/` + regexp.QuoteMeta(legacyProductSlug),
		regexp.QuoteMeta(legacyProductSlug) + `/tools/collect-sp11-windows-handoff\.ps1`,
		regexp.QuoteMeta(legacyProductSlug) + `_(?:[A-Za-z0-9.+-]+|<version>)_source\.tar\.gz`,
		regexp.QuoteMeta(legacyProductSlug) + `-\*\.json`,
		regexp.QuoteMeta(legacyProductSlug) + `-kernel-build-[0-9a-f]{16}`,
	}, `|`))
	// approvedLegacyContexts permits source representations which cannot expose
	// the whole compatibility path as one literal, plus narrowly worded rename
	// explanations in current documentation.
	approvedLegacyContexts = []legacyBrandAllowance{
		newLegacyBrandAllowance(`^internal/cleanup/(?:anchored\.go|cleanup_test\.go)$`, `"`+regexp.QuoteMeta(legacyProductSlug)+`"`),
		newLegacyBrandAllowance(`^internal/cli/root_test\.go$`, `filepath\.Join\(root, "var", "lib", "`+regexp.QuoteMeta(legacyProductSlug)+`", "backups"\)`),
		newLegacyBrandAllowance(`^internal/cli/handoff\.go$`, `\.`+regexp.QuoteMeta(legacyProductSlug)+`-handoffs`),
		newLegacyBrandAllowance(`^internal/image/companion/archive\.go$`, `path\.Join\("`+regexp.QuoteMeta(legacyProductSlug)+`"`),
		newLegacyBrandAllowance(`^internal/image/companion/(?:builder|validate)\.go$`, regexp.QuoteMeta(legacyProductSlug)+`_%s_source\.tar\.gz`),
		newLegacyBrandAllowance(`^internal/image/companion/builder_test\.go$`, regexp.QuoteMeta(legacyProductSlug)+`/(?:README\.md|bin/lexr|lexr|output\.iso|LICENSE|tools/collect-sp11-windows-handoff\.ps1)`),
		newLegacyBrandAllowance(`^internal/image/manifest_test\.go$`, `sp11/companion/(?:bin/`+regexp.QuoteMeta(legacyProductSlug)+`|source/`+regexp.QuoteMeta(legacyProductSlug)+`\.tar\.gz)`),
		newLegacyBrandAllowance(`^internal/image/ubuntu/validate\.go$`, `filepath\.Join\([^\n]*"`+regexp.QuoteMeta(legacyProductSlug)+`"`),
		newLegacyBrandAllowance(`^internal/kernel/releaseprep/plan\.go$`, regexp.QuoteMeta(legacyProductSlug)+`\)-kernel-build-`),
		newLegacyBrandAllowance(`^README\.md$`, `previously lived[^\n]*`+regexp.QuoteMeta(legacyProductTitle)),
		newLegacyBrandAllowance(`^README\.md$`, "`"+regexp.QuoteMeta(legacyProductSlug)+"` identifiers"),
		newLegacyBrandAllowance(`^CHANGELOG\.md$`, `from `+regexp.QuoteMeta(legacyProductTitle)+` to Lexr\.sh`),
		newLegacyBrandAllowance(`^docs/adr/adr-023-[^/]+\.md$`, `began as `+regexp.QuoteMeta(legacyProductTitle)),
		newLegacyBrandAllowance(`^docs/adr/adr-023-[^/]+\.md$`, "cli/"+regexp.QuoteMeta(legacyProductSlug)+"(?:/|`)"),
		newLegacyBrandAllowance(`^docs/adr/adr-023-[^/]+\.md$`, regexp.QuoteMeta(legacyProductTitle)+` name`),
		newLegacyBrandAllowance(`^docs/adr/adr-023-[^/]+\.md$`, "`"+regexp.QuoteMeta(legacyProductSlug)+"` value"),
	}
)

// legacyBrandAllowance binds one precise repository path to a line fragment
// which contains a reviewed legacy token.
type legacyBrandAllowance struct {
	// path selects the exact source context that owns the compatibility value.
	path *regexp.Regexp
	// line selects the complete identifier or explicit migration explanation.
	line *regexp.Regexp
}

// newLegacyBrandAllowance compiles one reviewed path-and-line allowance.
func newLegacyBrandAllowance(path, line string) legacyBrandAllowance {
	return legacyBrandAllowance{path: regexp.MustCompile(path), line: regexp.MustCompile(line)}
}

// TestCurrentBrandingAndCompatibilityBoundaries rejects the former module and
// product branding while preserving the exact reviewed compatibility surface.
func TestCurrentBrandingAndCompatibilityBoundaries(t *testing.T) {
	repositoryRoot := lexrRepositoryRoot(t)
	var issues []string
	err := filepath.WalkDir(repositoryRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if isIgnoredQualityDirectory(repositoryRoot, path, entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !isBrandingSource(relative) {
			return nil
		}
		fileIssues, err := inspectBrandingFile(path, relative)
		if err != nil {
			return err
		}
		issues = append(issues, fileIssues...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		sort.Strings(issues)
		t.Fatalf("retired product branding found outside reviewed compatibility identifiers:\n%s", strings.Join(issues, "\n"))
	}
}

// isIgnoredQualityDirectory reports whether a directory contains repository
// metadata, generated output, or dependencies rather than maintained sources.
func isIgnoredQualityDirectory(root, path, name string) bool {
	if name == ".git" || name == "vendor" {
		return true
	}
	for _, relative := range []string{"bin", "build", "dist"} {
		if path == filepath.Join(root, relative) {
			return true
		}
	}
	return false
}

// isBrandingSource selects maintained textual formats whose product naming is
// controlled by this repository.
func isBrandingSource(relative string) bool {
	if relative == ".gitignore" {
		return true
	}
	switch filepath.Ext(relative) {
	case ".go", ".json", ".md", ".mod", ".ps1", ".toml", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

// inspectBrandingFile returns every forbidden legacy occurrence in one bounded
// regular textual source file.
func inspectBrandingFile(path, relative string) ([]string, error) {
	information, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !information.Mode().IsRegular() || information.Mode()&os.ModeSymlink != 0 || information.Size() > 8<<20 {
		return nil, fmt.Errorf("branding source is not a bounded non-symlink regular file: %s", relative)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	var issues []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := scanner.Text()
		if strings.Contains(line, legacyModulePath) {
			issues = append(issues, fmt.Sprintf("%s:%d: former Go module path", relative, lineNumber))
			continue
		}
		for _, target := range []string{legacyProductSlug, legacyProductTitle} {
			for _, occurrence := range stringOccurrences(line, target) {
				if !legacyBrandOccurrenceAllowed(relative, line, occurrence[0], occurrence[1]) {
					issues = append(issues, fmt.Sprintf("%s:%d: replace retired product token %q", relative, lineNumber, target))
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return issues, nil
}

// stringOccurrences returns the half-open byte ranges of every non-overlapping
// target occurrence in line.
func stringOccurrences(line, target string) [][2]int {
	var occurrences [][2]int
	for offset := 0; offset < len(line); {
		index := strings.Index(line[offset:], target)
		if index < 0 {
			break
		}
		start := offset + index
		end := start + len(target)
		occurrences = append(occurrences, [2]int{start, end})
		offset = end
	}
	return occurrences
}

// legacyBrandOccurrenceAllowed reports whether one retired token is contained
// by an exact compatibility identifier or historical ADR prose allowance.
func legacyBrandOccurrenceAllowed(relative, line string, start, end int) bool {
	if historicalADRPath.MatchString(relative) {
		return true
	}
	for _, match := range approvedLegacyIdentifier.FindAllStringIndex(line, -1) {
		if match[0] <= start && match[1] >= end {
			return true
		}
	}
	for _, allowance := range approvedLegacyContexts {
		if !allowance.path.MatchString(relative) {
			continue
		}
		for _, match := range allowance.line.FindAllStringIndex(line, -1) {
			if match[0] <= start && match[1] >= end {
				return true
			}
		}
	}
	return false
}

// TestLegacyBrandAllowanceIsNarrow proves compatibility identities are allowed
// without accepting general product prose, transient names, or old imports.
func TestLegacyBrandAllowanceIsNarrow(t *testing.T) {
	stable := "/sp11/" + legacyProductSlug + "-manifest.json"
	stableOccurrence := strings.Index(stable, legacyProductSlug)
	if !legacyBrandOccurrenceAllowed("internal/image/manifest.go", stable, stableOccurrence, stableOccurrence+len(legacyProductSlug)) {
		t.Fatal("schema-3 media identity was not allowed")
	}
	explanation := "The project began as " + legacyProductTitle + " beneath `cli/" + legacyProductSlug + "` in the Surface Pro"
	for _, target := range []string{legacyProductTitle, legacyProductSlug} {
		start := strings.Index(explanation, target)
		if !legacyBrandOccurrenceAllowed("docs/adr/adr-023-lexr-standalone-repository-and-compatibility.md", explanation, start, start+len(target)) {
			t.Fatalf("explicit ADR023 migration explanation was not allowed for %q", target)
		}
	}
	for _, line := range []string{
		legacyProductSlug + " builds an image",
		legacyProductTitle + " release",
		"." + legacyProductSlug + "-temporary",
	} {
		target := legacyProductSlug
		if strings.Contains(line, legacyProductTitle) {
			target = legacyProductTitle
		}
		start := strings.Index(line, target)
		if legacyBrandOccurrenceAllowed("README.md", line, start, start+len(target)) {
			t.Fatalf("unapproved branding was allowed: %q", line)
		}
	}
}
