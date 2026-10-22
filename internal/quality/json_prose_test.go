package quality

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
)

// jsonProse masks object keys, which are schema identifiers rather than prose.
// It preserves every value byte and line break, so wording failures retain
// their original locations. Invalid JSON fails instead of escaping the check.
func jsonProse(content []byte) ([]byte, error) {
	if !json.Valid(content) {
		return nil, fmt.Errorf("invalid JSON")
	}
	prose := bytes.Clone(content)
	for index := 0; index < len(content); index++ {
		if content[index] != '"' {
			continue
		}
		start := index
		index++
		for content[index] != '"' {
			if content[index] == '\\' {
				index++
			}
			index++
		}
		next := index + 1
		for next < len(content) && bytes.ContainsRune([]byte(" \t\r\n"), rune(content[next])) {
			next++
		}
		if next < len(content) && content[next] == ':' {
			for offset := start; offset <= index; offset++ {
				prose[offset] = ' '
			}
		}
	}
	return prose, nil
}

// TestJSONProseKeepsValuesAndLocations checks that only actual object keys are
// masked, including escaped keys and punctuation embedded in string values.
func TestJSONProseKeepsValuesAndLocations(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"schema key", `{"artifact":"libssc.so.2"}`, `{          :"libssc.so.2"}`},
		{"editable value", `{"summary":"behavior"}`, `{         :"behavior"}`},
		{"root string", `"behavior"`, `"behavior"`},
		{"array string", `["behavior",null,true,12]`, `["behavior",null,true,12]`},
		{"nested minified", `{"artifact":{"license":"behavior"},"values":[{"color":"normalization"}]}`, `{          :{         :"behavior"},        :[{       :"normalization"}]}`},
		{"escaped key", `{"arti\"fact":"behavior"}`, `{            :"behavior"}`},
		{"quoted key in value", `{"summary":"before \"artifact\": behavior"}`, `{         :"before \"artifact\": behavior"}`},
		{"escaped slash", `{"summary":"\\ artifact"}`, `{         :"\\ artifact"}`},
		{"key line break", "{\n  \"artifact\"\n  : \"behavior\"\n}", "{\n            \n  : \"behavior\"\n}"},
		{"empty object", `{}`, `{}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := jsonProse([]byte(test.input))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("jsonProse() = %q, want %q", got, test.want)
			}
			if len(got) != len(test.input) || bytes.Count(got, []byte("\n")) != bytes.Count([]byte(test.input), []byte("\n")) {
				t.Fatal("JSON prose changed byte or line coordinates")
			}
		})
	}
}

// TestJSONProseStillRejectsAmericanValueWords keeps the wording gate active
// for prose values even when the same spelling is a legitimate schema key.
func TestJSONProseStillRejectsAmericanValueWords(t *testing.T) {
	for _, input := range []string{`{"artifact":"artifact"}`, `{"summary":"behavior"}`, `{"license":{"notes":"initialize the parser"}}`, `["color"]`} {
		prose, err := jsonProse([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		if americanEnglishPattern.Find(prose) == nil {
			t.Fatalf("wording gate stopped checking JSON value %q", input)
		}
	}
	prose, err := jsonProse([]byte(`{"artifact":"artefact","license":"licence","notes":"initialise the parser"}`))
	if err != nil {
		t.Fatal(err)
	}
	if americanEnglishPattern.Find(prose) != nil {
		t.Fatalf("schema identifiers were treated as prose: %s", prose)
	}
}

// TestJSONProseRejectsMalformedDocuments ensures truncated or invalid input
// cannot bypass the wording check or reach unchecked string scanning.
func TestJSONProseRejectsMalformedDocuments(t *testing.T) {
	for _, input := range []string{"", "{", `{"artifact":`, `{"artifact" "behavior"}`, `{"artifact":"unterminated}`, `{"summary":"bad\xescape"}`, "{\"summary\":\"raw\nnewline\"}"} {
		if _, err := jsonProse([]byte(input)); err == nil {
			t.Fatalf("invalid JSON was accepted: %q", input)
		}
	}
}
