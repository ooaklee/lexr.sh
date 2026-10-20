package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// loaderContractCase describes one common parser input and its separate archive oracle.
type loaderContractCase struct {
	ID            string `json:"id"`
	Artifact      string `json:"artifact"`
	Origin        string `json:"origin"`
	Raw           string `json:"raw"`
	ParseAccept   bool   `json:"parse_accept"`
	ArchiveAccept bool   `json:"archive_accept"`
	ArchiveError  string `json:"archive_error"`
}

// loaderContractCorpus retains the observed rejection separately from synthetic examples.
type loaderContractCorpus struct {
	Schema     string                 `json:"schema"`
	Provenance loaderGoldenProvenance `json:"provenance"`
	Cases      []loaderContractCase   `json:"cases"`
}

// loaderGoldenProvenance records byte offsets and digests, not private diagnostic paths.
type loaderGoldenProvenance struct {
	SourceCommit           string `json:"source_commit"`
	SourceRecipeFileSHA256 string `json:"source_recipe_file_sha256"`
	SourceScriptSHA256     string `json:"source_script_sha256"`
	SourceStdoutSHA256     string `json:"source_stdout_sha256"`
	SourceStdoutBytes      int    `json:"source_stdout_bytes"`
	GoldenOffset           int    `json:"golden_offset"`
	GoldenBytes            int    `json:"golden_bytes"`
	GoldenSHA256           string `json:"golden_sha256"`
	ObservedArtifact       string `json:"observed_artifact"`
	Architecture           string `json:"architecture"`
	GLibcPackage           string `json:"glibc_package"`
	Scope                  string `json:"scope"`
}

// readLoaderContractCorpus bounds and validates the shared, data-only fixture collection.
func readLoaderContractCorpus(t *testing.T) loaderContractCorpus {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "loader-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 128*1024 || bytes.Contains(data, []byte("/Users/")) {
		t.Fatal("loader fixtures exceed their bound or contain a private machine path")
	}
	var corpus loaderContractCorpus
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&corpus); err != nil {
		t.Fatal(err)
	}
	if corpus.Schema != "lexr-sensors-loader-contract-fixtures-v1" || len(corpus.Cases) != 147 {
		t.Fatal("shared loader fixture schema or closed case count differs")
	}
	seen := map[string]bool{}
	contexts := map[string]bool{}
	for _, row := range corpus.Cases {
		if seen[row.ID] || row.ID == "" || len(row.Raw) > 4096 {
			t.Fatal("duplicate, empty or oversized loader fixture")
		}
		seen[row.ID] = true
		if _, ok := expectedLoaderProviders()[row.Artifact]; !ok {
			t.Fatal("fixture uses an unknown binary context")
		}
		if row.Origin != "actual" && row.Origin != "derived" && row.Origin != "synthetic" {
			t.Fatal("fixture loses its observation boundary")
		}
		contexts[row.Artifact] = true
	}
	if len(contexts) != len(coreELFNames) {
		t.Fatal("loader fixtures do not cover all five binary contexts")
	}
	return corpus
}

// TestLoaderContractGoldenProvenance prevents a derived listing being labelled as observed.
func TestLoaderContractGoldenProvenance(t *testing.T) {
	corpus := readLoaderContractCorpus(t)
	actual, derived := corpus.Cases[0], corpus.Cases[1]
	if actual.ID != "actual-address-only-rejected" || actual.Origin != "actual" ||
		actual.Artifact != "libqmi-glib.so.5.12.0" || actual.ParseAccept || actual.ArchiveAccept {
		t.Fatal("the actual address-only listing must remain a refusal")
	}
	digest := sha256.Sum256([]byte(actual.Raw))
	if len(actual.Raw) != 1254 || hex.EncodeToString(digest[:]) != "90479fd1ebfdf59b93e35a1f7f155caa603b973e3a73b38389d407d130152c34" {
		t.Fatal("the retained actual loader listing changed")
	}
	_, named, ok := strings.Cut(actual.Raw, "\n")
	if !ok || derived.Origin != "derived" || derived.Raw != named || !derived.ParseAccept || !derived.ArchiveAccept {
		t.Fatal("named-only fixture must be the explicit first-row removal, not a runtime claim")
	}
	actualCount := 0
	for _, row := range corpus.Cases {
		if row.Origin == "actual" {
			actualCount++
		}
	}
	if actualCount != 1 {
		t.Fatal("only one native loader listing was observed")
	}
	expectedProvenance := loaderGoldenProvenance{
		SourceCommit:           "40cc9cff4194007aab1e9f39fbf3885f88c77999",
		SourceRecipeFileSHA256: "11712bb2bec2ddfbf7ca5f7af0c3bd949dc04cc4801de6f94ffacb5799f1ce59",
		SourceScriptSHA256:     "42f9c9c038b4025d655c61982bb04341bf4f189417219f20cb17bad23f3dae9d",
		SourceStdoutSHA256:     "ccef65b646f3c1e06f3e36838d8c788373cf7f4ebe51a59715e43d7406485a57",
		SourceStdoutBytes:      323204,
		GoldenOffset:           321950,
		GoldenBytes:            1254,
		GoldenSHA256:           "90479fd1ebfdf59b93e35a1f7f155caa603b973e3a73b38389d407d130152c34",
		ObservedArtifact:       "libqmi-glib.so.5.12.0",
		Architecture:           "aarch64",
		GLibcPackage:           "libc6:arm64=2.43-2ubuntu2.4",
		Scope:                  "Only the first libqmi listing is an observed native result. Named-only derivation and all other cases are synthetic software fixtures, not observations of the environment-controlled loader or hardware.",
	}
	if corpus.Provenance != expectedProvenance || corpus.Provenance.GoldenOffset+corpus.Provenance.GoldenBytes != corpus.Provenance.SourceStdoutBytes {
		t.Fatal("actual source digests, byte offsets or observation scope changed")
	}
}

// TestLoaderContractGoAndArchive exercises the real Go parser and refreshed serialized ELF archives.
func TestLoaderContractGoAndArchive(t *testing.T) {
	for _, row := range readLoaderContractCorpus(t).Cases {
		t.Run(row.ID, func(t *testing.T) {
			providers, err := parseLoader(row.Raw)
			accepted := err == nil && reflect.DeepEqual(providers, expectedLoaderProviders()[row.Artifact])
			if accepted != row.ParseAccept {
				t.Fatalf("Go parser/role authority accepted=%v, want=%v: %v", accepted, row.ParseAccept, err)
			}
			files, rules := artifactFixture(t)
			rewriteLoaderFixture(t, files, func(raw map[string]string) { raw[row.Artifact] = row.Raw })
			_, archiveErr := decodeArtifactArchive(writeFixtureTar(t, fixtureMembers(files)), 2, rules)
			if row.ArchiveAccept {
				if archiveErr != nil {
					t.Fatalf("valid shared loader fixture refused: %v", archiveErr)
				}
				return
			}
			expected := row.ArchiveError
			if expected == "" {
				expected = "raw loader origins differ"
			}
			if archiveErr == nil || !strings.Contains(archiveErr.Error(), expected) {
				t.Fatalf("fixture did not fail its intended loader gate %q: %v", expected, archiveErr)
			}
		})
	}
}

// loaderRecipeSegment extracts only a uniquely marked, bounded part of the compiled recipe.
func loaderRecipeSegment(t *testing.T, name string) string {
	t.Helper()
	begin, end := "# LEXR_SENSOR_LOADER_"+name+"_BEGIN\n", "# LEXR_SENSOR_LOADER_"+name+"_END\n"
	if strings.Count(SourceScript, begin) != 1 || strings.Count(SourceScript, end) != 1 {
		t.Fatal("loader recipe extraction markers must be unique")
	}
	start := strings.Index(SourceScript, begin) + len(begin)
	finish := strings.Index(SourceScript, end)
	if finish <= start || finish-start > 16*1024 {
		t.Fatal("loader recipe segment is missing, inverted or oversized")
	}
	return SourceScript[start:finish]
}

// loaderContractOutput retains at most 32 KiB from one Python diagnostic stream.
type loaderContractOutput struct {
	data     bytes.Buffer
	overflow bool
}

// Write consumes the complete write while recording truncation as a fixture failure.
func (output *loaderContractOutput) Write(data []byte) (int, error) {
	count := len(data)
	available := 32*1024 - output.data.Len()
	if len(data) > available {
		output.overflow = true
		data = data[:available]
	}
	_, _ = output.data.Write(data)
	return count, nil
}

// runLoaderContractPython runs only extracted pure functions, never a target loader or build script.
func runLoaderContractPython(t *testing.T, program string, input any) []byte {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("the controlled loader fixture requires Python 3:", err)
	}
	python, err = filepath.EvalSymlinks(python)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-I", "-S", "-B", "-c", program)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=" + filepath.Dir(python), "LANG=C", "LC_ALL=C"}
	cmd.Stdin = bytes.NewReader(data)
	cmd.WaitDelay = time.Second
	var stdout, stderr loaderContractOutput
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil || ctx.Err() != nil || stdout.overflow || stderr.overflow {
		t.Fatalf("controlled Python fixture did not complete normally: %v; deadline=%v; stdout=%q; stderr=%q",
			err, ctx.Err(), stdout.data.String(), stderr.data.String())
	}
	if stderr.data.Len() != 0 {
		t.Fatalf("unexpected Python fixture diagnostic: %s", stderr.data.String())
	}
	return stdout.data.Bytes()
}

// TestSourceLoaderClassifierSharedCorpus exercises the actual extracted producer classifier.
func TestSourceLoaderClassifierSharedCorpus(t *testing.T) {
	corpus := readLoaderContractCorpus(t)
	program := "import json, posixpath, re, sys\nfrom pathlib import Path\nPREFIX = Path('/work/job/providers')\n" +
		loaderRecipeSegment(t, "POLICY") + "\n" + loaderRecipeSegment(t, "PARSER") + "\n" + `
cases = json.load(sys.stdin)
rows = []
for case in cases:
    try:
        providers = parse_loader_listing(case['raw'], EXPECTED_LOADERS[case['artifact']])
    except AssertionError:
        rows.append({'id': case['id'], 'accepted': False, 'error': 'AssertionError', 'providers': None})
    else:
        rows.append({'id': case['id'], 'accepted': True, 'error': '', 'providers': providers})
print(json.dumps({'policy': EXPECTED_LOADERS, 'rows': rows}, sort_keys=True))
`
	var result struct {
		Policy map[string]map[string]string `json:"policy"`
		Rows   []struct {
			ID        string            `json:"id"`
			Accepted  bool              `json:"accepted"`
			Error     string            `json:"error"`
			Providers map[string]string `json:"providers"`
		} `json:"rows"`
	}
	if err := decodeJSON(runLoaderContractPython(t, program, corpus.Cases), &result, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Policy, expectedLoaderProviders()) || len(result.Rows) != len(corpus.Cases) {
		t.Fatal("Python's actual fixed provider policy or complete result count differs")
	}
	for index, row := range result.Rows {
		expected := corpus.Cases[index]
		if row.ID != expected.ID || row.Accepted != expected.ParseAccept {
			t.Fatalf("producer fixture %q accepted=%v, want=%v", row.ID, row.Accepted, expected.ParseAccept)
		}
		if row.Accepted {
			if row.Error != "" || !reflect.DeepEqual(row.Providers, expectedLoaderProviders()[expected.Artifact]) {
				t.Fatal("accepted producer row has false provider authority")
			}
		} else if row.Error != "AssertionError" || row.Providers != nil {
			t.Fatal("producer refusal must be its parser assertion, not an arbitrary process failure")
		}
	}
}

// TestSourceLoaderObserverKeepsEnvironmentLocal checks exact production functions with in-memory observations.
func TestSourceLoaderObserverKeepsEnvironmentLocal(t *testing.T) {
	program := "import io, json, os, subprocess, sys\nfrom pathlib import PurePosixPath\nfrom types import SimpleNamespace\n" +
		loaderRecipeSegment(t, "OBSERVER") + "\n" + `
real_stdout, real_stderr = sys.stdout, sys.stderr
sys.stdout, sys.stderr = io.StringIO(), io.StringIO()
os.environ = {'LD_TRACE_LOADED_OBJECTS': '2', 'LD_LIBRARY_PATH': '/work/job/providers/lib',
              'LD_BIND_NOW': '1', 'KEEP': 'one'}
events, mode = [], 'ok'
def fake_run(argv, **kwargs):
    events.append({'argv': argv, 'kwargs': kwargs, 'kwargs_keys': sorted(kwargs)})
    if mode == 'timeout':
        raise subprocess.TimeoutExpired(argv, kwargs['timeout'])
    return SimpleNamespace(stdout='observed stdout\n', stderr='observed stderr\n',
                           returncode=7 if mode == 'nonzero' else 0)
subprocess.run = fake_run
normal = observe(['/usr/bin/readelf', '-hW', '/fixture/library.so'])
first = observe_loader(PurePosixPath('/fixture/library.so'))
first_environment = events[1]['kwargs']['env']
first_unchanged = os.environ == {'LD_TRACE_LOADED_OBJECTS': '2',
    'LD_LIBRARY_PATH': '/work/job/providers/lib', 'LD_BIND_NOW': '1', 'KEEP': 'one'}
os.environ['KEEP'] = 'two'
second = observe_loader(PurePosixPath('/fixture/second.so'))
second_environment = events[2]['kwargs']['env']
expected_environment = dict(os.environ)
mode, nonzero_assert = 'nonzero', False
try:
    observe_loader(PurePosixPath('/fixture/failing.so'))
except AssertionError as error:
    nonzero_assert = error.args == (['/lib/ld-linux-aarch64.so.1', '--list', '/fixture/failing.so'],)
mode, timeout_propagated = 'timeout', False
try:
    observe_loader(PurePosixPath('/fixture/timeout.so'))
except subprocess.TimeoutExpired as error:
    timeout_propagated = error.timeout == 30 and error.cmd[-1] == '/fixture/timeout.so'
result = {'events': events, 'returns': [normal, first, second], 'stdout': sys.stdout.getvalue(),
          'stderr': sys.stderr.getvalue(), 'nonzero_assert': nonzero_assert,
          'timeout_propagated': timeout_propagated,
          'environment_unchanged': first_unchanged and os.environ == expected_environment,
          'copies_distinct': first_environment is not os.environ and
              second_environment is not os.environ and second_environment is not first_environment}
sys.stdout, sys.stderr = real_stdout, real_stderr
print(json.dumps(result, sort_keys=True))
`
	var result struct {
		Events []struct {
			Argv   []string `json:"argv"`
			Keys   []string `json:"kwargs_keys"`
			Kwargs struct {
				Stdout  int               `json:"stdout"`
				Stderr  int               `json:"stderr"`
				Text    bool              `json:"text"`
				Timeout int               `json:"timeout"`
				Check   bool              `json:"check"`
				Env     map[string]string `json:"env"`
			} `json:"kwargs"`
		} `json:"events"`
		Returns              []string `json:"returns"`
		Stdout               string   `json:"stdout"`
		Stderr               string   `json:"stderr"`
		NonzeroAssert        bool     `json:"nonzero_assert"`
		TimeoutPropagated    bool     `json:"timeout_propagated"`
		EnvironmentUnchanged bool     `json:"environment_unchanged"`
		CopiesDistinct       bool     `json:"copies_distinct"`
	}
	if err := decodeJSON(runLoaderContractPython(t, program, nil), &result, true); err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 5 || !result.NonzeroAssert || !result.TimeoutPropagated ||
		!result.EnvironmentUnchanged || !result.CopiesDistinct {
		t.Fatal("loader observer lost failure propagation, copied environment or exact call count")
	}
	for index, event := range result.Events {
		if !reflect.DeepEqual(event.Keys, []string{"check", "env", "stderr", "stdout", "text", "timeout"}) {
			t.Fatal("observer subprocess keyword set changed")
		}
		if event.Kwargs.Stdout != -1 || event.Kwargs.Stderr != -1 || !event.Kwargs.Text ||
			event.Kwargs.Timeout != 30 || event.Kwargs.Check {
			t.Fatal("observer capture, timeout or explicit return-code policy changed")
		}
		if index == 0 {
			if event.Kwargs.Env != nil || !reflect.DeepEqual(event.Argv, []string{"/usr/bin/readelf", "-hW", "/fixture/library.so"}) {
				t.Fatal("normal observation leaked the loader-specific environment")
			}
			continue
		}
		paths := []string{"/fixture/library.so", "/fixture/second.so", "/fixture/failing.so", "/fixture/timeout.so"}
		if !reflect.DeepEqual(event.Argv, []string{"/lib/ld-linux-aarch64.so.1", "--list", paths[index-1]}) {
			t.Fatal("loader observer changed its fixed invocation")
		}
		keep := "two"
		if index == 1 {
			keep = "one"
		}
		expected := map[string]string{"LD_TRACE_LOADED_OBJECTS": "1", "LD_LIBRARY_PATH": "/work/job/providers/lib", "LD_BIND_NOW": "1", "KEEP": keep}
		if !reflect.DeepEqual(event.Kwargs.Env, expected) {
			t.Fatal("loader child did not override only the trace setting in its private copy")
		}
	}
	if !reflect.DeepEqual(result.Returns, []string{"observed stdout\n", "observed stdout\n", "observed stdout\n"}) ||
		result.Stdout != strings.Repeat("observed stdout\n", 4) || result.Stderr != strings.Repeat("observed stderr\n", 4) {
		t.Fatal("observer did not retain complete child streams and successful return values")
	}
}
