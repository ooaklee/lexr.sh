// Package config loads the small, userspace-owned Lexr configuration file.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config contains the optional directories used by userspace bundle workflows.
type Config struct {
	// UserspaceDir is the cache root searched when userspace install omits --from.
	UserspaceDir string
	// CacheDir is the cache root used when userspace pull omits --cache-dir.
	CacheDir string
}

// ResolvePath selects an explicit path, the LEXR_CONFIG override, or the
// operating system's standard Lexr configuration path, in that order.
func ResolvePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = os.Getenv("LEXR_CONFIG")
	}
	if strings.TrimSpace(path) != "" {
		return expandUserHome(path)
	}
	userConfig, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user configuration directory: %w", err)
	}
	return filepath.Join(userConfig, "lexr", "lexr.yml"), nil
}

// Load reads a strict flat Lexr configuration file. A missing file produces an
// empty configuration whose directory resolvers supply standard defaults.
func Load(path string) (Config, error) {
	resolvedPath, err := ResolvePath(path)
	if err != nil {
		return Config{}, err
	}
	file, err := os.Open(resolvedPath)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("open configuration: %w", err)
	}
	defer file.Close()

	configuration := Config{}
	seen := make(map[string]struct{}, 2)
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Count(line, ":") != 1 {
			return Config{}, fmt.Errorf("line %d: expected exactly one key: value pair", lineNumber)
		}
		parts := strings.SplitN(line, ":", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			return Config{}, fmt.Errorf("line %d: configuration key is empty", lineNumber)
		}
		if value == "" {
			return Config{}, fmt.Errorf("line %d: configuration key %q requires a value", lineNumber, key)
		}
		if _, ok := seen[key]; ok {
			return Config{}, fmt.Errorf("line %d: duplicate configuration key %q", lineNumber, key)
		}
		seen[key] = struct{}{}
		value, err = expandUserHome(value)
		if err != nil {
			return Config{}, fmt.Errorf("line %d: expand configuration key %q: %w", lineNumber, key, err)
		}
		switch key {
		case "userspace_dir":
			configuration.UserspaceDir = value
		case "cache_dir":
			configuration.CacheDir = value
		default:
			return Config{}, fmt.Errorf("line %d: unknown configuration key %q", lineNumber, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	return configuration, nil
}

// ResolveUserspaceDir returns the configured userspace bundle root or the
// standard operating-system user cache location.
func (c Config) ResolveUserspaceDir() (string, error) {
	return resolveDirectory(c.UserspaceDir)
}

// ResolveCacheDir returns the configured pull cache root or the standard
// operating-system user cache location.
func (c Config) ResolveCacheDir() (string, error) {
	return resolveDirectory(c.CacheDir)
}

// resolveDirectory expands an explicitly configured home-relative directory or
// supplies the shared default userspace cache root.
func resolveDirectory(directory string) (string, error) {
	if strings.TrimSpace(directory) != "" {
		return expandUserHome(directory)
	}
	userCache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache directory: %w", err)
	}
	return filepath.Join(userCache, "lexr", "userspace"), nil
}

// expandUserHome expands the supported leading ~/ form without interpreting
// shell variables or other shell syntax.
func expandUserHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}
