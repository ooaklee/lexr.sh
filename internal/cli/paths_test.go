package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	kernelbuild "github.com/ooaklee/lexr.sh/internal/kernel/build"
)

// TestHostPathPrecedence exercises real flag registration and both pre-run
// hooks while replacing only the external command workflow with an observation.
// LEXR_CONFIG selects YAML; these flags have no separate environment variables.
func TestHostPathPrecedence(t *testing.T) {
	for _, test := range []struct {
		command, args        []string
		flag, yaml, relative string
	}{
		{[]string{"image", "create"}, nil, "cache-dir", "image: {create: {cache_dir: %q}}", "caches/image"},
		{[]string{"image", "create"}, nil, "workspace-dir", "image: {create: {workspace_dir: %q}}", "builds/image"},
		{[]string{"userspace", "pull"}, []string{"audio"}, "cache-dir", "userspace: {pull: {cache_dir: %q}}", "caches/userspace"},
		{[]string{"doctor"}, nil, "workspace", "doctor: {workspace: %q}", "builds/doctor"},
		{[]string{"kernel", "release", "download"}, nil, "output-dir", "kernel: {release: {download: {output_dir: %q}}}", "caches/kernel"},
		{[]string{"wizard"}, nil, "cache-dir", "wizard: {cache_dir: %q}", "caches/wizard"},
	} {
		for _, mode := range []string{"default", "empty config", "config", "environment config", "config flag over environment", "flag over config", "flag over environment", "flag over default", "empty flag"} {
			t.Run(strings.Join(test.command, " ")+"/"+test.flag+"/"+mode, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("USERPROFILE", home)
				t.Setenv("APPDATA", filepath.Join(home, "config"))
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
				t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
				t.Setenv("LEXR_CONFIG", "")
				base, err := os.UserConfigDir()
				if err != nil {
					t.Fatal(err)
				}
				configHome := filepath.Join(base, "lexr")
				want := filepath.Join(configHome, filepath.FromSlash(test.relative))
				configured := filepath.Join(home, "configured")
				environment := filepath.Join(home, "environment")
				configPath := filepath.Join(home, "explicit.yml")
				envPath := filepath.Join(home, "environment.yml")
				for path, value := range map[string]string{configPath: configured, envPath: environment} {
					if err := os.WriteFile(path, []byte(fmt.Sprintf(test.yaml, value)), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				arguments := append(append([]string{}, test.command...), test.args...)
				if test.command[0] == "image" || test.command[0] == "wizard" {
					arguments = append(arguments, "--profile", "x1e80100-microsoft-denali-oled")
				}
				if strings.Contains(mode, "environment") {
					t.Setenv("LEXR_CONFIG", envPath)
					want = environment
				}
				if mode == "config" || mode == "flag over config" || mode == "empty flag" || mode == "config flag over environment" || mode == "empty config" {
					arguments = append(arguments, "--config", configPath)
					want = configured
					if mode == "empty config" {
						if err := os.WriteFile(configPath, []byte(fmt.Sprintf(test.yaml, "")), 0o600); err != nil {
							t.Fatal(err)
						}
						want = filepath.Join(configHome, filepath.FromSlash(test.relative))
					}
				}
				if strings.HasPrefix(mode, "flag over") || mode == "empty flag" {
					want = "./flag/../chosen"
					if mode == "empty flag" {
						want = ""
					}
					arguments = append(arguments, "--"+test.flag+"="+want)
				}
				root := NewRootCommand(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
				command, _, err := root.Find(test.command)
				if err != nil {
					t.Fatal(err)
				}
				called := false
				command.RunE = func(command *cobra.Command, _ []string) error {
					called = true
					got, err := command.Flags().GetString(test.flag)
					if err != nil || got != want {
						t.Fatalf("resolved flag = %q, %v; want %q", got, err, want)
					}
					return nil
				}
				root.SetArgs(arguments)
				if err := root.Execute(); err != nil || !called {
					t.Fatalf("command executed = %t, error = %v", called, err)
				}
			})
		}
	}
}

// TestPathDefaultsRemainLazy ensures construction, help and unrelated commands
// leave the configuration home absent rather than creating every command's paths.
func TestPathDefaultsRemainLazy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("APPDATA", filepath.Join(home, "config"))
	t.Setenv("LEXR_CONFIG", "")
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"image", "create", "--help"}, {"version"}} {
		if _, _, err := executeCLI(t, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "lexr")); !os.IsNotExist(err) {
		t.Fatalf("unused config home exists: %v", err)
	}
}

// TestRepositoryFlagDefaultsUnchanged protects the workflow-owned relative
// defaults and the required fresh host output for kernel release preparation.
func TestRepositoryFlagDefaultsUnchanged(t *testing.T) {
	root := NewRootCommand(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	for _, test := range []struct {
		command    []string
		flag, want string
	}{
		{[]string{"kernel", "build"}, "work-dir", kernelbuild.DefaultWorkDirectory},
		{[]string{"kernel", "build"}, "output-dir", kernelbuild.DefaultOutputDirectory},
		{[]string{"image", "release", "prepare"}, "out-dir", ""},
		{[]string{"userspace", "camera", "release", "prepare"}, "output-dir", ""},
		{[]string{"userspace", "build"}, "output-dir", ""},
		{[]string{"kernel", "release", "prepare"}, "output-dir", ""},
	} {
		command, _, err := root.Find(test.command)
		if err != nil {
			t.Fatal(err)
		}
		flag := command.Flags().Lookup(test.flag)
		if flag == nil || flag.DefValue != test.want {
			t.Fatalf("%v --%s = %#v; want %q", test.command, test.flag, flag, test.want)
		}
		if strings.Join(test.command, " ") == "kernel release prepare" && len(flag.Annotations[cobra.BashCompOneRequiredFlag]) == 0 {
			t.Fatal("kernel release output must remain required")
		}
	}
}
