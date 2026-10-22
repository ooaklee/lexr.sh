package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestAuthenticateSourceSeparatesAcquisitionFromBuildCompatibility keeps old
// checkouts and declaration-bearing checkouts valid acquisition inputs without
// supplying a target tuple, compatibility override or native build authority.
func TestAuthenticateSourceSeparatesAcquisitionFromBuildCompatibility(t *testing.T) {
	for _, declaration := range []bool{false, true} {
		name := "legacy-source"
		if declaration {
			name = "declaration-source"
		}
		t.Run(name, func(t *testing.T) {
			root, tuning := makeCameraRepository(t)
			if !declaration {
				file := "userspace/compatibility/imx681-libcamera-v1/lexr-component-compatibility.json"
				for _, arguments := range [][]string{{"-C", root, "rm", "--", file}, {"-C", root, "commit", "-q", "-m", "Retain legacy source fixture"}} {
					if err := (platform.ExecRunner{}).Run(context.Background(), platform.Command{Name: "git", Args: arguments}); err != nil {
						t.Fatal(err)
					}
				}
			}
			runner := &fakeCameraRunner{tuning: tuning}
			if err := AuthenticateSource(context.Background(), runner, root); err != nil {
				t.Fatal(err)
			}
			if len(runner.commands) == 0 {
				t.Fatal("source inputs were not authenticated against Git")
			}
			for _, command := range runner.commands {
				if command.Name != "git" {
					t.Fatalf("source authentication invoked %s", command.Name)
				}
			}
			if _, err := os.Lstat(filepath.Join(root, "build")); !os.IsNotExist(err) {
				t.Fatalf("source authentication created build output: %v", err)
			}
		})
	}
}

// TestAuthenticateSourcePreservesInputAndContextRefusals rejects changed or
// linked fixed inputs rather than letting a source-only API weaken the builder.
func TestAuthenticateSourcePreservesInputAndContextRefusals(t *testing.T) {
	for _, name := range []string{"changed-input", "symlink-input", "cancelled", "nil-context", "nil-runner"} {
		t.Run(name, func(t *testing.T) {
			root, tuning := makeCameraRepository(t)
			input := filepath.Join(root, "userspace/camera/libcamera/imx681.yaml")
			ctx := context.Background()
			var runner platform.Runner = &fakeCameraRunner{tuning: tuning}
			switch name {
			case "changed-input":
				if err := os.WriteFile(input, []byte("changed\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink-input":
				if err := os.Remove(input); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("BASE.txt", input); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			case "nil-context":
				ctx = nil
			case "nil-runner":
				runner = nil
			}
			if err := AuthenticateSource(ctx, runner, root); err == nil {
				t.Fatal("invalid source authentication request was accepted")
			}
		})
	}
}
