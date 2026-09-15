package config

import (
	"os"
	"path/filepath"
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
// the existing operating-system userspace cache location without error.
func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	configuration, err := Load(filepath.Join(t.TempDir(), "missing.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if configuration.UserspaceDir != "" || configuration.CacheDir != "" {
		t.Fatalf("configuration = %#v, want empty configured overrides", configuration)
	}
	userCache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(userCache, "lexr", "userspace")
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

// TestLoadValidFile verifies supported keys, comments and blank lines in the
// deliberately small flat configuration format.
func TestLoadValidFile(t *testing.T) {
	path := writeConfigFile(t, "# Lexr directories\n\nuserspace_dir: /srv/lexr/userspace\ncache_dir: /var/cache/lexr\n")
	configuration, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.UserspaceDir != "/srv/lexr/userspace" || configuration.CacheDir != "/var/cache/lexr" {
		t.Fatalf("configuration = %#v", configuration)
	}
}

// TestLoadExpandsUserHome verifies that the supported ~/ prefix uses the
// current user's home directory for both directory settings.
func TestLoadExpandsUserHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := writeConfigFile(t, "userspace_dir: ~/bundles\ncache_dir: ~/.cache/lexr-test\n")
	configuration, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.UserspaceDir != filepath.Join(home, "bundles") || configuration.CacheDir != filepath.Join(home, ".cache", "lexr-test") {
		t.Fatalf("configuration = %#v", configuration)
	}
}

// TestLoadRejectsInvalidConfiguration verifies unknown, duplicate, malformed
// and empty settings produce line-specific errors.
func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct {
		name      string
		contents  string
		wantError string
	}{
		{name: "unknown", contents: "other_dir: /tmp/other\n", wantError: "unknown configuration key"},
		{name: "duplicate", contents: "cache_dir: /tmp/one\ncache_dir: /tmp/two\n", wantError: "duplicate configuration key"},
		{name: "missing colon", contents: "cache_dir /tmp/cache\n", wantError: "exactly one key: value pair"},
		{name: "extra colon", contents: "cache_dir: /tmp:cache\n", wantError: "exactly one key: value pair"},
		{name: "empty key", contents: ": /tmp/cache\n", wantError: "key is empty"},
		{name: "empty value", contents: "userspace_dir:\n", wantError: "requires a value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(writeConfigFile(t, test.contents))
			if err == nil || !strings.Contains(err.Error(), test.wantError) || !strings.Contains(err.Error(), "line ") {
				t.Fatalf("error = %v, want line-specific %q", err, test.wantError)
			}
		})
	}
}

// TestLoadUsesEnvironmentPathOverride verifies that LEXR_CONFIG supplies the
// path only when the caller does not pass an explicit one.
func TestLoadUsesEnvironmentPathOverride(t *testing.T) {
	path := writeConfigFile(t, "cache_dir: /tmp/environment-cache\n")
	t.Setenv("LEXR_CONFIG", path)
	configuration, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if configuration.CacheDir != "/tmp/environment-cache" {
		t.Fatalf("configuration = %#v", configuration)
	}
	explicitPath := writeConfigFile(t, "cache_dir: /tmp/explicit-cache\n")
	configuration, err = Load(explicitPath)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.CacheDir != "/tmp/explicit-cache" {
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
