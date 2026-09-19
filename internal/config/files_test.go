package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestLoadFilesMergesPresentKeys checks recursive maps and explicit zero/list overrides.
func TestLoadFilesMergesPresentKeys(t *testing.T) {
	base := writeConfigFile(t, `version: 1
profile: original
clean:
  plan: {root: /target, feature: [audio, camera], json: true, output: plan.json}
kernel: {build: {jobs: 8}}
`)
	overlay := writeConfigFile(t, `version: 1
profile: replacement
clean:
  plan: {feature: [touchscreen], json: false, output: ""}
kernel: {build: {jobs: 0}}
`)
	got, err := LoadFiles([]string{base, overlay})
	if err != nil {
		t.Fatal(err)
	}
	if got.Profile != "replacement" || got.Clean.Plan.Root != "/target" || got.Clean.Plan.JSON || got.Clean.Plan.Output != "" || got.Kernel.Build.Jobs != 0 || !reflect.DeepEqual(got.Clean.Plan.Feature, []string{"touchscreen"}) {
		t.Fatalf("unexpected merge: %#v", got)
	}
	empty := writeConfigFile(t, "clean: {plan: {feature: []}}\nuserspace: null\n")
	got, err = LoadFiles([]string{base, overlay, empty})
	if err != nil || len(got.Clean.Plan.Feature) != 0 || got.Clean.Plan.Root != "/target" || got.Version != 1 {
		t.Fatalf("empty/null override = %#v, %v", got, err)
	}
}

// TestLoadFilesRejectsInvalidLayers prevents later layers from hiding invalid YAML.
func TestLoadFilesRejectsInvalidLayers(t *testing.T) {
	base := writeConfigFile(t, "version: 1\n")
	for _, test := range []struct{ name, contents, want string }{
		{"unknown overwritten", "clean: {plan: {typo: true}}", "field typo not found"},
		{"bad type overwritten", "clean: {plan: {json: maybe}}", "cannot unmarshal"},
		{"duplicate overwritten", "profile: one\nprofile: two\n", "already defined"},
		{"extra document", "version: 1\n---\nprofile: other\n", "single YAML document"},
		{"version conflict", "version: 2\n", "conflicts with version 1"},
		{"zero version", "version: 0\n", "unsupported version"},
		{"null version", "version: null\n", "unsupported version"},
		{"string version", "version: '1'\n", "cannot unmarshal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bad := writeConfigFile(t, test.contents)
			last := writeConfigFile(t, "clean: null\nprofile: final\n")
			_, err := LoadFiles([]string{base, bad, last})
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), bad) {
				t.Fatalf("error = %v, want %q and file path", err, test.want)
			}
			if test.name == "version conflict" && !strings.Contains(err.Error(), base) {
				t.Fatalf("conflict must name both files: %v", err)
			}
		})
	}
	missing := filepath.Join(t.TempDir(), "missing.yml")
	bad := writeConfigFile(t, "unknown: value\n")
	_, err := LoadFiles([]string{missing, bad})
	if err == nil || !strings.Contains(err.Error(), missing) || !strings.Contains(err.Error(), bad) {
		t.Fatalf("expected errors for both files, got %v", err)
	}
	if _, err := LoadFiles([]string{t.TempDir()}); err == nil {
		t.Fatal("directory accepted as readable YAML file")
	}
}

// TestResolvePathsAndEmptyFiles distinguishes absent overrides from empty paths.
func TestResolvePathsAndEmptyFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LEXR_CONFIG", "~/environment.yml")
	paths, err := ResolvePaths(nil)
	if err != nil || !reflect.DeepEqual(paths, []string{filepath.Join(home, "environment.yml")}) {
		t.Fatalf("default paths = %v, %v", paths, err)
	}
	paths, err = ResolvePaths([]string{"~/first.yml", "second.yml"})
	second, _ := filepath.Abs("second.yml")
	if err != nil || !reflect.DeepEqual(paths, []string{filepath.Join(home, "first.yml"), second}) {
		t.Fatalf("explicit paths = %v, %v", paths, err)
	}
	for _, paths := range [][]string{{}, {""}, {"  "}, {"valid.yml", ""}} {
		if _, err := ResolvePaths(paths); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("ResolvePaths(%q) = %v", paths, err)
		}
		if _, err := LoadFiles(paths); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("LoadFiles(%q) = %v", paths, err)
		}
	}
	if _, err := LoadFiles(nil); err == nil {
		t.Fatal("empty file set accepted")
	}
	configuration, err := LoadFiles([]string{writeConfigFile(t, "# empty layer\n"), writeConfigFile(t, "version: 1\n")})
	if err != nil || configuration.Version != 1 {
		t.Fatalf("empty layer = %#v, %v", configuration, err)
	}
}

// TestWriteMergedExpandsPathsWithoutAnchorsOrDefaults checks effective YAML output.
func TestWriteMergedExpandsPathsWithoutAnchorsOrDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := writeConfigFile(t, `version: 1
doctor: {workspace: &workspace ~/work}
wizard: {cache_dir: *workspace}
userspace: {camera: {release: {prepare: {output_dir: ~/verbatim}}}}
`)
	var output bytes.Buffer
	if err := WriteMerged(&output, []string{path}); err != nil {
		t.Fatal(err)
	}
	for _, unexpected := range []string{"&workspace", "*workspace", "device:", "kernel:"} {
		if strings.Contains(output.String(), unexpected) {
			t.Fatalf("unexpected %q in %s", unexpected, &output)
		}
	}
	var got Config
	if err := yaml.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Doctor.Workspace != filepath.Join(home, "work") || got.Wizard.CacheDir != got.Doctor.Workspace || got.Userspace.Camera.Release.Prepare.OutputDir != "~/verbatim" {
		t.Fatalf("unexpected expanded configuration: %s", &output)
	}
	if _, err := os.Stat(filepath.Join(home, "work")); !os.IsNotExist(err) {
		t.Fatalf("show created a directory: %v", err)
	}
}
