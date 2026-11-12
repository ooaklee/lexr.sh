package serviceprobe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRunnerChildFixture is an explicitly marked benign child of this test
// binary. It never invokes busctl, D-Bus, systemd or a shell.
func TestRunnerChildFixture(t *testing.T) {
	if os.Getenv("LEXR_TEST_PASSIVE_RUNNER_CHILD") != "1" {
		return
	}
	if len(os.Args) == 0 {
		os.Exit(90)
	}
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "success":
		_, _ = io.WriteString(os.Stdout, `{"type":"s","data":["fixture"]}`)
	case "stdout-overflow":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("o"), int(maximumStdout)+1))
	case "stderr-overflow":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("e"), int(maximumStderr)+1))
	case "nonzero":
		_, _ = io.WriteString(os.Stderr, "private fixture diagnostic must remain hidden")
		os.Exit(23)
	case "sleep":
		time.Sleep(10 * time.Second)
	default:
		os.Exit(91)
	}
	os.Exit(0)
}

// fixtureExecutor verifies the original production invocation before replacing
// only this private test seam with the current test binary's benign fixture.
func fixtureExecutor(t *testing.T, mode string) processExecutor {
	t.Helper()
	return func(ctx context.Context, command *exec.Cmd) error {
		t.Helper()
		wantArgs, err := (request{operation: readBusID}).arguments()
		if err != nil {
			t.Fatal(err)
		}
		if command.Path != "/usr/bin/busctl" || !reflect.DeepEqual(command.Args, append([]string{"/usr/bin/busctl"}, wantArgs...)) || command.Dir != "/" || command.Stdin != nil || command.WaitDelay != processWaitDelay || command.Cancel == nil {
			t.Fatalf("production command shape changed: %#v", command)
		}
		if !reflect.DeepEqual(command.Env, []string{"PATH=/usr/bin:/bin", "LC_ALL=C", "LANG=C", "DBUS_SYSTEM_BUS_ADDRESS=unix:path=/run/dbus/system_bus_socket"}) {
			t.Fatalf("inherited or altered process environment: %#v", command.Env)
		}
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > maximumCallDuration || time.Until(deadline) <= 0 {
			t.Fatal("runner did not impose its bounded call deadline")
		}
		command.Path = os.Args[0]
		command.Args = []string{os.Args[0], "-test.run=^TestRunnerChildFixture$", "--", mode}
		command.Env = append(command.Env, "LEXR_TEST_PASSIVE_RUNNER_CHILD=1")
		return command.Run()
	}
}

// TestFixedRunnerExecutesOnlyControlledChild exercises actual portable process
// capture without ever launching the production executable or contacting a bus.
func TestFixedRunnerExecutesOnlyControlledChild(t *testing.T) {
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "tcp:host=untrusted.invalid,port=1")
	t.Setenv("SYSTEMD_PAGER", "untrusted-pager")
	t.Setenv("LD_PRELOAD", "/untrusted/loader.so")
	for _, mode := range []string{"success", "stdout-overflow", "stderr-overflow", "nonzero"} {
		t.Run(mode, func(t *testing.T) {
			runner := fixedRunner{execute: fixtureExecutor(t, mode)}
			data, err := runner.capture(context.Background(), request{operation: readBusID})
			if mode == "success" {
				if err != nil || string(data) != `{"type":"s","data":["fixture"]}` {
					t.Fatalf("successful bounded fixture: %q, %v", data, err)
				}
			} else if err == nil || data != nil || strings.Contains(err.Error(), "private fixture") {
				t.Fatalf("failure became bytes or leaked stderr: %q, %v", data, err)
			}
		})
	}
}

// TestFixedRunnerCancellationKillsControlledChild measures the real bounded
// child lifecycle, not a scripted context response.
func TestFixedRunnerCancellationKillsControlledChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	start := time.Now()
	var executed *exec.Cmd
	executor := fixtureExecutor(t, "sleep")
	data, err := (fixedRunner{execute: func(ctx context.Context, command *exec.Cmd) error {
		executed = command
		return executor(ctx, command)
	}}).capture(ctx, request{operation: readBusID})
	if !errors.Is(err, context.DeadlineExceeded) || data != nil || time.Since(start) > 2*time.Second {
		t.Fatalf("child did not stop within the cancellation bound: %q, %v, %v", data, err, time.Since(start))
	}
	if executed == nil || executed.Process == nil || executed.Process.Pid <= 0 || executed.ProcessState == nil {
		t.Fatal("cancellation was not bound to a started and reaped controlled child")
	}
}

// TestFixedRunnerRefusesInvalidRequests ensures failure precedes any process.
func TestFixedRunnerRefusesInvalidRequests(t *testing.T) {
	called := false
	runner := fixedRunner{execute: func(context.Context, *exec.Cmd) error {
		called = true
		return nil
	}}
	for _, r := range []request{{}, {operation: readBusID, peer: ":1.2"}, {operation: loadedUnits, peer: managerName}} {
		if _, err := runner.capture(context.Background(), r); err == nil {
			t.Fatal("invalid request admitted")
		}
	}
	if _, err := runner.capture(nil, request{operation: readBusID}); err == nil {
		t.Fatal("nil context admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.capture(ctx, request{operation: readBusID}); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancelled request admitted")
	}
	if called {
		t.Fatal("invalid request reached process execution")
	}
}

// TestBoundedBufferDrainsWithoutRetainingOverflow checks exact limits, one
// large write, concurrent drain writes and defensive copies.
func TestBoundedBufferDrainsWithoutRetainingOverflow(t *testing.T) {
	buffer := &boundedBuffer{maximum: 4}
	if n, err := buffer.Write([]byte("abcd")); n != 4 || err != nil {
		t.Fatal("exact bound was not drained")
	}
	data, err := buffer.result()
	if err != nil || string(data) != "abcd" {
		t.Fatal("exact bound was not retained")
	}
	data[0] = 'z'
	again, _ := buffer.result()
	if string(again) != "abcd" {
		t.Fatal("buffer result shared mutable data")
	}
	if n, err := buffer.Write(bytes.Repeat([]byte("x"), 1<<20)); n != 1<<20 || err != nil || buffer.buffer.Len() != 4 {
		t.Fatal("overflow was buffered or not drained")
	}
	if data, err := buffer.result(); err == nil || data != nil {
		t.Fatal("overflow was available as successful data")
	}
	concurrent := &boundedBuffer{maximum: 8}
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() { _, _ = concurrent.Write([]byte("four")) })
	}
	workers.Wait()
	if data, err := concurrent.result(); err == nil || data != nil || concurrent.buffer.Len() != 8 {
		t.Fatal("concurrent overflow exceeded its buffer bound")
	}
}

// TestFrozenEnvironmentAndBudget keeps returned environment slices independent
// and checks the production constants without executing a command.
func TestFrozenEnvironmentAndBudget(t *testing.T) {
	environment := frozenEnvironment()
	environment[0] = "PATH=/unsafe"
	if frozenEnvironment()[0] != "PATH=/usr/bin:/bin" || maximumCallDuration > 3*time.Second || maximumObservation > 20*time.Second || processWaitDelay > time.Second || maximumStdout != 64<<10 || maximumStderr != 8<<10 {
		t.Fatal("fixed process environment or resource budgets changed")
	}
	runner := fixedRunner{execute: func(ctx context.Context, command *exec.Cmd) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			return fmt.Errorf("unbounded context")
		}
		_, _ = io.WriteString(command.Stdout, "bounded")
		return nil
	}}
	data, err := runner.capture(context.Background(), request{operation: readBusID})
	if err != nil || string(data) != "bounded" {
		t.Fatalf("runner seam budget: %q, %v", data, err)
	}
}
