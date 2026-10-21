package quality

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// americanEnglishPattern identifies unambiguous American spellings for which
// this project has selected a British equivalent in public prose.
var americanEnglishPattern = regexp.MustCompile(`(?i)\b(?:artifacts?|behaviors?|behavioral|canonicaliz(?:e|es|ed|ing|ation)|containeriz(?:e|es|ed|ing|ation)|dockeriz(?:e|es|ed|ing|ation)|generaliz(?:e|es|ed|ing|ation)|neutraliz(?:e|es|ed|ing|ation)|prioritiz(?:e|es|ed|ing|ation)|randomiz(?:e|es|ed|ing|ation)|stabiliz(?:e|es|ed|ing|ation)|synthesiz(?:e|es|ed|ing|ation)|normaliz(?:e|es|ed|ing|ation)|recogniz(?:e|es|ed|ing)|unrecognized|initializ(?:e|es|ed|ing|ation)|uninitializ(?:e|es|ed|ing)|authoriz(?:e|es|ed|ing|ation)|serializ(?:e|es|ed|ing|ation|able)|organiz(?:e|es|ed|ing|ation)|colors?|colored|licenses?|defenses?|centers?|centered|centering|analyz(?:e|es|ed|ing)|optimiz(?:e|es|ed|ing|ation)|customiz(?:e|es|ed|ing)|canceled|canceling|labeled|labeling|modeled|modeling|materializ(?:e|es|ed|ing)|summariz(?:e|es|ed|ing)|sanitiz(?:e|es|ed|ing)|unsanitiz(?:e|es|ed|ing)|favors?|favored|honors?|honored|honoring)\b`)

// markdownURLPattern removes link destinations because their spellings belong
// to external resource identifiers rather than editable project prose.
var markdownURLPattern = regexp.MustCompile(`https?://[^\s)>]+`)

// markdownLinkDestinationPattern removes local link targets because filenames
// and stable slugs are identifiers rather than editable project prose.
var markdownLinkDestinationPattern = regexp.MustCompile(`\]\([^)]*\)`)

// sourcePathIdentifierPattern recognises canonical relative provenance paths,
// not sentences, absolute paths or traversal components.
var sourcePathIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~-]*(/[A-Za-z0-9][A-Za-z0-9._+~-]*)+$`)

// publicSourceHostPattern recognises fully qualified DNS reference hosts rather
// than local hostnames, IP addresses or credential-bearing authorities.
var publicSourceHostPattern = regexp.MustCompile(`(?i)^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

// TestDocumentationUsesBritishEnglish scans Lexr's Go and PowerShell comments
// plus its public Markdown and JSON prose so future changes preserve the
// project's chosen language style.
func TestDocumentationUsesBritishEnglish(t *testing.T) {
	repositoryRoot := lexrRepositoryRoot(t)
	var issues []string
	fileSet := token.NewFileSet()
	err := filepath.WalkDir(repositoryRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || path == filepath.Join(repositoryRoot, "build") || path == filepath.Join(repositoryRoot, "bin") {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".go":
			parsed, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
			if err != nil {
				return err
			}
			for _, group := range parsed.Comments {
				for _, comment := range group.List {
					// Compiler directives name paths and symbols, not public prose.
					if strings.HasPrefix(comment.Text, "//go:") {
						continue
					}
					text := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(comment.Text, "//"), "/*"))
					// Stable Go identifiers must begin their documentation with their
					// exact source spelling even when the public prose uses another form.
					for _, identifier := range []string{"Artifact", "Artifacts", "Recognized", "artifact", "artifacts"} {
						text = strings.TrimPrefix(text, identifier+" ")
					}
					text = strings.TrimPrefix(text, "Package artifact ")
					if spelling := americanEnglishPattern.FindString(text); spelling != "" {
						position := fileSet.Position(comment.Pos())
						relative, _ := filepath.Rel(repositoryRoot, path)
						issues = append(issues, fmt.Sprintf("%s:%d: replace %q with British English", filepath.ToSlash(relative), position.Line, spelling))
					}
				}
			}
		case ".md", ".json":
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			scanner := bufio.NewScanner(file)
			lineNumber := 0
			inFence := false
			for scanner.Scan() {
				lineNumber++
				line := scanner.Text()
				if filepath.Ext(path) == ".md" && strings.HasPrefix(strings.TrimSpace(line), "id:") {
					continue
				}
				if filepath.Ext(path) == ".md" && strings.HasPrefix(strings.TrimSpace(line), "```") {
					inFence = !inFence
					continue
				}
				if inFence {
					continue
				}
				prose := line
				if filepath.Ext(path) == ".md" {
					prose = markdownProse(line)
				} else {
					prose = jsonProse(line)
				}
				if spelling := americanEnglishPattern.FindString(prose); spelling != "" {
					relative, _ := filepath.Rel(repositoryRoot, path)
					issues = append(issues, fmt.Sprintf("%s:%d: replace %q with British English", filepath.ToSlash(relative), lineNumber, spelling))
				}
			}
			scanErr := scanner.Err()
			closeErr := file.Close()
			if scanErr != nil || closeErr != nil {
				return fmt.Errorf("scan British-English documentation %s: %w", path, errors.Join(scanErr, closeErr))
			}
		case ".ps1":
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, comment := range powerShellCommentLines(string(content)) {
				if spelling := americanEnglishPattern.FindString(comment.text); spelling != "" {
					relative, _ := filepath.Rel(repositoryRoot, path)
					issues = append(issues, fmt.Sprintf("%s:%d: replace %q with British English", filepath.ToSlash(relative), comment.line, spelling))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		sort.Strings(issues)
		t.Fatalf("American spellings found in documentation:\n%s", strings.Join(issues, "\n"))
	}
}

// powerShellComment is one comment fragment and its one-based source line.
type powerShellComment struct {
	// line is the one-based source line containing the comment.
	line int
	// text is the editable comment prose without comment delimiters.
	text string
}

// powerShellCommentLines extracts PowerShell block and line comments plus
// embedded C# XML summaries while leaving identifiers and string values alone.
func powerShellCommentLines(content string) []powerShellComment {
	comments := make([]powerShellComment, 0)
	inBlock := false
	for index, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "<#") {
			inBlock = true
			trimmed = strings.TrimSpace(strings.SplitN(trimmed, "<#", 2)[1])
		}
		if inBlock {
			if end := strings.Index(trimmed, "#>"); end >= 0 {
				trimmed = trimmed[:end]
				inBlock = false
			}
			comments = append(comments, powerShellComment{line: index + 1, text: trimmed})
			continue
		}
		if strings.HasPrefix(trimmed, "///") {
			comments = append(comments, powerShellComment{line: index + 1, text: strings.TrimSpace(strings.TrimPrefix(trimmed, "///"))})
			continue
		}
		if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "#requires") {
			comments = append(comments, powerShellComment{line: index + 1, text: strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))})
		}
	}
	return comments
}

// markdownProse removes inline code spans so stable identifiers and preserved
// command names are not mistaken for editable public prose.
func markdownProse(line string) string {
	line = markdownURLPattern.ReplaceAllString(line, " ")
	line = markdownLinkDestinationPattern.ReplaceAllString(line, "]")
	var prose strings.Builder
	inCode := false
	delimiterLength := 0
	for index := 0; index < len(line); {
		if line[index] != '`' {
			if !inCode {
				prose.WriteByte(line[index])
			}
			index++
			continue
		}
		runEnd := index + 1
		for runEnd < len(line) && line[runEnd] == '`' {
			runEnd++
		}
		runLength := runEnd - index
		if !inCode {
			inCode = true
			delimiterLength = runLength
		} else if runLength == delimiterLength {
			inCode = false
			delimiterLength = 0
			prose.WriteByte(' ')
		}
		index = runEnd
	}
	return prose.String()
}

// jsonProse leaves JSON prose checked while recognising only a complete string
// member in the provenance fields whose values are source identifiers.
func jsonProse(line string) string {
	member := strings.TrimSuffix(strings.TrimSpace(line), ",")
	decoder := json.NewDecoder(strings.NewReader("{" + member + "}"))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return line
	}
	key, err := decoder.Token()
	if err != nil {
		return line
	}
	var value string
	if err := decoder.Decode(&value); err != nil {
		return line
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return line
	}
	if _, err := decoder.Token(); err != io.EOF {
		return line
	}
	switch key {
	case "source_path":
		if sourcePathIdentifierPattern.MatchString(value) {
			return ""
		}
	case "source_url", "source_repository":
		if publicSourceURL(value) {
			return ""
		}
	case "notice":
		// Preserve the formal source title without exempting the surrounding prose.
		return strings.ReplaceAll(value, "GNU General Public License version 2", "GNU GPLv2")
	}
	return line
}

// publicSourceURL checks an HTTPS source reference structurally; it makes no
// claim about the remote resource's availability or ownership.
func publicSourceURL(value string) bool {
	reference, err := url.ParseRequestURI(value)
	if err != nil || reference.Scheme != "https" || reference.User != nil ||
		reference.Host != reference.Hostname() || !publicSourceHostPattern.MatchString(reference.Hostname()) ||
		reference.RawQuery != "" || reference.Fragment != "" || reference.Path == "" ||
		strings.ContainsAny(value, "# \t\r\n\\") {
		return false
	}
	host := strings.ToLower(reference.Hostname())
	for _, suffix := range []string{".localhost", ".local", ".internal", ".invalid", ".test"} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	return true
}

// TestMarkdownProseExcludesIdentifiersAndURLs verifies the prose scanner keeps
// editable words while ignoring stable inline code and external destinations.
func TestMarkdownProseExcludesIdentifiersAndURLs(t *testing.T) {
	input := "Review behavior beside `API behavior`, [an artefact](release-artifacts.md), and <https://example.org/behavior>."
	got := markdownProse(input)
	if strings.Count(got, "behavior") != 1 {
		t.Fatalf("markdownProse() retained %q, want only the editable prose occurrence", got)
	}
	if strings.Contains(got, "release-artifacts") {
		t.Fatalf("markdownProse() retained local link destination in %q", got)
	}
}

// TestJSONProseRecognisesOnlyStructuredProvenance checks that source identifiers
// and a formal title do not exempt malformed values or editable notice prose.
func TestJSONProseRecognisesOnlyStructuredProvenance(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		flagged bool
	}{
		{"source path", `"source_path": "LICENSES/preferred/GPL-2.0",`, false},
		{"source URL", `"source_url": "https://github.com/example/kernel/blob/main/LICENSES/preferred/GPL-2.0",`, false},
		{"source repository", `"source_repository": "https://example.org/behavior",`, false},
		{"formal title", `"notice": "The source states GNU General Public License version 2. Retain its licence text.",`, false},
		{"title with editable prose", `"notice": "GNU General Public License version 2 describes behavior.",`, true},
		{"ordinary notice", `"notice": "Retain the original License and behavior.",`, true},
		{"unknown field", `"description": "LICENSES/preferred/GPL-2.0",`, true},
		{"unknown URL field", `"description": "https://example.org/behavior",`, true},
		{"plain path prose", `"source_path": "License for everyone",`, true},
		{"single component", `"source_path": "LICENSES",`, true},
		{"absolute path", `"source_path": "/LICENSES/GPL-2.0",`, true},
		{"traversal path", `"source_path": "LICENSES/../GPL-2.0",`, true},
		{"empty component", `"source_path": "LICENSES//GPL-2.0",`, true},
		{"dot component", `"source_path": "LICENSES/./GPL-2.0",`, true},
		{"backslash path", `"source_path": "LICENSES\\GPL-2.0",`, true},
		{"nonstring value", `"source_path": ["LICENSES/GPL-2.0"],`, true},
		{"multiple members", `"source_path": "LICENSES/GPL-2.0", "notice": "behavior",`, true},
		{"duplicate member", `"source_path": "LICENSES/GPL-2.0", "source_path": "LICENSES/GPL-2.0",`, true},
		{"invalid JSON", `"source_path": "LICENSES/GPL-2.0,`, true},
		{"URL prose suffix", `"source_url": "https://example.org/behavior for everyone",`, true},
		{"URL credentials", `"source_url": "https://user@example.org/behavior",`, true},
		{"URL missing host", `"source_url": "https:///behavior",`, true},
		{"URL local host", `"source_url": "https://localhost/behavior",`, true},
		{"URL local suffix", `"source_url": "https://kernel.local/behavior",`, true},
		{"URL IP address", `"source_url": "https://127.0.0.1/behavior",`, true},
		{"URL query", `"source_url": "https://example.org/behavior?notice=behavior",`, true},
		{"URL fragment", `"source_url": "https://example.org/behavior#behavior",`, true},
		{"URL non-HTTPS", `"source_url": "http://example.org/behavior",`, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prose := jsonProse(test.line)
			if flagged := americanEnglishPattern.MatchString(prose); flagged != test.flagged {
				t.Fatalf("jsonProse() = %q, flagged = %v, want %v", prose, flagged, test.flagged)
			}
		})
	}
}
