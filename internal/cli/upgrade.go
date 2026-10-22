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

// applyUpgrade retains the atomic updater while allowing tests to use a
// temporary executable rather than replace the running test binary.
var applyUpgrade = update.Apply

// updateClient abstracts release discovery so tests can serve fixtures.
type updateClient interface {
	// Latest fetches the newest published release metadata.
	Latest(ctx context.Context) (*update.Release, error)
	// ByTag fetches one explicitly selected published release.
	ByTag(ctx context.Context, version string) (*update.Release, error)
	// Fetch downloads one release asset into memory.
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// newUpgradeCommand self-updates from the latest or an explicitly selected
// published GitHub release, verifying the release checksum before applying.
func (a *application) newUpgradeCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "upgrade",
		Short: "Update lexr to the latest or an exact published release",
		Long: "Update lexr to the latest release, or select an exact version with --version.\n\n" +
			"Checks GitHub for the newest published version, downloads the matching\n" +
			"release for this platform, verifies its SHA-256 checksum, and atomically\n" +
			"replaces the running executable. An explicit version can select a published\n" +
			"prerelease or older release. A network connection is required.",
		Args: cobra.NoArgs,
		RunE: a.runUpgrade,
	}
	command.Flags().String("version", "", "Install an exact published version, including a prerelease or older release")
	return command
}

// runUpgrade performs the check-download-verify-apply update flow.
func (a *application) runUpgrade(command *cobra.Command, _ []string) error {
	if command.Flags().Changed("version") {
		selected, err := command.Flags().GetString("version")
		if err != nil {
			return err
		}
		return a.upgradeToWith(command.Context(), a.updater, selected)
	}
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

// upgradeToWith installs an intentional version pin without falling back to
// Latest or allowing a mismatched release response to select another version.
func (a *application) upgradeToWith(ctx context.Context, client updateClient, selected string) error {
	tag, err := update.CanonicalReleaseTag(selected)
	if err != nil {
		return fmt.Errorf("invalid --version: %w", err)
	}
	release, err := client.ByTag(ctx, tag)
	if err != nil {
		if errors.Is(err, update.ErrOffline) {
			return fmt.Errorf("unable to find release %s: no network connection", tag)
		}
		return fmt.Errorf("unable to find release %s: %w", tag, err)
	}
	if release == nil || release.Tag != tag {
		return fmt.Errorf("release response does not match requested version %s; refusing to update", tag)
	}
	if release.Draft {
		return fmt.Errorf("release %s is a draft; refusing to update", tag)
	}
	buildVersion, _, _ := currentBuildVersion()
	if current, err := update.CanonicalReleaseTag(buildVersion); err == nil && current == tag {
		_, err := fmt.Fprintf(a.out, "lexr %s is already the selected version; no update needed.\n", tag)
		return err
	}
	if _, err := fmt.Fprintf(a.out, "Selected release %s (explicit --version).\n", tag); err != nil {
		return err
	}
	if update.IsNewer(tag, buildVersion) {
		if _, err := fmt.Fprintf(a.out, "Installing an explicitly selected older release: %s -> %s.\n", buildVersion, tag); err != nil {
			return err
		}
	}
	return a.applyRelease(ctx, client, release, buildVersion)
}

// applyRelease downloads, verifies, and installs one selected release.
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
	if err := applyUpgrade("", binary); err != nil {
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
