package platform

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// outputHelperCommand runs this test binary as a portable child process, so
// stream tests exercise os/exec without shell dependencies or global I/O swaps.
func outputHelperCommand(t *testing.T, mode string) Command {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{
		Name: executable, Args: []string{"-test.run=^TestExecRunnerOutputHelper$", "--", mode},
		Env: []string{"LEXR_TEST_OUTPUT_HELPER=1"},
	}
}

// TestExecRunnerOutputHelper emits distinguishable streams only when invoked
// as a child. The inheritance case creates a second child with a zero-value runner.
func TestExecRunnerOutputHelper(t *testing.T) {
	if os.Getenv("LEXR_TEST_OUTPUT_HELPER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "inherit" {
		if err := (ExecRunner{}).Run(context.Background(), outputHelperCommand(t, "emit")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	fmt.Fprintln(os.Stdout, "child stdout")
	fmt.Fprintln(os.Stderr, "child stderr")
	if mode == "fail" {
		os.Exit(7)
	}
	os.Exit(0)
}

// TestExecRunnerDefaultStreams exercises actual child output, including
// unchanged zero-value inheritance inside a separately captured parent process.
func TestExecRunnerDefaultStreams(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"emit", "inherit"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			runner := ExecRunner{Stdout: &stdout, Stderr: &stderr}
			if err := runner.Run(t.Context(), outputHelperCommand(t, mode)); err != nil {
				t.Fatal(err)
			}
			if stdout.String() != "child stdout\n" || stderr.String() != "child stderr\n" {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

// TestDiagnosticRunnerRetainsBothStreams prevents cleanup and build output
// being lost while keeping it off a machine-readable result stream.
func TestDiagnosticRunnerRetainsBothStreams(t *testing.T) {
	t.Parallel()
	var diagnostics bytes.Buffer
	if err := NewDiagnosticRunner(&diagnostics).Run(t.Context(), outputHelperCommand(t, "emit")); err != nil {
		t.Fatal(err)
	}
	text := diagnostics.String()
	if len(text) != len("child stdout\nchild stderr\n") ||
		!strings.Contains(text, "child stdout\n") || !strings.Contains(text, "child stderr\n") {
		t.Fatalf("diagnostics=%q", text)
	}
	if runner := NewDiagnosticRunner(nil); runner.Stdout != os.Stderr || runner.Stderr != os.Stderr {
		t.Fatal("nil diagnostic destination did not select stderr")
	}
}

// TestExecRunnerCommandStreamsTakePrecedence protects binary streams such as
// zstd output from being replaced by a runner's diagnostic defaults.
func TestExecRunnerCommandStreamsTakePrecedence(t *testing.T) {
	t.Parallel()
	var diagnostics, stdout, stderr bytes.Buffer
	command := outputHelperCommand(t, "emit")
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := NewDiagnosticRunner(&diagnostics).Run(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "child stdout\n" || stderr.String() != "child stderr\n" || diagnostics.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q diagnostics=%q", stdout.String(), stderr.String(), diagnostics.String())
	}
}

// TestExecRunnerExplicitStdoutRetainsDefaultDiagnostics models compressors
// which write binary data to a file while leaving stderr on the caller's stream.
func TestExecRunnerExplicitStdoutRetainsDefaultDiagnostics(t *testing.T) {
	t.Parallel()
	var stdout, diagnostics bytes.Buffer
	command := outputHelperCommand(t, "emit")
	command.Stdout = &stdout
	if err := NewDiagnosticRunner(&diagnostics).Run(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "child stdout\n" || diagnostics.String() != "child stderr\n" {
		t.Fatalf("stdout=%q diagnostics=%q", stdout.String(), diagnostics.String())
	}
}

// TestExecRunnerCaptureIgnoresDiagnosticDefaults keeps successful probe data
// isolated and preserves a failed child's stderr in the returned error.
func TestExecRunnerCaptureIgnoresDiagnosticDefaults(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"emit", "fail"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			var diagnostics bytes.Buffer
			output, err := NewDiagnosticRunner(&diagnostics).Capture(t.Context(), outputHelperCommand(t, mode))
			if mode == "emit" && (err != nil || string(output) != "child stdout\n") {
				t.Fatalf("output=%q err=%v", output, err)
			}
			if mode == "fail" && (err == nil || !strings.Contains(err.Error(), "child stderr") || output != nil) {
				t.Fatalf("output=%q err=%v", output, err)
			}
			if diagnostics.Len() != 0 {
				t.Fatalf("capture leaked diagnostics: %q", diagnostics.String())
			}
		})
	}
}
