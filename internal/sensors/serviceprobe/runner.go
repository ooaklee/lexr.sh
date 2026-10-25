package serviceprobe

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
)

// processExecutor is a private controlled-test seam; production always runs the
// fixed command assembled by fixedRunner, never a caller-selected process.
type processExecutor func(context.Context, *exec.Cmd) error

// fixedRunner captures only closed busctl queries on the fixed local bus.
type fixedRunner struct {
	execute processExecutor
}

// boundedBuffer retains only its fixed prefix while draining remaining output.
// Overflow is sticky and makes the complete command result unavailable.
type boundedBuffer struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	maximum  int64
	exceeded bool
}

// Write never buffers beyond the compiled maximum, including a single large
// write, and safely records overflow without blocking a noisy child forever.
func (b *boundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	length := len(data)
	remaining := b.maximum - int64(b.buffer.Len())
	if int64(length) > remaining {
		b.exceeded = true
	}
	if remaining > 0 {
		if int64(len(data)) > remaining {
			data = data[:remaining]
		}
		_, _ = b.buffer.Write(data)
	}
	return length, nil
}

// result returns defensive successful bytes or refuses any observed overflow.
func (b *boundedBuffer) result() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.exceeded {
		return nil, errors.New("passive sensor query exceeded its fixed output bound")
	}
	return append([]byte(nil), b.buffer.Bytes()...), nil
}

// frozenEnvironment excludes inherited bus redirection, loader, pager and
// authorisation settings from the read-only process invocation.
func frozenEnvironment() []string {
	return []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "LANG=C", "DBUS_SYSTEM_BUS_ADDRESS=unix:path=/run/dbus/system_bus_socket"}
}

// capture uses an absolute executable, no stdin, bounded streams, a per-call
// deadline and a bounded pipe wait. Raw stderr is never returned in errors.
func (runner fixedRunner) capture(ctx context.Context, r request) ([]byte, error) {
	args, err := r.arguments()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, errors.New("passive sensor query context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, maximumCallDuration)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stdout := &boundedBuffer{maximum: maximumStdout}
	stderr := &boundedBuffer{maximum: maximumStderr}
	command := exec.CommandContext(ctx, "/usr/bin/busctl", args...)
	command.Dir = "/"
	command.Env = frozenEnvironment()
	command.Stdin = nil
	command.Stdout, command.Stderr = stdout, stderr
	command.WaitDelay = processWaitDelay
	if runner.execute == nil {
		err = command.Run()
	} else {
		err = runner.execute(ctx, command)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	data, stdoutErr := stdout.result()
	_, stderrErr := stderr.result()
	if stdoutErr != nil || stderrErr != nil {
		return nil, errors.New("passive sensor query exceeded its fixed output bound")
	}
	if err != nil {
		return nil, errors.New("fixed passive sensor bus query failed or its utility is unavailable")
	}
	return data, nil
}
