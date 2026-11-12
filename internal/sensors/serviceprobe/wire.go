package serviceprobe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"unicode"
	"unicode/utf8"
)

// wireReplyLimit bounds the complete busctl JSON reply before decoding.
const wireReplyLimit = 64 << 10

// decodeString accepts one bounded D-Bus string result, not an identity claim.
func decodeString(data []byte) (string, error) {
	raw, err := decodeEnvelope(data, "s")
	if err != nil {
		return "", err
	}
	return wireText(raw, 255, true)
}

// decodeBool accepts one genuine JSON boolean for a D-Bus boolean result.
func decodeBool(data []byte) (bool, error) {
	raw, err := decodeEnvelope(data, "b")
	if err != nil {
		return false, err
	}
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("D-Bus boolean result is not a boolean")
	}
}

// decodePID accepts a positive integer in the Linux signed PID range only.
func decodePID(data []byte) (uint32, error) {
	raw, err := decodeEnvelope(data, "u")
	if err != nil {
		return 0, err
	}
	n, err := wireUint32(raw)
	if err != nil || n == 0 || n > math.MaxInt32 {
		return 0, errors.New("D-Bus process ID is outside the positive PID range")
	}
	return n, nil
}

// decodeUnits decodes only the two requested loaded-unit rows. An omitted row
// means no row was returned, not that the corresponding unit is uninstalled.
func decodeUnits(data []byte) ([]LoadedUnit, error) {
	raw, err := decodeEnvelope(data, "a(ssssssouso)")
	if err != nil {
		return nil, err
	}
	rows, err := wireArray(raw)
	if err != nil || len(rows) > 2 {
		return nil, errors.New("D-Bus unit result must contain at most two rows")
	}
	result := make([]LoadedUnit, 0, len(rows))
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		unit, err := decodeUnit(row)
		if err != nil {
			return nil, err
		}
		if seen[unit.Name] {
			return nil, errors.New("D-Bus unit result repeats a unit")
		}
		seen[unit.Name] = true
		result = append(result, unit)
	}
	return result, nil
}

// decodeEnvelope enforces busctl's exact type/data object and one result item.
// The v259 sd-bus JSON encoder represents arrays and structs as nested arrays.
func decodeEnvelope(data []byte, signature string) (json.RawMessage, error) {
	if len(data) == 0 || len(data) > wireReplyLimit || !utf8.Valid(data) {
		return nil, errors.New("D-Bus JSON reply is empty, oversized or invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("D-Bus JSON reply is not an object")
	}
	fields := make(map[string]json.RawMessage, 2)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, errors.New("D-Bus JSON reply has an invalid field")
		}
		name, ok := key.(string)
		if !ok || (name != "type" && name != "data") || fields[name] != nil {
			return nil, errors.New("D-Bus JSON reply has an unknown or repeated field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("D-Bus JSON reply has an invalid value")
		}
		fields[name] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("D-Bus JSON reply has an invalid object ending")
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, errors.New("D-Bus JSON reply contains trailing data")
	}
	if len(fields) != 2 {
		return nil, errors.New("D-Bus JSON reply is missing a field")
	}
	kind, err := wireText(fields["type"], 32, false)
	if err != nil || kind != signature {
		return nil, errors.New("D-Bus JSON reply has the wrong signature")
	}
	items, err := wireArray(fields["data"])
	if err != nil || len(items) != 1 {
		return nil, errors.New("D-Bus JSON reply must have exactly one result")
	}
	return items[0], nil
}

// wireArray rejects null and non-array values before decoding bounded children.
func wireArray(raw json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, errors.New("D-Bus JSON value is not an array")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, errors.New("D-Bus JSON array is invalid")
	}
	return items, nil
}

// wireText requires a bounded string and rejects control characters rather than
// allowing diagnostic output to carry terminal or record separators.
func wireText(raw json.RawMessage, maximum int, allowEmpty bool) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' || !wireUnicode(trimmed) {
		return "", errors.New("D-Bus JSON value is not a string")
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil || len(value) > maximum || (!allowEmpty && value == "") {
		return "", errors.New("D-Bus JSON string is invalid or outside its bound")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", errors.New("D-Bus JSON string contains a control character")
		}
	}
	return value, nil
}

// wireUnicode refuses unpaired escaped UTF-16 surrogates, which encoding/json
// would otherwise silently replace rather than report as malformed input.
func wireUnicode(raw []byte) bool {
	for i := 1; i+1 < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		if i+5 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil || (n >= 0xdc00 && n <= 0xdfff) {
			return false
		}
		i += 4
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+7 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

// wireUint32 requires decimal integer syntax without signs, exponents or floats.
func wireUint32(raw json.RawMessage) (uint32, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || (len(value) > 1 && value[0] == '0') {
		return 0, errors.New("D-Bus JSON value is not a canonical unsigned integer")
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, errors.New("D-Bus JSON value is not an unsigned integer")
		}
	}
	n, err := strconv.ParseUint(string(value), 10, 32)
	if err != nil {
		return 0, errors.New("D-Bus JSON integer exceeds uint32")
	}
	return uint32(n), nil
}

// decodeUnit validates the shape and exact object paths of one requested row.
func decodeUnit(raw json.RawMessage) (LoadedUnit, error) {
	items, err := wireArray(raw)
	if err != nil || len(items) != 10 {
		return LoadedUnit{}, errors.New("D-Bus unit row must have exactly ten fields")
	}
	var unit LoadedUnit
	strings := []struct {
		index   int
		maximum int
		empty   bool
		target  *string
	}{
		{0, 255, false, &unit.Name}, {1, 4096, true, &unit.Description},
		{2, 64, false, &unit.LoadState}, {3, 64, false, &unit.ActiveState},
		{4, 64, false, &unit.SubState}, {5, 255, true, &unit.Following},
		{6, 512, false, &unit.ObjectPath}, {8, 64, true, &unit.JobType},
		{9, 512, false, &unit.JobPath},
	}
	for _, field := range strings {
		*field.target, err = wireText(items[field.index], field.maximum, field.empty)
		if err != nil {
			return LoadedUnit{}, fmt.Errorf("D-Bus unit field %d: %w", field.index, err)
		}
	}
	var expectedPath string
	switch unit.Name {
	case "iio-sensor-proxy.service":
		expectedPath = "/org/freedesktop/systemd1/unit/iio_2dsensor_2dproxy_2eservice"
	case "lexr-native-sensors.service":
		expectedPath = "/org/freedesktop/systemd1/unit/lexr_2dnative_2dsensors_2eservice"
	default:
		return LoadedUnit{}, errors.New("D-Bus unit row names an unexpected unit")
	}
	if unit.ObjectPath != expectedPath {
		return LoadedUnit{}, errors.New("D-Bus unit row has an unexpected unit object path")
	}
	unit.JobID, err = wireUint32(items[7])
	if err != nil {
		return LoadedUnit{}, err
	}
	if unit.JobID == 0 {
		if unit.JobType != "" || unit.JobPath != "/" {
			return LoadedUnit{}, errors.New("D-Bus unit row has inconsistent absent-job fields")
		}
	} else if unit.JobType == "" || unit.JobPath != "/org/freedesktop/systemd1/job/"+strconv.FormatUint(uint64(unit.JobID), 10) {
		return LoadedUnit{}, errors.New("D-Bus unit row has inconsistent job fields")
	}
	return unit, nil
}
