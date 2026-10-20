package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestRecipeDigests binds both reviewed scripts to their exact compiled bytes.
func TestRecipeDigests(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, script, digest string
	}{
		{"init", InitScript, InitRecipeSHA256()},
		{"source", SourceScript, SourceRecipeSHA256()},
	} {
		digest := sha256.Sum256([]byte(test.script))
		if got := hex.EncodeToString(digest[:]); got != test.digest {
			t.Errorf("%s digest = %s, want %s", test.name, test.digest, got)
		}
		if !strings.HasPrefix(test.script, "#!/bin/bash\nset -euo pipefail\n") || !strings.HasSuffix(test.script, "\n") {
			t.Errorf("%s lost strict Bash policy or final newline", test.name)
		}
		for _, setting := range []string{"export PATH=/usr/sbin:/usr/bin:/sbin:/bin", "export LANG=C.UTF-8 LC_ALL=C.UTF-8 TZ=UTC"} {
			if strings.Index(test.script, setting) < 0 || strings.Index(test.script, setting) > strings.Index(test.script, `test "$(id -u)"`) {
				t.Errorf("%s does not fix probe environment before identity checks: %s", test.name, setting)
			}
		}
	}
}

// TestInitRecipeOwnsOnlyFreshVolume preserves case proof and sets permissions
// before giving the unprivileged source-build identity ownership of the job.
func TestInitRecipeOwnsOnlyFreshVolume(t *testing.T) {
	t.Parallel()
	for _, required := range []string{
		`first_work_entry="$(find /work -mindepth 1 -maxdepth 1 -print -quit)"`, `test -z "$first_work_entry"`,
		`test ! -e /work/job`, `test ! -L /work/job`, `mkdir -m 0700 /work/job`,
		`printf 'upper\n' >/work/job/CaseProbe`, `printf 'lower\n' >/work/job/caseprobe`,
		`test ! /work/job/CaseProbe -ef /work/job/caseprobe`,
		`chmod 0444 /work/job/CaseProbe /work/job/caseprobe`,
		`chown 65532:65532 /work/job/home /work/job/cache /work/job/tmp`,
	} {
		if !strings.Contains(InitScript, required) {
			t.Errorf("missing init boundary %q", required)
		}
	}
	if strings.Index(InitScript, "chmod 0700") > strings.Index(InitScript, "chown 65532:65532") {
		t.Fatal("ownership changes before private directory modes")
	}
	if strings.Index(InitScript, `test -z "$first_work_entry"`) > strings.Index(InitScript, "mkdir -m 0700 /work/job") {
		t.Fatal("initialization writes before rejecting nonempty volumes")
	}
	for _, forbidden := range []string{"rm ", "rm -", "apt-get", "/source", "mount ", "chown -R"} {
		if strings.Contains(InitScript, forbidden) {
			t.Errorf("initialization contains unrelated operation %q", forbidden)
		}
	}
}

// CHOWN alone does not bypass directory traversal permissions. After handing
// off the private parent, root cannot resolve its children; the native init
// therefore must transfer the parent only after every child, with no later
// filesystem operation under that parent.
func TestInitRecipeHandsOffParentLast(t *testing.T) {
	t.Parallel()
	const handoff = "chown 65532:65532 /work/job/home /work/job/cache /work/job/tmp \\\n" +
		"  /work/job/CaseProbe /work/job/caseprobe\n" +
		"chown 65532:65532 /work/job\n"
	if !strings.HasSuffix(InitScript, handoff) {
		t.Fatal("CHOWN-only init must transfer all children before the private parent, then exit")
	}
	if strings.Count(InitScript, "\nchown ") != 2 {
		t.Fatal("init must have only the exact child and final parent ownership transfers")
	}
}

// TestEmbeddedExportContract compares recipe literals with independent typed
// policy, so an in-band report cannot quietly replace the compiled roster.
func TestEmbeddedExportContract(t *testing.T) {
	t.Parallel()
	type rule struct {
		Maximum int64  `json:"maximum"`
		Mode    int64  `json:"mode"`
		License string `json:"license"`
	}
	var rules map[string]rule
	decodeRecipeJSON(t, "RULES", &rules)
	want := expectedFiles()
	if len(rules) != len(want) {
		t.Fatalf("recipe files = %d, want %d", len(rules), len(want))
	}
	for name, policy := range want {
		got, found := rules[name]
		if !found || got.Maximum != policy.maximum || got.Mode != policy.mode || got.License != policy.licenseSHA256 {
			t.Errorf("recipe rule %q = %#v, want %#v", name, got, policy)
		}
	}
	var links []Link
	var fixtures, targets []string
	decodeRecipeJSON(t, "LINKS", &links)
	decodeRecipeJSON(t, "FIXTURES", &fixtures)
	decodeRecipeJSON(t, "PROXY_TARGETS", &targets)
	if !reflect.DeepEqual(links, ArtifactLinks()) || !reflect.DeepEqual(fixtures, FixtureTargets()) || !reflect.DeepEqual(targets, ProxyInstallTargets()) {
		t.Fatalf("recipe link/fixture/install contract differs: %#v %#v %#v", links, fixtures, targets)
	}
	for _, required := range []string{
		"'source_commit': '" + SourceCommit + "'", "MAX_ARCHIVE = 134217728",
		"REPORT = '" + ReportName + "'", "ARCHIVE = JOB / '" + ArchiveName + "'",
		"if name == REPORT:", "'products': products", "tarfile.USTAR_FORMAT",
		"info.uid = info.gid = info.mtime = 0", "info.uname = info.gname = ''", "info.mode = 0o777",
		"os.utime(ARCHIVE, ns=(0, 0))",
	} {
		if !strings.Contains(SourceScript, required) {
			t.Errorf("missing fixed export policy %q", required)
		}
	}
}

// TestSourceRecipeKeepsOfflineProofBoundaries checks admission, minimal smoke
// commands, selected fixtures and non-activating private installation policy.
func TestSourceRecipeKeepsOfflineProofBoundaries(t *testing.T) {
	t.Parallel()
	for _, required := range []string{
		`case "$1" in [1-8])`, `test "$(id -u)" = 65532`, `test "$(id -g)" = 65532`,
		`test "$(gcc-14 -dumpversion)" = 14`, `test "$(gcc-14 -dumpmachine)" = aarch64-linux-gnu`,
		`df --output=avail -B1 /work/job`, `test "$available_bytes" -ge 6442450944`,
		`export LD_LIBRARY_PATH="$SENSOR_STAGE/lib"`, `export LD_BIND_NOW=1`,
		`-Ddefault_library=both`, `-Dcollection=full`, `-Dqrtr=true`,
		`-Dbuild-tests=false`, `-Dintrospection=disabled`, `-Dvapi=disabled`, `-Dwerror=true`,
		`-Dssc-support=enabled`, `--prefix=/usr --libexecdir=libexec`,
		`test "$policy_dir" = /usr/share/polkit-1/actions`,
		`assert set(plan.values()) == expected`,
		`DESTDIR=/work/job/proxy-root meson install`,
		`10s "$SENSOR_STAGE/bin/ssccli" --help`, `10s "$SENSOR_STAGE/bin/ssccli" --version`,
		`'/lib/ld-linux-aarch64.so.1', '--list'`,
		`assert providers == EXPECTED_LOADERS[name]`,
		`assert '/lib/ld-linux-aarch64.so.1' in seen`,
		`assert soname != 'ld-linux-aarch64.so.1'`,
		`assert not any(char in dependency['version'] for char in '\x00\r\n\t ')`,
		`not re.search(r'\((?:RPATH|RUNPATH)\)', dynamic)`,
		`'/usr/bin/du', '--bytes', '--summarize', '/work/job'`,
		`'job_apparent_before_archive_bytes': int(du_match[1])`,
		`Path('/sys/fs/cgroup/memory.peak')`,
		`assert 0 < memory_peak < 2**63`,
	} {
		if !strings.Contains(SourceScript, required) {
			t.Errorf("missing source-build boundary %q", required)
		}
	}
	if strings.Count(SourceScript, "--wrap-mode=nodownload") != 3 {
		t.Fatal("every component must refuse subproject downloads")
	}
	if strings.Count(SourceScript, `"$SENSOR_STAGE/bin/ssccli"`) != 2 {
		t.Fatal("only explicit SSC help and version smoke calls are allowed")
	}
	plan := strings.Index(SourceScript, "assert set(plan.values()) == expected")
	compile := strings.Index(SourceScript, "meson compile -C /work/job/proxy-build")
	if plan < 0 || compile < plan {
		t.Fatal("proxy compilation precedes destination-plan validation")
	}
	for _, forbidden := range []string{
		"apt-get", "curl ", "wget ", "pip install", "git clone", "systemctl ", "udevadm ",
		"--discover", "--sample-metadata", "monitor-sensor --", "qmicli --", "qmi-proxy --",
		"rm -", "chmod -R", "chown -R", "source_integrated_prior_validation",
	} {
		if strings.Contains(SourceScript, forbidden) {
			t.Errorf("source-only recipe contains forbidden workflow %q", forbidden)
		}
	}
}

// decodeRecipeJSON reads a fixed literal for static comparison only; it never
// invokes Bash, Python, a compiler, source code, Docker or a sensor provider.
func decodeRecipeJSON(t *testing.T, name string, output any) {
	t.Helper()
	pattern := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(name) + ` = json.loads\(r'''(.*?)'''\)`)
	match := pattern.FindStringSubmatch(SourceScript)
	if len(match) != 2 {
		t.Fatalf("missing fixed recipe JSON literal %s", name)
	}
	if err := json.Unmarshal([]byte(match[1]), output); err != nil {
		t.Fatalf("decode fixed recipe literal %s: %v", name, err)
	}
}
