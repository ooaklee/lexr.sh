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

	"github.com/spf13/cobra"

	"github.com/ooaklee/lexr.sh/internal/update"
)

// fakeUpdateClient serves canned release answers without network access.
type fakeUpdateClient struct {
	// release is returned from Latest or ByTag.
	release *update.Release
	// latestErr is returned from Latest when set.
	latestErr error
	// byTagErr is returned from ByTag when set.
	byTagErr error
	// latestCalls counts unpinned release lookups.
	latestCalls int
	// requestedTags records explicit release lookups.
	requestedTags []string
	// fetchedURLs records asset downloads.
	fetchedURLs []string
	// bodies maps asset URLs to downloaded bytes.
	bodies map[string][]byte
	// fetchErr is returned from Fetch when set.
	fetchErr error
}

// Latest returns the canned release or error.
func (f *fakeUpdateClient) Latest(_ context.Context) (*update.Release, error) {
	f.latestCalls++
	return f.release, f.latestErr
}

// ByTag records the exact pin and returns the canned release or error.
func (f *fakeUpdateClient) ByTag(_ context.Context, tag string) (*update.Release, error) {
	f.requestedTags = append(f.requestedTags, tag)
	return f.release, f.byTagErr
}

// Fetch returns the canned body for a URL.
func (f *fakeUpdateClient) Fetch(_ context.Context, url string) ([]byte, error) {
	f.fetchedURLs = append(f.fetchedURLs, url)
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

	target := stubExecutable(t, []byte("previous-lexr-binary"))

	if err := app.upgradeWith(context.Background(), client); err != nil {
		t.Fatalf("upgradeWith() error: %v", err)
	}
	text := output.String()
	for _, want := range []string{"Downloading v9.9.9", "Verifying checksum", "Updating binary", "Updated successfully"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
	assertUpgradeTarget(t, target, binary)
}

// stubExecutable redirects update.Apply's target to a temporary file for the
// duration of one test and returns its path for byte-preservation assertions.
func stubExecutable(t *testing.T, original []byte) string {
	t.Helper()
	path := t.TempDir() + "/lexr"
	if err := os.WriteFile(path, original, 0o755); err != nil {
		t.Fatalf("write stub executable: %v", err)
	}
	previous := applyUpgrade
	applyUpgrade = func(_ string, replacement []byte) error {
		return update.Apply(path, replacement)
	}
	t.Cleanup(func() { applyUpgrade = previous })
	return path
}

// assertUpgradeTarget checks the replacement or preservation of the real
// temporary executable used by the atomic updater.
func assertUpgradeTarget(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("executable bytes = %q, want %q", got, want)
	}
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
			flag := command.Flags().Lookup("version")
			if flag == nil || flag.Value.Type() != "string" || flag.DefValue != "" {
				t.Fatalf("upgrade version flag = %+v; want an optional string with no default pin", flag)
			}
			return
		}
	}
	t.Fatal("root command tree has no upgrade command")
}

// upgradeReleaseFixture supplies matching binary and checksum assets without
// consulting a remote release or altering a real installed executable.
func upgradeReleaseFixture(t *testing.T, tag string) (*update.Release, map[string][]byte, []byte) {
	t.Helper()
	binary := []byte("fixture executable for " + tag)
	name := update.AssetName(tag, runtime.GOOS, runtime.GOARCH)
	release := &update.Release{Tag: tag, Assets: []update.Asset{
		{Name: name, URL: "https://example.invalid/binary"},
		{Name: update.ChecksumAssetName(tag), URL: "https://example.invalid/checksums"},
	}}
	bodies := map[string][]byte{
		"https://example.invalid/binary":    binary,
		"https://example.invalid/checksums": []byte(manifestLineFor(t, name, binary)),
	}
	return release, bodies, binary
}

// executeUpgradeCommand exercises the actual Cobra flag and RunE wiring with
// an injected client rather than invoking the domain flow directly.
func executeUpgradeCommand(app *application, args ...string) error {
	command := app.newUpgradeCommand()
	command.SetOut(app.out)
	command.SetErr(app.errOut)
	command.SetArgs(args)
	return command.Execute()
}

// TestUpgradeExplicitVersionApplies checks prereleases, older releases, build
// metadata and deliberate pins from development builds through the real flag.
func TestUpgradeExplicitVersionApplies(t *testing.T) {
	for _, test := range []struct {
		name    string
		current string
		input   string
		tag     string
		older   bool
	}{
		{"alpha without prefix", "v0.5.0", "0.6.0-alpha", "v0.6.0-alpha", false},
		{"alpha with prefix", "v0.5.0", "v0.6.0-alpha", "v0.6.0-alpha", false},
		{"older release", "v0.7.0", "v0.6.0-alpha", "v0.6.0-alpha", true},
		{"build metadata", "v0.6.0+previous", "0.6.0+build.42", "v0.6.0+build.42", false},
		{"development build pin", "dev", "0.6.0-alpha", "v0.6.0-alpha", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			setCurrentVersion(t, test.current)
			release, bodies, binary := upgradeReleaseFixture(t, test.tag)
			client := &fakeUpdateClient{release: release, bodies: bodies}
			output := &bytes.Buffer{}
			app := testApplication(output)
			app.updater = client
			target := stubExecutable(t, []byte("preserved until verified"))
			if err := executeUpgradeCommand(app, "--version", test.input); err != nil {
				t.Fatalf("upgrade --version failed: %v", err)
			}
			if client.latestCalls != 0 || len(client.requestedTags) != 1 || client.requestedTags[0] != test.tag || len(client.fetchedURLs) != 2 {
				t.Fatalf("discovery/download calls = latest %d, tags %v, assets %v", client.latestCalls, client.requestedTags, client.fetchedURLs)
			}
			for _, want := range []string{"Selected release " + test.tag, "explicit --version", "Verifying checksum", "Updated successfully"} {
				if !strings.Contains(output.String(), want) {
					t.Errorf("output missing %q: %s", want, output.String())
				}
			}
			if older := strings.Contains(output.String(), "explicitly selected older release"); older != test.older {
				t.Fatalf("older-release notice = %v, want %v: %s", older, test.older, output.String())
			}
			assertUpgradeTarget(t, target, binary)
		})
	}
}

// TestUpgradeExplicitSameVersionDoesNotFetch checks canonical tag equality,
// including retained build metadata, without reapplying an installed version.
func TestUpgradeExplicitSameVersionDoesNotFetch(t *testing.T) {
	for _, test := range []struct{ current, input, tag string }{
		{"v0.6.0-alpha", "0.6.0-alpha", "v0.6.0-alpha"},
		{"0.6.0", "v0.6.0", "v0.6.0"},
		{"v0.6.0+build.42", "0.6.0+build.42", "v0.6.0+build.42"},
	} {
		t.Run(test.input, func(t *testing.T) {
			setCurrentVersion(t, test.current)
			client := &fakeUpdateClient{release: &update.Release{Tag: test.tag}}
			output := &bytes.Buffer{}
			app := testApplication(output)
			app.updater = client
			original := []byte("unchanged same-version executable")
			target := stubExecutable(t, original)
			if err := executeUpgradeCommand(app, "--version="+test.input); err != nil {
				t.Fatal(err)
			}
			if client.latestCalls != 0 || len(client.requestedTags) != 1 || client.requestedTags[0] != test.tag || len(client.fetchedURLs) != 0 {
				t.Fatalf("same-version calls = latest %d, tags %v, assets %v", client.latestCalls, client.requestedTags, client.fetchedURLs)
			}
			if !strings.Contains(output.String(), "already the selected version") {
				t.Fatalf("same-version output = %q", output.String())
			}
			assertUpgradeTarget(t, target, original)
		})
	}
}

// TestUpgradeInvalidExplicitVersionDoesNotFetch checks that an explicitly empty
// flag and malformed semantic versions cannot fall back to Latest or download.
func TestUpgradeInvalidExplicitVersionDoesNotFetch(t *testing.T) {
	for _, value := range []string{"", " ", " v0.6.0", "v0.6.0\n", "v0.6", "0", "latest", "dev", "v01.6.0", "v0.06.0", "v0.6.00", "v0.6.0-alpha.01", "v0.6.0-", "v0.6.0+", "v0.6.0/alpha", "../v0.6.0", "v0.6.0?x=1"} {
		t.Run(value, func(t *testing.T) {
			client := &fakeUpdateClient{}
			app := testApplication(&bytes.Buffer{})
			app.updater = client
			original := []byte("unchanged invalid-version executable")
			target := stubExecutable(t, original)
			err := executeUpgradeCommand(app, "--version="+value)
			if err == nil || !strings.Contains(err.Error(), "invalid --version") {
				t.Fatalf("invalid pin error = %v", err)
			}
			if client.latestCalls != 0 || len(client.requestedTags) != 0 || len(client.fetchedURLs) != 0 {
				t.Fatalf("invalid pin consulted client: %+v", client)
			}
			assertUpgradeTarget(t, target, original)
		})
	}
}

// TestUpgradeExplicitFailurePreservesExecutable checks release selection and
// download failures cannot bypass the existing asset and checksum checks.
func TestUpgradeExplicitFailurePreservesExecutable(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*fakeUpdateClient)
		want   string
	}{
		{"offline", func(c *fakeUpdateClient) { c.byTagErr = update.ErrOffline }, "no network connection"},
		{"missing release", func(c *fakeUpdateClient) { c.byTagErr = errors.New("HTTP 404: no published release") }, "404"},
		{"wrong tag", func(c *fakeUpdateClient) { c.release.Tag = "v0.6.0" }, "does not match requested version"},
		{"missing response", func(c *fakeUpdateClient) { c.release = nil }, "does not match requested version"},
		{"draft response", func(c *fakeUpdateClient) { c.release.Draft = true }, "is a draft"},
		{"missing binary", func(c *fakeUpdateClient) { c.release.Assets = c.release.Assets[1:] }, "has no"},
		{"missing checksum", func(c *fakeUpdateClient) { c.release.Assets = c.release.Assets[:1] }, "no checksum manifest"},
		{"checksum mismatch", func(c *fakeUpdateClient) { c.bodies["https://example.invalid/checksums"] = []byte("wrong digest") }, "checksum mismatch"},
		{"download failure", func(c *fakeUpdateClient) { c.fetchErr = update.ErrOffline }, "download"},
		{"checksum download failure", func(c *fakeUpdateClient) { delete(c.bodies, "https://example.invalid/checksums") }, "download checksum manifest"},
	} {
		t.Run(test.name, func(t *testing.T) {
			setCurrentVersion(t, "v0.5.0")
			release, bodies, _ := upgradeReleaseFixture(t, "v0.6.0-alpha")
			client := &fakeUpdateClient{release: release, bodies: bodies}
			test.mutate(client)
			output := &bytes.Buffer{}
			app := testApplication(output)
			app.updater = client
			original := []byte("unchanged failed-upgrade executable")
			target := stubExecutable(t, original)
			err := executeUpgradeCommand(app, "--version", "0.6.0-alpha")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("upgrade error = %v, want %q", err, test.want)
			}
			if client.latestCalls != 0 || len(client.requestedTags) != 1 || client.requestedTags[0] != "v0.6.0-alpha" {
				t.Fatalf("pin failure discovery calls = latest %d, tags %v", client.latestCalls, client.requestedTags)
			}
			if strings.Contains(output.String(), "Updating binary") {
				t.Fatalf("failed verification reached apply: %s", output.String())
			}
			assertUpgradeTarget(t, target, original)
		})
	}
}

// TestUpgradeCommandWithoutVersionStillUsesLatest preserves default discovery,
// including the existing development-build no-update policy.
func TestUpgradeCommandWithoutVersionStillUsesLatest(t *testing.T) {
	for _, current := range []string{"v0.6.0", "dev"} {
		t.Run(current, func(t *testing.T) {
			setCurrentVersion(t, current)
			client := &fakeUpdateClient{release: &update.Release{Tag: "v0.6.0"}}
			output := &bytes.Buffer{}
			app := testApplication(output)
			app.updater = client
			original := []byte("unchanged default-upgrade executable")
			target := stubExecutable(t, original)
			if err := executeUpgradeCommand(app); err != nil {
				t.Fatal(err)
			}
			if client.latestCalls != 1 || len(client.requestedTags) != 0 || len(client.fetchedURLs) != 0 {
				t.Fatalf("default calls = latest %d, tags %v, assets %v", client.latestCalls, client.requestedTags, client.fetchedURLs)
			}
			if !strings.Contains(output.String(), "already up to date") {
				t.Fatalf("default output = %q", output.String())
			}
			assertUpgradeTarget(t, target, original)
		})
	}
}

// TestUpgradeVersionFlagCoexistsWithRootVersion exercises the real root tree:
// the upgrade pin remains a string while the root's version display stays Boolean.
func TestUpgradeVersionFlagCoexistsWithRootVersion(t *testing.T) {
	t.Run("upgrade pin", func(t *testing.T) {
		setCurrentVersion(t, "v0.6.0-alpha")
		output := &bytes.Buffer{}
		client := &fakeUpdateClient{release: &update.Release{Tag: "v0.6.0-alpha"}}
		app := testApplication(output)
		app.updater = client
		root := NewRootCommand(strings.NewReader(""), output, output)
		upgrade, _, err := root.Find([]string{"upgrade"})
		if err != nil || upgrade.Name() != "upgrade" {
			t.Fatalf("find upgrade command: %v", err)
		}
		// Replace only delivery dependencies, not the actual flag or RunE logic.
		// Configuration loading is unrelated to this command-tree flag contract.
		root.PersistentPreRunE = nil
		upgrade.RunE = app.runUpgrade
		root.SetArgs([]string{"upgrade", "--version", "0.6.0-alpha"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if client.latestCalls != 0 || len(client.requestedTags) != 1 || client.requestedTags[0] != "v0.6.0-alpha" || len(client.fetchedURLs) != 0 {
			t.Fatalf("root upgrade calls = latest %d, tags %v, assets %v", client.latestCalls, client.requestedTags, client.fetchedURLs)
		}
		if !strings.Contains(output.String(), "already the selected version") {
			t.Fatalf("root upgrade output = %q", output.String())
		}
		if flag := upgrade.Flags().Lookup("version"); flag == nil || flag.Value.Type() != "string" {
			t.Fatalf("upgrade --version is not the local string pin: %+v", flag)
		}
	})
	t.Run("root version display", func(t *testing.T) {
		output := &bytes.Buffer{}
		root := NewRootCommand(strings.NewReader(""), output, output)
		root.RunE = func(_ *cobra.Command, _ []string) error {
			t.Fatal("root --version unexpectedly ran the root workflow")
			return nil
		}
		root.SetArgs([]string{"--version"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), root.Version) || strings.Contains(output.String(), "Selected release") {
			t.Fatalf("root --version output = %q, want build version %q", output.String(), root.Version)
		}
		if flag := root.Flags().Lookup("version"); flag == nil || flag.Value.Type() != "bool" {
			t.Fatalf("root --version is not the Boolean display flag: %+v", flag)
		}
	})
}
