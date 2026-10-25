//go:build linux

package bootidentity

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// TestBtrfsProbeHelperProcess is selected only by the private fixture factory.
// It reads its own inherited temporary directory; it never invokes btrfs.
func TestBtrfsProbeHelperProcess(t *testing.T) {
	args := os.Args
	marker := -1
	for i, arg := range args {
		if arg == "--lexr-btrfs-fixture" {
			marker = i
			break
		}
	}
	if marker == -1 {
		return
	}
	mode, expectedDev, expectedIno := args[marker+1], args[marker+2], args[marker+3]
	var st unix.Stat_t
	if unix.Fstat(3, &st) != nil || fmt.Sprint(st.Dev) != expectedDev || fmt.Sprint(st.Ino) != expectedIno || st.Mode&unix.S_IFMT != unix.S_IFDIR {
		os.Exit(8)
	}
	env := os.Environ()
	sort.Strings(env)
	if !reflect.DeepEqual(env, []string{"LANG=C", "LC_ALL=C", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}) {
		os.Exit(9)
	}
	switch mode {
	case "valid":
		fmt.Print("ID 5 (FS_TREE)\n")
	case "stderr":
		fmt.Print("ID 5 (FS_TREE)\n")
		fmt.Fprintln(os.Stderr, "fixture diagnostic")
	case "nonzero":
		fmt.Print("ID 5 (FS_TREE)\n")
		os.Exit(1)
	case "overflow":
		fmt.Print(strings.Repeat("x", 4096))
	case "stderr-overflow":
		fmt.Fprint(os.Stderr, strings.Repeat("x", 2048))
	case "sleep", "cancel", "descendant":
		time.Sleep(10 * time.Second)
	case "pipe-child", "quiet-child":
		child := exec.Command(os.Args[0], "-test.run=TestBtrfsProbeHelperProcess", "--", "--lexr-btrfs-fixture", "descendant", expectedDev, expectedIno)
		child.Env, child.ExtraFiles = env, []*os.File{os.NewFile(3, "fixture-directory")}
		if mode == "pipe-child" {
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
		}
		if child.Start() != nil {
			os.Exit(10)
		}
		if os.WriteFile(args[marker+4], []byte(strconv.Itoa(child.Process.Pid)), 0o600) != nil {
			os.Exit(11)
		}
		if mode == "quiet-child" {
			fmt.Print("ID 5 (FS_TREE)\n")
		}
	default:
		os.Exit(12)
	}
	os.Exit(0)
}

// TestBtrfsDefaultProbeExecutionBoundary exercises fixed command inputs and denial paths.
func TestBtrfsDefaultProbeExecutionBoundary(t *testing.T) {
	// Adopt only our orphaned fixture descendants so this test can reap them;
	// production never relies on the system init promptly reaping a zombie.
	var oldSubreaper int32
	if _, _, errno := unix.Syscall6(unix.SYS_PRCTL, unix.PR_GET_CHILD_SUBREAPER, uintptr(unsafe.Pointer(&oldSubreaper)), 0, 0, 0, 0); errno != 0 {
		t.Fatal(errno)
	}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, uintptr(oldSubreaper), 0, 0, 0); err != nil {
			t.Error(err)
		}
	})
	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	var st unix.Stat_t
	if err := unix.Fstat(int(dir.Fd()), &st); err != nil {
		t.Fatal(err)
	}
	old := btrfsCommand
	t.Cleanup(func() { btrfsCommand = old })
	for _, mode := range []string{"valid", "stderr", "nonzero", "overflow", "stderr-overflow", "sleep", "cancel", "pipe-child", "quiet-child"} {
		t.Run(mode, func(t *testing.T) {
			var command *exec.Cmd
			pidFile := filepath.Join(t.TempDir(), "descendant-pid")
			btrfsCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
				if name != "/usr/bin/btrfs" || !reflect.DeepEqual(args, []string{"subvolume", "get-default", "/proc/self/fd/3"}) {
					t.Fatal("production command scope widened")
				}
				command = exec.CommandContext(ctx, os.Args[0], "-test.run=TestBtrfsProbeHelperProcess", "--", "--lexr-btrfs-fixture", mode, fmt.Sprint(st.Dev), fmt.Sprint(st.Ino), pidFile)
				return command
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				timer := time.AfterFunc(100*time.Millisecond, cancel)
				defer timer.Stop()
			}
			start := time.Now()
			got := runBtrfsDefaultProbe(ctx, "/usr/bin/btrfs", dir)
			if got != (mode == "valid") || time.Since(start) > 3500*time.Millisecond {
				t.Fatalf("probe=%t elapsed=%v", got, time.Since(start))
			}
			if mode == "pipe-child" || mode == "quiet-child" {
				data, err := os.ReadFile(pidFile)
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(string(data))
				if err != nil || pid <= 0 {
					t.Fatal("invalid fixture descendant pid")
				}
				deadline := time.Now().Add(time.Second)
				for {
					var status unix.WaitStatus
					reaped, err := unix.Wait4(pid, &status, unix.WNOHANG, nil)
					if reaped == pid {
						if !status.Signaled() || status.Signal() != unix.SIGKILL {
							t.Fatal("fixture descendant was not killed by bounded cleanup")
						}
						break
					}
					if err != nil || time.Now().After(deadline) {
						t.Fatalf("fixture descendant was not reaped: %v", err)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if command == nil || command.Process == nil || unix.Kill(-command.Process.Pid, 0) != unix.ESRCH {
				t.Fatal("fixture process group not observed settled")
			}
		})
	}
	btrfsCommand = func(context.Context, string, ...string) *exec.Cmd {
		t.Fatal("unapproved or cancelled command started")
		return nil
	}
	if runBtrfsDefaultProbe(context.Background(), "/tmp/btrfs", dir) {
		t.Fatal("unapproved executable accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if runBtrfsDefaultProbe(ctx, "/usr/bin/btrfs", dir) {
		t.Fatal("cancelled attestation accepted")
	}
}
