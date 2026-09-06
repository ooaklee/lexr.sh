package manager

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	"github.com/ooaklee/lexr.sh/internal/catalog"
)

// TestRollingSourceRefreshPreservesAcceptedSnapshots simulates a publisher
// changing latest and failing downloads without losing previously pinned bytes.
func TestRollingSourceRefreshPreservesAcceptedSnapshots(t *testing.T) {
	t.Parallel()
	oldBytes, newBytes := "signed snapshot A", "signed snapshot B"
	oldDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(oldBytes)))
	newDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(newBytes)))
	var state atomic.Int32
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch state.Load() {
		case 0:
			fmt.Fprint(w, oldBytes)
		case 1:
			fmt.Fprint(w, newBytes)
		default:
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	manager := &ImageManager{Artifacts: artifact.NewResolver(server.Client())}
	entry := catalog.Entry{ID: "arch-snapshot", URL: server.URL + "/root.tar.gz", ArtifactKind: catalog.ArtifactKindRootfsTarGZ, Mutable: true, Checksum: &catalog.Checksum{Algorithm: "sha256", Value: oldDigest}}
	cache := t.TempDir()
	path, digest, err := manager.resolveSource(context.Background(), CreateImageRequest{}, entry, cache)
	if err != nil {
		t.Fatal(err)
	}
	if digest != oldDigest || !strings.HasSuffix(path, oldDigest+".tar.gz") {
		t.Fatalf("source is not pinned with its correct format: %q %q", path, digest)
	}
	for _, failedState := range []int32{1, 2} {
		state.Store(failedState)
		_, _, err := manager.resolveSource(context.Background(), CreateImageRequest{RefreshSource: true}, entry, cache)
		if err == nil {
			t.Fatal("refresh silently accepted changed or unavailable latest")
		}
		if failedState == 1 && !strings.Contains(err.Error(), "SHA-256 mismatch") {
			t.Fatalf("wrong mismatch error: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != oldBytes {
			t.Fatalf("failed refresh lost accepted snapshot: %q, %v", got, err)
		}
		if entry.Checksum.Value != oldDigest {
			t.Fatal("refresh changed expected digest")
		}
	}
	before := requests.Load()
	if _, _, err := manager.resolveSource(context.Background(), CreateImageRequest{}, entry, cache); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != before {
		t.Fatal("a valid cached snapshot required the unavailable rolling server")
	}
	state.Store(1)
	newPath, gotDigest, err := manager.resolveSource(context.Background(), CreateImageRequest{SourceSHA256: newDigest}, entry, cache)
	if err != nil {
		t.Fatal(err)
	}
	if newPath == path || gotDigest != newDigest {
		t.Fatal("an explicitly selected new snapshot overwrote the old identity")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != oldBytes {
		t.Fatal("new snapshot removed old bytes")
	}
	if _, _, err := manager.resolveSource(context.Background(), CreateImageRequest{SourceSHA256: newDigest, RefreshSource: true}, entry, cache); err != nil {
		t.Fatalf("matching refresh failed: %v", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(cache, "images", ".refresh-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("refresh left temporary state: %v %v", leftovers, err)
	}
}

// TestSourceCacheRequiresBoundedDigest prevents path-like checksum input from
// becoming a cache filename and refuses an unpinned root filesystem archive.
func TestSourceCacheRequiresBoundedDigest(t *testing.T) {
	t.Parallel()
	entry := catalog.Entry{ID: "arch", ArtifactKind: catalog.ArtifactKindRootfsTarGZ}
	for _, digest := range []string{"", "../escape", strings.Repeat("z", 64)} {
		if _, err := sourceCachePath(t.TempDir(), entry, digest); err == nil {
			t.Fatalf("accepted invalid digest %q", digest)
		}
	}
}

// TestLegacySourceCacheMigration retains offline access to accepted ISO/raw
// downloads while refusing to promote stale legacy bytes under a new digest.
func TestLegacySourceCacheMigration(t *testing.T) {
	t.Parallel()
	for _, kind := range []catalog.ArtifactKind{catalog.ArtifactKindISO, catalog.ArtifactKindRawXZ} {
		t.Run(string(kind), func(t *testing.T) {
			content := "previous accepted download"
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
			var requests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Error(w, "offline", http.StatusServiceUnavailable)
			}))
			defer server.Close()
			manager := &ImageManager{Artifacts: artifact.NewResolver(server.Client())}
			cache := t.TempDir()
			legacy := filepath.Join(cache, "images", "existing.iso")
			if err := os.MkdirAll(filepath.Dir(legacy), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(legacy, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			entry := catalog.Entry{ID: "existing", ArtifactKind: kind, URL: server.URL, Checksum: &catalog.Checksum{Algorithm: "sha256", Value: strings.ToUpper(digest)}}
			path, gotDigest, err := manager.resolveSource(context.Background(), CreateImageRequest{}, entry, cache)
			if err != nil || gotDigest != digest || requests.Load() != 0 {
				t.Fatalf("legacy migration failed: %q %v, requests %d", gotDigest, err, requests.Load())
			}
			suffix := ".iso"
			if kind == catalog.ArtifactKindRawXZ {
				suffix = ".raw.xz"
			}
			if !strings.HasSuffix(path, digest+suffix) || path == legacy {
				t.Fatalf("wrong migrated identity: %s", path)
			}
			if got, err := os.ReadFile(legacy); err != nil || string(got) != content {
				t.Fatal("migration altered old bytes")
			}
			entry.Checksum.Value = strings.Repeat("1", 64)
			if _, _, err := manager.resolveSource(context.Background(), CreateImageRequest{}, entry, cache); err == nil {
				t.Fatal("stale legacy input satisfied another pin")
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != content {
				t.Fatal("failed new pin discarded migrated snapshot")
			}
		})
	}
}
