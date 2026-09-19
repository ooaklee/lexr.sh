package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// errConfigHomeUnavailable distinguishes an unavailable OS location from a
// filesystem failure, which must not silently redirect writes elsewhere.
var errConfigHomeUnavailable = errors.New("user configuration directory unavailable")

// configHomePath resolves the standard home without creating it during Load.
func configHomePath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("%w: %w", errConfigHomeUnavailable, err)
	}
	base, err = expandUserHome(base)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "lexr"), nil
}

// ConfigHome creates and returns Lexr's home under os.UserConfigDir().
// Selecting a different YAML file does not relocate this home.
func ConfigHome() (string, error) {
	home, err := configHomePath()
	if err != nil {
		return "", err
	}
	return createDefaultDirectory(home)
}

// DefaultCacheDir creates the cache for one lowercase command domain on demand.
func DefaultCacheDir(domain string) (string, error) {
	return defaultDomainDirectory("caches", domain)
}

// DefaultBuildDir creates the workspace for one lowercase command domain on demand.
func DefaultBuildDir(domain string) (string, error) {
	return defaultDomainDirectory("builds", domain)
}

// defaultDomainDirectory confines domain names to a single lowercase component.
func defaultDomainDirectory(kind, domain string) (string, error) {
	if domain == "" || strings.Trim(domain, "abcdefghijklmnopqrstuvwxyz") != "" {
		return "", fmt.Errorf("invalid directory domain %q: use lowercase letters", domain)
	}
	home, err := ConfigHome()
	if err != nil {
		return "", err
	}
	return createDefaultDirectory(filepath.Join(home, kind, domain))
}

// createDefaultDirectory creates only an automatically selected directory.
func createDefaultDirectory(path string) (string, error) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("create Lexr directory %q: %w", path, err)
	}
	return path, nil
}

// ResolveCacheDir selects userspace.pull.cache_dir or caches/userspace.
func (c Config) ResolveCacheDir() (string, error) {
	return resolveHostDirectory(c.Userspace.Pull.CacheDir, "caches", "userspace", "")
}

// ResolveUserspaceDir follows the pull cache so installation finds downloaded bundles.
func (c Config) ResolveUserspaceDir() (string, error) {
	return c.ResolveCacheDir()
}

// ResolveImageCacheDir selects image.create.cache_dir or caches/image.
func (c Config) ResolveImageCacheDir() (string, error) {
	return resolveHostDirectory(c.Image.Create.CacheDir, "caches", "image", "")
}

// ResolveImageWorkspaceDir selects image.create.workspace_dir or builds/image.
func (c Config) ResolveImageWorkspaceDir() (string, error) {
	return resolveHostDirectory(c.Image.Create.WorkspaceDir, "builds", "image", "")
}

// ResolveDoctorWorkspace selects doctor.workspace or builds/doctor.
func (c Config) ResolveDoctorWorkspace() (string, error) {
	return resolveHostDirectory(c.Doctor.Workspace, "builds", "doctor", ".")
}

// ResolveKernelDownloadDir selects kernel.release.download.output_dir or caches/kernel.
func (c Config) ResolveKernelDownloadDir() (string, error) {
	return resolveHostDirectory(c.Kernel.Release.Download.OutputDir, "caches", "kernel", "kernel-bundle")
}

// ResolveWizardCacheDir selects wizard.cache_dir or caches/wizard.
func (c Config) ResolveWizardCacheDir() (string, error) {
	return resolveHostDirectory(c.Wizard.CacheDir, "caches", "wizard", "")
}

// resolveHostDirectory preserves overrides and creates defaults lazily. Legacy
// defaults apply only when the OS cannot supply a config home; creation errors
// remain errors. Empty legacy workspaces retain the manager's temporary directory.
func resolveHostDirectory(configured, kind, domain, legacy string) (string, error) {
	if configured != "" {
		return expandUserHome(configured)
	}
	directory, err := defaultDomainDirectory(kind, domain)
	if !errors.Is(err, errConfigHomeUnavailable) {
		return directory, err
	}
	if kind == "caches" && domain != "kernel" {
		base, cacheErr := os.UserCacheDir()
		if cacheErr != nil {
			return "", errors.Join(err, cacheErr)
		}
		if domain == "userspace" {
			return filepath.Join(base, "lexr", "userspace"), nil
		}
		return filepath.Join(base, "lexr"), nil
	}
	return legacy, nil
}
