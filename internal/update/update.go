// Package update implements Lexr's self-update flow against GitHub releases.
//
// The running executable replaces itself atomically: the new bytes are
// written to a temporary file in the same directory as the current
// executable, made executable, synced, and renamed over the old path. The
// running process keeps the old inode, so no helper process or restart
// dance is required on Linux and macOS; on Windows the running executable
// is renamed away first so the new one can take its place.
package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// DefaultAPIBase is the GitHub REST API root used for release discovery.
const DefaultAPIBase = "https://api.github.com"

// CheckTimeout bounds release discovery so version notices never hang.
const CheckTimeout = 3 * time.Second

// maximumReleaseTagBytes bounds an explicitly selected release tag.
const maximumReleaseTagBytes = 255

// maximumReleaseMetadataBytes bounds complete explicit release responses.
const maximumReleaseMetadataBytes = 1 << 20

// fullReleaseVersionPattern requires every core component before SemVer checks.
var fullReleaseVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// ErrOffline reports that the release check could not reach GitHub, either
// because no network is available or the request timed out.
var ErrOffline = errors.New("no network connection")

// Release describes the minimal published release metadata needed to update.
type Release struct {
	// Tag is the release tag, for example "v0.5.0".
	Tag string `json:"tag_name"`
	// Draft distinguishes unpublished drafts from explicitly selectable releases.
	Draft bool `json:"draft"`
	// Assets lists the downloadable files attached to the release.
	Assets []Asset `json:"assets"`
}

// Asset is one downloadable release file and its browser URL.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Client discovers releases and applies them over the running executable.
type Client struct {
	// APIBase overrides the GitHub API root for tests.
	APIBase string
	// HTTP performs release requests; nil uses a default client.
	HTTP *http.Client
	// Repo is the "owner/name" repository slug.
	Repo string
}

// NewClient returns a release client for the given repository.
func NewClient(repo string) *Client {
	return &Client{APIBase: DefaultAPIBase, Repo: repo}
}

// Latest fetches the latest published release. Network, timeout, and
// rate-limit failures return ErrOffline so callers can treat "unknown"
// distinctly from a definitive answer.
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	base := c.APIBase
	if base == "" {
		base = DefaultAPIBase
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: CheckTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/repos/"+c.Repo+"/releases/latest", nil)
	if err != nil {
		return nil, fmt.Errorf("build release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "lexr-cli")
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, ErrOffline
	}
	defer func() { _ = response.Body.Close() }()
	switch {
	case response.StatusCode == http.StatusTooManyRequests:
		// Rate limiting means we have no information, not a failure.
		return nil, ErrOffline
	case response.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("no published release found for %s", c.Repo)
	case response.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("release check returned HTTP %d", response.StatusCode)
	}
	var release Release
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode release response: %w", err)
	}
	if release.Tag == "" {
		return nil, fmt.Errorf("release response omitted a tag name")
	}
	return &release, nil
}

// CanonicalReleaseTag accepts one complete SemVer pin with an optional v prefix.
// It preserves build metadata and rejects shorthand, whitespace and path input.
func CanonicalReleaseTag(value string) (string, error) {
	if len(value) == 0 || len(value) > maximumReleaseTagBytes || !fullReleaseVersionPattern.MatchString(value) {
		return "", errors.New("release version must be a complete MAJOR.MINOR.PATCH SemVer with an optional v prefix")
	}
	tag := value
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	if len(tag) > maximumReleaseTagBytes || !semver.IsValid(tag) {
		return "", errors.New("release version is not a bounded valid SemVer tag")
	}
	return tag, nil
}

// ByTag fetches only the exact explicitly selected published release. It never
// substitutes Latest, including when the pin names an older or pre-release tag.
func (c *Client) ByTag(ctx context.Context, version string) (*Release, error) {
	tag, err := CanonicalReleaseTag(version)
	if err != nil {
		return nil, err
	}
	base := c.APIBase
	if base == "" {
		base = DefaultAPIBase
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: CheckTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/repos/"+c.Repo+"/releases/tags/"+url.PathEscape(tag), nil)
	if err != nil {
		return nil, fmt.Errorf("build pinned release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "lexr-cli")
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, ErrOffline
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusTooManyRequests:
		return nil, ErrOffline
	case http.StatusNotFound:
		return nil, fmt.Errorf("no published release found for %s at %s", c.Repo, tag)
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("pinned release check returned HTTP %d", response.StatusCode)
	}
	metadata, err := io.ReadAll(io.LimitReader(response.Body, maximumReleaseMetadataBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read pinned release response: %w", err)
	}
	if len(metadata) > maximumReleaseMetadataBytes {
		return nil, errors.New("pinned release response exceeds the metadata limit")
	}
	return decodePinnedRelease(metadata, tag)
}

// decodePinnedRelease checks one complete object, its exact tag and publication
// state while permitting the additional metadata returned by GitHub.
func decodePinnedRelease(metadata []byte, tag string) (*Release, error) {
	decoder := json.NewDecoder(bytes.NewReader(metadata))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, errors.New("pinned release response is not a JSON object")
	}
	seen := make(map[string]bool)
	var release Release
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode pinned release member: %w", err)
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return nil, errors.New("pinned release response contains an invalid or duplicate member")
		}
		seen[name] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode pinned release value: %w", err)
		}
		switch name {
		case "tag_name":
			if err := json.Unmarshal(value, &release.Tag); err != nil {
				return nil, fmt.Errorf("decode pinned release tag: %w", err)
			}
		case "draft":
			var draft *bool
			if err := json.Unmarshal(value, &draft); err != nil || draft == nil {
				return nil, errors.New("pinned release publication state must be a boolean")
			}
			release.Draft = *draft
		case "assets":
			if err := json.Unmarshal(value, &release.Assets); err != nil {
				return nil, fmt.Errorf("decode pinned release assets: %w", err)
			}
		}
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return nil, errors.New("pinned release response has an incomplete JSON object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("pinned release response has trailing data")
	}
	if !seen["tag_name"] || release.Tag == "" {
		return nil, errors.New("pinned release response omitted a tag name")
	}
	if release.Tag != tag {
		return nil, errors.New("pinned release response tag differs from the requested version")
	}
	if !seen["draft"] {
		return nil, errors.New("pinned release response omitted its publication state")
	}
	if release.Draft {
		return nil, errors.New("requested release is still a draft")
	}
	return &release, nil
}

// IsNewer reports whether candidate strictly outranks current. Both inputs
// tolerate a missing "v" prefix; dev or otherwise non-semantic builds never
// report an update.
func IsNewer(current, candidate string) bool {
	currentVersion, okCurrent := normalizeSemver(current)
	candidateVersion, okCandidate := normalizeSemver(candidate)
	if !okCurrent || !okCandidate {
		return false
	}
	return semver.Compare(candidateVersion, currentVersion) > 0
}

// normalizeSemver canonicalises a version string for golang.org/x/mod/semver,
// which requires a leading "v". A "dev" or otherwise non-semantic value is
// rejected so local builds never attempt an in-place upgrade.
func normalizeSemver(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "v") {
		value = "v" + value
	}
	if !semver.IsValid(value) {
		return "", false
	}
	return value, true
}

// AssetName returns the GoReleaser binary asset name for a version and
// platform, matching .goreleaser.yaml's archive template.
func AssetName(version, goos, goarch string) string {
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	name := "lexr-" + version + "-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// ChecksumAssetName returns the release checksum manifest asset name.
func ChecksumAssetName(version string) string {
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return "lexr-" + version + ".sha256sums"
}

// Fetch downloads a release asset into memory, using the supplied fetcher.
func (c *Client) Fetch(ctx context.Context, url string) ([]byte, error) {
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build asset request: %w", err)
	}
	request.Header.Set("User-Agent", "lexr-cli")
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, ErrOffline
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("asset download returned HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 256<<20))
}

// VerifyChecksum reports whether data matches the SHA-256 digest recorded
// for assetName in a GoReleaser "sha256sums" manifest body.
func VerifyChecksum(manifest []byte, assetName string, data []byte) bool {
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != assetName {
			continue
		}
		digest := sha256.Sum256(data)
		return strings.EqualFold(fields[0], hex.EncodeToString(digest[:]))
	}
	return false
}

// Apply atomically installs newBinary over the executable at path. The
// replacement is written beside the target so the rename stays on one
// filesystem, and fails with a clear message when the directory is not
// writable.
func Apply(path string, newBinary []byte) error {
	if runtime.GOOS == "windows" {
		return applyWindows(path, newBinary)
	}
	target, err := executablePath(path)
	if err != nil {
		return err
	}
	if err := replaceFile(target, newBinary); err != nil {
		return err
	}
	return nil
}

// applyWindows moves the running executable aside before renaming the new
// one into place, because Windows refuses to replace a running image.
func applyWindows(path string, newBinary []byte) error {
	target, err := executablePath(path)
	if err != nil {
		return err
	}
	previous := target + ".old"
	_ = os.Remove(previous)
	if err := os.Rename(target, previous); err != nil {
		return fmt.Errorf("move the running executable aside: %w", err)
	}
	if err := replaceFile(target, newBinary); err != nil {
		// Restore the previous executable so a failed apply is not fatal.
		_ = os.Rename(previous, target)
		return err
	}
	return nil
}

// executablePath resolves symlinks so the update replaces the real binary
// rather than a symbolic link pointing at it.
func executablePath(path string) (string, error) {
	if path == "" {
		executable, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("locate the running executable: %w", err)
		}
		path = executable
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// A missing target still flows through so the replacement write
		// reports the underlying directory problem.
		return filepath.Abs(path)
	}
	return resolved, nil
}

// replaceFile writes newBinary to a sibling temporary file, makes it
// executable, syncs it, and renames it over target.
func replaceFile(target string, newBinary []byte) error {
	directory := filepath.Dir(target)
	temporary, err := os.CreateTemp(directory, ".lexr-update-*")
	if err != nil {
		return fmt.Errorf("the installation directory %s is not writable: %w", directory, err)
	}
	tempName := temporary.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := temporary.Write(newBinary); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write the replacement binary: %w", err)
	}
	if err := temporary.Chmod(0o755); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("make the replacement executable: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync the replacement binary: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close the replacement binary: %w", err)
	}
	if err := os.Rename(tempName, target); err != nil {
		return fmt.Errorf("install the replacement over %s: %w", target, err)
	}
	return nil
}
