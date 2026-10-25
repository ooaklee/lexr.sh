package relocation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/packageplan"
	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
)

// All ordinary fixtures in this file are synthetic parser/contract tests. They
// do not execute an ARM64 program or manufacture an opaque verified package.
func syntheticListing(name string) string {
	rows := []string{"\tlinux-vdso.so.1 (0x1234)"}
	names := systemNames(name)
	delete(names, "ld-linux-aarch64.so.1")
	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		rows = append(rows, fmt.Sprintf("\t%s => /usr/lib/aarch64-linux-gnu/%s (0x1234)", name, name))
	}
	providers := providerRoutes(name)
	keys = keys[:0]
	for name := range providers {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		rows = append(rows, fmt.Sprintf("\t%s => %s (0x1234)", name, providers[name]))
	}
	return strings.Join(append(rows, "\t"+interpreter+" (0x1234)"), "\n") + "\n"
}

// TestSyntheticClosedListings checks complete permitted synthetic loader routes.
func TestSyntheticClosedListings(t *testing.T) {
	for _, name := range elfPaths() {
		t.Run(name, func(t *testing.T) {
			context := elfContext{needed: []string{"libc.so.6", "ld-linux-aarch64.so.1"}}
			listing := syntheticListing(name)
			if err := verifyListing(name, context, listing); err != nil {
				t.Fatal(err)
			}
			// The exact named interpreter spelling is a statically permitted
			// alternative, not claimed as prior native evidence.
			named := strings.Replace(listing, "\t"+interpreter+" (", "\tld-linux-aarch64.so.1 => "+interpreter+" (", 1)
			if err := verifyListing(name, context, named); err != nil {
				t.Fatalf("exact named interpreter: %v", err)
			}
			standardLib := strings.ReplaceAll(listing, "/usr/lib/aarch64-linux-gnu/", "/lib/aarch64-linux-gnu/")
			if err := verifyListing(name, context, standardLib); err != nil {
				t.Fatalf("standard /lib route: %v", err)
			}
		})
	}
}

// TestSyntheticListingInverseControls refuses every malformed or altered row.
func TestSyntheticListingInverseControls(t *testing.T) {
	base := syntheticListing("bin/ssccli")
	context := elfContext{needed: []string{"libc.so.6", "libssc.so.2", "ld-linux-aarch64.so.1"}}
	qmiRoute := packageplan.PrivatePrefix + "/lib/libqmi-glib.so.5"
	cases := map[string]string{
		"empty": "", "only whitespace": " \t\n", "leading blank": "\n" + base,
		"trailing blank": base + "\n", "interior blank": strings.Replace(base, "\n", "\n\n", 1),
		"CRLF": strings.ReplaceAll(base, "\n", "\r\n"), "NUL": base + "\x00",
		"ANSI": "\x1b[31m" + base, "invalid UTF8": base + string([]byte{0xff}),
		"unnamed address": base + " (0x1234)\n", "not found": base + "libmissing.so => not found\n",
		"unknown trailing text": base + "arbitrary words\n", "unknown soname": base + "libunknown.so.1 => /usr/lib/libunknown.so.1 (0x1234)\n",
		"missing system":                  strings.Replace(base, "\tlibc.so.6 => /usr/lib/aarch64-linux-gnu/libc.so.6 (0x1234)\n", "", 1),
		"duplicate system":                base + "libc.so.6 => /lib/libc.so.6 (0x2222)\n",
		"duplicate private":               base + "libqmi-glib.so.5 => " + qmiRoute + " (0x2222)\n",
		"duplicate virtual":               base + "linux-vdso.so.1 (0x2222)\n",
		"duplicate interpreter":           base + interpreter + " (0x2222)\n",
		"crossform duplicate interpreter": base + "ld-linux-aarch64.so.1 => " + interpreter + " (0x2222)\n",
		"named interpreter wrong path":    strings.Replace(base, interpreter+" (", "ld-linux-aarch64.so.1 => /usr/lib/ld-linux-aarch64.so.1 (", 1),
		"interpreter absent":              strings.Replace(base, "\t"+interpreter+" (0x1234)\n", "", 1),
		"virtual absent":                  strings.Replace(base, "\tlinux-vdso.so.1 (0x1234)\n", "", 1),
		"old build route":                 strings.Replace(base, qmiRoute, "/work/job/providers/lib/libqmi-glib.so.5", 1),
		"system QMI fallback":             strings.Replace(base, qmiRoute, "/usr/lib/aarch64-linux-gnu/libqmi-glib.so.5", 1),
		"wrong private alias":             strings.Replace(base, qmiRoute, packageplan.PrivatePrefix+"/lib/libqmi-glib.so.5.12.0", 1),
		"wrong private version":           strings.ReplaceAll(base, "libqmi-glib.so.5", "libqmi-glib.so.6"),
		"double slash":                    strings.Replace(base, qmiRoute, "/"+qmiRoute, 1),
		"dot path":                        strings.Replace(base, qmiRoute, packageplan.PrivatePrefix+"/lib/./libqmi-glib.so.5", 1),
		"dotdot path":                     strings.Replace(base, qmiRoute, packageplan.PrivatePrefix+"/lib/../lib/libqmi-glib.so.5", 1),
		"relative path":                   strings.Replace(base, qmiRoute, "lib/libqmi-glib.so.5", 1),
		"system temporary path":           strings.Replace(base, "/usr/lib/aarch64-linux-gnu/libc.so.6", "/tmp/libc.so.6", 1),
		"wrong system basename":           strings.Replace(base, "/usr/lib/aarch64-linux-gnu/libc.so.6", "/usr/lib/aarch64-linux-gnu/libfake.so.6", 1),
		"unsafe soname":                   strings.Replace(base, "libc.so.6 =>", "../libc.so.6 =>", 1),
		"bad address":                     strings.Replace(base, "0x1234", "0xZZZZ", 1),
		"additional address token":        strings.Replace(base, "(0x1234)", "(0x1234) extra", 1),
		"too many rows":                   base + strings.Repeat("linux-vdso.so.1 (0x1234)\n", 129),
		"oversized":                       strings.Repeat(" ", maximumTextBytes+1),
	}
	for name, listing := range cases {
		t.Run(name, func(t *testing.T) {
			if err := verifyListing("bin/ssccli", context, listing); err == nil {
				t.Fatal("accepted invalid synthetic listing")
			}
		})
	}
	if err := verifyListing("bin/ssccli", elfContext{needed: []string{"libnotlisted.so.1"}}, base); err == nil {
		t.Fatal("accepted omitted direct ELF dependency")
	}
	if err := verifyListing("bin/unknown", context, base); err == nil {
		t.Fatal("accepted unknown ELF role")
	}
}

// TestSyntheticProviderRoleAndSelfConfusion refuses invented provider contexts.
func TestSyntheticProviderRoleAndSelfConfusion(t *testing.T) {
	for _, name := range []string{"lib/libqmi-glib.so.5.12.0", "bin/monitor-sensor"} {
		base := syntheticListing(name)
		for _, route := range []string{packageplan.PrivatePrefix + "/lib/libqmi-glib.so.5", "/usr/lib/libqmi-glib.so.5"} {
			listing := base + "libqmi-glib.so.5 => " + route + " (0x1234)\n"
			if err := verifyListing(name, elfContext{soname: "libqmi-glib.so.5"}, listing); err == nil {
				t.Fatalf("accepted another/own role provider in %s", name)
			}
		}
	}
	// Named root DSO self-rows have no observed contract and cannot be invented
	// by relabelling an unexpected provider dependency as the input object.
	listing := syntheticListing("lib/libqmi-glib.so.5.12.0") + "libqmi-glib.so.5 => " + packageplan.PrivatePrefix + "/lib/libqmi-glib.so.5.12.0 (0x1234)\n"
	if err := verifyListing("lib/libqmi-glib.so.5.12.0", elfContext{soname: "libqmi-glib.so.5"}, listing); err == nil {
		t.Fatal("accepted speculative named DSO self-row")
	}
}

// syntheticSmokes supplies non-executed output fixtures for the CLI whitelist.
func syntheticSmokes() []Smoke {
	help := func(name, marker string) string {
		return "Usage:\n  " + name + " [OPTION…]\n\nHelp Options:\n  -h, --help       Show help options\n\nApplication Options:\n  " + marker + "       Show component-specific information\n"
	}
	return []Smoke{
		{"bin/ssccli", []string{"--help"}, 0, help("ssccli", "--version"), ""},
		{"bin/ssccli", []string{"--version"}, 0, "libssc version 0.4.7\n", ""},
		{"libexec/iio-sensor-proxy", []string{"--help"}, 0, help("iio-sensor-proxy", "--replace"), ""},
		{"bin/monitor-sensor", []string{"--help"}, 0, help("monitor-sensor", "--accel"), ""},
	}
}

// TestSyntheticSmokeWhitelist rejects unsafe invocations and changed output.
func TestSyntheticSmokeWhitelist(t *testing.T) {
	if err := verifySmokes(syntheticSmokes()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func([]Smoke) []Smoke{
		"missing":            func(s []Smoke) []Smoke { return s[:3] },
		"extra":              func(s []Smoke) []Smoke { return append(s, s[0]) },
		"duplicate":          func(s []Smoke) []Smoke { s[3] = s[0]; return s },
		"reordered":          func(s []Smoke) []Smoke { s[0], s[1] = s[1], s[0]; return s },
		"absolute path":      func(s []Smoke) []Smoke { s[0].Path = packageplan.PrivatePrefix + "/bin/ssccli"; return s },
		"bare invocation":    func(s []Smoke) []Smoke { s[0].Args = nil; return s },
		"extra argument":     func(s []Smoke) []Smoke { s[0].Args = []string{"--help", "--discover=all"}; return s },
		"unsafe invocation":  func(s []Smoke) []Smoke { s[2].Args = []string{"--replace"}; return s },
		"nonzero":            func(s []Smoke) []Smoke { s[0].ExitCode = 1; return s },
		"stderr":             func(s []Smoke) []Smoke { s[0].Stderr = "warning\n"; return s },
		"empty output":       func(s []Smoke) []Smoke { s[0].Stdout = ""; return s },
		"wrong version":      func(s []Smoke) []Smoke { s[1].Stdout = "libssc version 0.4.8\n"; return s },
		"version whitespace": func(s []Smoke) []Smoke { s[1].Stdout = "libssc version 0.4.7\n\n"; return s },
		"help marker": func(s []Smoke) []Smoke {
			s[0].Stdout = strings.ReplaceAll(s[0].Stdout, "--version", "--wrong")
			return s
		},
		"help executable": func(s []Smoke) []Smoke {
			s[0].Stdout = strings.ReplaceAll(s[0].Stdout, "ssccli", "other-ssccli")
			return s
		},
		"control output":   func(s []Smoke) []Smoke { s[0].Stdout += "\x00"; return s },
		"oversized output": func(s []Smoke) []Smoke { s[0].Stdout += strings.Repeat("x", maximumTextBytes); return s },
	}
	for name, mutation := range cases {
		t.Run(name, func(t *testing.T) {
			if err := verifySmokes(mutation(syntheticSmokes())); err == nil {
				t.Fatal("accepted invalid synthetic CLI smoke")
			}
		})
	}
}

// syntheticControl describes the expected, non-executed missing-provider error.
func syntheticControl() MissingProviderControl {
	return MissingProviderControl{"bin/ssccli", MissingProviderDirectory, 127, "", packageplan.PrivatePrefix + "/bin/ssccli: error while loading shared libraries: libssc.so.2: cannot open shared object file: No such file or directory\n"}
}

// TestSyntheticMissingProviderControl requires the exact inverse failure form.
func TestSyntheticMissingProviderControl(t *testing.T) {
	context := elfContext{needed: []string{"libssc.so.2", "libc.so.6"}}
	if err := verifyMissingProvider(context, syntheticControl()); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*MissingProviderControl){
		"success":          func(c *MissingProviderControl) { c.ExitCode = 0 },
		"signal":           func(c *MissingProviderControl) { c.ExitCode = -9 },
		"wrong failure":    func(c *MissingProviderControl) { c.ExitCode = 1 },
		"stdout":           func(c *MissingProviderControl) { c.Stdout = "unexpected" },
		"wrong executable": func(c *MissingProviderControl) { c.Path = "bin/monitor-sensor" },
		"private search":   func(c *MissingProviderControl) { c.LibraryPath = packageplan.PrivatePrefix + "/lib" },
		"empty failure":    func(c *MissingProviderControl) { c.Stderr = "" },
		"other error":      func(c *MissingProviderControl) { c.Stderr = "permission denied\n" },
		"wrong provider": func(c *MissingProviderControl) {
			c.Stderr = strings.ReplaceAll(c.Stderr, "libssc.so.2", "libqmi-glib.so.5")
		},
		"old path": func(c *MissingProviderControl) {
			c.Stderr = strings.ReplaceAll(c.Stderr, packageplan.PrivatePrefix, "/work/job/providers")
		},
		"extra newline": func(c *MissingProviderControl) { c.Stderr += "\n" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			control := syntheticControl()
			mutate(&control)
			if err := verifyMissingProvider(context, control); err == nil {
				t.Fatal("accepted invalid synthetic inverse control")
			}
		})
	}
	if err := verifyMissingProvider(elfContext{needed: []string{"libc.so.6"}}, syntheticControl()); err == nil {
		t.Fatal("accepted missing ELF provider context")
	}
}

// syntheticObservation provides a structural fixture, not native proof data.
func syntheticObservation() Observation {
	return Observation{Schema, "aarch64", packageplan.PrivatePrefix, map[string]string{}, expectedLinks(), map[string]string{}, syntheticSmokes(), syntheticControl()}
}

// TestObservationClosedDecode rejects ambiguous or unbounded JSON structures.
func TestObservationClosedDecode(t *testing.T) {
	data, err := json.Marshal(syntheticObservation())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"empty": nil, "oversized": []byte(strings.Repeat("x", MaximumObservationBytes+1)),
		"invalid UTF8":       append(append([]byte(nil), data...), 0xff),
		"trailing":           append(append([]byte(nil), data...), []byte(" {}")...),
		"duplicate root":     []byte(strings.Replace(string(data), `"schema":`, `"schema":"extra","schema":`, 1)),
		"duplicate nested":   []byte(strings.Replace(string(data), `"args":`, `"path":"extra","args":`, 1)),
		"unknown root":       []byte(strings.Replace(string(data), `"schema":`, `"extra":true,"schema":`, 1)),
		"incorrect case":     []byte(strings.Replace(string(data), `"schema":`, `"Schema":`, 1)),
		"missing field":      []byte(strings.Replace(string(data), `"architecture":"aarch64",`, "", 1)),
		"nested case":        []byte(strings.Replace(string(data), `"exitCode":`, `"ExitCode":`, 1)),
		"nested unknown":     []byte(strings.Replace(string(data), `"args":`, `"extra":false,"args":`, 1)),
		"nested omitted":     []byte(strings.Replace(string(data), `"exitCode":0,`, "", 1)),
		"nested null":        []byte(strings.Replace(string(data), `"stdout":`, `"stdout":null,"unused":`, 1)),
		"wrong numeric type": []byte(strings.Replace(string(data), `"exitCode":0`, `"exitCode":0.25`, 1)),
		"deep JSON":          []byte(strings.Replace(string(data), `"architecture":"aarch64"`, `"architecture":[[[[[[[[[[1]]]]]]]]]]`, 1)),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(raw); err == nil {
				t.Fatal("accepted structurally invalid synthetic observation")
			}
		})
	}
}

// TestOpaqueVerifiedPackageRequired refuses absent or invalid authority handles.
func TestOpaqueVerifiedPackageRequired(t *testing.T) {
	for _, pkg := range []*runtimepkg.VerifiedPackage{nil, new(runtimepkg.VerifiedPackage)} {
		if err := Verify(pkg, syntheticObservation()); err == nil {
			t.Fatal("accepted nil/zero opaque package")
		}
	}
	if _, err := inspectContext("bin/ssccli", []byte("not an ELF")); err == nil {
		t.Fatal("accepted invalid ELF bytes")
	}
}

// TestSavedRelocationObservation is an explicitly opt-in replay of real saved
// package/observer data, not a substitute for external native-lifecycle review.
// All four environment values are independently pinned by the coordinator;
// this test never starts a container or executes the packaged programs.
func TestSavedRelocationObservation(t *testing.T) {
	testSavedRelocationProfile(t, false)
}

// TestSavedRelocationObservation048 verifies newly retained metadata0.4.8
// evidence only; it never executes an ARM64 program or selects a live target.
func TestSavedRelocationObservation048(t *testing.T) {
	testSavedRelocationProfile(t, true)
}

// testSavedRelocationProfile selects an explicit test gate, never JSON policy.
func testSavedRelocationProfile(t *testing.T, metadata048 bool) {
	t.Helper()
	keys := []string{"LEXR_SENSOR_RELOCATION_PACKAGE", "LEXR_SENSOR_RELOCATION_PACKAGE_SHA256", "LEXR_SENSOR_RELOCATION_OBSERVATION", "LEXR_SENSOR_RELOCATION_OBSERVATION_SHA256"}
	if metadata048 {
		for i := range keys {
			keys[i] += "_048"
		}
	}
	values := make([]string, len(keys))
	present := 0
	for i, key := range keys {
		values[i] = os.Getenv(key)
		if values[i] != "" {
			present++
		}
	}
	if present == 0 {
		t.Skip("no independently pinned saved relocation replay supplied")
	}
	if present != len(keys) {
		t.Fatal("all four saved package/observation path and SHA256 values are required")
	}
	readPinned := func(name, pin string, maximum int64) []byte {
		t.Helper()
		decoded, err := hex.DecodeString(pin)
		if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != pin || !filepath.IsAbs(name) {
			t.Fatal("saved observation pin/path differs")
		}
		info, err := os.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
			t.Fatal("saved replay file is not a bounded regular file")
		}
		stream, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		data, err := io.ReadAll(io.LimitReader(stream, maximum+1))
		if err != nil || int64(len(data)) != info.Size() || int64(len(data)) > maximum {
			t.Fatal("saved replay file changed or exceeds policy")
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != pin {
			t.Fatal("saved replay SHA256 differs")
		}
		return data
	}
	packageBytes := readPinned(values[0], values[1], runtimepkg.MaximumPackageBytes)
	decode := runtimepkg.Decode
	verify := Verify
	opposite := Verify048
	if metadata048 {
		decode, verify, opposite = runtimepkg.Decode048, Verify048, Verify
	}
	pkg, err := decode(packageBytes, runtimepkg.Reference{Bytes: int64(len(packageBytes)), SHA256: values[1]})
	if err != nil {
		t.Fatal(err)
	}
	observationBytes := readPinned(values[2], values[3], MaximumObservationBytes)
	observation, err := Decode(observationBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := verify(pkg, observation); err != nil {
		t.Fatal(err)
	}
	if err := opposite(pkg, observation); err == nil {
		t.Fatal("opposite relocation profile accepted this opaque package")
	}
	t.Logf("verified saved package %s against saved native observation %s; no native execution performed by test", values[1], values[3])
}
