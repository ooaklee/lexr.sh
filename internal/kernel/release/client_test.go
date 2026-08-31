package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// testReleaseABI is the exact kernel ABI represented by release fixtures.
const testReleaseABI = "7.2.2-jg-0sp11v21-qcom-x1e"

// testReleaseVersion is the Debian version represented by release fixtures.
const testReleaseVersion = "7.2.2-jg-0sp11v21"

// TestDownloadBundleRequiresCompleteHeaderPair verifies that explicitly
// requesting headers fails closed unless both supported header roles exist.
func TestDownloadBundleRequiresCompleteHeaderPair(t *testing.T) {
	tests := []struct {
		name          string
		roles         []kernel.PackageRole
		wantPackages  int
		wantHeaderErr bool
	}{
		{name: "no headers", wantHeaderErr: true},
		{name: "ABI headers only", roles: []kernel.PackageRole{kernel.RoleHeaders}, wantHeaderErr: true},
		{name: "common headers only", roles: []kernel.PackageRole{kernel.RoleCommonHeaders}, wantHeaderErr: true},
		{
			name:         "complete header pair",
			roles:        []kernel.PackageRole{kernel.RoleHeaders, kernel.RoleCommonHeaders},
			wantPackages: 4,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newKernelReleaseServer(t, kernelReleaseFiles(test.roles...))
			defer server.Close()

			client := NewClient(server.Client())
			client.APIBaseURL = server.URL
			bundle, err := client.DownloadBundle(
				context.Background(), "owner/repository", "v21", t.TempDir(), true,
			)
			if test.wantHeaderErr {
				if err == nil || !strings.Contains(err.Error(), "requires both ABI-specific headers and common headers") {
					t.Fatalf("DownloadBundle() error = %v, want incomplete-header error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(bundle.Packages) != test.wantPackages {
				t.Fatalf("packages = %d, want %d", len(bundle.Packages), test.wantPackages)
			}
			for _, role := range []kernel.PackageRole{kernel.RoleHeaders, kernel.RoleCommonHeaders} {
				if _, ok := bundle.Package(role); !ok {
					t.Errorf("bundle has no %s package", role)
				}
			}
		})
	}
}

// TestDownloadBundleRejectsWrongCommonHeaderBase proves a same-version common
// header asset cannot be attached to an unrelated kernel base.
func TestDownloadBundleRejectsWrongCommonHeaderBase(t *testing.T) {
	files := kernelReleaseFiles(kernel.RoleHeaders, kernel.RoleCommonHeaders)
	commonName := packageName(kernel.RoleCommonHeaders)
	contents := files[commonName]
	delete(files, commonName)
	wrongName := "linux-qcom-x1e-headers-wrong-base_" + testReleaseVersion + "_all.deb"
	files[wrongName] = contents
	files["SHA256SUMS"] = kernelReleaseChecksums(files)

	server := newKernelReleaseServer(t, files)
	defer server.Close()
	client := NewClient(server.Client())
	client.APIBaseURL = server.URL
	_, err := client.DownloadBundle(
		context.Background(), "owner/repository", "v21", t.TempDir(), true,
	)
	if err == nil || !strings.Contains(err.Error(), "requires common headers package") || !strings.Contains(err.Error(), wrongName) {
		t.Fatalf("DownloadBundle() error = %v, want wrong common-header base error", err)
	}
}

// TestDownloadBundleRuntimeSelectionIgnoresHeaders verifies that the runtime
// package mode remains valid even when a release publishes an incomplete pair.
func TestDownloadBundleRuntimeSelectionIgnoresHeaders(t *testing.T) {
	server := newKernelReleaseServer(t, kernelReleaseFiles(kernel.RoleHeaders))
	defer server.Close()

	directory := t.TempDir()
	client := NewClient(server.Client())
	client.APIBaseURL = server.URL
	bundle, err := client.DownloadBundle(
		context.Background(), "owner/repository", "v21", directory, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Packages) != 2 {
		t.Fatalf("packages = %d, want runtime pair", len(bundle.Packages))
	}
	if _, ok := bundle.Package(kernel.RoleHeaders); ok {
		t.Fatal("runtime bundle unexpectedly includes ABI headers")
	}
	if _, err := os.Stat(filepath.Join(directory, packageName(kernel.RoleHeaders))); !os.IsNotExist(err) {
		t.Fatalf("ABI headers were downloaded in runtime mode: %v", err)
	}
}

// kernelReleaseFiles returns a complete runtime release plus requested header
// roles and an authoritative checksum manifest for every package asset.
func kernelReleaseFiles(headerRoles ...kernel.PackageRole) map[string][]byte {
	roles := append([]kernel.PackageRole{kernel.RoleImage, kernel.RoleModules}, headerRoles...)
	files := make(map[string][]byte, len(roles)+1)
	for _, role := range roles {
		files[packageName(role)] = []byte("fixture for " + role)
	}
	files["SHA256SUMS"] = kernelReleaseChecksums(files)
	return files
}

// kernelReleaseChecksums returns a stable checksum manifest for every package
// entry in files while excluding any existing manifest entry.
func kernelReleaseChecksums(files map[string][]byte) []byte {
	names := make([]string, 0, len(files))
	for name := range files {
		if name == "SHA256SUMS" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var checksums strings.Builder
	for _, name := range names {
		fmt.Fprintf(&checksums, "%s  %s\n", releaseDigest(files[name]), name)
	}
	return []byte(checksums.String())
}

// packageName maps each supported release package role to its exact fixture
// filename.
func packageName(role kernel.PackageRole) string {
	switch role {
	case kernel.RoleImage:
		return "linux-image-" + testReleaseABI + "_" + testReleaseVersion + "_arm64.deb"
	case kernel.RoleModules:
		return "linux-modules-" + testReleaseABI + "_" + testReleaseVersion + "_arm64.deb"
	case kernel.RoleHeaders:
		return "linux-headers-" + testReleaseABI + "_" + testReleaseVersion + "_arm64.deb"
	case kernel.RoleCommonHeaders:
		return "linux-qcom-x1e-headers-" + strings.TrimSuffix(testReleaseABI, "-qcom-x1e") + "_" + testReleaseVersion + "_all.deb"
	default:
		panic("unsupported test package role " + role)
	}
}

// newKernelReleaseServer serves GitHub-compatible metadata and immutable assets
// with digests derived from the fixture bytes.
func newKernelReleaseServer(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/repos/") {
			names := make([]string, 0, len(files))
			for name := range files {
				names = append(names, name)
			}
			sort.Strings(names)
			assets := make([]Asset, 0, len(names))
			for _, name := range names {
				contents := files[name]
				assets = append(assets, Asset{
					Name: name, DownloadURL: "https://" + request.Host + "/assets/" + name,
					Digest: "sha256:" + releaseDigest(contents), Size: int64(len(contents)),
				})
			}
			_ = json.NewEncoder(writer).Encode(Release{TagName: "v21", Assets: assets})
			return
		}
		name := strings.TrimPrefix(request.URL.Path, "/assets/")
		contents, ok := files[name]
		if !ok {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write(contents)
	}))
	return server
}

// releaseDigest returns the lowercase SHA-256 used by release fixtures.
func releaseDigest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}
