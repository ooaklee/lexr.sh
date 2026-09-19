package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestSetProfilePreservesYAMLValues covers comments, nulls and scalar aliases.
func TestSetProfilePreservesYAMLValues(t *testing.T) {
	for _, test := range []struct{ name, contents, want string }{
		{"comments only", "# Keep this header\n", "# Keep this header"},
		{"null document", "# Keep this header\nnull\n", "# Keep this header"},
		{"empty profile", "profile: '' # Hardware\n", "# Hardware"},
		{"aliased profile", "userspace: {pull: {cache_dir: &cache /keep}}\nprofile: *cache\n", "&cache /keep"},
		{"profile anchor", "profile: &profile old\nuserspace: {pull: {cache_dir: *profile}}\n", "cache_dir: old"},
		{"YAML merge key", "doctor: {boot: &boot {root: /target}, hardware: {<<: *boot}}\nprofile: old\n", "&boot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeConfigFile(t, test.contents)
			if err := SetProfile(path, "new", true); err != nil {
				t.Fatal(err)
			}
			configuration, err := LoadFiles([]string{path})
			if err != nil || configuration.Profile != "new" || configuration.Version != 1 {
				t.Fatalf("profile update = %#v, %v", configuration, err)
			}
			if strings.Contains(test.contents, "/keep") && configuration.Userspace.Pull.CacheDir != "/keep" {
				t.Fatalf("unrelated value changed: %#v", configuration.Userspace)
			}
			data, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(data), test.want) {
				t.Fatalf("missing preserved %q in %s, %v", test.want, data, err)
			}
		})
	}
}

// TestSetProfileRefusesInvalidEvenWithForce keeps force scoped to profile conflicts.
func TestSetProfileRefusesInvalidEvenWithForce(t *testing.T) {
	for _, contents := range []string{"unknown: true\n", "version: 2\n", "profile: old\n---\nprofile: other\n"} {
		path := writeConfigFile(t, contents)
		if err := SetProfile(path, "new", true); err == nil {
			t.Fatalf("invalid file accepted: %s", contents)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != contents {
			t.Fatalf("invalid file changed: %s, %v", data, err)
		}
	}
}

// TestSetProfilePreservesSymlinkAndMode checks replacement of an existing target.
func TestSetProfilePreservesSymlinkAndMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions and symlink fixture")
	}
	path := writeConfigFile(t, "version: 1\n")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "config.yml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := SetProfile(link, "new", false); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != path {
		t.Fatalf("symlink changed: %q, %v", target, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("file mode changed: %v, %v", info, err)
	}
	configuration, err := LoadFiles([]string{path})
	if err != nil || configuration.Profile != "new" {
		t.Fatalf("symlink target not updated: %#v, %v", configuration, err)
	}
}

// TestSetProfilePreservesDanglingSymlink checks that a symlink whose target is
// absent causes the target's creation instead of replacing the link itself.
func TestSetProfilePreservesDanglingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink fixture")
	}
	target := filepath.Join(t.TempDir(), "actual.yml")
	link := filepath.Join(t.TempDir(), "config.yml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := SetProfile(link, "new-profile", false); err != nil {
		t.Fatal(err)
	}
	if current, err := os.Readlink(link); err != nil || current != target {
		t.Fatalf("symlink changed: %q, %v", current, err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("configuration path replaced instead of symlink target: %v, %v", info, err)
	}
	configuration, err := LoadFiles([]string{target})
	if err != nil || configuration.Profile != "new-profile" {
		t.Fatalf("dangling target not created: %#v, %v", configuration, err)
	}
}

// TestSetProfilePreservesChainedSymlinks checks a link pointing through a
// second link updates only the final target.
func TestSetProfilePreservesChainedSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink fixtures")
	}
	target := filepath.Join(t.TempDir(), "actual.yml")
	intermediate := filepath.Join(t.TempDir(), "intermediate.yml")
	if err := os.Symlink(target, intermediate); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "config.yml")
	if err := os.Symlink(intermediate, link); err != nil {
		t.Fatal(err)
	}
	if err := SetProfile(link, "chained", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("outer symlink broken: %v", err)
	}
	if _, err := os.Lstat(intermediate); err != nil {
		t.Fatalf("intermediate symlink broken: %v", err)
	}
	configuration, err := LoadFiles([]string{target})
	if err != nil || configuration.Profile != "chained" {
		t.Fatalf("final target not updated: %#v, %v", configuration, err)
	}
}
