package build

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// The positive fixture substitutes only four licence texts through an
// unexported policy seam. ELF metadata is serialized by fixtureELF and goes
// through the real debug/elf verifier; no test claims native execution.
func artifactFixture(t *testing.T) (map[string][]byte, map[string]fileRule) {
	t.Helper()
	files := fixtureELFs(t)
	rules := expectedFiles()
	for name, rule := range rules {
		if rule.licenseSHA256 != "" {
			data := []byte("fixture licence for " + name + "\n")
			files[name] = data
			rule.maximum = int64(len(data))
			rule.licenseSHA256 = bytesDigest(data)
			rules[name] = rule
		}
	}
	var logJSON, logText bytes.Buffer
	for _, name := range FixtureTargets() {
		entry := map[string]any{"name": name, "result": "OK", "returncode": 0, "stdout": "1..1\nok 1 /local/fixture\n", "stderr": "", "duration": 0.01}
		b, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		logJSON.Write(b)
		logJSON.WriteByte('\n')
		fmt.Fprintf(&logText, "%s: OK\n1..1\nok 1 /local/fixture\n", name)
	}
	files["testlog.json"] = logJSON.Bytes()
	files["testlog.txt"] = logText.Bytes()
	writeJSON := func(name string, value any) {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = append(b, '\n')
	}
	writeJSON("qmi-dependencies.json", []dependencyEvidence{{Name: "glib-2.0", Version: "2.88.0"}})
	qmi := dependencyEvidence{Name: "qmi-glib", Version: "1.39.1", CompileArgs: []string{"-I" + ProviderDirectory + "/include/libqmi-glib"}, LinkArgs: []string{ProviderDirectory + "/lib/libqmi-glib.so"}}
	ssc := dependencyEvidence{Name: "libssc", Version: "0.4.7", CompileArgs: []string{"-I" + ProviderDirectory + "/include/libssc", "-I" + ProviderDirectory + "/include/libqmi-glib"}, LinkArgs: []string{ProviderDirectory + "/lib/libssc.so", ProviderDirectory + "/lib/libqmi-glib.so"}}
	writeJSON("ssc-dependencies.json", []dependencyEvidence{qmi})
	writeJSON("proxy-dependencies.json", []dependencyEvidence{ssc})
	rawLoaders := map[string]string{}
	var loaders []Loader
	for _, name := range coreELFNames {
		providers := expectedLoaderProviders()[name]
		text := "\tlinux-vdso.so.1 (0x1234)\n\tlibc.so.6 => /lib/aarch64-linux-gnu/libc.so.6 (0x4321)\n"
		if name == "monitor-sensor" {
			text += "\tlibglib-2.0.so.0 => /lib/aarch64-linux-gnu/libglib-2.0.so.0 (0x4567)\n"
		}
		var names []string
		for provider := range providers {
			names = append(names, provider)
		}
		sort.Strings(names)
		for _, provider := range names {
			text += "\t" + provider + " => " + providers[provider] + " (0x2345)\n"
		}
		text += "\t/lib/ld-linux-aarch64.so.1 (0x3456)\n"
		rawLoaders[name] = text
		loaders = append(loaders, Loader{Artifact: name, Providers: providers})
	}
	writeJSON("loader-evidence.txt", rawLoaders)
	peak := int64(32 << 20)
	report := BuildReport{Schema: "lexr-sensors-source-build-v1", SourceCommit: SourceCommit, Jobs: 2, Links: ArtifactLinks(), Fixtures: FixtureTargets(), ProxyInstallTargets: ProxyInstallTargets(), Loaders: loaders, VolumeFreeBeforeBytes: 8 << 30, JobApparentBeforeArchiveBytes: 1 << 20, MemoryPeakBytes: &peak}
	report.Providers = []Provider{{"qmi-glib", "1.39.1", ProviderDirectory + "/lib/pkgconfig", ProviderDirectory + "/lib/libqmi-glib.so.5.12.0", bytesDigest(files["libqmi-glib.so.5.12.0"])}, {"libssc", "0.4.7", ProviderDirectory + "/lib/pkgconfig", ProviderDirectory + "/lib/libssc.so.2", bytesDigest(files["libssc.so.2"])}}
	writeJSON(ReportName, report)
	rewriteFixtureReport(t, files, nil)
	return files, rules
}

func rewriteFixtureReport(t *testing.T, files map[string][]byte, mutate func(*BuildReport)) {
	t.Helper()
	var report BuildReport
	if err := json.Unmarshal(files[ReportName], &report); err != nil {
		t.Fatal(err)
	}
	report.Products = nil
	for _, name := range ArtifactFiles() {
		if name == ReportName {
			continue
		}
		data := files[name]
		report.Products = append(report.Products, Artifact{name, int64(len(data)), bytesDigest(data), expectedFiles()[name].mode})
	}
	for i := range report.Providers {
		switch report.Providers[i].Name {
		case "qmi-glib":
			report.Providers[i].SHA256 = bytesDigest(files["libqmi-glib.so.5.12.0"])
		case "libssc":
			report.Providers[i].SHA256 = bytesDigest(files["libssc.so.2"])
		}
	}
	if mutate != nil {
		mutate(&report)
	}
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	files[ReportName] = append(b, '\n')
}

type fixtureTarMember struct {
	header tar.Header
	data   []byte
}

func writeFixtureTar(t *testing.T, members []fixtureTarMember) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	for _, member := range members {
		h := member.header
		if err := writer.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(member.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func fixtureMembers(files map[string][]byte) []fixtureTarMember {
	var result []fixtureTarMember
	for _, name := range ArtifactFiles() {
		data, ok := files[name]
		if !ok {
			continue
		}
		result = append(result, fixtureTarMember{tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(data)), Mode: expectedFiles()[name].mode, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}, data})
	}
	for _, link := range ArtifactLinks() {
		result = append(result, fixtureTarMember{header: tar.Header{Name: link.Name, Linkname: link.Target, Typeflag: tar.TypeSymlink, Mode: 0o777, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}})
	}
	return result
}

func wrapFixtureCopy(t *testing.T, inner []byte) []byte {
	t.Helper()
	return writeFixtureTar(t, []fixtureTarMember{{tar.Header{Name: ArchiveName, Mode: 0o600, Size: int64(len(inner)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}, inner}})
}

func TestArtifactArchiveAndDockerTransportPositiveFixture(t *testing.T) {
	files, rules := artifactFixture(t)
	inner := writeFixtureTar(t, fixtureMembers(files))
	v, err := decodeDockerCopy(wrapFixtureCopy(t, inner), 2, rules)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Files()) != 16 || len(v.Links()) != 3 || v.Report().Jobs != 2 {
		t.Fatal("verified result is incomplete")
	}
	if _, err := DecodeArtifactArchive(inner, 2); err == nil || !strings.Contains(err.Error(), "licence identity") {
		t.Fatalf("public verifier accepted test licence seam: %v", err)
	}
	if _, err := DecodeDockerCopy(wrapFixtureCopy(t, inner), 2); err == nil {
		t.Fatal("public Docker-copy verifier accepted synthetic licences")
	}
	if expectedFiles()["COPYING.libqmi"].licenseSHA256 != "8177f97513213526df2cf6184d8ff986c675afb514d4e68a404010521b880643" {
		t.Fatal("fixture changed production licence authority")
	}
}

func TestArtifactAccessorsAreDefensiveAndZeroValuesUntrusted(t *testing.T) {
	files, rules := artifactFixture(t)
	v, err := decodeArtifactArchive(writeFixtureTar(t, fixtureMembers(files)), 2, rules)
	if err != nil {
		t.Fatal(err)
	}
	before := v.Report()
	a := v.Files()
	a[0].SHA256 = "changed"
	links := v.Links()
	links[0].Target = "changed"
	data, ok := v.Data("ssccli")
	if !ok {
		t.Fatal("missing data")
	}
	data[0] = 0
	r := v.Report()
	r.Products[0].Name = "changed"
	r.Links[0].Target = "changed"
	r.Providers[0].Version = "changed"
	r.Loaders[1].Providers["libqmi-glib.so.5"] = "changed"
	r.Fixtures[0] = "changed"
	r.ProxyInstallTargets[0] = "changed"
	*r.MemoryPeakBytes = 0
	if !reflect.DeepEqual(v.Report(), before) {
		t.Fatal("report accessor leaked mutable storage")
	}
	got, _ := v.Data("ssccli")
	if got[0] != 0x7f {
		t.Fatal("data accessor leaked storage")
	}
	if v.Files()[0].SHA256 == "changed" || v.Links()[0].Target == "changed" {
		t.Fatal("metadata accessor leaked storage")
	}
	for _, empty := range []*VerifiedArtifacts{nil, {}} {
		if empty.Files() != nil || empty.Links() != nil || !reflect.DeepEqual(empty.Report(), BuildReport{}) {
			t.Fatal("zero value grants authority")
		}
		if _, ok := empty.Data("ssccli"); ok {
			t.Fatal("zero value exposes bytes")
		}
	}
}

func TestArtifactInnerArchiveRefusesChangedHeadersAndClosure(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]fixtureTarMember) []fixtureTarMember
	}{
		{"missing", func(m []fixtureTarMember) []fixtureTarMember { return m[1:] }},
		{"duplicate", func(m []fixtureTarMember) []fixtureTarMember { return append(m, m[0]) }},
		{"case-collision", func(m []fixtureTarMember) []fixtureTarMember {
			x := m[0]
			x.header.Name = strings.ToUpper(x.header.Name)
			return append(m, x)
		}},
		{"absolute", func(m []fixtureTarMember) []fixtureTarMember { m[0].header.Name = "/outside"; return m }},
		{"parent", func(m []fixtureTarMember) []fixtureTarMember { m[0].header.Name = "../outside"; return m }},
		{"directory", func(m []fixtureTarMember) []fixtureTarMember {
			m[0] = fixtureTarMember{header: tar.Header{Name: "other", Typeflag: tar.TypeDir, Mode: 0o755, Format: tar.FormatUSTAR}}
			return m
		}},
		{"hardlink", func(m []fixtureTarMember) []fixtureTarMember {
			m[0] = fixtureTarMember{header: tar.Header{Name: m[0].header.Name, Typeflag: tar.TypeLink, Linkname: "ssccli", Mode: 0o644, Format: tar.FormatUSTAR}}
			return m
		}},
		{"fifo", func(m []fixtureTarMember) []fixtureTarMember {
			m[0] = fixtureTarMember{header: tar.Header{Name: m[0].header.Name, Typeflag: tar.TypeFifo, Mode: 0o644, Format: tar.FormatUSTAR}}
			return m
		}},
		{"pax", func(m []fixtureTarMember) []fixtureTarMember {
			m[0].header.Format = tar.FormatPAX
			m[0].header.PAXRecords = map[string]string{"comment": "not accepted"}
			return m
		}},
		{"gnu", func(m []fixtureTarMember) []fixtureTarMember { m[0].header.Format = tar.FormatGNU; return m }},
		{"owner", func(m []fixtureTarMember) []fixtureTarMember { m[0].header.Uid = 123; return m }},
		{"owner-name", func(m []fixtureTarMember) []fixtureTarMember { m[0].header.Uname = "private-host"; return m }},
		{"mode", func(m []fixtureTarMember) []fixtureTarMember { m[0].header.Mode = 0o777; return m }},
		{"setuid", func(m []fixtureTarMember) []fixtureTarMember { m[0].header.Mode = 0o4644; return m }},
		{"alias-target", func(m []fixtureTarMember) []fixtureTarMember { m[len(m)-1].header.Linkname = "../outside"; return m }},
		{"alias-as-file", func(m []fixtureTarMember) []fixtureTarMember {
			n := len(m) - 1
			m[n].header.Typeflag = tar.TypeReg
			m[n].header.Linkname = ""
			m[n].header.Size = 1
			m[n].data = []byte("x")
			return m
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, rules := artifactFixture(t)
			data := writeFixtureTar(t, tc.mutate(fixtureMembers(files)))
			if _, err := decodeArtifactArchive(data, 2, rules); err == nil {
				t.Fatal("accepted invalid archive")
			}
		})
	}
}

func TestArtifactArchiveEndRecordsAndDeclaredSizeBounds(t *testing.T) {
	files, rules := artifactFixture(t)
	good := writeFixtureTar(t, fixtureMembers(files))
	for _, kind := range []string{"truncate", "missing-end", "one-end", "trailing-data", "misaligned"} {
		t.Run(kind, func(t *testing.T) {
			data := bytes.Clone(good)
			switch kind {
			case "truncate":
				data = data[:len(data)-1500]
			case "missing-end":
				data = data[:len(data)-1024]
			case "one-end":
				data = data[:len(data)-512]
			case "trailing-data":
				data = append(data, append([]byte{1}, make([]byte, 511)...)...)
			case "misaligned":
				data = append(data, 0)
			}
			if _, err := decodeArtifactArchive(data, 2, rules); err == nil {
				t.Fatal("accepted truncated/trailing archive")
			}
		})
	}
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	if err := writer.WriteHeader(&tar.Header{Name: "ssccli", Mode: 0o755, Size: 8<<20 + 1, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeArtifactArchive(output.Bytes(), 2, rules); err == nil {
		t.Fatal("accepted oversized declared member")
	}
	for _, jobs := range []int{0, -1, 9} {
		if _, err := decodeArtifactArchive(good, jobs, rules); err == nil {
			t.Fatal("accepted invalid jobs")
		}
	}
}

func TestArtifactInnerTimestampsRequireCompiledEpoch(t *testing.T) {
	for _, kind := range []string{"regular", "alias"} {
		t.Run(kind, func(t *testing.T) {
			files, rules := artifactFixture(t)
			members := fixtureMembers(files)
			index := 0
			if kind == "alias" {
				index = len(members) - 1
			}
			members[index].header.ModTime = time.Unix(1, 0)
			if _, err := decodeArtifactArchive(writeFixtureTar(t, members), 2, rules); err == nil || !strings.Contains(err.Error(), "modification time") {
				t.Fatalf("non-epoch %s timestamp did not refuse at its gate: %v", kind, err)
			}
		})
	}
	files, rules := artifactFixture(t)
	if _, err := decodeArtifactArchive(writeFixtureTar(t, fixtureMembers(files)), 2, rules); err != nil {
		t.Fatalf("compiled epoch fixture refused: %v", err)
	}
	inner := writeFixtureTar(t, fixtureMembers(files))
	outer := writeFixtureTar(t, []fixtureTarMember{{header: tar.Header{Name: ArchiveName, Typeflag: tar.TypeReg, Size: int64(len(inner)), Mode: 0o600, ModTime: time.Unix(7, 0), Format: tar.FormatUSTAR}, data: inner}})
	if _, err := decodeDockerCopy(outer, 2, rules); err != nil {
		t.Fatalf("transport timestamp was incorrectly treated as inner authority: %v", err)
	}
}

func TestDockerCopyOuterArchiveRefusesWrongTransport(t *testing.T) {
	files, rules := artifactFixture(t)
	inner := writeFixtureTar(t, fixtureMembers(files))
	member := fixtureTarMember{tar.Header{Name: ArchiveName, Mode: 0o600, Size: int64(len(inner)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}, inner}
	for _, kind := range []string{"wrong-name", "link", "extra", "pax", "missing-end", "trailing", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			m := member
			var data []byte
			switch kind {
			case "wrong-name":
				m.header.Name = "other.tar"
			case "link":
				m.header.Typeflag = tar.TypeSymlink
				m.header.Linkname = "other.tar"
				m.header.Size = 0
				m.data = nil
			case "pax":
				m.header.Format = tar.FormatPAX
				m.header.PAXRecords = map[string]string{"path": ArchiveName}
			}
			members := []fixtureTarMember{m}
			if kind == "extra" {
				members = append(members, m)
			}
			data = writeFixtureTar(t, members)
			switch kind {
			case "missing-end":
				data = data[:len(data)-1024]
			case "trailing":
				data = append(data, append([]byte{1}, make([]byte, 511)...)...)
			case "truncated":
				data = data[:512]
			}
			if _, err := decodeDockerCopy(data, 2, rules); err == nil {
				t.Fatal("accepted invalid Docker copy stream")
			}
		})
	}
}

func TestBuildReportAndRawEvidenceRefuseSelfAttestation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string][]byte)
		report func(*BuildReport)
	}{
		{name: "schema", report: func(r *BuildReport) { r.Schema = "unreviewed" }},
		{name: "licence-bytes", mutate: func(files map[string][]byte) { files["COPYING.libqmi"][0] ^= 1 }},
		{name: "ELF-bytes", mutate: func(files map[string][]byte) { files["ssccli"][0] = 0 }},
		{name: "source", report: func(r *BuildReport) { r.SourceCommit = strings.Repeat("a", 40) }},
		{name: "jobs", report: func(r *BuildReport) { r.Jobs = 3 }},
		{name: "missing-product", report: func(r *BuildReport) { r.Products = r.Products[1:] }},
		{name: "duplicate-product", report: func(r *BuildReport) { r.Products = append(r.Products, r.Products[0]) }},
		{name: "product-hash", report: func(r *BuildReport) { r.Products[0].SHA256 = strings.Repeat("0", 64) }},
		{name: "product-mode", report: func(r *BuildReport) { r.Products[0].Mode = 0o777 }},
		{name: "self-product", report: func(r *BuildReport) { r.Products = append(r.Products, Artifact{Name: ReportName}) }},
		{name: "alias", report: func(r *BuildReport) { r.Links[0].Target = "wrong" }},
		{name: "provider-version", report: func(r *BuildReport) { r.Providers[0].Version = "1.0" }},
		{name: "provider-path", report: func(r *BuildReport) { r.Providers[0].Library = "/usr/lib/libqmi-glib.so.5" }},
		{name: "provider-pcdir", report: func(r *BuildReport) { r.Providers[0].PCFileDirectory = "/usr/lib/pkgconfig" }},
		{name: "duplicate-provider", report: func(r *BuildReport) { r.Providers = append(r.Providers, r.Providers[0]) }},
		{name: "loader-report", report: func(r *BuildReport) { r.Loaders[1].Providers["libqmi-glib.so.5"] = "/usr/lib/libqmi-glib.so.5" }},
		{name: "fixtures", report: func(r *BuildReport) { r.Fixtures[0] = "hardware-discovery" }},
		{name: "proxy-plan", report: func(r *BuildReport) { r.ProxyInstallTargets = append(r.ProxyInstallTargets, "/etc/unexpected") }},
		{name: "space", report: func(r *BuildReport) { r.VolumeFreeBeforeBytes = 1024 }},
		{name: "apparent", report: func(r *BuildReport) { r.JobApparentBeforeArchiveBytes = 1 }},
		{name: "memory", report: func(r *BuildReport) { v := int64(-1); r.MemoryPeakBytes = &v }},
		{name: "test-result", mutate: func(files map[string][]byte) {
			files["testlog.json"] = bytes.Replace(files["testlog.json"], []byte(`"result":"OK"`), []byte(`"result":"SKIP"`), 1)
		}},
		{name: "test-return-code", mutate: func(files map[string][]byte) {
			files["testlog.json"] = bytes.Replace(files["testlog.json"], []byte(`"returncode":0`), []byte(`"returncode":1`), 1)
		}},
		{name: "test-duplicate", mutate: func(files map[string][]byte) {
			line := bytes.Split(files["testlog.json"], []byte("\n"))[0]
			files["testlog.json"] = append(files["testlog.json"], append(line, '\n')...)
		}},
		{name: "test-no-log", mutate: func(files map[string][]byte) { files["testlog.txt"] = []byte("no selected tests\n") }},
		{name: "provider-flags", mutate: func(files map[string][]byte) {
			files["ssc-dependencies.json"] = bytes.ReplaceAll(files["ssc-dependencies.json"], []byte(ProviderDirectory), []byte("/usr"))
		}},
		{name: "loader-raw", mutate: func(files map[string][]byte) {
			files["loader-evidence.txt"] = bytes.ReplaceAll(files["loader-evidence.txt"], []byte(ProviderDirectory+"/lib/libqmi-glib.so.5"), []byte("/usr/lib/libqmi-glib.so.5"))
		}},
		{name: "loader-self-missing", mutate: func(files map[string][]byte) {
			var raw map[string]string
			if err := json.Unmarshal(files["loader-evidence.txt"], &raw); err != nil {
				panic(err)
			}
			for name, text := range raw {
				raw[name] = strings.ReplaceAll(text, "\t/lib/ld-linux-aarch64.so.1 (0x3456)\n", "")
			}
			data, err := json.Marshal(raw)
			if err != nil {
				panic(err)
			}
			files["loader-evidence.txt"] = append(data, '\n')
		}},
		{name: "duplicate-json", mutate: func(files map[string][]byte) { files["loader-evidence.txt"] = []byte("{\"x\":\"a\",\"x\":\"b\"}\n") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files, rules := artifactFixture(t)
			if tc.mutate != nil {
				tc.mutate(files)
			}
			rewriteFixtureReport(t, files, tc.report)
			if _, err := decodeArtifactArchive(writeFixtureTar(t, fixtureMembers(files)), 2, rules); err == nil {
				t.Fatal("accepted false/incoherent evidence")
			}
		})
	}
}

func TestNestedLocaleSkipDoesNotBecomeWholeFixtureSkip(t *testing.T) {
	files, rules := artifactFixture(t)
	files["testlog.json"] = bytes.ReplaceAll(files["testlog.json"], []byte(`ok 1 /local/fixture`), []byte(`ok 1 /French # SKIP locale unavailable`))
	rewriteFixtureReport(t, files, nil)
	if _, err := decodeArtifactArchive(writeFixtureTar(t, fixtureMembers(files)), 2, rules); err != nil {
		t.Fatal(err)
	}
}

func TestLoaderAndRosterParserRefusals(t *testing.T) {
	for _, text := range []string{"", "libqmi-glib.so.5 => not found\n", "libc.so.6 => /tmp/libc.so.6 (0x1)\n", "libssc.so.1 => /work/job/providers/lib/libssc.so.1 (0x1)\n", "libc.so.6 => /lib/libc.so.6 (0x1)\nlibc.so.6 => /lib/libc.so.6 (0x2)\n", "nonsense\n", "/tmp/loader (0x1)\n"} {
		if _, err := parseLoader(text); err == nil {
			t.Fatalf("accepted bad loader record %q", text)
		}
	}
	if err := checkRosterNames(map[string]fileRule{"A": {}, "a": {}}, nil); err == nil {
		t.Fatal("accepted casefold collision")
	}
	for _, name := range []string{"", ".", "..", "a/b", "../a", "é", "a\x00b", "with space"} {
		if safeFlatName(name) {
			t.Fatalf("accepted unsafe name %q", name)
		}
	}
}

func TestLoaderEvidenceRequiresExactlyOneFixedSelfRow(t *testing.T) {
	dependency := "libc.so.6 => /lib/aarch64-linux-gnu/libc.so.6 (0x1)\n"
	self := "/lib/ld-linux-aarch64.so.1 (0x2)\n"
	// The virtual DSO row is intentionally not required; the actual fixed loader
	// self-row is required even when there are no private provider dependencies.
	if providers, err := parseLoader(dependency + self); err != nil || len(providers) != 0 {
		t.Fatalf("fixed self-row without optional vdso refused: %v", err)
	}
	for _, tc := range []struct{ name, text string }{
		{"missing", dependency},
		{"wrong", dependency + "/usr/lib/ld-linux-aarch64.so.1 (0x2)\n"},
		{"wrong-architecture", dependency + "/lib/ld-linux-other.so.1 (0x2)\n"},
		{"duplicate", dependency + self + self},
		{"resolved-only", dependency + "ld-linux-aarch64.so.1 => /lib/ld-linux-aarch64.so.1 (0x2)\n"},
		{"resolved-extra", dependency + self + "ld-linux-aarch64.so.1 => /lib/ld-linux-aarch64.so.1 (0x2)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseLoader(tc.text); err == nil {
				t.Fatal("accepted missing/wrong/ambiguous loader self-row")
			}
		})
	}
}
