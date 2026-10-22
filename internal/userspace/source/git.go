package source

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

const (
	// maximumGitCaptureBytes bounds metadata; component validators bound blobs.
	maximumGitCaptureBytes = 64 << 10
	// maximumGitRunBytes preserves the existing camera source-input size limits.
	maximumGitRunBytes = 8 << 20
	// maximumGitStderrBytes bounds private diagnostics before redaction.
	maximumGitStderrBytes = 64 << 10
	// gitCommandTimeout bounds each acquisition or component-validation command.
	gitCommandTimeout = 2 * time.Minute
	// gitWaitDelay bounds pipe cleanup after Git exits or cancellation begins.
	gitWaitDelay = 250 * time.Millisecond
)

// isolatedGitRunner runs only compiled Git commands. It neither runs repository
// scripts nor inherits the caller's Git, credential, loader or proxy settings.
// Request argument validation remains the responsibility of the source manager.
type isolatedGitRunner struct{}

// safeGitArguments precedes every internal Git operation, including Git reads
// made by component validators. The caller never controls these settings.
func safeGitArguments() []string {
	return []string{
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "init.templateDir=" + os.DevNull,
		"-c", "core.attributesFile=" + os.DevNull,
		"-c", "core.askPass=",
		"-c", "credential.helper=",
		"-c", "credential.interactive=false",
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
		"-c", "http.followRedirects=false",
		"-c", "http.sslVerify=true",
		"-c", "http.extraHeader=",
		"-c", "http.lowSpeedLimit=1",
		"-c", "http.lowSpeedTime=30",
		"-c", "fetch.recurseSubmodules=false",
		"-c", "submodule.recurse=false",
		"-c", "fetch.fsckObjects=true",
		"-c", "transfer.fsckObjects=true",
		"-c", "core.autocrlf=false",
		"-c", "core.eol=lf",
	}
}

// isolatedGitEnvironment deliberately replaces, rather than appends to, the
// parent environment. HOME and the user's configuration are not changed.
func isolatedGitEnvironment() []string {
	env := []string{
		"LANG=C", "LC_ALL=C", "TZ=UTC",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=" + os.DevNull,
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_NO_LAZY_FETCH=1",
	}
	if value, ok := os.LookupEnv("PATH"); ok {
		env = append(env, "PATH="+value)
	}
	return env
}

// boundedGitBuffer cancels its owned command group immediately on overflow.
// Returning a complete write prevents a blocked copy pipe after cancellation.
type boundedGitBuffer struct {
	buffer   bytes.Buffer
	maximum  int
	overflow bool
	cancel   context.CancelFunc
}

// Write stores only bounded bytes and cancels the owned process on overflow.
func (buffer *boundedGitBuffer) Write(data []byte) (int, error) {
	remaining := buffer.maximum - buffer.buffer.Len()
	if len(data) > remaining {
		if remaining > 0 {
			_, _ = buffer.buffer.Write(data[:remaining])
		}
		buffer.overflow = true
		buffer.cancel()
		return len(data), nil
	}
	_, _ = buffer.buffer.Write(data)
	return len(data), nil
}

// execute bounds wall time, child processes and both output streams. Git's
// stderr is retained only in a bounded private buffer, never exposed in errors.
func (*isolatedGitRunner) execute(ctx context.Context, command platform.Command, maximumStdout int) ([]byte, error) {
	if command.Name != "git" {
		return nil, errors.New("isolated source runner accepts only Git")
	}
	if command.Env != nil || command.Stdin != nil {
		return nil, errors.New("isolated Git does not accept caller environment or stdin streams")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	boundedContext, cancel := context.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()
	args := append(safeGitArguments(), command.Args...)
	cmd := exec.CommandContext(boundedContext, "git", args...)
	cmd.Dir = command.Dir
	cmd.Env = isolatedGitEnvironment()
	cmd.WaitDelay = gitWaitDelay
	if err := configureGitProcess(cmd); err != nil {
		return nil, err
	}
	stdout := &boundedGitBuffer{maximum: maximumStdout, cancel: cancel}
	stderr := &boundedGitBuffer{maximum: maximumGitStderrBytes, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		// A transport/helper retained an output pipe after Git exited. The
		// watcher can no longer cancel an exited parent, so stop this owned
		// group explicitly instead of only closing its inherited pipes.
		_ = cmd.Cancel()
	}
	if stdout.overflow {
		return nil, errors.New("isolated Git stdout exceeds its compiled limit")
	}
	if stderr.overflow {
		return nil, errors.New("isolated Git stderr exceeds its compiled limit")
	}
	if boundedContext.Err() != nil {
		return nil, fmt.Errorf("isolated Git cancelled: %w", boundedContext.Err())
	}
	if err != nil {
		// ExitError does not disclose stderr here. Do not include Git's raw
		// diagnostics, which may quote URLs, local paths or transport values.
		var status *exec.ExitError
		if errors.As(err, &status) {
			return nil, fmt.Errorf("isolated Git command failed with exit code %d", status.ExitCode())
		}
		return nil, errors.New("isolated Git command could not start or complete")
	}
	return append([]byte(nil), stdout.buffer.Bytes()...), nil
}

// Run releases caller stdout only after a successful, completely bounded Git
// invocation. Source blob reads retain the component's smaller writer limits.
func (runner *isolatedGitRunner) Run(ctx context.Context, command platform.Command) error {
	data, err := runner.execute(ctx, command, maximumGitRunBytes)
	if err != nil {
		return err
	}
	if command.Stdout != nil {
		if _, err := io.Copy(command.Stdout, bytes.NewReader(data)); err != nil {
			return errors.New("isolated Git output destination failed")
		}
	}
	return nil
}

// Capture returns only successful bounded metadata, without streaming output.
func (runner *isolatedGitRunner) Capture(ctx context.Context, command platform.Command) ([]byte, error) {
	if command.Stdout != nil {
		return nil, errors.New("isolated Git capture does not accept a stdout stream")
	}
	return runner.execute(ctx, command, maximumGitCaptureBytes)
}

// validPublicationNames restricts descriptor-relative publication to two flat
// names beneath the already opened parent directory.
func validPublicationNames(parent *os.File, stagingName, destinationName string) error {
	if parent == nil {
		return errors.New("source publication parent is unavailable")
	}
	info, err := parent.Stat()
	if err != nil || !info.IsDir() {
		return errors.New("source publication parent is not an open directory")
	}
	for _, name := range []string{stagingName, destinationName} {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
			return errors.New("source publication requires flat child names")
		}
	}
	if stagingName == destinationName {
		return errors.New("source publication source and destination must differ")
	}
	return nil
}
