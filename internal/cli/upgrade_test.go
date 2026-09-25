package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/update"
)

// fakeUpdateClient serves canned release answers without network access.
type fakeUpdateClient struct {
	// release is returned from Latest.
	release *update.Release
	// latestErr is returned from Latest when set.
	latestErr error
	// bodies maps asset URLs to downloaded bytes.
	bodies map[string][]byte
	// fetchErr is returned from Fetch when set.
	fetchErr error
}

// Latest returns the canned release or error.
func (f *fakeUpdateClient) Latest(_ context.Context) (*update.Release, error) {
	return f.release, f.latestErr
}

// Fetch returns the canned body for a URL.
func (f *fakeUpdateClient) Fetch(_ context.Context, url string) ([]byte, error) {
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	body, ok := f.bodies[url]
	if !ok {
		return nil, errors.New("no fixture for " + url)
	}
	return body, nil
}

// testApplication returns an application whose update flow writes to the
// supplied buffer.
func testApplication(output *bytes.Buffer) *application {
	return &application{out: output, errOut: output, in: strings.NewReader("")}
}

// manifestLineFor renders a sha256sums manifest line for the fixture binary.
func manifestLineFor(t *testing.T, assetName string, body []byte) string {
	t.Helper()
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]) + "  " + assetName + "\n"
}

// setCurrentVersion pins the build version seen by the update flow.
func setCurrentVersion(t *testing.T, value string) {
	t.Helper()
	previous := currentBuildVersion
	currentBuildVersion = func() (string, string, string) { return value, "test", "test" }
	t.Cleanup(func() { currentBuildVersion = previous })
}

// TestUpgradeNewerVersionApplies verifies the happy download-verify-apply
// flow with a fixture release whose binary lands on a temp executable path.
func TestUpgradeNewerVersionApplies(t *testing.T) {
	setCurrentVersion(t, "v1.0.0")
	binary := []byte("fake-lexr-binary")
	binaryName := update.AssetName("v9.9.9", runtime.GOOS, runtime.GOARCH)
	manifest := []byte(manifestLineFor(t, binaryName, binary))
	release := &update.Release{
		Tag: "v9.9.9",
		Assets: []update.Asset{
			{Name: binaryName, URL: "https://example.invalid/" + binaryName},
			{Name: update.ChecksumAssetName("v9.9.9"), URL: "https://example.invalid/checksums"},
		},
	}
	client := &fakeUpdateClient{
		release: release,
		bodies: map[string][]byte{
			"https://example.invalid/" + binaryName: binary,
			"https://example.invalid/checksums":     manifest,
		},
	}
	output := &bytes.Buffer{}
	app := testApplication(output)

	t.Setenv("LEXR_UPDATE_TARGET_OVERRIDE_FOR_TESTS", "") // documented no-op; apply target comes from stubExecutable
	restore := stubExecutable(t, binary)
	defer restore()

	if err := app.upgradeWith(context.Background(), client); err != nil {
		t.Fatalf("upgradeWith() error: %v", err)
	}
	text := output.String()
	for _, want := range []string{"Downloading v9.9.9", "Verifying checksum", "Updating binary", "Updated successfully"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}

// stubExecutable redirects update.Apply's target to a temporary file for the
// duration of one test and returns a restore func.
func stubExecutable(t *testing.T, original []byte) func() {
	t.Helper()
	path := t.TempDir() + "/lexr"
	if err := os.WriteFile(path, original, 0o755); err != nil {
		t.Fatalf("write stub executable: %v", err)
	}
	t.Setenv("LEXR_UPDATE_TARGET_OVERRIDE_FOR_TESTS", path)
	return func() {}
}

// TestUpgradeAlreadyLatest verifies the friendly no-op message.
func TestUpgradeAlreadyLatest(t *testing.T) {
	setCurrentVersion(t, "v9.9.9")
	client := &fakeUpdateClient{release: &update.Release{Tag: "v9.9.9"}}
	output := &bytes.Buffer{}
	app := testApplication(output)
	if err := app.upgradeWith(context.Background(), client); err != nil {
		t.Fatalf("upgradeWith() error: %v", err)
	}
	if !strings.Contains(output.String(), "already up to date") {
		t.Fatalf("output = %q; want an already-up-to-date message", output.String())
	}
}

// TestUpgradeOffline verifies the clear offline error surfaced to the user.
func TestUpgradeOffline(t *testing.T) {
	client := &fakeUpdateClient{latestErr: update.ErrOffline}
	app := testApplication(&bytes.Buffer{})
	err := app.upgradeWith(context.Background(), client)
	if err == nil || !strings.Contains(err.Error(), "no network connection") {
		t.Fatalf("upgradeWith() error = %v; want a no-network message", err)
	}
}

// TestUpgradeChecksumMismatch verifies that a tampered binary is rejected.
func TestUpgradeChecksumMismatch(t *testing.T) {
	setCurrentVersion(t, "v1.0.0")
	binaryName := update.AssetName("v9.9.9", runtime.GOOS, runtime.GOARCH)
	release := &update.Release{
		Tag: "v9.9.9",
		Assets: []update.Asset{
			{Name: binaryName, URL: "https://example.invalid/" + binaryName},
			{Name: update.ChecksumAssetName("v9.9.9"), URL: "https://example.invalid/checksums"},
		},
	}
	client := &fakeUpdateClient{
		release: release,
		bodies: map[string][]byte{
			"https://example.invalid/" + binaryName: []byte("tampered"),
			"https://example.invalid/checksums":     []byte("0000000000000000000000000000000000000000000000000000000000000000  " + binaryName + "\n"),
		},
	}
	app := testApplication(&bytes.Buffer{})
	err := app.upgradeWith(context.Background(), client)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("upgradeWith() error = %v; want a checksum mismatch", err)
	}
}

// TestNoticeForLatestReleasePrintsForNewer verifies the version-command
// notice appears only for a strictly newer release.
func TestNoticeForLatestReleasePrintsForNewer(t *testing.T) {
	setCurrentVersion(t, "v1.0.0")
	output := &bytes.Buffer{}
	client := &fakeUpdateClient{release: &update.Release{Tag: "v9.9.9"}}
	noticeForLatestRelease(context.Background(), client, "v1.0.0", output)
	text := output.String()
	if !strings.Contains(text, "A new release is available: v9.9.9 (current v1.0.0)") {
		t.Fatalf("notice = %q; want the upgrade notice", text)
	}
	if !strings.Contains(text, "`lexr upgrade`") {
		t.Fatalf("notice = %q; want the upgrade command hint", text)
	}
}

// TestNoticeForLatestReleaseQuietOnFailure verifies offline and rate-limited
// checks print nothing, matching the quiet version-command contract.
func TestNoticeForLatestReleaseQuietOnFailure(t *testing.T) {
	output := &bytes.Buffer{}
	noticeForLatestRelease(context.Background(), &fakeUpdateClient{latestErr: update.ErrOffline}, "v1.0.0", output)
	if output.Len() != 0 {
		t.Fatalf("offline notice printed %q; want silence", output.String())
	}
}

// TestNoticeForLatestReleaseQuietWhenCurrent verifies no notice when the
// release does not outrank the running build.
func TestNoticeForLatestReleaseQuietWhenCurrent(t *testing.T) {
	setCurrentVersion(t, "v9.9.9")
	output := &bytes.Buffer{}
	noticeForLatestRelease(context.Background(), &fakeUpdateClient{release: &update.Release{Tag: "v1.0.0"}}, "v1.0.0", output)
	if output.Len() != 0 {
		t.Fatalf("same-version notice printed %q; want silence", output.String())
	}
}

// TestVersionCommandQuietWhenOffline runs the real version command with an
// updater whose release check fails, and asserts the version still prints
// without the notice or an error.
func TestVersionCommandQuietWhenOffline(t *testing.T) {
	output := &bytes.Buffer{}
	app := testApplication(output)
	app.updater = &fakeUpdateClient{latestErr: update.ErrOffline}
	if err := app.newVersionCommand().Execute(); err != nil {
		t.Fatalf("version command failed offline: %v", err)
	}
	if !strings.Contains(output.String(), "lexr ") {
		t.Fatalf("version output = %q", output.String())
	}
	if strings.Contains(output.String(), "A new release") {
		t.Fatal("offline version command printed an upgrade notice")
	}
}

// TestVersionCommandPrintsNoticeOnline verifies the notice follows the
// version metadata when a newer release is discovered.
func TestVersionCommandPrintsNoticeOnline(t *testing.T) {
	setCurrentVersion(t, "v1.0.0")
	output := &bytes.Buffer{}
	app := testApplication(output)
	app.updater = &fakeUpdateClient{release: &update.Release{Tag: "v9.9.9"}}
	if err := app.newVersionCommand().Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	if !strings.Contains(output.String(), "A new release is available: v9.9.9") {
		t.Fatalf("version output = %q; want the notice", output.String())
	}
}

// TestUpgradeCommandRegistered verifies the command tree exposes upgrade.
func TestUpgradeCommandRegistered(t *testing.T) {
	root := NewRootCommand(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	for _, command := range root.Commands() {
		if command.Name() == "upgrade" {
			return
		}
	}
	t.Fatal("root command tree has no upgrade command")
}
