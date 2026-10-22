package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// requireGitHost skips process and publication fixtures on unsupported hosts.
func requireGitHost(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("isolated Git execution requires Linux or macOS")
	}
}

// fakeSourceGit installs a private finite helper without invoking network Git.
func fakeSourceGit(t *testing.T, script string) {
	t.Helper()
	requireGitHost(t)
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "git"), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// isolatedFixtureGit runs bounded local Git fixture operations through policy.
func isolatedFixtureGit(t *testing.T, args ...string) []byte {
	t.Helper()
	result, err := (&isolatedGitRunner{}).Capture(context.Background(), platform.Command{Name: "git", Args: args})
	if err != nil {
		t.Fatalf("Git fixture %q: %v", args, err)
	}
	return result
}

// TestGitIsolationRejectsOtherProgramsAndCallerEnvironment keeps the process
// boundary limited to Git with its own environment and no supplied stdin.
func TestGitIsolationRejectsOtherProgramsAndCallerEnvironment(t *testing.T) {
	runner := &isolatedGitRunner{}
	for _, command := range []platform.Command{
		{Name: "sh", Args: []string{"-c", "not executed"}},
		{Name: "/usr/bin/git"},
		{Name: "git", Env: []string{"GIT_CONFIG_COUNT=1"}},
		{Name: "git", Stdin: strings.NewReader("untrusted")},
	} {
		if _, err := runner.Capture(context.Background(), command); err == nil {
			t.Fatalf("accepted noncompiled process request %#v", command)
		}
	}
}

// TestGitEnvironmentIsReplaceOnlyAndWhitelisted rejects inherited Git,
// credential, loader, proxy and temporary-path settings.
func TestGitEnvironmentIsReplaceOnlyAndWhitelisted(t *testing.T) {
	for _, name := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0",
		"GIT_CONFIG", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_ASKPASS", "GIT_SSH_COMMAND",
		"GIT_TEMPLATE_DIR", "SSH_ASKPASS", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "GITHUB_TOKEN", "GH_TOKEN",
		"HTTPS_PROXY", "HTTP_PROXY", "SSL_CERT_FILE", "TMPDIR", "TMP", "TEMP",
	} {
		t.Setenv(name, "must-not-cross-boundary")
	}
	actual := make(map[string]string)
	for _, entry := range isolatedGitEnvironment() {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || actual[name] != "" {
			t.Fatalf("malformed or duplicate environment entry %q", entry)
		}
		actual[name] = value
	}
	expected := map[string]string{
		"PATH": os.Getenv("PATH"), "LANG": "C", "LC_ALL": "C", "TZ": "UTC",
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_GLOBAL": os.DevNull,
		"GIT_ATTR_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0", "GIT_OPTIONAL_LOCKS": "0",
		"GIT_LFS_SKIP_SMUDGE": "1", "GIT_NO_REPLACE_OBJECTS": "1", "GIT_NO_LAZY_FETCH": "1",
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("child environment is not the closed whitelist: %#v", actual)
	}
	if _, ok := actual["HOME"]; ok {
		t.Fatal("the runner must not supply or repurpose HOME")
	}
}

// TestGitPolicyPrefixesEveryInvocation checks that fixed policy precedes all
// argument-separated Git commands, including component-validation reads.
func TestGitPolicyPrefixesEveryInvocation(t *testing.T) {
	fakeSourceGit(t, `printf '%s\n' "$@"`)
	actual, err := (&isolatedGitRunner{}).Capture(context.Background(), platform.Command{Name: "git", Args: []string{"rev-parse", "HEAD"}})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(actual), "\n"), "\n")
	expected := append(safeGitArguments(), "rev-parse", "HEAD")
	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("policy not applied before Git arguments: %#v", args)
	}
	for _, policy := range []string{
		"core.hooksPath=" + os.DevNull, "init.templateDir=" + os.DevNull, "core.attributesFile=" + os.DevNull,
		"credential.helper=", "protocol.allow=never", "protocol.https.allow=always", "http.followRedirects=false",
		"http.sslVerify=true", "fetch.recurseSubmodules=false", "submodule.recurse=false", "core.autocrlf=false",
	} {
		if !strings.Contains(string(actual), policy+"\n") {
			t.Fatalf("required Git policy omitted: %s", policy)
		}
	}
}

// TestGitFailureDoesNotReleaseStdoutOrStderr ensures partial output and private
// transport diagnostics cannot escape after an unsuccessful invocation.
func TestGitFailureDoesNotReleaseStdoutOrStderr(t *testing.T) {
	fakeSourceGit(t, `printf 'partial secret output'; printf 'private-credential-value' >&2; exit 7`)
	var stdout, stderr bytes.Buffer
	err := (&isolatedGitRunner{}).Run(context.Background(), platform.Command{Name: "git", Args: []string{"status"}, Stdout: &stdout, Stderr: &stderr})
	if err == nil || !strings.Contains(err.Error(), "exit code 7") {
		t.Fatalf("expected bounded exit failure, got %v", err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "credential") {
		t.Fatalf("failed Git leaked diagnostics: stdout=%q stderr=%q err=%v", &stdout, &stderr, err)
	}
}

// TestGitSuccessfulRunReleasesOnlyStdout preserves validated caller output
// while never forwarding Git's diagnostic stream.
func TestGitSuccessfulRunReleasesOnlyStdout(t *testing.T) {
	fakeSourceGit(t, `printf 'authenticated bytes'; printf 'private diagnostics' >&2`)
	var stdout, stderr bytes.Buffer
	err := (&isolatedGitRunner{}).Run(context.Background(), platform.Command{Name: "git", Args: []string{"show"}, Stdout: &stdout, Stderr: &stderr})
	if err != nil || stdout.String() != "authenticated bytes" || stderr.Len() != 0 {
		t.Fatalf("unexpected streams: %q %q %v", &stdout, &stderr, err)
	}
	if _, err := (&isolatedGitRunner{}).Capture(context.Background(), platform.Command{Name: "git", Stdout: &stdout}); err == nil {
		t.Fatal("Capture accepted a second output destination")
	}
}

// TestGitCaptureOverflowCancelsAndReturnsNoPartialOutput enforces the smaller
// metadata cap without releasing a truncated result as successful evidence.
func TestGitCaptureOverflowCancelsAndReturnsNoPartialOutput(t *testing.T) {
	fakeSourceGit(t, `dd if=/dev/zero bs=65536 count=2 2>/dev/null`)
	actual, err := (&isolatedGitRunner{}).Capture(context.Background(), platform.Command{Name: "git", Args: []string{"show"}})
	if err == nil || !strings.Contains(err.Error(), "stdout exceeds") || len(actual) != 0 {
		t.Fatalf("metadata overflow not refused: %d bytes, %v", len(actual), err)
	}
}

// TestGitRunPreservesCameraBlobLimits keeps the existing camera patch-size
// contract instead of applying Git's smaller metadata limit to source blobs.
func TestGitRunPreservesCameraBlobLimits(t *testing.T) {
	// Existing camera patches permit 4 MiB and tuning permits 2 MiB. Run must
	// not impose the smaller metadata cap on their bounded component writer.
	fakeSourceGit(t, `dd if=/dev/zero bs=1048576 count=4 2>/dev/null`)
	var stdout bytes.Buffer
	err := (&isolatedGitRunner{}).Run(context.Background(), platform.Command{Name: "git", Args: []string{"show"}, Stdout: &stdout})
	if err != nil || stdout.Len() != 4<<20 {
		t.Fatalf("valid component blob was narrowed: %d bytes %v", stdout.Len(), err)
	}
}

// TestGitRunOverflowCancelsBeforeReleasingOutput bounds source-blob reads and
// refuses oversized output before it reaches the component's consumer.
func TestGitRunOverflowCancelsBeforeReleasingOutput(t *testing.T) {
	fakeSourceGit(t, `dd if=/dev/zero bs=1048576 count=9 2>/dev/null`)
	var stdout bytes.Buffer
	err := (&isolatedGitRunner{}).Run(context.Background(), platform.Command{Name: "git", Args: []string{"show"}, Stdout: &stdout})
	if err == nil || !strings.Contains(err.Error(), "stdout exceeds") || stdout.Len() != 0 {
		t.Fatalf("blob overflow not refused: %d bytes %v", stdout.Len(), err)
	}
}

// TestGitStderrOverflowIsBoundedAndRedacted limits failed transport diagnostics
// and reports only the compiled overflow error.
func TestGitStderrOverflowIsBoundedAndRedacted(t *testing.T) {
	fakeSourceGit(t, `dd if=/dev/zero bs=65536 count=2 1>&2 2>/dev/null`)
	_, err := (&isolatedGitRunner{}).Capture(context.Background(), platform.Command{Name: "git"})
	if err == nil || err.Error() != "isolated Git stderr exceeds its compiled limit" {
		t.Fatalf("stderr overflow not bounded/redacted: %v", err)
	}
}

// TestGitAlreadyCancelledDoesNotStartProcess refuses work before crossing the
// process boundary when the caller has already cancelled.
func TestGitAlreadyCancelledDoesNotStartProcess(t *testing.T) {
	requireGitHost(t)
	marker := filepath.Join(t.TempDir(), "executed")
	fakeSourceGit(t, "printf called >"+sourceShellQuote(marker))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&isolatedGitRunner{}).Run(ctx, platform.Command{Name: "git"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pre-cancelled Git started a process")
	}
}

// TestGitCancellationStopsOwnedChildWriter verifies cancellation reaches Git's
// owned descendant group rather than terminating only the parent process.
func TestGitCancellationStopsOwnedChildWriter(t *testing.T) {
	requireGitHost(t)
	marker := filepath.Join(t.TempDir(), "child-writes")
	// The child is finite even if this regression fails, so a broken runner
	// cannot leave an unbounded test process behind.
	script := fmt.Sprintf(`(i=0; while [ "$i" -lt 30 ]; do printf x >>%s; i=$((i+1)); sleep 0.05; done) &
wait`, sourceShellQuote(marker))
	fakeSourceGit(t, script)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (&isolatedGitRunner{}).Run(ctx, platform.Command{Name: "git"}) }()
	limit := time.Now().Add(3 * time.Second)
	for {
		info, err := os.Stat(marker)
		if err == nil && info.Size() > 0 {
			break
		}
		if time.Now().After(limit) {
			t.Fatal("test child did not begin writing")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling Git did not terminate its owned group")
	}
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	after, err := os.ReadFile(marker)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("a descendant continued writing after cancellation returned")
	}
}

// TestGitDeadlineIsBounded checks that a shorter caller deadline is preserved.
func TestGitDeadlineIsBounded(t *testing.T) {
	fakeSourceGit(t, `sleep 2`)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := (&isolatedGitRunner{}).Run(ctx, platform.Command{Name: "git"})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("deadline not honored: %v after %s", err, time.Since(started))
	}
}

// TestGitWaitDelayStopsPipeHoldingDescendant cleans up a helper that retains
// inherited pipes after its Git parent exits.
func TestGitWaitDelayStopsPipeHoldingDescendant(t *testing.T) {
	requireGitHost(t)
	marker := filepath.Join(t.TempDir(), "after-parent-exit")
	fakeSourceGit(t, fmt.Sprintf(`(i=0; while [ "$i" -lt 40 ]; do printf x >>%s; i=$((i+1)); sleep 0.05; done) &
exit 0`, sourceShellQuote(marker)))
	err := (&isolatedGitRunner{}).Run(context.Background(), platform.Command{Name: "git"})
	if err == nil {
		t.Fatal("pipe-holding descendant was silently accepted")
	}
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	after, err := os.ReadFile(marker)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("pipe-holding descendant survived bounded wait failure")
	}
}

// TestGitIgnoresAmbientHooksFiltersTemplatesAndRepositoryRedirects exercises
// real local Git with hostile inherited configuration and executable hooks.
func TestGitIgnoresAmbientHooksFiltersTemplatesAndRepositoryRedirects(t *testing.T) {
	requireGitHost(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git executable not installed")
	}
	root := t.TempDir()
	hooks := filepath.Join(root, "hooks")
	templates := filepath.Join(root, "templates")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(templates, "hooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	hookMarker := filepath.Join(root, "hook-ran")
	filterMarker := filepath.Join(root, "filter-ran")
	templateMarker := filepath.Join(root, "template-ran")
	for _, item := range []struct{ path, marker string }{
		{filepath.Join(hooks, "post-checkout"), hookMarker},
		{filepath.Join(templates, "hooks", "post-checkout"), templateMarker},
	} {
		if err := os.WriteFile(item.path, []byte("#!/bin/sh\nprintf called >"+sourceShellQuote(item.marker)+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(root, "ambient.gitconfig")
	text := fmt.Sprintf("[core]\n hooksPath = %s\n[init]\n templateDir = %s\n[filter \"hostile\"]\n smudge = sh -c 'printf called >%s; cat'\n", hooks, templates, filterMarker)
	if err := os.WriteFile(config, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_TEMPLATE_DIR", templates)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", hooks)
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.hooksPath'='"+hooks+"'")
	t.Setenv("GIT_DIR", filepath.Join(root, "wrong.git"))
	t.Setenv("GIT_WORK_TREE", filepath.Join(root, "wrong-work-tree"))
	repository := filepath.Join(root, "checkout")
	isolatedFixtureGit(t, "init", "--", repository)
	for name, data := range map[string]string{".gitattributes": "payload filter=hostile\n", "payload": "plain bytes\n"} {
		if err := os.WriteFile(filepath.Join(repository, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	isolatedFixtureGit(t, "-C", repository, "add", "--", ".gitattributes", "payload")
	isolatedFixtureGit(t, "-C", repository, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "fixture")
	if err := os.Remove(filepath.Join(repository, "payload")); err != nil {
		t.Fatal(err)
	}
	isolatedFixtureGit(t, "-C", repository, "checkout", "--", "payload")
	got, err := os.ReadFile(filepath.Join(repository, "payload"))
	if err != nil || string(got) != "plain bytes\n" {
		t.Fatalf("checkout bytes changed: %q %v", got, err)
	}
	for _, marker := range []string{hookMarker, filterMarker, templateMarker} {
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("ambient executable policy ran: %s", marker)
		}
	}
	if _, err := os.Stat(filepath.Join(repository, ".git", "hooks", "post-checkout")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ambient template hook was copied")
	}
}

// sourceShellQuote protects temporary paths embedded in fixed test-only scripts.
func sourceShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// TestPublishNoReplaceIsDescriptorRelative follows the already opened parent
// even if its pathname is replaced before publication.
func TestPublishNoReplaceIsDescriptorRelative(t *testing.T) {
	requireGitHost(t)
	root := t.TempDir()
	parentPath := filepath.Join(root, "parent")
	if err := os.Mkdir(parentPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(parentPath, "stage"), 0o700); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	moved := filepath.Join(root, "moved-parent")
	if err := os.Rename(parentPath, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parentPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := publishNoReplace(parent, "stage", "published"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, "published")); err != nil {
		t.Fatal("publication did not follow the opened parent descriptor")
	}
	if _, err := os.Stat(filepath.Join(parentPath, "published")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("publication followed a replacement parent path")
	}
}

// TestPublishNoReplacePreservesEveryExistingDestination refuses existing files,
// directories and symlinks without removing its staging source.
func TestPublishNoReplacePreservesEveryExistingDestination(t *testing.T) {
	requireGitHost(t)
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "stage"), 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "published")
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(target, []byte("preserve"), 0o600)
			case "directory":
				err = os.Mkdir(target, 0o700)
			case "symlink":
				err = os.Symlink("do-not-follow", target)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			parent, err := os.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			if err := publishNoReplace(parent, "stage", "published"); err == nil {
				t.Fatal("replaced an existing publication")
			}
			after, err := os.Lstat(target)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("existing destination identity changed")
			}
			if _, err := os.Stat(filepath.Join(root, "stage")); err != nil {
				t.Fatal("failed publication removed its source")
			}
		})
	}
}

// TestPublishNoReplaceRejectsUnsafeNamesAndClosedParents rejects traversal,
// malformed child names and unavailable parent-directory descriptors.
func TestPublishNoReplaceRejectsUnsafeNamesAndClosedParents(t *testing.T) {
	requireGitHost(t)
	parent, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, names := range [][2]string{{"", "out"}, {".", "out"}, {"..", "out"}, {"a/b", "out"}, {"stage", "../out"}, {"stage", "a\\b"}, {"stage", "same\x00name"}, {"same", "same"}} {
		if err := publishNoReplace(parent, names[0], names[1]); err == nil {
			t.Fatalf("unsafe relative publication accepted: %#v", names)
		}
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	if err := publishNoReplace(parent, "stage", "out"); err == nil {
		t.Fatal("accepted a closed parent descriptor")
	}
	if err := publishNoReplace(nil, "stage", "out"); err == nil {
		t.Fatal("accepted missing parent descriptor")
	}
}
