package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// recipeJSON048 decodes one fixed embedded policy without running the source script.
func recipeJSON048(t *testing.T, name string, into any) {
	t.Helper()
	pattern := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(name) + ` = json\.loads\(r'''(.*?)'''\)`)
	groups := pattern.FindStringSubmatch(SourceScript048())
	if len(groups) != 2 {
		t.Fatalf("missing unique recipe policy %s", name)
	}
	if err := json.Unmarshal([]byte(groups[1]), into); err != nil {
		t.Fatal(err)
	}
}

// TestSourceRecipe048ExactPolicy checks successor changes and the unchanged legacy bytes.
func TestSourceRecipe048ExactPolicy(t *testing.T) {
	script := SourceScript048()
	for _, required := range []string{
		"-Dbuild-tests=false -Dbuild-metadata-tests=true", "--setup=metadata --suite=metadata-local",
		"--logbase ssc-metadata-testlog", "test-metadata-diagnostics test-metadata-diagnostics-integration",
		"'ssc-metadata-testlog-metadata.' + extension", "'ssc-metadata-testlog.' + extension",
		"'source_commit': '" + SourceCommit048 + "'", "'libssc': '0.4.8'", "libssc version 0.4.8",
		"meson setup /work/job/qmi-build", "meson compile -C /work/job/qmi-build",
		"meson setup /work/job/ssc-build", "meson setup /work/job/proxy-build",
		"MAX_ARCHIVE = 134217728", "info.uid = info.gid = info.mtime = 0",
		"test \"$available_bytes\" -ge 6442450944", "type(row['returncode']) is int",
		"assert row['command'] == [command]", "assert metadata_seen == set(METADATA_CASES)",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("successor recipe missing %q", required)
		}
	}
	if strings.Contains(script, "0.4.7") || strings.Contains(script, SourceCommit) || strings.Contains(SourceScript, "ssc-metadata-testlog") || strings.Contains(SourceScript, "-Dbuild-metadata-tests=true") {
		t.Fatal("recipe profiles collapsed or successor retained a stale version")
	}
	if strings.Count(script, "--wrap-mode=nodownload") != 3 || strings.Count(script, "meson test -C /work/job/ssc-build") != 1 {
		t.Fatal("offline stages or exact metadata execution changed")
	}
	for _, forbidden := range []string{"/prior", "apt-get", "systemctl ", "qrtr_server", "LD_PRELOAD", "rm -"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("unexpected successor operation %q", forbidden)
		}
	}
	digest := sha256.Sum256([]byte(script))
	if SourceRecipeSHA256048() != hex.EncodeToString(digest[:]) || SourceRecipeSHA256048() == SourceRecipeSHA256() {
		t.Fatal("successor digest does not bind its exact distinct bytes")
	}
	type rule struct {
		Maximum int64  `json:"maximum"`
		Mode    int64  `json:"mode"`
		License string `json:"license"`
	}
	var rules map[string]rule
	recipeJSON048(t, "RULES", &rules)
	if len(rules) != 18 {
		t.Fatal("recipe roster is not exactly 18 regular files")
	}
	for name, wanted := range expectedFiles048() {
		got, found := rules[name]
		if !found || got.Maximum != wanted.maximum || got.Mode != wanted.mode || got.License != wanted.licenseSHA256 {
			t.Fatalf("recipe rule differs for %s", name)
		}
	}
	var cases map[string][]string
	recipeJSON048(t, "METADATA_CASES", &cases)
	if !reflect.DeepEqual(cases, metadataTestCases048()) {
		t.Fatal("recipe and independent decoder disagree about exact TAP cases")
	}
}

// TestSourceRecipe048BashSyntax parses only the compiled script, never a native build.
func TestSourceRecipe048BashSyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("host Bash parser unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-n")
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "LANG=C"}
	command.Stdin = strings.NewReader(SourceScript048())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("compiled Bash syntax: %v: %s", err, output)
	}
}

// metadataPythonFixture048 executes only the pure metadata-export policy over memory paths.
func metadataPythonFixture048(t *testing.T, data, text []byte) error {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("host Python unavailable for pure recipe-policy fixture")
	}
	input, err := json.Marshal(map[string]string{"json": string(data), "txt": string(text)})
	if err != nil {
		t.Fatal(err)
	}
	const harness = `import json, re, sys
fixture = json.load(sys.stdin)
products = {}
class MemoryPath:
    def __init__(self, name): self.name = name
    def __truediv__(self, child): return MemoryPath(self.name + '/' + child)
    def read_text(self): return products[self.name].decode('utf-8')
JOB, EXPORT = MemoryPath('job'), MemoryPath('export')
RULES = {name: {'maximum': 8388608} for name in ('ssc-metadata-testlog.json', 'ssc-metadata-testlog.txt')}
def regular_bytes(path, maximum):
    expected = 'job/ssc-build/meson-logs/ssc-metadata-testlog-metadata.'
    assert path.name in (expected + 'json', expected + 'txt')
    data = fixture[path.name.rsplit('.', 1)[1]].encode('utf-8')
    assert 0 < len(data) <= maximum
    return data
def write_product(name, data):
    assert name in RULES and 'export/' + name not in products
    products['export/' + name] = data
`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-I", "-B", "-c", harness+metadataExport048)
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "LANG=C"}
	command.Stdin = bytes.NewReader(input)
	return command.Run()
}

// TestSourceRecipe048PureMetadataPolicy proves semantic positives and negatives without IO or target execution.
func TestSourceRecipe048PureMetadataPolicy(t *testing.T) {
	data, text := metadataLogFixture048(t)
	if err := metadataPythonFixture048(t, data, text); err != nil {
		t.Fatalf("pure Python positive policy: %v", err)
	}
	for _, bad := range [][]byte{
		bytes.ReplaceAll(data, []byte(`"returncode":0`), []byte(`"returncode":1`)),
		bytes.ReplaceAll(data, []byte("matrix-all-rows\\n"), []byte("matrix-all-rows # SKIP\\n")),
		bytes.ReplaceAll(data, []byte("1..6"), []byte("1..5")),
		bytes.ReplaceAll(data, []byte("/work/job/ssc-build/"), []byte("/tmp/ssc-build/")),
		append(bytes.Clone(data), data...),
	} {
		if err := metadataPythonFixture048(t, bad, text); err == nil {
			t.Fatal("pure Python metadata policy accepted a negative control")
		}
	}
	if err := metadataPythonFixture048(t, data, []byte("unbound text log\n")); err == nil {
		t.Fatal("pure Python policy accepted unbound text log")
	}
}

// TestSourceRecipe048AnchorDriftRefuses prevents silently partial recipe derivation.
func TestSourceRecipe048AnchorDriftRefuses(t *testing.T) {
	for _, value := range []string{"missing", "anchor anchor"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("nonunique recipe anchor did not refuse")
				}
			}()
			replaceRecipe048(value, "anchor", "replacement")
		}()
	}
}
