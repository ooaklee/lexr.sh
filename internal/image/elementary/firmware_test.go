package elementary

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// firmwareTransport supplies bounded test data without external downloads.
type firmwareTransport func(*http.Request) (*http.Response, error)

// RoundTrip implements the HTTP test seam.
func (f firmwareTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestFirmwareDownloadRejectsChangedBytes covers exact size, digest and status
// before any upstream binary or licence is admitted to the image.
func TestFirmwareDownloadRejectsChangedBytes(t *testing.T) {
	expected := "firmware bytes"
	pin := firmwareInput{Path: "qcom/test.bin", File: "test.bin", SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(expected))), Size: int64(len(expected))}
	for _, tt := range []struct {
		name, body string
		status     int
		ok         bool
	}{
		{"correct", expected, 200, true}, {"truncated", "firm", 200, false}, {"changed", "changed bytes!", 200, false}, {"oversize", expected + "!", 200, false}, {"error", expected, 404, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: firmwareTransport(func(r *http.Request) (*http.Response, error) {
				if !strings.Contains(r.URL.Path, firmwareRevision) {
					t.Fatal("download is not revision pinned")
				}
				return &http.Response{StatusCode: tt.status, Status: fmt.Sprint(tt.status), Request: r, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
			})}
			_, err := downloadFirmwareInput(context.Background(), client, pin)
			if (err == nil) != tt.ok {
				t.Fatalf("error=%v wantOK=%v", err, tt.ok)
			}
		})
	}
}

// TestFirmwareCopiesRejectStaleOverrides protects the firmware loader's ABI
// search order and concatenated CPIO overlays without forbidding unrelated files.
func TestFirmwareCopiesRejectStaleOverrides(t *testing.T) {
	data := []byte("expected")
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	for _, tt := range []struct {
		name, extra string
		ok          bool
	}{
		{"base", "", true}, {"unrelated", "main/usr/lib/firmware/qcom/other.bin", true},
		{"earlier copy", "early/usr/lib/firmware/qcom/test.bin", false},
		{"ABI override", "main/usr/lib/firmware/updates/test-abi/qcom/test.bin", false},
		{"compressed sibling", "main/usr/lib/firmware/qcom/test.bin.zst", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			base := filepath.Join(root, "main/usr/lib/firmware/qcom/test.bin")
			if err := os.MkdirAll(filepath.Dir(base), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(base, data, 0644); err != nil {
				t.Fatal(err)
			}
			if tt.extra != "" {
				p := filepath.Join(root, tt.extra)
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte("stale"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			err := validateFirmwareCopies(root, []string{"main", "early"}, "test-abi", "qcom/test.bin", digest, int64(len(data)))
			if (err == nil) != tt.ok {
				t.Fatalf("error=%v wantOK=%v", err, tt.ok)
			}
		})
	}
}
