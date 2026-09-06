package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
)

// TestImageValidateJSONRoutesRealToolDiagnostics invokes the production CLI
// wiring and real child processes. The fixture fails manifest extraction before
// an adapter can run, so no Docker daemon, real image or storage device is needed.
func TestImageValidateJSONRoutesRealToolDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture; portable process routing is tested in platform")
	}
	directory := t.TempDir()
	fixture := `#!/bin/sh
case "$1" in
    info) printf 'fixture Docker version\n' ;;
    image) exit 0 ;;
    run)
        printf 'fixture tool stdout\n'
        printf 'fixture tool stderr\n' >&2
        exit 9
        ;;
    *) exit 10 ;;
esac
`
	if err := os.WriteFile(filepath.Join(directory, "docker"), []byte(fixture), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	imagePath := filepath.Join(directory, "fixture.iso")
	if err := os.WriteFile(imagePath, []byte("bounded routing fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	var result, diagnostics bytes.Buffer
	command := NewRootCommand(strings.NewReader(""), &result, &diagnostics)
	command.SetArgs([]string{"image", "validate", imagePath, "--json"})
	if err := command.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "extract") {
		t.Fatalf("expected extraction failure, got %v", err)
	}
	decoder := json.NewDecoder(&result)
	var report imagecontract.ValidationReport
	if err := decoder.Decode(&report); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if report.Valid || report.Path != imagePath {
		t.Fatalf("unexpected report: %+v", report)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("trailing result output: %v", err)
	}
	for _, expected := range []string{"fixture tool stdout\n", "fixture tool stderr\n"} {
		if !strings.Contains(diagnostics.String(), expected) {
			t.Fatalf("child output bypassed caller diagnostics: %q", diagnostics.String())
		}
	}
}
