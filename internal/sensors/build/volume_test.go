package build

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const volumeTestToken = "0123456789abcdef01234567"
const volumeTestName = "lexr-sensors-work-0123456789abcdef01234567"

func volumeTestInspection() volumeInspection {
	return volumeInspection{Name: volumeTestName, Driver: "local", Scope: "local", Mountpoint: "daemon-bookkeeping-not-a-host-access-grant", Labels: map[string]string{
		"io.lexr.sensors.source":        "source-build-v1",
		"io.lexr.sensors.token":         volumeTestToken,
		"io.lexr.sensors.init-recipe":   InitRecipeSHA256(),
		"io.lexr.sensors.source-recipe": SourceRecipeSHA256(),
	}}
}

func TestVolumeCreateArgumentsAreFixedAndIdentityBound(t *testing.T) {
	wanted := []string{"volume", "create", "--driver", "local", "--label", "io.lexr.sensors.source=source-build-v1", "--label", "io.lexr.sensors.token=" + volumeTestToken, "--label", "io.lexr.sensors.init-recipe=" + InitRecipeSHA256(), "--label", "io.lexr.sensors.source-recipe=" + SourceRecipeSHA256(), volumeTestName}
	if got := volumeCreateArgs(volumeTestName, volumeTestToken); !reflect.DeepEqual(got, wanted) {
		t.Fatalf("volume creation grammar differs: %#v", got)
	}
	// Returned argument slices must not retain caller mutations between calls.
	first := volumeCreateArgs(volumeTestName, volumeTestToken)
	first[0] = "rm"
	if !reflect.DeepEqual(volumeCreateArgs(volumeTestName, volumeTestToken), wanted) {
		t.Fatal("volume creation arguments are mutable shared policy")
	}
	for _, input := range [][2]string{{"", ""}, {volumeTestName, ""}, {volumeTestName, strings.Repeat("A", 24)}, {volumeTestName, strings.Repeat("a", 23)}, {volumeTestName, strings.Repeat("a", 25)}, {volumeTestName, volumeTestToken[:23] + "/"}, {"--driver=remote", volumeTestToken}, {volumeTestName + "/child", volumeTestToken}, {volumeTestName + "\n", volumeTestToken}, {"lexr-sensors-work-" + strings.Repeat("b", 24), volumeTestToken}} {
		if got := volumeCreateArgs(input[0], input[1]); got != nil {
			t.Fatalf("invalid identity produced runnable arguments: %#v", got)
		}
		if err := validateOwnedVolume(volumeTestInspection(), input[0], input[1]); err == nil {
			t.Fatal("invalid requested identity accepted")
		}
	}
}

func TestOwnedVolumeRequiresExactFreshLocalPolicy(t *testing.T) {
	for _, options := range []map[string]string{nil, {}} {
		v := volumeTestInspection()
		v.Options = options
		if err := validateOwnedVolume(v, volumeTestName, volumeTestToken); err != nil {
			t.Fatalf("empty driver options refused: %v", err)
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*volumeInspection)
	}{
		{"name", func(v *volumeInspection) { v.Name += "x" }},
		{"remote-driver", func(v *volumeInspection) { v.Driver = "nfs" }},
		{"missing-driver", func(v *volumeInspection) { v.Driver = "" }},
		{"driver-case", func(v *volumeInspection) { v.Driver = "LOCAL" }},
		{"scope", func(v *volumeInspection) { v.Scope = "global" }},
		{"missing-scope", func(v *volumeInspection) { v.Scope = "" }},
		{"owner", func(v *volumeInspection) { v.Labels[sourceOwnerLabel] = "other" }},
		{"token", func(v *volumeInspection) { v.Labels[sourceTokenLabel] = strings.Repeat("b", 24) }},
		{"init-recipe", func(v *volumeInspection) { v.Labels[sourceInitRecipeLabel] = strings.Repeat("0", 64) }},
		{"source-recipe", func(v *volumeInspection) { v.Labels[sourceRecipeLabel] = strings.Repeat("0", 64) }},
		{"missing-label", func(v *volumeInspection) { delete(v.Labels, sourceOwnerLabel) }},
		{"nil-labels", func(v *volumeInspection) { v.Labels = nil }},
		{"extra-label", func(v *volumeInspection) { v.Labels["unreviewed"] = "extra" }},
		{"bind-device", func(v *volumeInspection) { v.Options = map[string]string{"device": "/host/path"} }},
		{"bind-options", func(v *volumeInspection) { v.Options = map[string]string{"o": "bind"} }},
		{"nfs-type", func(v *volumeInspection) { v.Options = map[string]string{"type": "nfs"} }},
		{"empty-valued-option", func(v *volumeInspection) { v.Options = map[string]string{"device": ""} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := volumeTestInspection()
			test.mutate(&v)
			if err := validateOwnedVolume(v, volumeTestName, volumeTestToken); err == nil {
				t.Fatal("non-owned or configured volume accepted")
			}
		})
	}
	// No host filesystem access is authorized or needed by this pure validator.
	for _, mountpoint := range []string{"", "/path/that/does/not/exist", "../not-a-host-grant"} {
		v := volumeTestInspection()
		v.Mountpoint = mountpoint
		if err := validateOwnedVolume(v, volumeTestName, volumeTestToken); err != nil {
			t.Fatal("daemon bookkeeping was treated as a host-path authority")
		}
	}
}

func volumeTestJSON(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal([]volumeInspection{volumeTestInspection()})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestVolumeInspectionDecodesOneTypedObject(t *testing.T) {
	for _, options := range []string{"null", "{}"} {
		raw := strings.Replace(volumeTestJSON(t), `"Options":null`, `"Options":`+options, 1)
		v, err := decodeVolumeInspection([]byte(raw))
		if err != nil || validateOwnedVolume(v, volumeTestName, volumeTestToken) != nil {
			t.Fatalf("valid volume inspect representation refused: %+v, %v", v, err)
		}
	}
	// Docker's irrelevant metadata can evolve without becoming authority.
	raw := strings.Replace(volumeTestJSON(t), `[{`, `[{"CreatedAt":"future bookkeeping","Status":{"opaque":true},`, 1)
	if _, err := decodeVolumeInspection([]byte(raw)); err != nil {
		t.Fatalf("irrelevant Docker metadata became a closed schema: %v", err)
	}
}

func TestVolumeInspectionRefusesAmbiguousJSONAndWrongTypes(t *testing.T) {
	valid := volumeTestJSON(t)
	cases := map[string]string{
		"empty":             "",
		"empty-array":       "[]",
		"null":              "null",
		"null-entry":        "[null]",
		"array-entry":       "[[]]",
		"object-root":       strings.TrimSuffix(strings.TrimPrefix(valid, "["), "]"),
		"two-entries":       "[" + valid[1:len(valid)-1] + "," + valid[1:len(valid)-1] + "]",
		"trailing-object":   valid + "{}",
		"trailing-garbage":  valid + "garbage",
		"duplicate-name":    strings.Replace(valid, `[{`, `[{"Name":"first",`, 1),
		"case-alias":        strings.Replace(valid, `[{`, `[{"name":"ambiguous",`, 1),
		"duplicate-label":   strings.Replace(valid, `"Labels":{`, `"Labels":{"io.lexr.sensors.source":"first",`, 1),
		"null-labels":       strings.Replace(valid, `"Labels":{`, `"Labels":null,"discarded":{`, 1),
		"array-labels":      strings.Replace(valid, `"Labels":{`, `"Labels":[],"discarded":{`, 1),
		"null-name":         strings.Replace(valid, `"Name":"`+volumeTestName+`"`, `"Name":null`, 1),
		"numeric-name":      strings.Replace(valid, `"Name":"`+volumeTestName+`"`, `"Name":42`, 1),
		"null-mountpoint":   strings.Replace(valid, `"Mountpoint":"daemon-bookkeeping-not-a-host-access-grant"`, `"Mountpoint":null`, 1),
		"numeric-label":     strings.Replace(valid, `"io.lexr.sensors.source":"source-build-v1"`, `"io.lexr.sensors.source":42`, 1),
		"null-label-value":  strings.Replace(valid, `"io.lexr.sensors.source":"source-build-v1"`, `"io.lexr.sensors.source":null`, 1),
		"string-options":    strings.Replace(valid, `"Options":null`, `"Options":"bind"`, 1),
		"array-options":     strings.Replace(valid, `"Options":null`, `"Options":[]`, 1),
		"numeric-option":    strings.Replace(valid, `"Options":null`, `"Options":{"o":42}`, 1),
		"null-option-value": strings.Replace(valid, `"Options":null`, `"Options":{"o":null}`, 1),
		"missing-options":   strings.Replace(valid, `,"Options":null`, "", 1),
		"invalid-utf8":      valid[:len(valid)-1] + string([]byte{0xff}) + "]",
		"oversized":         strings.Repeat(" ", maximumVolumeJSONBytes+1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeVolumeInspection([]byte(raw)); err == nil {
				t.Fatal("malformed or ambiguous Docker volume inspection accepted")
			}
		})
	}
	for _, name := range []string{"Name", "Driver", "Scope", "Mountpoint", "Labels", "Options"} {
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal([]byte(valid), &entries); err != nil {
			t.Fatal(err)
		}
		delete(entries[0], name)
		raw, err := json.Marshal(entries)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeVolumeInspection(raw); err == nil {
			t.Fatalf("missing required field %s accepted", name)
		}
	}
}
