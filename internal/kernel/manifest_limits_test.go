package kernel

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// TestWriteJSONRejectsOversizeBeforePublication prevents a producer from
// publishing a delivery declaration its unchanged bounded readers reject.
func TestWriteJSONRejectsOversizeBeforePublication(t *testing.T) {
	bundle := Bundle{Release: strings.Repeat("x", int(MaximumBundleManifestBytes))}
	var output bytes.Buffer
	if err := bundle.WriteJSON(&output); err == nil || output.Len() != 0 {
		t.Fatalf("oversized declaration emitted bytes: error=%v bytes=%d", err, output.Len())
	}
}

// TestWriteJSONRetainsStableEncodingAndDetectsShortWrites keeps the existing
// formatting contract and never reports a partial declaration as complete.
func TestWriteJSONRetainsStableEncodingAndDetectsShortWrites(t *testing.T) {
	bundle, err := NewBundle(validBundleOptions(RequestedBootImageModeStubble, DTBDeliveryEmbedded, false))
	if err != nil {
		t.Fatal(err)
	}
	var expected, actual bytes.Buffer
	encoder := json.NewEncoder(&expected)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(bundle); err != nil {
		t.Fatal(err)
	}
	if err := bundle.WriteJSON(&actual); err != nil || !bytes.Equal(actual.Bytes(), expected.Bytes()) {
		t.Fatalf("stable encoding changed: %v", err)
	}
	if err := bundle.WriteJSON(shortManifestWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error = %v", err)
	}
}

// shortManifestWriter simulates a broken output sink, not an input exception.
type shortManifestWriter struct{}

// Write deliberately returns an incomplete successful write for the test.
func (shortManifestWriter) Write(value []byte) (int, error) {
	return len(value) / 2, nil
}
