package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// isolateDirectoryHome keeps all default directories within the test fixture.
func isolateDirectoryHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("APPDATA", filepath.Join(home, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("LEXR_CONFIG", "")
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(base, "lexr")
}

// TestDirectoryResolvers verifies empty YAML defaults, explicit overrides,
// home expansion, lazy creation and an unchanged loaded configuration.
func TestDirectoryResolvers(t *testing.T) {
	for _, test := range []struct {
		name, yaml, relative string
		resolve              func(Config) (string, error)
	}{
		{"userspace", "userspace: {pull: {cache_dir: '~/chosen'}}", "caches/userspace", Config.ResolveCacheDir},
		{"install lookup", "userspace: {pull: {cache_dir: '~/chosen'}}", "caches/userspace", Config.ResolveUserspaceDir},
		{"image cache", "image: {create: {cache_dir: '~/chosen'}}", "caches/image", Config.ResolveImageCacheDir},
		{"image workspace", "image: {create: {workspace_dir: '~/chosen'}}", "builds/image", Config.ResolveImageWorkspaceDir},
		{"doctor", "doctor: {workspace: '~/chosen'}", "builds/doctor", Config.ResolveDoctorWorkspace},
		{"kernel download", "kernel: {release: {download: {output_dir: '~/chosen'}}}", "caches/kernel", Config.ResolveKernelDownloadDir},
		{"wizard", "wizard: {cache_dir: '~/chosen'}", "caches/wizard", Config.ResolveWizardCacheDir},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := isolateDirectoryHome(t)
			configuration, err := Load("")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("Load created home: %v", err)
			}
			for range 2 {
				got, err := test.resolve(configuration)
				want := filepath.Join(home, filepath.FromSlash(test.relative))
				if err != nil || got != want {
					t.Fatalf("default = %q, %v; want %q", got, err, want)
				}
				if info, err := os.Stat(got); err != nil || !info.IsDir() {
					t.Fatalf("default directory missing: %v", err)
				}
			}
			if !reflect.DeepEqual(configuration, Config{}) {
				t.Fatal("resolver mutated loaded values")
			}
			configuration, err = Load(writeConfigFile(t, test.yaml))
			if err != nil {
				t.Fatal(err)
			}
			got, err := test.resolve(configuration)
			userHome, _ := os.UserHomeDir()
			if want := filepath.Join(userHome, "chosen"); err != nil || got != want {
				t.Fatalf("override = %q, %v; want %q", got, err, want)
			}
			if _, err := os.Stat(got); !os.IsNotExist(err) {
				t.Fatalf("resolver created explicit override: %v", err)
			}
		})
	}
}

// TestDirectoryHelpers validates domains before creation and surfaces filesystem errors.
func TestDirectoryHelpers(t *testing.T) {
	home := isolateDirectoryHome(t)
	for _, domain := range []string{"", "Image", "../image", "/image", "image/cache", `image\cache`} {
		if _, err := DefaultCacheDir(domain); err == nil {
			t.Fatalf("accepted domain %q", domain)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("invalid domains created home: %v", err)
	}
	if got, err := ConfigHome(); err != nil || got != home {
		t.Fatalf("ConfigHome = %q, %v", got, err)
	}
	if got, err := DefaultBuildDir("image"); err != nil || got != filepath.Join(home, "builds", "image") {
		t.Fatalf("DefaultBuildDir = %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(home, "caches"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Config{}).ResolveImageCacheDir(); err == nil {
		t.Fatal("creation error silently fell back to legacy cache")
	}
	c := Config{Image: ImageConfig{Create: ImageCreateConfig{CacheDir: "./custom/../cache"}}}
	if got, err := c.ResolveImageCacheDir(); err != nil || got != "./custom/../cache" {
		t.Fatalf("explicit relative path = %q, %v", got, err)
	}
}

// TestDirectoryOverrideExpandsHome verifies resolvers also expand manually
// constructed settings, without requiring a YAML load first.
func TestDirectoryOverrideExpandsHome(t *testing.T) {
	isolateDirectoryHome(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	c := Config{Doctor: DoctorConfig{Workspace: "~"}}
	if got, err := c.ResolveDoctorWorkspace(); err != nil || got != home {
		t.Fatalf("expanded home = %q, %v", got, err)
	}
	c.Doctor.Workspace = "~/work"
	if got, err := c.ResolveDoctorWorkspace(); err != nil || got != filepath.Join(home, "work") {
		t.Fatalf("expanded workspace = %q, %v", got, err)
	}
}

// TestDirectoryLegacyFallback verifies legacy values only follow an unavailable
// OS configuration location, while explicit values remain usable.
func TestDirectoryLegacyFallback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("tests Linux XDG location resolution")
	}
	isolateDirectoryHome(t)
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, test := range []struct {
		resolve func(Config) (string, error)
		want    string
	}{
		{Config.ResolveDoctorWorkspace, "."},
		{Config.ResolveImageWorkspaceDir, ""},
		{Config.ResolveKernelDownloadDir, "kernel-bundle"},
		{Config.ResolveCacheDir, filepath.Join(os.Getenv("XDG_CACHE_HOME"), "lexr", "userspace")},
		{Config.ResolveImageCacheDir, filepath.Join(os.Getenv("XDG_CACHE_HOME"), "lexr")},
		{Config.ResolveWizardCacheDir, filepath.Join(os.Getenv("XDG_CACHE_HOME"), "lexr")},
	} {
		if got, err := test.resolve(Config{}); err != nil || got != test.want {
			t.Fatalf("legacy = %q, %v; want %q", got, err, test.want)
		}
	}
	c := Config{Doctor: DoctorConfig{Workspace: "chosen"}}
	if got, err := c.ResolveDoctorWorkspace(); err != nil || got != "chosen" {
		t.Fatalf("explicit = %q, %v", got, err)
	}
}

// TestRepositoryPathsLoadVerbatim protects paths interpreted beneath repository roots.
func TestRepositoryPathsLoadVerbatim(t *testing.T) {
	isolateDirectoryHome(t)
	c, err := Load(writeConfigFile(t, `kernel:
  build: {work_dir: '~/work', output_dir: './build/../packages'}
image:
  release:
    prepare: {out_dir: '~/release'}
userspace:
  build: {output_dir: '~/build'}
  camera:
    release:
      prepare: {output_dir: '~/camera'}
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Kernel.Build.WorkDir != "~/work" || c.Kernel.Build.OutputDir != "./build/../packages" ||
		c.Image.Release.Prepare.OutDir != "~/release" || c.Userspace.Build.OutputDir != "~/build" ||
		c.Userspace.Camera.Release.Prepare.OutputDir != "~/camera" {
		t.Fatalf("repository-relative values were rewritten: %#v", c)
	}
}
