package build

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	sourceOwnerLabel       = "io.lexr.sensors.source"
	sourceOwnerValue       = "source-build-v1"
	sourceTokenLabel       = "io.lexr.sensors.token"
	sourceInitRecipeLabel  = "io.lexr.sensors.init-recipe"
	sourceRecipeLabel      = "io.lexr.sensors.source-recipe"
	sourceVolumePrefix     = "lexr-sensors-work-"
	maximumVolumeJSONBytes = 256 << 10
)

// Mountpoint is daemon bookkeeping only. It must never authorize a host read,
// write, bind mount, cleanup or other filesystem operation.
type volumeInspection struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Scope      string            `json:"Scope"`
	Mountpoint string            `json:"Mountpoint"`
	Labels     map[string]string `json:"Labels"`
	Options    map[string]string `json:"Options"`
}

func validSourceVolumeIdentity(name, token string) bool {
	return len(token) == 24 && strings.Trim(token, "0123456789abcdef") == "" && name == sourceVolumePrefix+token
}

// A nil result is a refusal, never an empty/default Docker operation. The
// supervisor must check it before invoking its Runner.
func volumeCreateArgs(name, token string) []string {
	if !validSourceVolumeIdentity(name, token) {
		return nil
	}
	return []string{"volume", "create", "--driver", "local",
		"--label", sourceOwnerLabel + "=" + sourceOwnerValue,
		"--label", sourceTokenLabel + "=" + token,
		"--label", sourceInitRecipeLabel + "=" + InitRecipeSHA256(),
		"--label", sourceRecipeLabel + "=" + SourceRecipeSHA256(), name}
}

func validateOwnedVolume(v volumeInspection, name, token string) error {
	if !validSourceVolumeIdentity(name, token) || v.Name != name {
		return errors.New("source work volume identity differs")
	}
	// A local driver with driver options can represent a host bind or remote
	// filesystem. Neither the driver name nor labels alone establish this policy.
	if v.Driver != "local" || v.Scope != "local" || len(v.Options) != 0 {
		return errors.New("source work volume must be local with no driver options")
	}
	if len(v.Labels) != 4 || v.Labels[sourceOwnerLabel] != sourceOwnerValue || v.Labels[sourceTokenLabel] != token || v.Labels[sourceInitRecipeLabel] != InitRecipeSHA256() || v.Labels[sourceRecipeLabel] != SourceRecipeSHA256() {
		return errors.New("source work volume ownership or recipe labels differ")
	}
	return nil
}

// decodeVolumeInspection accepts Docker volume inspect's one-object array.
// Irrelevant Docker metadata may evolve, but authority fields must be present,
// canonically named and correctly typed. Duplicate/trailing JSON is refused by
// the existing shared decodeJSON/jsonstrict boundary before typed decoding.
func decodeVolumeInspection(data []byte) (volumeInspection, error) {
	var zero volumeInspection
	if len(data) == 0 || len(data) > maximumVolumeJSONBytes {
		return zero, errors.New("source volume inspection exceeds its fixed JSON bound")
	}
	var entries []json.RawMessage
	if err := decodeJSON(data, &entries, false); err != nil {
		return zero, err
	}
	if len(entries) != 1 {
		return zero, errors.New("source volume inspection must contain exactly one object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(entries[0], &fields); err != nil || fields == nil {
		return zero, errors.New("source volume inspection entry must be an object")
	}
	wanted := []string{"Name", "Driver", "Scope", "Mountpoint", "Labels", "Options"}
	for _, expected := range wanted {
		if _, present := fields[expected]; !present {
			return zero, fmt.Errorf("source volume inspection is missing %s", expected)
		}
		for observed := range fields {
			if observed != expected && strings.EqualFold(observed, expected) {
				return zero, errors.New("source volume inspection has an ambiguous field name")
			}
		}
	}
	for _, name := range wanted[:4] {
		if err := volumeJSONString(fields[name]); err != nil {
			return zero, fmt.Errorf("source volume inspection %s: %w", name, err)
		}
	}
	for _, name := range wanted[4:] {
		raw := bytes.TrimSpace(fields[name])
		if name == "Options" && bytes.Equal(raw, []byte("null")) {
			continue // Docker represents no driver options as either null or {}.
		}
		var values map[string]json.RawMessage
		if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &values) != nil || values == nil {
			return zero, fmt.Errorf("source volume inspection %s must be a string map", name)
		}
		for _, value := range values {
			if err := volumeJSONString(value); err != nil {
				return zero, fmt.Errorf("source volume inspection %s: %w", name, err)
			}
		}
	}
	var result volumeInspection
	if err := json.Unmarshal(entries[0], &result); err != nil {
		return zero, err
	}
	return result, nil
}

func volumeJSONString(raw json.RawMessage) error {
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return errors.New("expected a non-null JSON string")
	}
	return nil
}
