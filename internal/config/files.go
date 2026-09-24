package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// ResolvePaths uses the environment/default only when no explicit set is given.
// A non-nil empty slice represents an explicitly empty --config flag.
func ResolvePaths(paths []string) ([]string, error) {
	if paths == nil {
		path, err := ResolvePath("")
		if err != nil {
			return nil, err
		}
		paths = []string{path}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("configuration path %q is empty", "")
	}
	resolved := make([]string, len(paths))
	for i, path := range paths {
		if strings.TrimSpace(path) == "" {
			return nil, fmt.Errorf("configuration path %q at position %d is empty", path, i+1)
		}
		expanded, err := expandUserHome(path)
		if err == nil {
			expanded, err = filepath.Abs(expanded)
		}
		if err != nil {
			return nil, fmt.Errorf("resolve configuration %q: %w", path, err)
		}
		resolved[i] = expanded
	}
	return resolved, nil
}

// LoadFiles requires every file, validates each before merging, and expands home
// paths after merging. Maps merge recursively; scalars, lists and null replace.
func LoadFiles(paths []string) (Config, error) {
	configuration, _, err := loadFiles(paths)
	return configuration, err
}

// LoadValues returns the typed settings and only the keys explicitly configured.
// Presence is retained so false, zero, empty strings and empty lists can override
// command defaults. Null means unset. Paths use the same expansion as Config.
func LoadValues(paths []string) (Config, map[string]any, error) {
	configuration, values, err := loadFiles(paths)
	if err == nil {
		projectValues(values, reflect.ValueOf(configuration))
	}
	return configuration, values, err
}

// WriteMerged emits only configured keys, with effective home-expanded values.
// Map encoding has no aliases/anchors; comments belong to the source files.
func WriteMerged(writer io.Writer, paths []string) error {
	configuration, values, err := loadFiles(paths)
	if err != nil {
		return err
	}
	projectValues(values, reflect.ValueOf(configuration))
	encoder := yaml.NewEncoder(writer)
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(values)
}

// loadFiles collects per-file errors and returns typed and presence-aware values.
func loadFiles(paths []string) (Config, map[string]any, error) {
	if len(paths) == 0 {
		return Config{}, nil, fmt.Errorf("configuration file set is empty")
	}
	resolved, err := ResolvePaths(paths)
	if err != nil {
		return Config{}, nil, err
	}
	merged := make(map[string]any)
	var problems []error
	var version any
	var versionPath string
	for _, path := range resolved {
		data, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Errorf("read configuration %q: %w", path, err))
			continue
		}
		if _, err := decodeStrict(data); err != nil {
			problems = append(problems, fmt.Errorf("configuration %q: %w", path, err))
			continue
		}
		var values map[string]any
		if err := yaml.Unmarshal(data, &values); err != nil {
			problems = append(problems, fmt.Errorf("configuration %q: %w", path, err))
			continue
		}
		if next, present := values["version"]; present {
			if versionPath != "" && !reflect.DeepEqual(version, next) {
				problems = append(problems, fmt.Errorf("configuration %q: version %v conflicts with version %v in %q", path, next, version, versionPath))
			}
			if versionPath == "" {
				version, versionPath = next, path
			}
			if next != 1 {
				problems = append(problems, fmt.Errorf("configuration %q: unsupported version %v; expected integer 1", path, next))
			}
		}
		mergeValues(merged, values)
	}
	if err := errors.Join(problems...); err != nil {
		return Config{}, nil, err
	}
	data, err := yaml.Marshal(merged)
	if err != nil {
		return Config{}, nil, err
	}
	configuration, err := decodeStrict(data)
	if err == nil {
		err = expandConfigurationHome(reflect.ValueOf(&configuration).Elem())
	}
	if err != nil {
		return Config{}, nil, fmt.Errorf("merged configuration from %q: %w", resolved, err)
	}
	return configuration, merged, nil
}

// decodeStrict validates every original document before an override can hide an
// unknown key, bad type, duplicate key, or additional YAML document.
func decodeStrict(data []byte) (Config, error) {
	var configuration Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&configuration); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return Config{}, fmt.Errorf("decode configuration: %w", err)
		}
		return Config{}, fmt.Errorf("configuration must contain a single YAML document")
	}
	return configuration, nil
}

// mergeValues recursively overlays maps, replacing all other present values.
func mergeValues(base, overlay map[string]any) {
	for key, value := range overlay {
		previousMap, previousIsMap := base[key].(map[string]any)
		nextMap, nextIsMap := value.(map[string]any)
		if previousIsMap && nextIsMap {
			mergeValues(previousMap, nextMap)
		} else {
			base[key] = value
		}
	}
}

// projectValues keeps the merged document's key presence (including nulls and
// explicit zeroes), while reflecting the same path expansion used by commands.
func projectValues(values map[string]any, configuration reflect.Value) {
	for i := 0; i < configuration.NumField(); i++ {
		key := configuration.Type().Field(i).Tag.Get("yaml")
		value, present := values[key]
		if !present || value == nil {
			continue
		}
		field := configuration.Field(i)
		if nested, ok := value.(map[string]any); ok && field.Kind() == reflect.Struct {
			projectValues(nested, field)
		} else {
			values[key] = field.Interface()
		}
	}
}
