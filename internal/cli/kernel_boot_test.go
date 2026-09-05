package cli

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// recordingKernelBootRunner records the one direct helper invocation.
type recordingKernelBootRunner struct {
	commands []platform.Command
}

// Run records command without changing the test host.
func (runner *recordingKernelBootRunner) Run(_ context.Context, command platform.Command) error {
	runner.commands = append(runner.commands, command)
	return nil
}

// Capture is not used by kernel boot refresh.
func (runner *recordingKernelBootRunner) Capture(context.Context, platform.Command) ([]byte, error) {
	return nil, errors.New("unexpected capture")
}

// TestKernelBootRefreshUsesExactABIHelper proves both live and alternate-root
// forms retain argument boundaries and derive the image from the exact ABI.
func TestKernelBootRefreshUsesExactABIHelper(t *testing.T) {
	const abi = "7.2.2-jg-0sp11v10-qcom-x1e"
	for _, test := range []struct {
		name string
		root string
		want platform.Command
	}{
		{
			name: "live root", root: "/",
			want: platform.Command{Name: "/usr/libexec/lexr/kernel-boot-refresh", Args: []string{
				"refresh", "--root", "/", "--abi", abi, "--image", "/boot/vmlinuz-" + abi, "--platform", "surface-pro-11-x1e-oled",
			}},
		},
		{
			name: "alternate root", root: "/mnt/target",
			want: platform.Command{Name: "/usr/sbin/chroot", Args: []string{
				"/mnt/target", "/usr/libexec/lexr/kernel-boot-refresh", "refresh", "--root", "/", "--abi", abi,
				"--image", "/boot/vmlinuz-" + abi, "--platform", "surface-pro-11-x1e-oled",
			}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingKernelBootRunner{}
			app := &application{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, kernelBootRunner: runner}
			command := app.newKernelBootCommand()
			command.SetArgs([]string{"refresh", "--root", test.root, "--abi", abi, "--profile", "surface-pro-11-x1e-oled"})
			if err := command.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("kernel boot refresh error = %v", err)
			}
			if len(runner.commands) != 1 || !reflect.DeepEqual(runner.commands[0], test.want) {
				t.Fatalf("commands = %#v, want %#v", runner.commands, test.want)
			}
		})
	}
}

// TestKernelBootRefreshRejectsRelativeRoot proves no helper runs before the
// target filesystem boundary is absolute.
func TestKernelBootRefreshRejectsRelativeRoot(t *testing.T) {
	runner := &recordingKernelBootRunner{}
	app := &application{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, kernelBootRunner: runner}
	command := app.newKernelBootCommand()
	command.SetArgs([]string{"refresh", "--root", "relative", "--abi", "7.2.2-jg-0sp11v10-qcom-x1e"})
	if err := command.ExecuteContext(context.Background()); err == nil {
		t.Fatal("kernel boot refresh accepted a relative root")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("relative-root command invoked helper: %#v", runner.commands)
	}
}
