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

// metadataLogFixture048 makes nonexecuted Meson/TAP evidence for both exact programmes.
func metadataLogFixture048(t *testing.T) ([]byte, []byte) {
	t.Helper()
	var data, text bytes.Buffer
	wanted := metadataTestCases048()
	for _, name := range []string{"test-metadata-diagnostics", "test-metadata-diagnostics-integration"} {
		cases := wanted[name]
		output := fmt.Sprintf("TAP version 14\n# random seed: fixture\n1..%d\n", len(cases))
		for n, path := range cases {
			output += fmt.Sprintf("ok %d %s\n", n+1, path)
		}
		command := "/work/job/ssc-build/tests/metadata/" + name
		row := map[string]any{"name": "metadata-local - libssc:" + name, "command": []string{command}, "result": "OK", "returncode": 0, "stdout": output, "stderr": nil}
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		data.Write(raw)
		data.WriteByte('\n')
		fmt.Fprintf(&text, "test: metadata-local - libssc:%s\ncommand: %s\n%s", name, command, output)
	}
	return data.Bytes(), text.Bytes()
}

// artifactFixture048 retains the old licence seam but selects the real 0.4.8 ELF policy.
func artifactFixture048(t *testing.T) (map[string][]byte, map[string]fileRule) {
	t.Helper()
	files, rules := artifactFixture(t)
	for name, data := range fixtureELFs048(t) {
		files[name] = data
	}
	for _, name := range []string{"ssc-metadata-testlog.json", "ssc-metadata-testlog.txt"} {
		rules[name] = expectedFiles048()[name]
	}
	files["ssc-metadata-testlog.json"], files["ssc-metadata-testlog.txt"] = metadataLogFixture048(t)
	files["proxy-dependencies.json"] = bytes.ReplaceAll(files["proxy-dependencies.json"], []byte("0.4.7"), []byte("0.4.8"))
	rewriteFixtureReport048(t, files, nil)
	return files, rules
}

// rewriteFixtureReport048 refreshes only fixed successor report fields in tests.
func rewriteFixtureReport048(t *testing.T, files map[string][]byte, mutate func(*BuildReport)) {
	t.Helper()
	var report BuildReport
	if err := json.Unmarshal(files[ReportName], &report); err != nil {
		t.Fatal(err)
	}
	report.SourceCommit = SourceCommit048
	report.Products = nil
	for _, name := range ArtifactFiles048() {
		if name != ReportName {
			data := files[name]
			report.Products = append(report.Products, Artifact{name, int64(len(data)), bytesDigest(data), expectedFiles048()[name].mode})
		}
	}
	for n := range report.Providers {
		if report.Providers[n].Name == "libssc" {
			report.Providers[n].Version = "0.4.8"
			report.Providers[n].SHA256 = bytesDigest(files["libssc.so.2"])
		}
	}
	if mutate != nil {
		mutate(&report)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	files[ReportName] = append(data, '\n')
}

// fixtureMembers048 serialises the closed successor roster without target execution.
func fixtureMembers048(files map[string][]byte) []fixtureTarMember {
	var result []fixtureTarMember
	for _, name := range ArtifactFiles048() {
		if data, found := files[name]; found {
			result = append(result, fixtureTarMember{tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(data)), Mode: expectedFiles048()[name].mode, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}, data})
		}
	}
	for _, link := range ArtifactLinks() {
		result = append(result, fixtureTarMember{header: tar.Header{Name: link.Name, Linkname: link.Target, Typeflag: tar.TypeSymlink, Mode: 0o777, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}})
	}
	return result
}

// TestArtifact048ProfilesRemainSeparate proves entry-point selection, not in-band discovery.
func TestArtifact048ProfilesRemainSeparate(t *testing.T) {
	files, rules := artifactFixture048(t)
	inner := writeFixtureTar(t, fixtureMembers048(files))
	verified, err := decodeArtifactArchiveProfile(inner, 2, rules, artifact048)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.IsMetadata048() || len(verified.Files()) != 18 || len(verified.Links()) != 3 || verified.Report().SourceCommit != SourceCommit048 {
		t.Fatal("successor result lacks its closed profile or roster")
	}
	copyVerified, err := decodeDockerCopyProfile(wrapFixtureCopy(t, inner), 2, rules, artifact048)
	if err != nil || !copyVerified.IsMetadata048() {
		t.Fatalf("successor transport rejected: %v", err)
	}
	if _, err := DecodeArtifactArchive048(inner, 2); err == nil || !strings.Contains(err.Error(), "licence identity") {
		t.Fatalf("public successor accepted fixture licence authority: %v", err)
	}
	if _, err := DecodeDockerCopy048(wrapFixtureCopy(t, inner), 2); err == nil {
		t.Fatal("public successor transport accepted synthetic licences")
	}
	if _, err := decodeArtifactArchive(inner, 2, rules); err == nil {
		t.Fatal("old profile adopted new source or ABI")
	}
	oldFiles, oldRules := artifactFixture(t)
	oldInner := writeFixtureTar(t, fixtureMembers(oldFiles))
	old, err := decodeArtifactArchive(oldInner, 2, oldRules)
	if err != nil || old.IsMetadata048() {
		t.Fatalf("old profile changed: %v", err)
	}
	if _, err := decodeArtifactArchiveProfile(oldInner, 2, rules, artifact048); err == nil {
		t.Fatal("successor adopted old incomplete artefacts")
	}
	for _, empty := range []*VerifiedArtifacts{nil, {}} {
		if empty.IsMetadata048() {
			t.Fatal("zero handle acquired successor authority")
		}
	}
	if _, err := decodeArtifactArchiveProfile(inner, 2, rules, artifactProfile(99)); err == nil {
		t.Fatal("unknown private profile accepted")
	}
	report := verified.Report()
	report.SourceCommit = SourceCommit
	report.Products[0].Name = "changed"
	data, _ := verified.Data("ssc-metadata-testlog.json")
	data[0] = '!'
	if !verified.IsMetadata048() || verified.Report().SourceCommit != SourceCommit048 {
		t.Fatal("accessor mutation changed a sealed successor handle")
	}
	if got, _ := verified.Data("ssc-metadata-testlog.json"); got[0] == '!' {
		t.Fatal("successor data accessor exposed storage")
	}
}

// TestArtifact048RejectsProfileEvidenceDrift refuses metadata, source and provider substitutions.
func TestArtifact048RejectsProfileEvidenceDrift(t *testing.T) {
	for _, name := range []string{"old-source", "old-provider", "old-dependency", "missing-metadata", "wrong-metadata-mode", "metadata-failure", "old-elf"} {
		t.Run(name, func(t *testing.T) {
			files, rules := artifactFixture048(t)
			switch name {
			case "old-source":
				rewriteFixtureReport048(t, files, func(r *BuildReport) { r.SourceCommit = SourceCommit })
			case "old-provider":
				rewriteFixtureReport048(t, files, func(r *BuildReport) { r.Providers[1].Version = "0.4.7" })
			case "old-dependency":
				files["proxy-dependencies.json"] = bytes.ReplaceAll(files["proxy-dependencies.json"], []byte("0.4.8"), []byte("0.4.7"))
				rewriteFixtureReport048(t, files, nil)
			case "missing-metadata":
				delete(files, "ssc-metadata-testlog.json")
			case "metadata-failure":
				files["ssc-metadata-testlog.json"] = bytes.ReplaceAll(files["ssc-metadata-testlog.json"], []byte(`"returncode":0`), []byte(`"returncode":1`))
				rewriteFixtureReport048(t, files, nil)
			case "old-elf":
				files["libssc.so.2"] = fixtureELF(t, "libssc.so.2", nil)
				rewriteFixtureReport048(t, files, nil)
			}
			members := fixtureMembers048(files)
			if name == "wrong-metadata-mode" {
				for n := range members {
					if members[n].header.Name == "ssc-metadata-testlog.txt" {
						members[n].header.Mode = 0o755
					}
				}
			}
			if _, err := decodeArtifactArchiveProfile(writeFixtureTar(t, members), 2, rules, artifact048); err == nil {
				t.Fatal("successor accepted contract drift")
			}
		})
	}
}

// mutateMetadataRows048 changes copied structured fixture evidence only.
func mutateMetadataRows048(t *testing.T, data []byte, mutate func([]map[string]any) []map[string]any) []byte {
	t.Helper()
	var rows []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var row map[string]any
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	var result []byte
	for _, row := range mutate(rows) {
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		result = append(append(result, data...), '\n')
	}
	return result
}

// TestMetadata048SemanticEvidence refuses missing cases, skips and forged Meson status.
func TestMetadata048SemanticEvidence(t *testing.T) {
	good, text := metadataLogFixture048(t)
	if err := validateMetadataTestLog048(good, text); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func([]map[string]any) []map[string]any{
		"missing-programme":   func(r []map[string]any) []map[string]any { return r[:1] },
		"duplicate-programme": func(r []map[string]any) []map[string]any { return append(r, r[0]) },
		"wrong-name":          func(r []map[string]any) []map[string]any { r[0]["name"] = "libssc:test-metadata-diagnostics"; return r },
		"wrong-command": func(r []map[string]any) []map[string]any {
			r[0]["command"] = []string{"/tmp/test-metadata-diagnostics"}
			return r
		},
		"extra-command": func(r []map[string]any) []map[string]any {
			r[0]["command"] = []string{"/work/job/ssc-build/tests/metadata/test-metadata-diagnostics", "--skip"}
			return r
		},
		"nonzero":      func(r []map[string]any) []map[string]any { r[0]["returncode"] = 1; return r },
		"boolean-code": func(r []map[string]any) []map[string]any { r[0]["returncode"] = false; return r },
		"missing-code": func(r []map[string]any) []map[string]any { delete(r[0], "returncode"); return r },
		"status":       func(r []map[string]any) []map[string]any { r[0]["result"] = "SKIP"; return r },
		"stderr":       func(r []map[string]any) []map[string]any { r[0]["stderr"] = "runtime error\n"; return r },
		"missing-all-rows": func(r []map[string]any) []map[string]any {
			r[1]["stdout"] = strings.ReplaceAll(r[1]["stdout"].(string), "ok 4 /metadata-native/matrix-all-rows\n", "")
			return r
		},
		"skip": func(r []map[string]any) []map[string]any {
			r[1]["stdout"] = strings.ReplaceAll(r[1]["stdout"].(string), "matrix-all-rows\n", "matrix-all-rows # SKIP\n")
			return r
		},
		"todo": func(r []map[string]any) []map[string]any {
			r[0]["stdout"] = r[0]["stdout"].(string) + "# TODO missing case\n"
			return r
		},
		"wrong-plan": func(r []map[string]any) []map[string]any {
			r[0]["stdout"] = strings.ReplaceAll(r[0]["stdout"].(string), "1..6", "1..5")
			return r
		},
		"duplicate-header": func(r []map[string]any) []map[string]any {
			r[0]["stdout"] = "TAP version 14\n" + r[0]["stdout"].(string)
			return r
		},
		"unknown-case": func(r []map[string]any) []map[string]any {
			r[0]["stdout"] = strings.ReplaceAll(r[0]["stdout"].(string), "/metadata/owned-slots", "/metadata/unknown")
			return r
		},
	}
	names := make([]string, 0, len(mutations))
	for name := range mutations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			if err := validateMetadataTestLog048(mutateMetadataRows048(t, good, mutations[name]), text); err == nil {
				t.Fatal("invalid metadata evidence accepted")
			}
		})
	}
	for _, bad := range [][]byte{append(bytes.Clone(good), []byte("{}\n")...), bytes.Replace(good, []byte(`"result":"OK"`), []byte(`"result":"OK","result":"OK"`), 1), bytes.TrimSuffix(good, []byte("\n"))} {
		if err := validateMetadataTestLog048(bad, text); err == nil {
			t.Fatal("invalid JSON framing accepted")
		}
	}
	if err := validateMetadataTestLog048(good, []byte("unrelated log\n")); err == nil {
		t.Fatal("unbound text log accepted")
	}
}

// TestArtifact048RosterCopiesPreserveLegacy fixes both member counts and defensive copies.
func TestArtifact048RosterCopiesPreserveLegacy(t *testing.T) {
	old, next := ArtifactFiles(), ArtifactFiles048()
	if len(old) != 16 || len(next) != 18 || SourceCommit048 == SourceCommit {
		t.Fatal("source profiles or member counts collapsed")
	}
	copyOf := append([]string(nil), next...)
	next[0] = "caller-change"
	if !reflect.DeepEqual(copyOf, ArtifactFiles048()) || len(ArtifactFiles()) != 16 {
		t.Fatal("roster exposed policy mutation")
	}
}
