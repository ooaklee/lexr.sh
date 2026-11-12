package serviceprobe

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestWireScalarReplies covers the v259 busctl scalar envelope and bounds.
func TestWireScalarReplies(t *testing.T) {
	for _, value := range []string{"", "0123456789abcdef0123456789abcdef", ":1.23", "Unicode ☃", "literal replacement �", strings.Repeat("x", 255)} {
		got, err := decodeString(wireFixture(t, "s", value))
		if err != nil || got != value {
			t.Fatalf("string %q: got %q, %v", value, got, err)
		}
	}
	for _, value := range []bool{true, false} {
		got, err := decodeBool(wireFixture(t, "b", value))
		if err != nil || got != value {
			t.Fatalf("boolean %v: got %v, %v", value, got, err)
		}
	}
	for _, value := range []uint32{1, 12345, 2147483647} {
		got, err := decodePID(wireFixture(t, "u", value))
		if err != nil || got != value {
			t.Fatalf("PID %d: got %d, %v", value, got, err)
		}
	}
	got, err := decodeString([]byte(`{"data":["\ud83d\ude00"],"type":"s"}`))
	if err != nil || got != "😀" {
		t.Fatalf("valid escaped surrogate pair: got %q, %v", got, err)
	}
	got, err = decodeString([]byte(`{"type":"s","data":["\\ud800"]}`))
	if err != nil || got != `\ud800` {
		t.Fatalf("literal escaped slash: got %q, %v", got, err)
	}
}

// TestWireRejectsMalformedEnvelopes exercises closed fields and JSON framing.
func TestWireRejectsMalformedEnvelopes(t *testing.T) {
	cases := map[string][]byte{
		"empty":                 nil,
		"null":                  []byte(`null`),
		"array":                 []byte(`[]`),
		"truncated":             []byte(`{"type":"s","data":["a"]`),
		"missing-type":          []byte(`{"data":["a"]}`),
		"missing-data":          []byte(`{"type":"s"}`),
		"null-type":             []byte(`{"type":null,"data":["a"]}`),
		"wrong-type-value":      []byte(`{"type":1,"data":["a"]}`),
		"wrong-signature":       []byte(`{"type":"u","data":["a"]}`),
		"null-data":             []byte(`{"type":"s","data":null}`),
		"scalar-data":           []byte(`{"type":"s","data":"a"}`),
		"object-data":           []byte(`{"type":"s","data":{"a":"b","a":"c"}}`),
		"no-items":              []byte(`{"type":"s","data":[]}`),
		"two-items":             []byte(`{"type":"s","data":["a","b"]}`),
		"null-item":             []byte(`{"type":"s","data":[null]}`),
		"object-item":           []byte(`{"type":"s","data":[{"a":1,"a":2}]}`),
		"unknown-field":         []byte(`{"type":"s","data":["a"],"sender":":1.2"}`),
		"duplicate-type":        []byte(`{"type":"s","type":"s","data":["a"]}`),
		"escaped-duplicate-key": []byte(`{"type":"s","\u0074ype":"s","data":["a"]}`),
		"duplicate-data":        []byte(`{"type":"s","data":["a"],"data":["b"]}`),
		"duplicate-null-data":   []byte(`{"type":"s","data":null,"data":["b"]}`),
		"trailing-object":       []byte(`{"type":"s","data":["a"]}{}`),
		"trailing-garbage":      []byte(`{"type":"s","data":["a"]} x`),
		"invalid-utf8":          append([]byte(`{"type":"s","data":["`), 0xff, '"', ']', '}'),
		"oversized":             []byte(strings.Repeat(" ", wireReplyLimit) + `{"type":"s","data":["a"]}`),
		"long-string":           wireFixture(t, "s", strings.Repeat("x", 256)),
		"control":               []byte(`{"type":"s","data":["a\n"]}`),
		"high-surrogate":        []byte(`{"type":"s","data":["\ud800"]}`),
		"low-surrogate":         []byte(`{"type":"s","data":["\udc00"]}`),
		"wrong-low-surrogate":   []byte(`{"type":"s","data":["\ud800\u0041"]}`),
		"bad-json-escape":       []byte(`{"type":"s","data":["\x00"]}`),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if value, err := decodeString(data); err == nil {
				t.Fatalf("accepted malformed reply as %q", value)
			}
		})
	}
	if value, err := decodeString([]byte(" \n\t" + `{"type":"s","data":["a"]}` + " \r\n")); err != nil || value != "a" {
		t.Fatalf("valid surrounding JSON whitespace: %q, %v", value, err)
	}
	bounded := []byte(`{"type":"s","data":["a"]}`)
	bounded = append(bounded, []byte(strings.Repeat(" ", wireReplyLimit-len(bounded)))...)
	if value, err := decodeString(bounded); err != nil || value != "a" {
		t.Fatalf("exact bounded reply: %q, %v", value, err)
	}
}

// TestWireRejectsNonIntegerPIDs ensures no coercion from JSON number variants.
func TestWireRejectsNonIntegerPIDs(t *testing.T) {
	for _, value := range []string{"0", "-1", "+1", "1.0", "1e0", "1E0", "01", "2147483648", "4294967295", "4294967296", "18446744073709551616", `"12"`, "null", "true", "[]", "{}"} {
		t.Run(value, func(t *testing.T) {
			data := []byte(`{"type":"u","data":[` + value + `]}`)
			if pid, err := decodePID(data); err == nil {
				t.Fatalf("accepted %s as PID %d", value, pid)
			}
		})
	}
	for _, value := range []string{"0", "1", `"true"`, "null", "[]", "{}"} {
		if result, err := decodeBool([]byte(`{"type":"b","data":[` + value + `]}`)); err == nil {
			t.Fatalf("accepted %s as boolean %v", value, result)
		}
	}
	if _, err := decodePID(wireFixture(t, "s", 1)); err == nil {
		t.Fatal("accepted wrong PID signature")
	}
	if _, err := decodeBool(wireFixture(t, "s", true)); err == nil {
		t.Fatal("accepted wrong boolean signature")
	}
}

// TestWireLoadedUnits covers actual nested array shapes without absence claims.
func TestWireLoadedUnits(t *testing.T) {
	vendor := unitWireFixture("iio-sensor-proxy.service")
	native := unitWireFixture("lexr-native-sensors.service")
	native[5] = "an-untrusted-following.service"
	native[7], native[8], native[9] = uint32(4294967295), "start", "/org/freedesktop/systemd1/job/4294967295"
	got, err := decodeUnits(wireFixture(t, "a(ssssssouso)", []any{vendor, native}))
	if err != nil || len(got) != 2 {
		t.Fatalf("valid units: %#v, %v", got, err)
	}
	if got[0].Name != "iio-sensor-proxy.service" || got[0].JobID != 0 || got[0].JobPath != "/" ||
		got[1].Name != "lexr-native-sensors.service" || got[1].Following != "an-untrusted-following.service" || got[1].JobID != 4294967295 {
		t.Fatalf("unit field mapping changed: %#v", got)
	}
	for _, rows := range [][]any{{}, {native}, {native, vendor}} {
		result, err := decodeUnits(wireFixture(t, "a(ssssssouso)", rows))
		if err != nil || len(result) != len(rows) {
			t.Fatalf("valid loaded-unit count %d: %#v, %v", len(rows), result, err)
		}
	}
	if !reflect.DeepEqual(got[0], LoadedUnit{
		Name: "iio-sensor-proxy.service", Description: "Sensor proxy", LoadState: "loaded", ActiveState: "active", SubState: "running",
		ObjectPath: "/org/freedesktop/systemd1/unit/iio_2dsensor_2dproxy_2eservice", JobPath: "/",
	}) {
		t.Fatalf("unexpected vendor fields: %#v", got[0])
	}
}

// TestWireRejectsMalformedUnits checks closed names, tuple arity and job routes.
func TestWireRejectsMalformedUnits(t *testing.T) {
	mutations := map[string]func([]any) []any{
		"short-row":        func(row []any) []any { return row[:9] },
		"long-row":         func(row []any) []any { return append(row, "extra") },
		"unknown-name":     func(row []any) []any { row[0] = "other.service"; return row },
		"wrong-name-type":  func(row []any) []any { row[0] = 1; return row },
		"null-description": func(row []any) []any { row[1] = nil; return row },
		"long-description": func(row []any) []any { row[1] = strings.Repeat("x", 4097); return row },
		"empty-load":       func(row []any) []any { row[2] = ""; return row },
		"long-active":      func(row []any) []any { row[3] = strings.Repeat("x", 65); return row },
		"null-substate":    func(row []any) []any { row[4] = nil; return row },
		"long-following":   func(row []any) []any { row[5] = strings.Repeat("x", 256); return row },
		"null-following":   func(row []any) []any { row[5] = nil; return row },
		"wrong-path":       func(row []any) []any { row[6] = "/org/freedesktop/systemd1/unit/other_2eservice"; return row },
		"unescaped-path":   func(row []any) []any { row[6] = "/org/freedesktop/systemd1/unit/iio-sensor-proxy.service"; return row },
		"crossed-path": func(row []any) []any {
			row[6] = "/org/freedesktop/systemd1/unit/lexr_2dnative_2dsensors_2eservice"
			return row
		},
		"wrong-job-type":   func(row []any) []any { row[7] = "1"; return row },
		"float-job":        func(row []any) []any { row[7] = json.Number("1.0"); return row },
		"overflow-job":     func(row []any) []any { row[7] = uint64(4294967296); return row },
		"negative-job":     func(row []any) []any { row[7] = -1; return row },
		"zero-with-type":   func(row []any) []any { row[8] = "start"; return row },
		"zero-with-path":   func(row []any) []any { row[9] = "/org/freedesktop/systemd1/job/0"; return row },
		"job-without-type": func(row []any) []any { row[7] = 1; row[9] = "/org/freedesktop/systemd1/job/1"; return row },
		"job-without-path": func(row []any) []any { row[7] = 1; row[8] = "start"; return row },
		"crossed-job-path": func(row []any) []any {
			row[7] = 1
			row[8] = "start"
			row[9] = "/org/freedesktop/systemd1/job/2"
			return row
		},
		"noncanonical-job-path": func(row []any) []any {
			row[7] = 1
			row[8] = "start"
			row[9] = "/org/freedesktop/systemd1/job/01"
			return row
		},
		"long-job-type": func(row []any) []any {
			row[7] = 1
			row[8] = strings.Repeat("x", 65)
			row[9] = "/org/freedesktop/systemd1/job/1"
			return row
		},
		"control": func(row []any) []any { row[1] = "line\nnext"; return row },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			data := wireFixture(t, "a(ssssssouso)", []any{mutate(unitWireFixture("iio-sensor-proxy.service"))})
			if result, err := decodeUnits(data); err == nil {
				t.Fatalf("accepted malformed row: %#v", result)
			}
		})
	}
	vendor := unitWireFixture("iio-sensor-proxy.service")
	for name, value := range map[string]any{
		"null": nil, "non-array": "units", "null-row": []any{nil}, "object-row": []any{map[string]any{"name": "iio-sensor-proxy.service"}},
		"duplicate": []any{vendor, vendor}, "three": []any{vendor, vendor, vendor},
	} {
		t.Run(name, func(t *testing.T) {
			if result, err := decodeUnits(wireFixture(t, "a(ssssssouso)", value)); err == nil {
				t.Fatalf("accepted malformed rows: %#v", result)
			}
		})
	}
	if _, err := decodeUnits(wireFixture(t, "s", []any{})); err == nil {
		t.Fatal("accepted wrong unit signature")
	}
}

// wireFixture renders exactly one busctl result item in the required envelope.
func wireFixture(t *testing.T, signature string, value any) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"type": signature, "data": []any{value}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// unitWireFixture returns one independently mutable ten-field loaded-unit row.
func unitWireFixture(name string) []any {
	objectPath := "/org/freedesktop/systemd1/unit/iio_2dsensor_2dproxy_2eservice"
	if name == "lexr-native-sensors.service" {
		objectPath = "/org/freedesktop/systemd1/unit/lexr_2dnative_2dsensors_2eservice"
	}
	return []any{name, "Sensor proxy", "loaded", "active", "running", "", objectPath, uint32(0), "", "/"}
}
