// Package profile identifies supported hardware independently of command flags,
// configuration files and kernel bundle naming conventions.
package profile

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Profile connects a public profile name to the identifiers used by workflows.
type Profile struct {
	// ID is the canonical name saved by lexr init.
	ID string `json:"id"`
	// Name is the human-readable hardware model.
	Name string `json:"name"`
	// Platform is the stable identifier in kernel bundles and boot registries.
	Platform string `json:"platform"`
	// Device is the historical boot diagnostic variant.
	Device string `json:"device"`
	// Compatible is the exact device-tree compatible token for this model.
	Compatible string `json:"compatible"`
	// SoC disambiguates older device trees that use only the shared board token.
	SoC string `json:"soc"`
}

// supported is the reviewed hardware identity registry, not a qualification claim.
var supported = []Profile{
	{ID: "x1e80100-microsoft-denali-oled", Name: "Surface Pro 11 — Snapdragon X Elite (OLED)", Platform: "surface-pro-11-x1e-oled", Device: "x1e-oled", Compatible: "microsoft,denali-oled", SoC: "qcom,x1e80100"},
	{ID: "x1p64100-microsoft-denali", Name: "Surface Pro 11 — Snapdragon X Plus (LCD)", Platform: "surface-pro-11-x1p-lcd", Device: "x1p-lcd", Compatible: "microsoft,denali-lcd", SoC: "qcom,x1p64100"},
}

// List returns a copy of the supported identities in display order.
func List() []Profile { return append([]Profile(nil), supported...) }

// Resolve accepts a canonical name or a stable kernel platform identifier.
// It rejects the short boot-doctor variants: the profile ID is the only
// supported selection mechanism.
func Resolve(value string) (Profile, error) {
	for _, candidate := range supported {
		if value == candidate.ID || value == candidate.Platform {
			return candidate, nil
		}
	}
	return Profile{}, fmt.Errorf("unknown hardware profile %q; run 'lexr profile list' and select an ID with --profile or 'lexr init <profile>'", value)
}

// MatchCompatible requires one exact, unambiguous device-tree compatible token.
// A processor name alone cannot establish the device model.
func MatchCompatible(data []byte) (Profile, error) {
	tokens := make(map[string]bool)
	for _, token := range strings.Split(string(data), "\x00") {
		tokens[token] = true
	}
	var matched Profile
	for _, candidate := range supported {
		boardAndSoC := tokens["microsoft,denali"] && tokens[candidate.SoC]
		legacyLCD := candidate.Device == "x1p-lcd" && tokens["microsoft,denali-x1p"]
		if !tokens[candidate.Compatible] && !boardAndSoC && !legacyLCD {
			continue
		}
		if matched.ID != "" && matched.ID != candidate.ID {
			return Profile{}, fmt.Errorf("device-tree evidence matches more than one hardware profile")
		}
		matched = candidate
	}
	if matched.ID == "" {
		return Profile{}, fmt.Errorf("device-tree evidence does not identify a supported hardware profile")
	}
	return matched, nil
}

// Detect reads bounded device-tree evidence within the selected filesystem root.
// It never infers a model from the build host's processor or an absent setting.
func Detect(root string) (Profile, error) {
	filesystem, err := os.OpenRoot(root)
	if err != nil {
		return Profile{}, fmt.Errorf("open hardware identity root: %w", err)
	}
	defer filesystem.Close()
	file, err := filesystem.Open("sys/firmware/devicetree/base/compatible")
	if err != nil {
		return Profile{}, fmt.Errorf("read hardware identity: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return Profile{}, fmt.Errorf("hardware identity must be a readable regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return Profile{}, fmt.Errorf("read hardware identity: %w", err)
	}
	if len(data) > 4096 {
		return Profile{}, fmt.Errorf("hardware identity exceeds 4096 bytes")
	}
	return MatchCompatible(data)
}
