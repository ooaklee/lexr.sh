package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

// Template documents every top-level namespace without setting command defaults.
const Template = `# Lexr configuration. Validate with: lexr config check
# Configuration schema version (currently 1).
version: 1
# Hardware profile; set with: lexr init <profile>
# profile: x1e80100-microsoft-denali-oled
# Shared catalogue paths.
# global: {}
# Catalogue command settings.
# catalog: {}
# Reversible cleanup settings.
# clean: {}
# Diagnostic settings and workspace.
# doctor: {}
# Windows evidence handoff settings.
# handoff: {}
# Image creation, validation and release settings.
# image: {}
# Kernel build, installation, boot and release settings.
# kernel: {}
# Userspace build, cache, installation and component settings.
# userspace: {}
# Interactive wizard paths.
# wizard: {}
`

// EnsureTemplate creates an absent file without overwriting an existing file,
// even when it is invalid and needs to be opened for repair.
func EnsureTemplate(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create configuration directory for %q: %w", path, err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create configuration %q: %w", path, err)
	}
	_, writeErr := file.WriteString(Template)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write configuration %q: %w", path, writeErr)
	}
	return closeErr
}

// SetProfile preserves the original YAML nodes, including comments and unrelated
// values. Force only permits replacing a different non-empty profile.
func SetProfile(path, profile string, force bool) error {
	if strings.TrimSpace(profile) == "" {
		return fmt.Errorf("profile must not be empty")
	}
	resolved, err := ResolvePaths([]string{path})
	if err != nil {
		return err
	}
	path = resolved[0]
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read configuration %q: %w", path, err)
	}
	mode := os.FileMode(0o600)
	if err == nil {
		configuration, err := LoadFiles([]string{path})
		if err != nil {
			return err
		}
		if configuration.Profile != "" && configuration.Profile != profile && !force {
			return fmt.Errorf("configuration %q already has profile %q; use --force to replace it with %q", path, configuration.Profile, profile)
		}
	}
	// Update the target of a symlink while keeping the symlink itself intact.
	// Chained and relative links are followed lexically without cleaning ..
	// segments: the kernel resolves symlink directories and .. physically when
	// the replacement file is created and renamed, which mirrors readlink(2)
	// semantics for relative targets. A dangling link resolves to a missing
	// target, not the link's replacement.
	for depth := 0; ; depth++ {
		rawTarget, linkErr := os.Readlink(path)
		if linkErr != nil {
			if errors.Is(linkErr, syscall.EINVAL) || os.IsNotExist(linkErr) {
				break
			}
			return linkErr
		}
		if depth >= 32 {
			return fmt.Errorf("resolve configuration %q: too many levels of symbolic links", path)
		}
		if !filepath.IsAbs(rawTarget) {
			rawTarget = uncleanedDir(path) + "/" + rawTarget
		}
		path = rawTarget
	}
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("decode configuration %q: %w", path, err)
	}
	if len(document.Content) == 0 {
		// Keep comments from an otherwise empty document.
		if err := yaml.Unmarshal(append(data, []byte("\nversion: 1\n")...), &document); err != nil {
			return err
		}
	}
	mapping := document.Content[0]
	if mapping.Kind == yaml.ScalarNode && mapping.Tag == "!!null" {
		mapping.Kind, mapping.Tag, mapping.Value = yaml.MappingNode, "!!map", ""
	}
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf("configuration %q must contain a YAML mapping", path)
	}
	if mappingValue(mapping, "version") == nil {
		mapping.Content = append(mapping.Content, scalar("version"), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
	}
	if value := mappingValue(mapping, "profile"); value != nil {
		// An alias elsewhere may refer to the old profile. Materialise those
		// references before changing it so unrelated values remain unchanged.
		preserveAliases(&document, value)
		value.Kind, value.Tag, value.Value = yaml.ScalarNode, "!!str", profile
		value.Alias, value.Content, value.Anchor = nil, nil, ""
	} else {
		mapping.Content = append(mapping.Content, scalar("profile"), scalar(profile))
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return replaceFile(path, output.Bytes(), mode)
}

// scalar creates a string node, letting yaml.v3 quote ambiguous values safely.
func scalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// uncleanedDir returns the directory prefix of a possibly unclean path without
// resolving .. segments, so the kernel can honour symlink components between
// the directory and the file name.
func uncleanedDir(path string) string {
	if separator := strings.LastIndexByte(path, '/'); separator >= 0 {
		if separator == 0 {
			return "/"
		}
		return path[:separator]
	}
	return "."
}

// mappingValue finds an explicitly present top-level setting without decoding.
func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// preserveAliases keeps references to an edited scalar's previous value intact.
func preserveAliases(node, target *yaml.Node) {
	if node.Kind == yaml.AliasNode && node.Alias == target {
		head, line, foot := node.HeadComment, node.LineComment, node.FootComment
		*node = *target
		node.Anchor = ""
		node.HeadComment, node.LineComment, node.FootComment = head, line, foot
		return
	}
	for _, child := range node.Content {
		preserveAliases(child, target)
	}
}

// replaceFile stages complete YAML beside its destination before replacing it.
// The uncleaned directory keeps staging on the destination's filesystem even
// when .. or symlink components precede the file name.
func replaceFile(path string, data []byte, mode os.FileMode) error {
	directory := uncleanedDir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create configuration directory for %q: %w", path, err)
	}
	file, err := os.CreateTemp(directory, ".lexr-config-*")
	if err != nil {
		return fmt.Errorf("write configuration %q: %w", path, err)
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(data)
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("write configuration %q: %w", path, err)
	}
	return nil
}
