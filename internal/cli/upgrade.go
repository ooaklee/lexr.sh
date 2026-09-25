package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/ooaklee/lexr.sh/internal/update"
	"github.com/ooaklee/lexr.sh/internal/version"
)

// updateRepository is the GitHub repository that publishes Lexr releases.
const updateRepository = "ooaklee/lexr.sh"

// currentBuildVersion is indirected so tests can simulate a release build
// with a concrete semantic version.
var currentBuildVersion = version.Info

// updateClient abstracts release discovery so tests can serve fixtures.
type updateClient interface {
	// Latest fetches the newest published release metadata.
	Latest(ctx context.Context) (*update.Release, error)
	// Fetch downloads one release asset into memory.
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// newUpgradeCommand self-updates the installed executable from the latest
// published GitHub release, verifying the release checksum before applying.
func (a *application) newUpgradeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Update lexr to the latest release",
		Long: "Update lexr to the latest release.\n\n" +
			"Checks GitHub for the newest published version, downloads the matching\n" +
			"release for this platform, verifies its SHA-256 checksum, and atomically\n" +
			"replaces the running executable. A network connection is required.",
		Args: cobra.NoArgs,
		RunE: a.runUpgrade,
	}
}

// runUpgrade performs the check-download-verify-apply update flow.
func (a *application) runUpgrade(command *cobra.Command, _ []string) error {
	return a.upgradeWith(command.Context(), a.updater)
}

// upgradeWith runs the upgrade flow against a specific client so tests can
// inject fixture servers without touching the network.
func (a *application) upgradeWith(ctx context.Context, client updateClient) error {
	buildVersion, _, _ := currentBuildVersion()
	release, err := client.Latest(ctx)
	if err != nil {
		if errors.Is(err, update.ErrOffline) {
			return errors.New("unable to check for updates: no network connection")
		}
		return fmt.Errorf("unable to check for updates: %w", err)
	}
	if !update.IsNewer(buildVersion, release.Tag) {
		_, err := fmt.Fprintf(a.out, "lexr %s is already up to date.\n", buildVersion)
		return err
	}
	return a.applyRelease(ctx, client, release, buildVersion)
}

// applyRelease downloads, verifies, and installs one newer release.
func (a *application) applyRelease(ctx context.Context, client updateClient, release *update.Release, buildVersion string) error {
	binaryName := update.AssetName(release.Tag, runtime.GOOS, runtime.GOARCH)
	binaryAsset := findAsset(release, binaryName)
	if binaryAsset == nil {
		return fmt.Errorf("release %s has no %s asset for this platform", release.Tag, binaryName)
	}
	fmt.Fprintf(a.out, "Downloading %s\n", release.Tag)
	binary, err := client.Fetch(ctx, binaryAsset.URL)
	if err != nil {
		return fmt.Errorf("download %s: %w", binaryName, err)
	}
	if manifestAsset := findAsset(release, update.ChecksumAssetName(release.Tag)); manifestAsset != nil {
		fmt.Fprintln(a.out, "Verifying checksum")
		manifest, err := client.Fetch(ctx, manifestAsset.URL)
		if err != nil {
			return fmt.Errorf("download checksum manifest: %w", err)
		}
		if !update.VerifyChecksum(manifest, binaryName, binary) {
			return fmt.Errorf("checksum mismatch for %s; the download was corrupted or tampered with", binaryName)
		}
	} else {
		return fmt.Errorf("release %s publishes no checksum manifest; refusing to update", release.Tag)
	}
	fmt.Fprintln(a.out, "Updating binary")
	if err := update.Apply("", binary); err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.out, "Updated successfully: %s -> %s\n", buildVersion, release.Tag)
	return err
}

// findAsset returns the named release asset, or nil when absent.
func findAsset(release *update.Release, name string) *update.Asset {
	for i := range release.Assets {
		if release.Assets[i].Name == name {
			return &release.Assets[i]
		}
	}
	return nil
}

// noticeForLatestRelease returns the upgrade notice for a newer release, or
// an empty string when there is none, the check failed, or the check was
// rate-limited. Failures stay quiet so `lexr version` never errors offline.
func noticeForLatestRelease(ctx context.Context, client updateClient, buildVersion string, out io.Writer) {
	release, err := client.Latest(ctx)
	if err != nil || !update.IsNewer(buildVersion, release.Tag) {
		return
	}
	_, _ = fmt.Fprintf(out, "\nA new release is available: %s (current %s). Run `lexr upgrade` to update.\n", release.Tag, buildVersion)
}
