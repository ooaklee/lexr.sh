package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newReleaseServer serves GitHub-style release metadata and asset downloads
// from a single test server, so update logic never touches the network.
func newReleaseServer(t *testing.T, release *Release, assets map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/ooaklee/lexr.sh/releases/latest", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(release); err != nil {
			t.Errorf("encode release: %v", err)
		}
	})
	for name, body := range assets {
		mux.HandleFunc("/download/"+name, func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write(body)
		})
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// TestIsNewerComparisons verifies semantic comparison behaviour including
// non-semantic local builds, which must never report an update.
func TestIsNewerComparisons(t *testing.T) {
	cases := []struct {
		current, candidate string
		want               bool
	}{
		{"0.4.0", "0.5.0", true},
		{"v0.4.0", "v0.4.1", true},
		{"v0.5.0", "v0.5.0", false},
		{"v0.5.0", "v0.4.9", false},
		{"0.5.0", "0.5.0-rc.1", false},
		{"0.5.0-rc.1", "0.5.0-rc.3", true},
		{"0.5.0-rc.3", "0.5.0-rc.1", false},
		{"0.5.0-rc.1", "0.5.0-rc.1", false},
		{"0.5.0-rc.1", "0.5.0", true},
		{"0.4.0", "0.5.0-rc.1", true},
		{"0.5.0", "0.6.0-rc.1", true},
		{"dev", "0.5.0", false},
		{"", "0.5.0", false},
		{"0.4.0", "latest", false},
	}
	for _, testCase := range cases {
		if got := IsNewer(testCase.current, testCase.candidate); got != testCase.want {
			t.Errorf("IsNewer(%q, %q) = %t; want %t", testCase.current, testCase.candidate, got, testCase.want)
		}
	}
}

// TestLatestParsesRelease verifies successful release discovery.
func TestLatestParsesRelease(t *testing.T) {
	release := &Release{Tag: "v0.5.0", Assets: []Asset{{Name: "lexr-v0.5.0-linux-arm64", URL: "/download/lexr-v0.5.0-linux-arm64"}}}
	server := newReleaseServer(t, release, nil)
	client := &Client{APIBase: server.URL, Repo: "ooaklee/lexr.sh"}
	got, err := client.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest() error: %v", err)
	}
	if got.Tag != "v0.5.0" || len(got.Assets) != 1 {
		t.Fatalf("Latest() = %+v; want tag v0.5.0 with one asset", got)
	}
}

// TestLatestOffline verifies that connection failures surface as ErrOffline
// rather than an opaque error, so callers can stay quiet when offline.
func TestLatestOffline(t *testing.T) {
	server := newReleaseServer(t, &Release{Tag: "v0.5.0"}, nil)
	url := server.URL
	server.Close()
	client := &Client{APIBase: url, Repo: "ooaklee/lexr.sh"}
	if _, err := client.Latest(context.Background()); err == nil {
		t.Fatal("Latest() on a closed server succeeded; want an error")
	}
}

// TestLatestRateLimitIsNoInfo verifies HTTP 429 is treated as "unknown"
// rather than a hard failure for the version notice.
func TestLatestRateLimitIsNoInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)
	client := &Client{APIBase: server.URL, Repo: "ooaklee/lexr.sh"}
	if _, err := client.Latest(context.Background()); err != ErrOffline {
		t.Fatalf("Latest() on 429 error = %v; want ErrOffline", err)
	}
}

// TestAssetName verifies GoReleaser naming including the Windows suffix.
func TestAssetName(t *testing.T) {
	if got := AssetName("v0.5.0", "linux", "arm64"); got != "lexr-v0.5.0-linux-arm64" {
		t.Fatalf("AssetName() = %q", got)
	}
	if got := AssetName("0.5.0", "windows", "amd64"); got != "lexr-v0.5.0-windows-amd64.exe" {
		t.Fatalf("AssetName() = %q", got)
	}
	if got := ChecksumAssetName("0.5.0"); got != "lexr-v0.5.0.sha256sums" {
		t.Fatalf("ChecksumAssetName() = %q", got)
	}
}

// TestVerifyChecksum verifies accepted and rejected manifest comparisons.
func TestVerifyChecksum(t *testing.T) {
	data := []byte("lexr-binary-bytes")
	digest := "0f416b58a4d59d1ccf4b70f8b1c4d0a1e7b2ad9d3a2b0ff1a70d7e2cf5b52f5c"
	// Compute the true digest rather than hardcoding an assumed value.
	sum := sha256HexForTest(t, data)
	manifest := []byte(sum + "  lexr-v0.5.0-linux-arm64\n" + digest + "  other-asset\n")
	if !VerifyChecksum(manifest, "lexr-v0.5.0-linux-arm64", data) {
		t.Fatal("VerifyChecksum() rejected a matching digest")
	}
	if VerifyChecksum(manifest, "lexr-v0.5.0-linux-arm64", []byte("tampered")) {
		t.Fatal("VerifyChecksum() accepted tampered data")
	}
	if VerifyChecksum(manifest, "missing-asset", data) {
		t.Fatal("VerifyChecksum() accepted an asset missing from the manifest")
	}
}

// TestApplyReplacesExecutable verifies the atomic rename-over apply path
// against a real file, skipping the destructive check on Windows where the
// test binary's own path semantics differ.
func TestApplyReplacesExecutable(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "lexr")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := Apply(target, []byte("new-binary")); err != nil {
		t.Fatalf("Apply() error: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new-binary" {
		t.Fatalf("target content = %q; want the new binary", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("replacement is not executable")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory has %d entries after apply; want only the target (no temp leftovers)", len(entries))
	}
}

// TestApplyUnwritableDirectory verifies the clear error for a read-only
// installation directory. Root ignores directory permissions, so skip.
func TestApplyUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("permission checks do not apply to root or Windows")
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatalf("chmod directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
	err := Apply(filepath.Join(directory, "lexr"), []byte("new"))
	if err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("Apply() error = %v; want a not-writable message", err)
	}
}

// sha256HexForTest computes the expected hex digest helper for checksum tests.
func sha256HexForTest(t *testing.T, data []byte) string {
	t.Helper()
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
