package compatibility

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// Load decodes only the schema's unique canonical byte representation. The
// structural pass bounds work before typed decoding and rejects ambiguity.
func Load(data []byte) (*Manifest, error) {
	if len(data) == 0 || len(data) > MaxBytes || !utf8.Valid(data) {
		return nil, errors.New("manifest is empty, oversized or not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSON(decoder, 0); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("manifest contains trailing JSON")
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, err
	}
	canonical, err := Marshal(&manifest)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(data, canonical) {
		return nil, errors.New("manifest must use canonical JSON bytes with exact field spelling, indentation and final newline")
	}
	return &manifest, nil
}

// Marshal validates before producing the fixed field-order schema spelling.
func Marshal(manifest *Manifest) ([]byte, error) {
	if err := Validate(manifest); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if len(data) > MaxBytes {
		return nil, errors.New("canonical manifest exceeds size bound")
	}
	return data, nil
}

// walkJSON recursively checks one value with a small fixed depth limit. Object
// keys are tracked per object, never confused with string values or siblings.
func walkJSON(decoder *json.Decoder, depth int) error {
	if depth > 12 {
		return errors.New("manifest exceeds maximum nesting depth")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("null is not permitted in compatibility manifests")
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			if len(seen) >= 64 {
				return errors.New("manifest object exceeds field bound")
			}
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or invalid object key %q", name)
			}
			seen[name] = true
			if err := walkJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("unterminated manifest object")
		}
	case '[':
		count := 0
		for decoder.More() {
			count++
			if count > 64 {
				return errors.New("manifest array exceeds 64 entries")
			}
			if err := walkJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("unterminated manifest array")
		}
	default:
		return errors.New("unexpected manifest delimiter")
	}
	return nil
}
