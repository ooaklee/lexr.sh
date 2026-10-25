package relocation

import (
	"errors"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/sensors/packageplan"
)

var (
	// resolvedRow recognises one named library and its absolute resolved route.
	resolvedRow = regexp.MustCompile(`^([^[:space:]]+) => (/[^[:space:]]+) \(0x[0-9a-fA-F]+\)$`)
	// absoluteRow recognises the loader's absolute interpreter self-route.
	absoluteRow = regexp.MustCompile(`^(/[^[:space:]]+) \(0x[0-9a-fA-F]+\)$`)
	// virtualRow recognises the fixed virtual DSO without inventing a file.
	virtualRow = regexp.MustCompile(`^linux-vdso\.so\.1 \(0x[0-9a-fA-F]+\)$`)
	// flatName bounds a safe single-component dependency SONAME.
	flatName = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,128}$`)
)

// safeName refuses dependency names containing traversal or path separators.
func safeName(name string) bool {
	return flatName.MatchString(name) && name != "." && name != ".."
}

// providerRoutes fixes the private providers permitted for each ELF role.
func providerRoutes(name string) map[string]string {
	qmi := packageplan.PrivatePrefix + "/lib/libqmi-glib.so.5"
	ssc := packageplan.PrivatePrefix + "/lib/libssc.so.2"
	switch name {
	case "lib/libqmi-glib.so.5.12.0", "bin/monitor-sensor":
		return map[string]string{}
	case "lib/libssc.so.2":
		return map[string]string{"libqmi-glib.so.5": qmi}
	case "bin/ssccli", "libexec/iio-sensor-proxy":
		return map[string]string{"libqmi-glib.so.5": qmi, "libssc.so.2": ssc}
	default:
		return nil
	}
}

// systemNames is the closed dependency inventory of the accepted pinned native
// builder image. This is deliberately not a claim about other distributions or
// future image revisions. It is transcribed from the accepted development
// archive's loader-evidence.txt, SHA256
// b0ad84328493568050f6b5315f29e27cf0622a81fb00142e52971c247a81d23b.
// Paths may use either standard library directory, but a new or missing SONAME
// requires separate qualification, not a silent skip.
func systemNames(name string) map[string]bool {
	names := []string{"libgio-2.0.so.0", "libgobject-2.0.so.0", "libglib-2.0.so.0", "libc.so.6", "libgmodule-2.0.so.0", "libz.so.1", "libmount.so.1", "libselinux.so.1", "libffi.so.8", "libatomic.so.1", "libm.so.6", "libpcre2-8.so.0", "libblkid.so.1", "ld-linux-aarch64.so.1"}
	if name != "bin/monitor-sensor" {
		names = append(names, "libqrtr-glib.so.0")
	}
	if name == "lib/libssc.so.2" || name == "bin/ssccli" || name == "libexec/iio-sensor-proxy" {
		names = append(names, "libprotobuf-c.so.1")
	}
	if name == "libexec/iio-sensor-proxy" {
		names = append(names, "libgudev-1.0.so.0", "libpolkit-gobject-1.so.0", "libudev.so.1", "libsystemd.so.0")
	}
	result := make(map[string]bool, len(names))
	for _, soname := range names {
		result[soname] = true
	}
	return result
}

// verifyListing consumes every row. There is no address-only-line cleanup or
// transformation which could conceal an unnamed, missing or ambiguous route.
func verifyListing(name string, context elfContext, text string) error {
	wanted := providerRoutes(name)
	if wanted == nil || len(text) == 0 || !validText(text) {
		return errors.New("loader role or text differs")
	}
	// Accept the loader's final newline and horizontal indentation only. Empty
	// records, leading/trailing blank records and extra newlines remain errors.
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) > 128 {
		return errors.New("loader row count exceeds policy")
	}
	resolved := map[string]string{}
	providers := map[string]string{}
	observedSystem := map[string]bool{}
	seen := map[string]bool{}
	wantedSystem := systemNames(name)
	for _, raw := range lines {
		line := strings.Trim(raw, " \t")
		if virtualRow.MatchString(line) {
			if seen["linux-vdso.so.1"] {
				return errors.New("duplicate loader virtual row")
			}
			seen["linux-vdso.so.1"] = true
			continue
		}
		if row := absoluteRow.FindStringSubmatch(line); row != nil {
			if row[1] != interpreter || seen["ld-linux-aarch64.so.1"] {
				return errors.New("loader absolute self-route differs or is duplicated")
			}
			seen["ld-linux-aarch64.so.1"] = true
			resolved["ld-linux-aarch64.so.1"] = interpreter
			observedSystem["ld-linux-aarch64.so.1"] = true
			continue
		}
		row := resolvedRow.FindStringSubmatch(line)
		if row == nil || !safeName(row[1]) || seen[row[1]] || strings.HasPrefix(row[2], "//") || path.Clean(row[2]) != row[2] {
			return errors.New("malformed, unnamed or duplicate loader row")
		}
		soname, origin := row[1], row[2]
		if soname == "ld-linux-aarch64.so.1" {
			// LD_TRACE_LOADED_OBJECTS=1 may name the interpreter. Its exact
			// canonical path is still mandatory and both textual forms share
			// the same duplicate key. No arbitrary named self-route is allowed.
			if origin != interpreter {
				return errors.New("loader named interpreter self-route differs")
			}
			seen[soname] = true
			resolved[soname] = origin
			observedSystem[soname] = true
			continue
		}
		seen[soname] = true
		if strings.HasPrefix(soname, "libqmi-glib.so") || strings.HasPrefix(soname, "libssc.so") {
			if expected, ok := wanted[soname]; !ok || origin != expected {
				return errors.New("loader private provider origin or role differs")
			}
			providers[soname] = origin
		} else {
			if !wantedSystem[soname] || (!strings.HasPrefix(origin, "/lib/") && !strings.HasPrefix(origin, "/usr/lib/")) || path.Base(origin) != soname {
				return errors.New("loader system dependency name or route differs")
			}
			observedSystem[soname] = true
		}
		resolved[soname] = origin
	}
	if !seen["linux-vdso.so.1"] || !seen["ld-linux-aarch64.so.1"] || !reflect.DeepEqual(providers, wanted) || !reflect.DeepEqual(observedSystem, wantedSystem) {
		return errors.New("loader complete provider, system or virtual inventory differs")
	}
	for _, dependency := range context.needed {
		if _, found := resolved[dependency]; !found {
			return fmt.Errorf("direct ELF dependency absent from loader evidence: %s", dependency)
		}
	}
	return nil
}
