package quality

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var (
	// retiredProductStem encodes the former product's first word without
	// reproducing its complete retired identifier in maintained source.
	retiredProductStem = string([]byte{0x6c, 0x69, 0x6e, 0x75, 0x78})
	// retiredProductSuffix encodes the former product's second word without
	// reproducing its complete retired identifier in maintained source.
	retiredProductSuffix = string([]byte{0x61, 0x72, 0x6d, 0x65, 0x72})
)

// TestCurrentBrandingIsLexrOnly rejects every maintained occurrence of the
// former product name, including hyphenated, underscored and prose forms.
func TestCurrentBrandingIsLexrOnly(t *testing.T) {
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
		if containsRetiredProductToken(strings.ToLower(relative)) {
			issues = append(issues, fmt.Sprintf("%s: replace retired product naming in filename", relative))
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
		t.Fatalf("retired product naming found in maintained sources:\n%s", strings.Join(issues, "\n"))
	}
}

// isIgnoredQualityDirectory reports whether a directory contains repository
// metadata, generated output or dependencies rather than maintained sources.
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

// inspectBrandingFile returns every retired product token found in one bounded
// maintained regular file.
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
		line := strings.ToLower(scanner.Text())
		if containsRetiredProductToken(line) {
			issues = append(issues, fmt.Sprintf("%s:%d: replace retired product naming", relative, lineNumber))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return issues, nil
}

// retiredProductTokens returns the collapsed and separated forms previously
// used for the former product identifier or title.
func retiredProductTokens() []string {
	return []string{
		retiredProductStem + retiredProductSuffix,
		retiredProductStem + "-" + retiredProductSuffix,
		retiredProductStem + "_" + retiredProductSuffix,
		retiredProductStem + " " + retiredProductSuffix,
	}
}

// containsRetiredProductToken reports whether lowercase text contains any
// complete retired product form.
func containsRetiredProductToken(text string) bool {
	for _, target := range retiredProductTokens() {
		if strings.Contains(text, target) {
			return true
		}
	}
	return false
}
