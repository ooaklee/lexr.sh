package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/ooaklee/lexr.sh/internal/bootdoctor"
	lexrconfig "github.com/ooaklee/lexr.sh/internal/config"
)

// configFixture writes one isolated YAML input for command tests.
func configFixture(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestConfigCheck verifies validation results and complete per-file reporting.
func TestConfigCheck(t *testing.T) {
	for _, test := range []struct{ name, contents, want string }{
		{"valid", "version: 1\nprofile: x1e80100-microsoft-denali-oled\n", ""},
		{"invalid", "doctor: {boot: {unknown: true}}\n", "field unknown not found"},
		{"version", "version: -1\n", "unsupported version"},
		{"unknown profile", "profile: misspelled-device\n", "unknown hardware profile"},
		{"missing", "", "no such file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := configFixture(t, test.contents)
			if test.name == "missing" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			out, _, err := executeCLI(t, "config", "check", "--config", path)
			if !strings.Contains(out, path) {
				t.Fatalf("resolved path missing: %s", out)
			}
			if test.want == "" {
				if err != nil || !strings.Contains(out, "Configuration valid") {
					t.Fatalf("check = %s, %v", out, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("expected named file error: %v", err)
			} else if test.name != "missing" && !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %s", err, test.want)
			}
		})
	}
	first := configFixture(t, "unknown: true\n")
	second := configFixture(t, "version: 8\n")
	out, _, err := executeCLI(t, "--config", first, "config", "check", "--config", second)
	if err == nil || !strings.Contains(out, first) || !strings.Contains(out, second) || !strings.Contains(err.Error(), first) || !strings.Contains(err.Error(), second) {
		t.Fatalf("all files should be checked: %s, %v", out, err)
	}
}

// TestInitChecksOverlaysBeforeWriting ensures a broken secondary file cannot
// leave a newly written primary behind after reporting that initialisation failed.
func TestInitChecksOverlaysBeforeWriting(t *testing.T) {
	for _, contents := range []string{"", "unknown: true\n"} {
		primary := filepath.Join(t.TempDir(), "lexr.yml")
		overlay := filepath.Join(t.TempDir(), "overlay.yml")
		if contents != "" {
			if err := os.WriteFile(overlay, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := executeCLI(t, "--config", primary, "--config", overlay, "init", "x1e80100-microsoft-denali-oled"); err == nil {
			t.Fatal("accepted a missing or invalid overlay")
		}
		if _, err := os.Stat(primary); !os.IsNotExist(err) {
			t.Fatalf("primary was written before overlay validation: %v", err)
		}
	}
}

// TestConfigShowAndFlagPrecedence exercises repeatable flags and effective YAML.
func TestConfigShowAndFlagPrecedence(t *testing.T) {
	environment := configFixture(t, "profile: environment\n")
	t.Setenv("LEXR_CONFIG", environment)
	first := configFixture(t, "version: 1\nprofile: base\nclean: {plan: {root: /target, feature: [audio, camera], json: true}}\n")
	second := configFixture(t, "profile: overlay\nclean: {plan: {feature: [touchscreen], json: false}}\n")
	for _, args := range [][]string{
		{"config", "show", "--config", first, "--config", second},
		{"--config", first + "," + second, "config", "show"},
	} {
		out, _, err := executeCLI(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, first) || !strings.Contains(out, second) || strings.Contains(out, environment) || strings.Index(out, first) > strings.Index(out, second) {
			t.Fatalf("unexpected source paths: %s", out)
		}
		var got lexrconfig.Config
		if err := yaml.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(err)
		}
		if got.Profile != "overlay" || got.Clean.Plan.Root != "/target" || got.Clean.Plan.JSON || !reflect.DeepEqual(got.Clean.Plan.Feature, []string{"touchscreen"}) {
			t.Fatalf("incorrect merged output: %s", out)
		}
		if !strings.Contains(out, "  plan:\n    feature:") {
			t.Fatalf("expected indentation of two spaces: %s", out)
		}
	}
	out, _, err := executeCLI(t, "config", "show")
	if err != nil || !strings.Contains(out, "profile: environment") || !strings.Contains(out, environment) {
		t.Fatalf("environment selection = %s, %v", out, err)
	}
	for _, args := range [][]string{
		{"--config=", "version"},
		{"--config", first, "--config=", "version"},
		{"--config", first + ",", "version"},
		{"--config", "  ", "version"},
	} {
		if _, _, err := executeCLI(t, args...); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("empty path %v accepted: %v", args, err)
		}
	}
}

// TestConfigMissingDefaultAndExplicitFiles limits optional loading to defaults.
func TestConfigMissingDefaultAndExplicitFiles(t *testing.T) {
	t.Setenv("LEXR_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if _, _, err := executeCLI(t, "version"); err != nil {
		t.Fatalf("ordinary command needs no default file: %v", err)
	}
	for _, operation := range []string{"check", "show"} {
		if _, _, err := executeCLI(t, "config", operation); err == nil {
			t.Fatalf("config %s accepted missing default", operation)
		}
	}
	missing := filepath.Join(t.TempDir(), "missing.yml")
	if _, _, err := executeCLI(t, "--config", missing, "version"); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("explicit missing file accepted: %v", err)
	}
	t.Setenv("LEXR_CONFIG", missing)
	if _, _, err := executeCLI(t, "version"); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing environment file accepted: %v", err)
	}
}

// TestConfigEdit checks editor precedence and creation without a real editor.
func TestConfigEdit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("editor fixture uses a POSIX shell")
	}
	directory := t.TempDir()
	editor := filepath.Join(directory, "test editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$LEXR_TEST_EDITOR_LOG\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(directory, "editor.log")
	t.Setenv("LEXR_TEST_EDITOR_LOG", log)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", editor)
	primary := filepath.Join(directory, "new", "lexr.yml")
	secondary := configFixture(t, "unknown: ignored by edit\n")
	if _, _, err := executeCLI(t, "config", "edit", "--config", primary, "--config", secondary); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil || string(data) != primary+"\n" {
		t.Fatalf("editor arguments = %q, %v", data, err)
	}
	data, err = os.ReadFile(primary)
	if err != nil || string(data) != lexrconfig.Template {
		t.Fatalf("template = %q, %v", data, err)
	}
	if _, err := lexrconfig.LoadFiles([]string{primary}); err != nil {
		t.Fatalf("template must validate: %v", err)
	}
	// VISUAL wins over EDITOR, and existing invalid files remain editable.
	t.Setenv("VISUAL", editor)
	t.Setenv("EDITOR", "nonexistent-editor")
	if _, _, err := executeCLI(t, "config", "edit", "--config", secondary); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(secondary)
	if err != nil || string(data) != "unknown: ignored by edit\n" {
		t.Fatalf("existing file overwritten: %q, %v", data, err)
	}
	t.Setenv("VISUAL", "nonexistent-lexr-editor")
	if _, _, err := executeCLI(t, "config", "edit", "--config", primary); err == nil || !strings.Contains(err.Error(), primary) {
		t.Fatalf("editor failure should name file: %v", err)
	}
}

// TestInitProfile checks creation, confirmation and preservation through the CLI.
func TestInitProfile(t *testing.T) {
	t.Setenv("LEXR_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	profile := "x1e80100-microsoft-denali-oled"
	if _, _, err := executeCLI(t, "init", profile); err != nil {
		t.Fatal(err)
	}
	primary, err := lexrconfig.ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	got, err := lexrconfig.LoadFiles([]string{primary})
	if err != nil || got.Version != 1 || got.Profile != profile {
		t.Fatalf("new configuration = %#v, %v", got, err)
	}
	// Idempotent invocation requires no confirmation.
	if _, _, err := executeCLI(t, "init", profile); err != nil {
		t.Fatal(err)
	}
	original := "# User settings\nversion: 1 # schema\n# Hardware choice\nprofile: old # retain this comment\nuserspace:\n  pull:\n    cache_dir: /custom/cache # keep cache\n"
	path := configFixture(t, original)
	if _, _, err := executeCLI(t, "--config", path, "init", profile); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("conflicting profile accepted: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("refusal changed the file: %q, %v", data, err)
	}
	secondary := configFixture(t, "profile: secondary\n")
	if _, _, err := executeCLI(t, "--config", path, "--config", secondary, "init", profile, "--force"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# User settings", "# schema", "# Hardware choice", "# retain this comment", "/custom/cache # keep cache", "profile: " + profile} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing preserved value %q: %s", want, data)
		}
	}
	data, err = os.ReadFile(secondary)
	if err != nil || string(data) != "profile: secondary\n" {
		t.Fatalf("secondary changed: %q, %v", data, err)
	}
	noProfile := configFixture(t, "# Existing cache\nuserspace: {pull: {cache_dir: /keep}}\n")
	t.Setenv("LEXR_CONFIG", noProfile)
	if _, _, err := executeCLI(t, "init", profile); err != nil {
		t.Fatal(err)
	}
	got, err = lexrconfig.LoadFiles([]string{noProfile})
	if err != nil || got.Profile != profile || got.Version != 1 || got.Userspace.Pull.CacheDir != "/keep" {
		t.Fatalf("profile insertion = %#v, %v", got, err)
	}
	if _, _, err := executeCLI(t, "init", " ", "--force"); err == nil {
		t.Fatal("empty profile accepted")
	}
}

// TestBootDoctorProfileFallback exercises the shared resolver before domain
// delivery. Legacy device flags and saved selectors no longer exist: only the
// top-level profile or --profile can choose the diagnostic variant.
func TestBootDoctorProfileFallback(t *testing.T) {
	for _, test := range []struct {
		name, profile, want, wantError string
		flags                          []string
	}{
		{name: "OLED", profile: "x1e80100-microsoft-denali-oled", want: "x1e-oled"},
		{name: "LCD", profile: "x1p64100-microsoft-denali", want: "x1p-lcd"},
		{name: "unknown", profile: "future-device", wantError: "unknown hardware profile"},
		{name: "one-shot wins over saved", profile: "x1e80100-microsoft-denali-oled", flags: []string{"--profile", "x1p64100-microsoft-denali"}, want: "x1p-lcd"},
		{name: "auto keeps live detection", profile: "x1e80100-microsoft-denali-oled", flags: []string{"--profile", "auto"}, wantError: "needs a hardware profile"},
		{name: "removed legacy flag", flags: []string{"--device", "x1p-lcd"}, wantError: "unknown flag: --device"},
		{name: "missing offline choice", wantError: "needs a hardware profile"},
	} {
		t.Run(test.name, func(t *testing.T) {
			configuration := lexrconfig.Config{Profile: test.profile}
			var output, diagnostics bytes.Buffer
			app := &application{configuration: configuration, out: &output, errOut: &diagnostics}
			stub := &bootDoctorStub{report: bootdoctor.Report{Ready: true}}
			root := &cobra.Command{Use: "lexr", SilenceErrors: true, SilenceUsage: true,
				PersistentPreRunE: func(command *cobra.Command, _ []string) error { return app.selectProfile(command) },
			}
			root.SetOut(&output)
			root.SetErr(&diagnostics)
			root.PersistentFlags().StringVar(&app.profileName, "profile", "", "hardware profile")
			doctor := &cobra.Command{Use: "doctor"}
			doctor.AddCommand(app.newBootDoctorCommand(func() bootDoctorWorkflow { return stub }))
			root.AddCommand(doctor)
			root.SetArgs(append([]string{"doctor", "boot", "--root", t.TempDir(), "--json"}, test.flags...))
			err := root.Execute()
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if stub.options.Device != test.want {
				t.Fatalf("device = %q, want %q", stub.options.Device, test.want)
			}
			if !strings.HasPrefix(output.String(), "{") {
				t.Fatalf("JSON polluted: %s", output.String())
			}
		})
	}
}
