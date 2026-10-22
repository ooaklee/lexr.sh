package compatibility

import (
	"errors"
	"regexp"
	"strings"
)

// semverPattern implements canonical SemVer 2.0 spelling without metadata.
var semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

// semverParts validates every numeric identifier without machine integer limits.
func semverParts(value string) ([]string, error) {
	if len(value) > 128 {
		return nil, errors.New("Lexr version exceeds bound")
	}
	parts := semverPattern.FindStringSubmatch(value)
	if parts == nil {
		return nil, errors.New("Lexr version must be canonical SemVer without v or build metadata")
	}
	for _, part := range strings.Split(parts[4], ".") {
		if numeric(part) && len(part) > 1 && part[0] == '0' {
			return nil, errors.New("numeric prerelease has a leading zero")
		}
	}
	return parts[1:], nil
}

// numeric recognises decimal identifiers, including arbitrary-size integers.
func numeric(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// compareNumber compares canonical decimal strings without overflow.
func compareNumber(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

// compareVersion applies SemVer precedence after validation.
func compareVersion(a, b string) int {
	x, _ := semverParts(a)
	y, _ := semverParts(b)
	for i := 0; i < 3; i++ {
		if n := compareNumber(x[i], y[i]); n != 0 {
			return n
		}
	}
	if x[3] == y[3] {
		return 0
	}
	if x[3] == "" {
		return 1
	}
	if y[3] == "" {
		return -1
	}
	left, right := strings.Split(x[3], "."), strings.Split(y[3], ".")
	for i := 0; i < len(left) && i < len(right); i++ {
		a, b := left[i], right[i]
		n := 0
		switch {
		case numeric(a) && numeric(b):
			n = compareNumber(a, b)
		case numeric(a):
			n = -1
		case numeric(b):
			n = 1
		default:
			n = strings.Compare(a, b)
		}
		if n != 0 {
			return n
		}
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

// runtimeVersion distinguishes unknown builds from a valid precedence identity.
func runtimeVersion(value string) (string, bool) {
	base, metadata, hasMetadata := strings.Cut(value, "+")
	if hasMetadata && !regexp.MustCompile(`^[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*$`).MatchString(metadata) {
		return "", false
	}
	lower := strings.ToLower(value)
	if regexp.MustCompile(`(?:^|[.+-])(?:dirty|dev|unknown)(?:$|[.+-])`).MatchString(lower) {
		return "", false
	}
	if _, err := semverParts(base); err != nil {
		return "", false
	}
	return base, true
}

// osVersion validates a distribution-specific persisted version identity.
func osVersion(id, value string) bool {
	if len(value) > 32 {
		return false
	}
	switch id {
	case "ubuntu":
		return regexp.MustCompile(`^[0-9]{2}\.(04|10)$`).MatchString(value)
	case "fedora", "debian":
		return regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(value)
	case "elementary":
		return regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(value)
	}
	return false
}

// compareOS orders versions only after a caller has established the exact ID.
func compareOS(id, a, b string) int {
	if id == "ubuntu" {
		return strings.Compare(a, b)
	}
	if id == "elementary" {
		x, y := strings.Split(a, "."), strings.Split(b, ".")
		if n := compareNumber(x[0], y[0]); n != 0 {
			return n
		}
		return compareNumber(x[1], y[1])
	}
	return compareNumber(a, b)
}
