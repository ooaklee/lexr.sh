package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestResolvePathUsesStandardLocation verifies the default file is placed in
// the operating system's user configuration directory beneath lexr/lexr.yml.
func TestResolvePathUsesStandardLocation(t *testing.T) {
	t.Setenv("LEXR_CONFIG", "")
	userConfig, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(userConfig, "lexr", "lexr.yml"); path != want {
		t.Fatalf("ResolvePath() = %q, want %q", path, want)
	}
}

// TestLoadMissingFileReturnsDefaults verifies that an absent optional file uses
// config home userspace cache location without error.
func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	configuration, err := Load(filepath.Join(t.TempDir(), "missing.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(configuration, Config{}) {
		t.Fatalf("configuration = %#v, want empty configured overrides", configuration)
	}
	userCache, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(userCache, "lexr", "caches", "userspace")
	userspaceDirectory, err := configuration.ResolveUserspaceDir()
	if err != nil {
		t.Fatal(err)
	}
	cacheDirectory, err := configuration.ResolveCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if userspaceDirectory != want || cacheDirectory != want {
		t.Fatalf("resolved directories = %q, %q; want %q", userspaceDirectory, cacheDirectory, want)
	}
}

// TestLoadValidFile verifies nested settings, comments, lists, typed values and
// quoted strings in the YAML configuration format.
func TestLoadValidFile(t *testing.T) {
	path := writeConfigFile(t, `# Lexr settings
version: 1

global:
  catalog: "https://example.com/catalog.json"
  userspace_catalog: /srv/catalog.json
catalog:
  list: {json: true}
clean:
  plan:
    root: /target
    feature: [audio, camera]
    output: /tmp/plan.json
doctor:
  boot: {target_abi: test-abi, json: true}
  userspace: {feature: [audio]}
handoff:
  apply: {feature: [audio], adsp_policy: preserve}
image:
  create:
    source: "/tmp/source:disk.img"
    companion_userspace: [audio, camera]
    refresh_source: true
  release:
    prepare: {part_size_bytes: 5368709120}
kernel:
  boot:
    register-arch: {arch_root: /target, esp: /boot/efi}
  release:
    prepare: {source: [source.tar], licence: [COPYING]}
    list: {limit: 3}
    download: {headers: true}
  install: {package_set: standard, dry_run: true}
  build: {jobs: 4}
userspace:
  catalog:
    validate: {}
  status: {feature: [camera]}
  pull: {cache_dir: /var/cache/lexr, json: true}
  build: {jobs: 2, minimum_free_gib: 32}
  audio:
    release:
      prepare: {kernel_abi: test-abi}
  camera:
    capture: {frames: 8}
    render: {frame: 2, linear: true}
    release:
      validate: {authority_sha256: abc123}
wizard:
  output: /tmp/lexr.img
`)
	configuration, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Version != 1 ||
		configuration.Global.Catalog != "https://example.com/catalog.json" ||
		configuration.Global.UserspaceCatalog != "/srv/catalog.json" || !configuration.Catalog.List.JSON ||
		configuration.Clean.Plan.Root != "/target" || configuration.Clean.Plan.Output != "/tmp/plan.json" ||
		!reflect.DeepEqual(configuration.Clean.Plan.Feature, []string{"audio", "camera"}) ||
		configuration.Doctor.Boot.TargetABI != "test-abi" || !configuration.Doctor.Boot.JSON ||
		!reflect.DeepEqual(configuration.Doctor.Userspace.Feature, []string{"audio"}) ||
		!reflect.DeepEqual(configuration.Handoff.Apply.Feature, []string{"audio"}) ||
		configuration.Handoff.Apply.ADSPPolicy != "preserve" ||
		configuration.Image.Create.Source != "/tmp/source:disk.img" || !configuration.Image.Create.RefreshSource ||
		!reflect.DeepEqual(configuration.Image.Create.CompanionUserspace, []string{"audio", "camera"}) ||
		configuration.Image.Release.Prepare.PartSizeBytes != 5368709120 ||
		configuration.Kernel.Boot.RegisterArch.ArchRoot != "/target" ||
		configuration.Kernel.Boot.RegisterArch.ESP != "/boot/efi" ||
		!reflect.DeepEqual(configuration.Kernel.Release.Prepare.Source, []string{"source.tar"}) ||
		!reflect.DeepEqual(configuration.Kernel.Release.Prepare.Licence, []string{"COPYING"}) ||
		configuration.Kernel.Release.List.Limit != 3 || !configuration.Kernel.Release.Download.Headers ||
		configuration.Kernel.Install.PackageSet != "standard" || !configuration.Kernel.Install.DryRun ||
		configuration.Kernel.Build.Jobs != 4 || !reflect.DeepEqual(configuration.Userspace.Status.Feature, []string{"camera"}) ||
		configuration.Userspace.Pull.CacheDir != "/var/cache/lexr" || !configuration.Userspace.Pull.JSON ||
		configuration.Userspace.Build.Jobs != 2 || configuration.Userspace.Build.MinimumFreeGiB != 32 ||
		configuration.Userspace.Audio.Release.Prepare.KernelABI != "test-abi" ||
		configuration.Userspace.Camera.Capture.Frames != 8 || configuration.Userspace.Camera.Render.Frame != 2 ||
		!configuration.Userspace.Camera.Render.Linear ||
		configuration.Userspace.Camera.Release.Validate.AuthoritySHA256 != "abc123" ||
		configuration.Wizard.Output != "/tmp/lexr.img" {
		t.Fatalf("configuration = %#v", configuration)
	}
}

// TestLoadExpandsUserHome verifies that the supported ~/ prefix uses the
// current user's home directory in nested strings, lists and the file path.
func TestLoadExpandsUserHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	contents := "userspace:\n  pull:\n    cache_dir: ~/.cache/lexr-test\nkernel:\n  release:\n    prepare:\n      source: [~/source.tar, '$HOME/literal']\n"
	if err := os.WriteFile(filepath.Join(home, "lexr.yml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	configuration, err := Load("~/lexr.yml")
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Userspace.Pull.CacheDir != filepath.Join(home, ".cache", "lexr-test") ||
		!reflect.DeepEqual(configuration.Kernel.Release.Prepare.Source, []string{filepath.Join(home, "source.tar"), "$HOME/literal"}) {
		t.Fatalf("configuration = %#v", configuration)
	}
}

// TestLoadRejectsInvalidConfiguration verifies unknown keys at every nesting
// depth, duplicates, malformed YAML and incorrectly typed values are rejected.
func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct {
		name      string
		contents  string
		wantError string
	}{
		{"unknown", "other_dir: /tmp/other\n", "field other_dir not found"},
		{"old cache key", "cache_dir: /tmp/cache\n", "field cache_dir not found"},
		{"old userspace key", "userspace_dir: /tmp/bundles\n", "field userspace_dir not found"},
		{"unknown nested", "userspace:\n  pull:\n    other_dir: /tmp/other\n", "field other_dir not found"},
		{"empty schema", "userspace:\n  catalog:\n    validate: {json: true}\n", "field json not found"},
		{"register arch tag", "kernel:\n  boot:\n    register_arch: {}\n", "field register_arch not found"},
		{"duplicate", "userspace:\n  pull:\n    cache_dir: /tmp/one\n    cache_dir: /tmp/two\n", "already defined"},
		{"missing colon", "version 1\n", "cannot unmarshal"},
		{"empty key", "version: 1\n: /tmp/cache\n", "did not find expected key"},
		{"malformed", "userspace: [\n", "did not find expected node content"},
		{"invalid bool", "catalog: {list: {json: perhaps}}\n", "cannot unmarshal"},
		{"invalid integer", "version: first\n", "cannot unmarshal"},
		{"invalid list", "clean: {scan: {feature: audio}}\n", "cannot unmarshal"},
		{"invalid feature list", "doctor: {userspace: {feature: audio}}\n", "cannot unmarshal"},
		{"legacy device variant", "device: {variant: x1p-lcd}\n", "field device not found"},
		{"legacy boot device", "doctor: {boot: {device: x1p-lcd}}\n", "field device not found"},
		{"legacy kernel profile", "image: {create: {kernel_profile: surface-pro-11-x1p-lcd}}\n", "field kernel_profile not found"},
		{"legacy refresh profile", "kernel: {boot: {refresh: {profile: x1p-lcd}}}\n", "field profile not found"},
		{"YAML consent is command-line-only", "image: {write: {confirm: true}}\n", "field confirm not found"},
		{"YAML storage target is command-line-only", "image: {write: {device: /dev/danger}}\n", "field device not found"},
		{"YAML safety bypass is command-line-only", "kernel: {install: {overwrite: true}}\n", "field overwrite not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(writeConfigFile(t, test.contents))
			if err == nil || !strings.Contains(err.Error(), test.wantError) || !strings.Contains(err.Error(), "line ") {
				t.Fatalf("error = %v, want line-specific %q", err, test.wantError)
			}
		})
	}
}

// TestLoadRejectsMultipleDocuments verifies trailing YAML cannot be silently ignored.
func TestLoadRejectsMultipleDocuments(t *testing.T) {
	_, err := Load(writeConfigFile(t, "version: 1\n---\nversion: 2\n"))
	if err == nil || !strings.Contains(err.Error(), "single YAML document") {
		t.Fatalf("error = %v, want multiple-document rejection", err)
	}
}

// TestLoadEmptyFileReturnsEmpty verifies empty files and YAML null values use
// zero values, as do omitted optional settings.
func TestLoadEmptyFileReturnsEmpty(t *testing.T) {
	for _, contents := range []string{"", "# comment only\n", "userspace:\n  pull:\n    cache_dir:\n"} {
		configuration, err := Load(writeConfigFile(t, contents))
		if err != nil || !reflect.DeepEqual(configuration, Config{}) {
			t.Fatalf("Load(%q) = %#v, %v; want empty configuration", contents, configuration, err)
		}
	}
}

// TestLoadUsesEnvironmentPathOverride verifies that LEXR_CONFIG supplies the
// path only when the caller does not pass an explicit one.
func TestLoadUsesEnvironmentPathOverride(t *testing.T) {
	path := writeConfigFile(t, "userspace:\n  pull:\n    cache_dir: /tmp/environment-cache\n")
	t.Setenv("LEXR_CONFIG", path)
	configuration, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Userspace.Pull.CacheDir != "/tmp/environment-cache" {
		t.Fatalf("configuration = %#v", configuration)
	}
	explicitPath := writeConfigFile(t, "userspace:\n  pull:\n    cache_dir: /tmp/explicit-cache\n")
	configuration, err = Load(explicitPath)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.Userspace.Pull.CacheDir != "/tmp/explicit-cache" {
		t.Fatalf("explicit configuration = %#v", configuration)
	}
}

// writeConfigFile creates one private test configuration and returns its path.
func writeConfigFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lexr.yml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
