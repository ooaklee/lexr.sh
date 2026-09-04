// Package release resolves versioned kernel bundles from GitHub releases.
package release

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/ooaklee/lexr.sh/internal/artifact"
	"github.com/ooaklee/lexr.sh/internal/kernel"
)

// DefaultRepository is the release source used when callers do not select a
// different owner and repository.
const DefaultRepository = "ooaklee/linux-surface-pro-11-oe"

const (
	// releaseBundleManifestName is the authoritative published delivery contract.
	releaseBundleManifestName = "lexr-kernel-bundle.json"
	// maximumReleaseBundleBytes bounds the downloaded delivery contract.
	maximumReleaseBundleBytes = 1 << 20
)

// Asset is the subset of GitHub release-asset metadata needed for verified
// acquisition.
type Asset struct {
	// Name is the exact published asset filename.
	Name string `json:"name"`
	// DownloadURL is GitHub's browser download location for the asset.
	DownloadURL string `json:"browser_download_url"`
	// Digest is GitHub's optional algorithm-prefixed content digest.
	Digest string `json:"digest"`
	// Size is GitHub's reported asset length in bytes.
	Size int64 `json:"size"`
}

// Release is the subset of GitHub release metadata used to select a candidate
// runtime kernel bundle before its checksums are downloaded and verified.
type Release struct {
	// TagName is the immutable release reference used in bundle manifests.
	TagName string `json:"tag_name"`
	// Name is the publisher's human-readable release title.
	Name string `json:"name"`
	// PublishedAt records when GitHub published the release.
	PublishedAt time.Time `json:"published_at"`
	// Draft excludes unpublished releases from selection.
	Draft bool `json:"draft"`
	// Prerelease reports the publisher's release-channel classification.
	Prerelease bool `json:"prerelease"`
	// Assets contains the files attached to the release.
	Assets []Asset `json:"assets"`
}

// Client queries GitHub releases and resolves their assets through an
// integrity-checking artefact resolver.
type Client struct {
	// HTTP performs API requests.
	HTTP *http.Client
	// APIBaseURL permits tests or compatible GitHub endpoints to replace the
	// public API base.
	APIBaseURL string
	// Token is an optional bearer token used for authenticated API requests.
	Token string
	// Artifacts downloads and atomically publishes release assets.
	Artifacts *artifact.Resolver
}

// NewClient returns a release client with the public GitHub API and the token
// from GITHUB_TOKEN, using the default HTTP client when httpClient is nil.
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{
		HTTP:       httpClient,
		APIBaseURL: "https://api.github.com",
		Token:      os.Getenv("GITHUB_TOKEN"),
		Artifacts:  artifact.NewResolver(httpClient),
	}
}

// List returns non-draft releases that contain both runtime packages. Invalid
// limits fall back to a bounded default page size.
func (c *Client) List(ctx context.Context, repository string, limit int) ([]Release, error) {
	if repository == "" {
		repository = DefaultRepository
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	endpoint := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", strings.TrimRight(c.APIBaseURL, "/"), repository, limit)
	var releases []Release
	if err := c.getJSON(ctx, endpoint, &releases); err != nil {
		return nil, err
	}
	filtered := releases[:0]
	for _, item := range releases {
		if item.Draft || !hasRuntimePackages(item) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered, nil
}

// Resolve selects the latest release or an exact tag and requires it to contain
// a candidate image-and-modules runtime pair. DownloadBundle performs the
// integrity checks needed to call the resulting bundle verified.
func (c *Client) Resolve(ctx context.Context, repository, ref string) (Release, error) {
	if repository == "" {
		repository = DefaultRepository
	}
	base := fmt.Sprintf("%s/repos/%s/releases", strings.TrimRight(c.APIBaseURL, "/"), repository)
	endpoint := base + "/latest"
	if ref != "" && ref != "latest" {
		endpoint = base + "/tags/" + url.PathEscape(ref)
	}
	var selected Release
	if err := c.getJSON(ctx, endpoint, &selected); err != nil {
		return Release{}, err
	}
	if selected.Draft {
		return Release{}, fmt.Errorf("release %s is still a draft", selected.TagName)
	}
	if !hasRuntimePackages(selected) {
		return Release{}, fmt.Errorf("release %s has no candidate image-and-modules runtime pair", selected.TagName)
	}
	return selected, nil
}

// DownloadBundle acquires an exact release, verifies every selected package
// against SHA256SUMS and any GitHub digest, then atomically publishes a bundle
// manifest. Headers are omitted unless includeHeaders is true, in which case
// both the ABI-specific and common headers packages are required.
func (c *Client) DownloadBundle(ctx context.Context, repository, ref, directory string, includeHeaders bool) (kernel.Bundle, error) {
	selected, err := c.Resolve(ctx, repository, ref)
	if err != nil {
		return kernel.Bundle{}, err
	}
	if repository == "" {
		repository = DefaultRepository
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return kernel.Bundle{}, fmt.Errorf("create kernel bundle directory: %w", err)
	}
	checksumAsset, ok := findAsset(selected, func(name string) bool { return name == "SHA256SUMS" })
	if !ok {
		return kernel.Bundle{}, errors.New("release has no SHA256SUMS asset")
	}
	checksumExpected := assetSHA256(checksumAsset)
	checksumResult, err := c.Artifacts.Acquire(ctx, artifact.Source{
		Location: checksumAsset.DownloadURL, ExpectedSHA256: checksumExpected,
	}, filepath.Join(directory, checksumAsset.Name))
	if err != nil {
		return kernel.Bundle{}, fmt.Errorf("download checksum manifest: %w", err)
	}
	checksums, err := parseChecksums(checksumResult.Path)
	if err != nil {
		return kernel.Bundle{}, err
	}
	bundleAsset, ok := findAsset(selected, func(name string) bool { return name == releaseBundleManifestName })
	if !ok {
		return kernel.Bundle{}, errors.New("release has no kernel bundle manifest")
	}
	bundleDigest, covered := checksums[bundleAsset.Name]
	if !covered {
		return kernel.Bundle{}, errors.New("SHA256SUMS does not cover the kernel bundle manifest")
	}
	if githubDigest := assetSHA256(bundleAsset); githubDigest != "" && githubDigest != bundleDigest {
		return kernel.Bundle{}, errors.New("GitHub digest and SHA256SUMS disagree for the kernel bundle manifest")
	}
	bundleInput := filepath.Join(directory, ".lexr-kernel-bundle.download")
	bundleResult, err := c.Artifacts.Acquire(ctx, artifact.Source{
		Location: bundleAsset.DownloadURL, ExpectedSHA256: bundleDigest,
	}, bundleInput)
	if err != nil {
		return kernel.Bundle{}, fmt.Errorf("download kernel bundle manifest: %w", err)
	}
	defer os.Remove(bundleResult.Path)
	recorded, err := decodeReleaseBundle(bundleResult.Path)
	if err != nil {
		return kernel.Bundle{}, err
	}
	if recorded.Release != selected.TagName {
		return kernel.Bundle{}, errors.New("release bundle identity differs from the selected GitHub release")
	}
	type selectedPackage struct {
		asset    Asset
		declared kernel.Package
	}
	packageAssets := make([]selectedPackage, 0, len(recorded.Packages))
	for _, declared := range recorded.Packages {
		if !includeHeaders && (declared.Role == kernel.RoleHeaders || declared.Role == kernel.RoleCommonHeaders) {
			continue
		}
		asset, present := findAsset(selected, func(name string) bool { return name == declared.Name })
		if !present {
			return kernel.Bundle{}, fmt.Errorf("release is missing declared package %s", declared.Name)
		}
		packageAssets = append(packageAssets, selectedPackage{asset: asset, declared: declared})
	}
	if includeHeaders {
		_, hasHeaders := recorded.Package(kernel.RoleHeaders)
		commonHeaders, hasCommonHeaders := recorded.Package(kernel.RoleCommonHeaders)
		if !hasHeaders || !hasCommonHeaders {
			return kernel.Bundle{}, errors.New("including headers requires both ABI-specific headers and common headers packages")
		}
		expectedCommonHeaders := "linux-qcom-x1e-headers-" + strings.TrimSuffix(recorded.ABI, "-qcom-x1e") + "_" + recorded.Version + "_all.deb"
		if commonHeaders.Name != expectedCommonHeaders {
			return kernel.Bundle{}, fmt.Errorf("including headers requires common headers package %s, got %s", expectedCommonHeaders, commonHeaders.Name)
		}
	}

	var packages []kernel.Package
	for _, candidate := range packageAssets {
		asset := candidate.asset
		expected, exists := checksums[asset.Name]
		if !exists {
			return kernel.Bundle{}, fmt.Errorf("SHA256SUMS does not cover %s", asset.Name)
		}
		if githubDigest := assetSHA256(asset); githubDigest != "" && githubDigest != expected {
			return kernel.Bundle{}, fmt.Errorf("GitHub digest and SHA256SUMS disagree for %s", asset.Name)
		}
		if candidate.declared.SHA256 != expected {
			return kernel.Bundle{}, fmt.Errorf("authoritative kernel bundle and SHA256SUMS disagree for %s", asset.Name)
		}
		result, err := c.Artifacts.Acquire(ctx, artifact.Source{
			Location: asset.DownloadURL, ExpectedSHA256: expected,
		}, filepath.Join(directory, asset.Name))
		if err != nil {
			return kernel.Bundle{}, fmt.Errorf("download %s: %w", asset.Name, err)
		}
		if result.SHA256 != candidate.declared.SHA256 || result.Size != candidate.declared.Size {
			return kernel.Bundle{}, fmt.Errorf("downloaded package bytes disagree with authoritative kernel bundle for %s", asset.Name)
		}
		packages = append(packages, kernel.Package{
			Role: candidate.declared.Role, Name: asset.Name, Path: result.Path, URL: asset.DownloadURL,
			SHA256: result.SHA256, Size: result.Size, Verified: result.Verified,
		})
	}
	bundle, err := kernel.NewBundle(kernel.BundleOptions{
		Release: recorded.Release, Repository: recorded.Repository,
		RequestedBootImageMode: recorded.RequestedBootImageMode,
		EffectiveDTBDelivery:   recorded.EffectiveDTBDelivery, EmbeddedDTBCount: recorded.EmbeddedDTBCount,
		DTBSelectionProvenance: recorded.DTBSelectionProvenance,
		Packages:               packages, DeviceTrees: recorded.DeviceTrees,
	})
	if err != nil {
		return kernel.Bundle{}, err
	}
	manifestPath := filepath.Join(directory, releaseBundleManifestName)
	file, err := os.OpenFile(manifestPath+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return kernel.Bundle{}, fmt.Errorf("create kernel bundle manifest: %w", err)
	}
	writeErr := bundle.WriteJSON(file)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(manifestPath + ".tmp")
		return kernel.Bundle{}, errors.Join(writeErr, closeErr)
	}
	if err := os.Rename(manifestPath+".tmp", manifestPath); err != nil {
		return kernel.Bundle{}, fmt.Errorf("publish kernel bundle manifest: %w", err)
	}
	return bundle, nil
}

// decodeReleaseBundle strictly reads and canonicalises one downloaded contract.
func decodeReleaseBundle(path string) (kernel.Bundle, error) {
	listed, err := os.Lstat(path)
	if err != nil {
		return kernel.Bundle{}, err
	}
	if listed.Mode()&os.ModeSymlink != 0 || !listed.Mode().IsRegular() || listed.Size() <= 0 || listed.Size() > maximumReleaseBundleBytes {
		return kernel.Bundle{}, errors.New("release kernel bundle is not a bounded non-empty regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return kernel.Bundle{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(listed, opened) {
		return kernel.Bundle{}, errors.Join(errors.New("release kernel bundle changed before it was read"), err)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maximumReleaseBundleBytes+1))
	decoder.DisallowUnknownFields()
	var recorded kernel.Bundle
	if err := decoder.Decode(&recorded); err != nil {
		return kernel.Bundle{}, fmt.Errorf("decode release kernel bundle: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return kernel.Bundle{}, errors.New("release kernel bundle contains trailing JSON")
	}
	if recorded.SchemaVersion != kernel.BundleSchemaVersion {
		return kernel.Bundle{}, fmt.Errorf("release kernel bundle schema is %d, expected %d", recorded.SchemaVersion, kernel.BundleSchemaVersion)
	}
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(listed, current) || current.Size() != listed.Size() {
		return kernel.Bundle{}, errors.Join(errors.New("release kernel bundle changed while it was read"), err)
	}
	canonical, err := kernel.NewBundle(kernel.BundleOptions{
		Release: recorded.Release, Repository: recorded.Repository,
		RequestedBootImageMode: recorded.RequestedBootImageMode,
		EffectiveDTBDelivery:   recorded.EffectiveDTBDelivery, EmbeddedDTBCount: recorded.EmbeddedDTBCount,
		DTBSelectionProvenance: recorded.DTBSelectionProvenance,
		Packages:               recorded.Packages, DeviceTrees: recorded.DeviceTrees,
	})
	if err != nil || !reflect.DeepEqual(recorded, canonical) {
		return kernel.Bundle{}, errors.Join(errors.New("release kernel bundle is invalid or non-canonical"), err)
	}
	return canonical, nil
}

// getJSON performs one authenticated GitHub API request and decodes its success
// body into destination.
func (c *Client) getJSON(ctx context.Context, endpoint string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "lexr")
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("query GitHub releases: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("query GitHub releases: server returned %s", response.Status)
	}
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		return fmt.Errorf("decode GitHub release response: %w", err)
	}
	return nil
}

// hasRuntimePackages reports whether a release contains recognisable image and
// modules packages, regardless of optional headers.
func hasRuntimePackages(item Release) bool {
	hasImage, hasModules := false, false
	for _, asset := range item.Assets {
		role, _, _, err := kernel.ParsePackageName(asset.Name)
		if err != nil {
			continue
		}
		hasImage = hasImage || role == kernel.RoleImage
		hasModules = hasModules || role == kernel.RoleModules
	}
	return hasImage && hasModules
}

// findAsset returns the first release asset whose name satisfies predicate.
func findAsset(item Release, predicate func(string) bool) (Asset, bool) {
	for _, asset := range item.Assets {
		if predicate(asset.Name) {
			return asset, true
		}
	}
	return Asset{}, false
}

// assetSHA256 normalises GitHub's optional sha256-prefixed digest for direct
// comparison with SHA256SUMS.
func assetSHA256(asset Asset) string {
	return strings.TrimPrefix(strings.ToLower(asset.Digest), "sha256:")
}

// parseChecksums loads a non-empty SHA256SUMS manifest and rejects malformed,
// duplicate, or path-bearing entries.
func parseChecksums(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	result := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || len(fields[0]) != 64 {
			return nil, fmt.Errorf("malformed SHA256SUMS line %q", scanner.Text())
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return nil, fmt.Errorf("malformed SHA256SUMS digest %q: %w", fields[0], err)
		}
		name := strings.TrimPrefix(fields[1], "*")
		if filepath.Base(name) != name || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			return nil, fmt.Errorf("unsafe path in SHA256SUMS: %q", name)
		}
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("duplicate SHA256SUMS entry %q", name)
		}
		result[name] = strings.ToLower(fields[0])
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errors.New("SHA256SUMS is empty")
	}
	return result, nil
}

// SortByPublished orders releases in place from newest to oldest publication
// time.
func SortByPublished(releases []Release) {
	sort.Slice(releases, func(i, j int) bool { return releases[i].PublishedAt.After(releases[j].PublishedAt) })
}
