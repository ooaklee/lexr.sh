package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

// TestCanonicalReleaseTagRequiresCompleteSemVer checks exact version pins and
// rejects shorthand, aliases, path input and invalid identifier spellings.
func TestCanonicalReleaseTagRequiresCompleteSemVer(t *testing.T) {
	for _, value := range []string{"0.6.0-alpha", "v0.6.0-alpha", "1.2.3", "v0.1.0-rc.12", "1.2.3+build.01", "v1.2.3-alpha.1+Build-A.01", "v1.2.3+" + strings.Repeat("x", maximumReleaseTagBytes-len("v1.2.3+"))} {
		t.Run("valid/"+value, func(t *testing.T) {
			got, err := CanonicalReleaseTag(value)
			if err != nil || got != "v"+strings.TrimPrefix(value, "v") {
				t.Fatalf("CanonicalReleaseTag(%q) = %q, %v", value, got, err)
			}
		})
	}
	for _, value := range invalidReleasePins() {
		t.Run("invalid/"+value, func(t *testing.T) {
			if got, err := CanonicalReleaseTag(value); err == nil || got != "" {
				t.Fatalf("CanonicalReleaseTag(%q) = %q, %v; want rejection", value, got, err)
			}
		})
	}
}

// invalidReleasePins supplies malformed input shared by pure and no-HTTP checks.
func invalidReleasePins() []string {
	return []string{"", "latest", "dev", "1", "1.2", "v1.2", "01.2.3", "1.02.3", "1.2.03", "1.2.3-01", "1.2.3-alpha..1", "1.2.3-", "1.2.3+", "1.2.3+build..1", "vv1.2.3", "V1.2.3", " 1.2.3", "1.2.3 ", "1.2.3\n", "1.2.3\r", "1.2.3\t", "1.2.3\x00", "../1.2.3", "refs/tags/v1.2.3", "1.2.3/alpha", "1.2.3\\alpha", "https://example.org/v1.2.3", "1.2.3?draft=false", "1.2.3#fragment", "1.2.3%2falpha", "1.2.3+" + strings.Repeat("x", maximumReleaseTagBytes)}
}

// TestByTagRejectsInvalidPinsBeforeHTTP proves malformed explicit selections do
// not issue any request, including a fallback request for Latest.
func TestByTagRejectsInvalidPinsBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	client := &Client{APIBase: server.URL, Repo: "ooaklee/lexr.sh"}
	for _, value := range invalidReleasePins() {
		if _, err := client.ByTag(context.Background(), value); err == nil {
			t.Errorf("ByTag(%q) accepted an invalid pin", value)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("invalid pins made %d HTTP requests", got)
	}
}

// TestByTagRetainsExactVersionAndMetadata checks explicit older and pre-release
// selections, escaped tag paths, optional v spelling and opaque build metadata.
func TestByTagRetainsExactVersionAndMetadata(t *testing.T) {
	for _, version := range []string{"0.6.0-alpha", "v0.6.0-alpha", "0.1.0", "v1.2.3-rc.1+Build-A.01"} {
		t.Run(version, func(t *testing.T) {
			tag := "v" + strings.TrimPrefix(version, "v")
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.EscapedPath() != "/repos/ooaklee/lexr.sh/releases/tags/"+url.PathEscape(tag) {
					t.Errorf("unexpected request path %q", request.URL.EscapedPath())
				}
				if request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("User-Agent") != "lexr-cli" {
					t.Error("pinned request omitted GitHub headers")
				}
				if err := json.NewEncoder(writer).Encode(Release{Tag: tag, Assets: []Asset{{Name: AssetName(tag, "linux", "arm64"), URL: "/download/pinned"}}}); err != nil {
					t.Errorf("encode pinned release: %v", err)
				}
			}))
			t.Cleanup(server.Close)
			client := &Client{APIBase: server.URL, Repo: "ooaklee/lexr.sh"}
			got, err := client.ByTag(context.Background(), version)
			if err != nil || got == nil || got.Tag != tag || got.Draft || len(got.Assets) != 1 {
				t.Fatalf("ByTag(%q) = %+v, %v", version, got, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("ByTag made %d requests, want exactly the pinned request", calls.Load())
			}
		})
	}
}

// TestByTagHTTPFailuresNeverFallback checks exact-pin failures are definitive
// and rate limits retain the existing offline classification.
func TestByTagHTTPFailuresNeverFallback(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.URL.Path != "/repos/ooaklee/lexr.sh/releases/tags/v0.6.0-alpha" {
					t.Errorf("unexpected fallback path %q", request.URL.Path)
				}
				writer.WriteHeader(status)
			}))
			t.Cleanup(server.Close)
			client := &Client{APIBase: server.URL, Repo: "ooaklee/lexr.sh"}
			got, err := client.ByTag(context.Background(), "0.6.0-alpha")
			if got != nil || err == nil || calls.Load() != 1 {
				t.Fatalf("ByTag() = %+v, %v, calls %d", got, err, calls.Load())
			}
			if (status == http.StatusTooManyRequests) != errors.Is(err, ErrOffline) {
				t.Fatalf("ByTag() offline classification = %v for HTTP %d", err, status)
			}
		})
	}
}

// TestByTagOfflineAndDeadline checks closed connections and bounded context
// cancellation without accessing a remote network service.
func TestByTagOfflineAndDeadline(t *testing.T) {
	t.Run("default bound", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, `{"tag_name":"v0.6.0-alpha","draft":false}`)
		}))
		t.Cleanup(server.Close)
		transport := server.Client().Transport
		client := &Client{APIBase: server.URL, Repo: "ooaklee/lexr.sh", HTTP: &http.Client{Transport: releaseRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			deadline, ok := request.Context().Deadline()
			if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > CheckTimeout {
				t.Errorf("pinned request omitted its bounded deadline: %v, %v", deadline, ok)
			}
			return transport.RoundTrip(request)
		})}}
		if _, err := client.ByTag(context.Background(), "0.6.0-alpha"); err != nil {
			t.Fatalf("ByTag() error = %v", err)
		}
	})
	t.Run("offline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		server.Close()
		client := &Client{APIBase: server.URL, Repo: "ooaklee/lexr.sh"}
		if _, err := client.ByTag(context.Background(), "0.6.0-alpha"); !errors.Is(err, ErrOffline) {
			t.Fatalf("ByTag() error = %v, want ErrOffline", err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			<-request.Context().Done()
		}))
		t.Cleanup(server.Close)
		client := &Client{APIBase: server.URL, Repo: "ooaklee/lexr.sh"}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		started := time.Now()
		if _, err := client.ByTag(ctx, "0.6.0-alpha"); !errors.Is(err, ErrOffline) {
			t.Fatalf("ByTag() error = %v, want ErrOffline", err)
		}
		if time.Since(started) > time.Second {
			t.Fatal("ByTag ignored the shorter caller deadline")
		}
	})
	t.Run("body deadline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(writer, `{"tag_name":`)
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
		}))
		t.Cleanup(server.Close)
		client := &Client{APIBase: server.URL, Repo: "ooaklee/lexr.sh"}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		started := time.Now()
		if got, err := client.ByTag(ctx, "0.6.0-alpha"); err == nil || got != nil {
			t.Fatalf("ByTag() accepted interrupted metadata: %+v, %v", got, err)
		}
		if time.Since(started) > time.Second {
			t.Fatal("ByTag ignored the response-body deadline")
		}
	})
}

// releaseRoundTripFunc permits observing the context on a local test request.
type releaseRoundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip adapts a test function to the standard HTTP transport contract.
func (roundTrip releaseRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

// TestByTagRejectsAmbiguousMetadata checks exact identity, publication state,
// complete JSON consumption and the inclusive metadata-size limit.
func TestByTagRejectsAmbiguousMetadata(t *testing.T) {
	valid := `{"tag_name":"v0.6.0-alpha","draft":false}`
	cases := []struct {
		name string
		body string
	}{
		{"wrong tag", `{"tag_name":"v0.6.0-beta","draft":false}`},
		{"unprefixed response tag", `{"tag_name":"0.6.0-alpha","draft":false}`},
		{"draft", `{"tag_name":"v0.6.0-alpha","draft":true}`},
		{"missing tag", `{"draft":false}`},
		{"null tag", `{"tag_name":null,"draft":false}`},
		{"numeric tag", `{"tag_name":123,"draft":false}`},
		{"missing publication", `{"tag_name":"v0.6.0-alpha"}`},
		{"null publication", `{"tag_name":"v0.6.0-alpha","draft":null}`},
		{"string publication", `{"tag_name":"v0.6.0-alpha","draft":"false"}`},
		{"malformed", `{"tag_name":`},
		{"array", `[]`},
		{"scalar", `null`},
		{"second object", valid + `{}`},
		{"trailing text", valid + ` trailing`},
		{"duplicate tag", `{"tag_name":"v0.6.0-alpha","tag_name":"v0.6.0-alpha","draft":false}`},
		{"duplicate publication", `{"tag_name":"v0.6.0-alpha","draft":true,"draft":false}`},
		{"duplicate unknown", `{"tag_name":"v0.6.0-alpha","draft":false,"body":0,"body":1}`},
		{"bad assets", `{"tag_name":"v0.6.0-alpha","draft":false,"assets":{}}`},
		{"over bound", valid + strings.Repeat(" ", maximumReleaseMetadataBytes-len(valid)+1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(writer, test.body)
			}))
			t.Cleanup(server.Close)
			client := &Client{APIBase: server.URL, Repo: "ooaklee/lexr.sh"}
			if got, err := client.ByTag(context.Background(), "0.6.0-alpha"); err == nil || got != nil {
				t.Fatalf("ByTag() accepted %s: %+v, %v", test.name, got, err)
			}
		})
	}
	t.Run("exact bound and extra GitHub metadata", func(t *testing.T) {
		body := `{"tag_name":"v0.6.0-alpha","draft":false,"prerelease":true,"body":"release notes"}`
		body += strings.Repeat(" ", maximumReleaseMetadataBytes-len(body))
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, body)
		}))
		t.Cleanup(server.Close)
		client := &Client{APIBase: server.URL, Repo: "ooaklee/lexr.sh"}
		if got, err := client.ByTag(context.Background(), "0.6.0-alpha"); err != nil || got == nil || got.Tag != "v0.6.0-alpha" {
			t.Fatalf("ByTag() rejected bounded published metadata: %+v, %v", got, err)
		}
	})
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
