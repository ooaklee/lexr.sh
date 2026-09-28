package compatibility

import (
	"bufio"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// osIDPattern implements os-release's bounded, exact ID vocabulary.
var osIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ReadOSRelease inspects only the selected root, with in-root symlink handling
// and no shell evaluation, target binary execution, ID_LIKE or host fallback.
func ReadOSRelease(directory string) (id, version string, err error) {
	if directory == "" {
		return "", "", errors.New("target root is required")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	file, err := OpenRegular(root, "etc/os-release")
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", "", errors.New("target os-release is not regular")
	}
	if info.Size() > MaxBytes {
		return "", "", errors.New("target os-release exceeds bound")
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
	if err != nil {
		return "", "", err
	}
	if len(data) > MaxBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return "", "", errors.New("invalid bounded UTF-8 os-release")
	}
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024), MaxBytes)
	count := 0
	for scanner.Scan() {
		count++
		if count > 512 {
			return "", "", errors.New("too many os-release lines")
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			return "", "", errors.New("malformed os-release assignment")
		}
		if key != "ID" && key != "VERSION_ID" {
			continue
		}
		if _, duplicate := values[key]; duplicate {
			return "", "", errors.New("duplicate os-release identity field")
		}
		if strings.HasPrefix(value, `"`) {
			if len(value) < 2 || value[len(value)-1] != '"' {
				return "", "", errors.New("malformed quoted os-release identity")
			}
			// Registered identity values need no escapes. Do not interpret Go
			// or JSON escapes as shell-compatible os-release syntax.
			value = value[1 : len(value)-1]
		} else if strings.HasPrefix(value, "'") {
			if len(value) < 2 || value[len(value)-1] != '\'' {
				return "", "", errors.New("malformed quoted os-release identity")
			}
			value = value[1 : len(value)-1]
		}
		if value == "" || len(value) > 64 || strings.ContainsAny(value, " \t\r\n\\\"'$`") {
			return "", "", errors.New("malformed os-release identity value")
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return "", "", err
	}
	if !osIDPattern.MatchString(values["ID"]) || values["VERSION_ID"] == "" {
		return "", "", errors.New("target requires exact ID and VERSION_ID")
	}
	return values["ID"], values["VERSION_ID"], nil
}
