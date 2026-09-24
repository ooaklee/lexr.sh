package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	lexrconfig "github.com/ooaklee/lexr.sh/internal/config"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"github.com/ooaklee/lexr.sh/internal/profile"
)

// TestGlobalProfilePlacement verifies that before/after command forms and the
// saved profile all select the same offline target without changing the file.
func TestGlobalProfilePlacement(t *testing.T) {
	t.Setenv("LEXR_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	config := configFixture(t, "profile: x1e80100-microsoft-denali-oled\n")
	for _, flags := range [][]string{
		{"--profile", "x1p64100-microsoft-denali", "image", "create", "--dry-run"},
		{"image", "create", "--dry-run", "--profile", "x1p64100-microsoft-denali"},
		{"image", "create", "--dry-run", "--profile", "surface-pro-11-x1p-lcd"},
	} {
		out, diagnostic, err := executeCLI(t, append([]string{"--config", config}, flags...)...)
		if err != nil || diagnostic != "" || !strings.Contains(out, `"profile": "x1p64100-microsoft-denali"`) {
			t.Fatalf("flags %v: %s, %s, %v", flags, out, diagnostic, err)
		}
	}
	data, err := os.ReadFile(config)
	if err != nil || string(data) != "profile: x1e80100-microsoft-denali-oled\n" {
		t.Fatalf("one-shot profile changed config: %s, %v", data, err)
	}
}

// TestProfileRequirementsAndEscapeCommands covers missing/invalid choices and
// recovery commands, without invoking hardware, Docker or network services.
func TestProfileRequirementsAndEscapeCommands(t *testing.T) {
	t.Setenv("LEXR_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"image", "create", "--dry-run"}, "needs a hardware profile"},
		{[]string{"image", "create", "--dry-run", "--profile", "auto"}, "offline target"},
		{[]string{"doctor", "boot", "--root", t.TempDir()}, "needs a hardware profile"},
		{[]string{"userspace", "status", "--root", t.TempDir()}, "needs a hardware profile"},
		{[]string{"--profile=", "catalog", "list"}, "must not be empty"},
		{[]string{"--profile", "typo", "catalog", "list"}, "unknown hardware profile"},
		{[]string{"init", "typo"}, "unknown hardware profile"},
		{[]string{"profile", "list", "--json"}, ""},
		{[]string{"catalog", "list"}, ""},
		{[]string{"version"}, ""},
	} {
		_, _, err := executeCLI(t, test.args...)
		if test.want == "" && err != nil {
			t.Fatalf("%v: %v", test.args, err)
		}
		if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
			t.Fatalf("%v: %v, want %q", test.args, err, test.want)
		}
	}
	bad := configFixture(t, "profile: typo\n")
	for _, args := range [][]string{{"profile", "list"}, {"doctor", "boot", "--help"}} {
		if _, _, err := executeCLI(t, append([]string{"--config", bad}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
}

// TestBuildReleaseProfileIsolation proves saved targets cannot narrow artefact
// production, including nested audio/camera release operations.
func TestBuildReleaseProfileIsolation(t *testing.T) {
	config := configFixture(t, "profile: future-platform\n")
	for _, path := range [][]string{
		{"kernel", "build"}, {"kernel", "release", "list"}, {"image", "release", "validate"},
		{"userspace", "build"}, {"userspace", "audio", "release", "prepare"}, {"userspace", "camera", "release", "validate"},
	} {
		for _, explicit := range []bool{false, true} {
			root := NewRootCommand(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
			command, _, err := root.Find(path)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			command.Args = cobra.ArbitraryArgs
			command.Flags().VisitAll(func(flag *pflag.Flag) { delete(flag.Annotations, cobra.BashCompOneRequiredFlag) })
			command.RunE = func(*cobra.Command, []string) error { called = true; return nil }
			args := append([]string{"--config", config}, path...)
			if explicit {
				args = append(args, "--profile", "x1e80100-microsoft-denali-oled")
			}
			root.SetArgs(args)
			err = root.Execute()
			if explicit {
				if err == nil || called || !strings.Contains(err.Error(), "omit --profile") {
					t.Fatalf("%v: called=%t, err=%v", args, called, err)
				}
			} else if err != nil || !called {
				t.Fatalf("%v: called=%t, err=%v", args, called, err)
			}
		}
	}
}

// TestKernelRefreshReceivesResolvedProfile binds configuration to the actual
// helper argument instead of merely testing acceptance of the global flag.
func TestKernelRefreshReceivesResolvedProfile(t *testing.T) {
	for _, id := range []string{"x1e80100-microsoft-denali-oled", "x1p64100-microsoft-denali"} {
		runner := &recordingKernelBootRunner{}
		app := &application{configuration: lexrconfig.Config{Profile: id}, kernelBootRunner: runner}
		root := &cobra.Command{Use: "lexr", PersistentPreRunE: func(command *cobra.Command, _ []string) error { return app.selectProfile(command) }}
		root.PersistentFlags().StringVar(&app.profileName, "profile", "", "hardware profile")
		root.AddCommand(app.newKernelCommand())
		root.SetArgs([]string{"kernel", "boot", "refresh", "--abi", "7.2.2-jg-0sp11v10-qcom-x1e", "--root", t.TempDir()})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		selected, _ := profile.Resolve(id)
		if len(runner.commands) != 1 || runner.commands[0].Args[len(runner.commands[0].Args)-1] != selected.Platform {
			t.Fatalf("helper did not receive %s: %#v", selected.Platform, runner.commands)
		}
	}
}

// TestProfileMismatchStopsHardwareAndUSB checks model mismatches before an
// operation is allowed to use a conflicting target or plan a destructive write.
func TestProfileMismatchStopsHardwareAndUSB(t *testing.T) {
	target := t.TempDir()
	identity := filepath.Join(target, "sys/firmware/devicetree/base/compatible")
	if err := os.MkdirAll(filepath.Dir(identity), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identity, []byte("microsoft,denali-lcd\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCLI(t, "doctor", "boot", "--root", target, "--profile", "x1e80100-microsoft-denali-oled")
	if err == nil || !strings.Contains(err.Error(), "conflicts with target hardware") {
		t.Fatalf("mismatch error = %v", err)
	}
	selected, _ := profile.Resolve("x1p64100-microsoft-denali")
	app := &application{profile: selected, imageValidator: func(context.Context, string) (imagecontract.ValidationReport, error) {
		return imagecontract.ValidationReport{
			Valid:        true,
			DeviceTrees:  []string{"surface-pro-11-x1e-oled", "surface-pro-11-x1p-lcd"},
			BootProfiles: []string{"surface-pro-11-x1e-oled"},
		}, nil
	}}
	report, err := app.validateImageForMedia(context.Background(), "unused.iso")
	if err == nil || report.Valid || len(report.Checks) != 1 {
		t.Fatalf("mismatch report = %#v, %v", report, err)
	}
	encoded, _ := json.Marshal(report)
	if !strings.Contains(string(encoded), `"name":"hardware-profile"`) {
		t.Fatalf("missing JSON failure: %s", encoded)
	}
	app.imageValidator = func(context.Context, string) (imagecontract.ValidationReport, error) {
		return imagecontract.ValidationReport{Valid: true, BootProfiles: []string{selected.Platform}}, nil
	}
	if report, err := app.validateImageForMedia(context.Background(), "unused.iso"); err != nil || !report.Valid {
		t.Fatalf("declared LCD boot target rejected: %#v, %v", report, err)
	}
}
