package ubuntu

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// validateInstallerProductInfo checks the source ISO identity consumed by
// Ubuntu Desktop Bootstrap. Its product parser slices before the first quote
// without guarding against a missing quote, which makes a custom unquoted
// label abort installer startup. Keep the original bytes, including the release
// and quoted codename; Lexr provenance belongs under /sp11 instead.
func validateInstallerProductInfo(workspace string) error {
	data, err := readBoundedExtractedFile(workspace, "disk-info", maximumValidationTextBytes)
	if err != nil {
		return fmt.Errorf("read Ubuntu .disk/info: %w", err)
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return errors.New("Ubuntu .disk/info must be UTF-8 text without NUL bytes")
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		quote := strings.IndexByte(line, '"')
		if quote < 2 || line[quote-1] != ' ' || strings.TrimSpace(line[:quote-1]) == "" {
			return errors.New("Ubuntu .disk/info must contain a product name followed by a space and quoted codename")
		}
		codename := line[quote+1:]
		end := strings.IndexByte(codename, '"')
		if end < 1 || strings.TrimSpace(codename[:end]) == "" {
			return errors.New("Ubuntu .disk/info must contain a non-empty, closed quoted codename")
		}
		return nil
	}
	return errors.New("Ubuntu .disk/info has no product identity line")
}
