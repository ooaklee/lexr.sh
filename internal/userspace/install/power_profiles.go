package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

const (
	// powerProfilesSourceIdentityPath anchors discovery to maintained source policy.
	powerProfilesSourceIdentityPath = "userspace/power-profiles-daemon-sp11/BASE.txt"
	// powerProfilesBuildPath is the sole repository-relative qualification output.
	powerProfilesBuildPath = "build/lexr/power-profiles-daemon-sp11.1"
	// powerProfilesPackageName is the exact installable ARM64 package basename.
	powerProfilesPackageName = "power-profiles-daemon_0.30-2+sp11.1_arm64.deb"
)

// powerProfilesSourceIdentity prevents unrelated directories satisfying discovery.
var powerProfilesSourceIdentity = immutableFile{
	name: powerProfilesSourceIdentityPath, sha256: "abbf3ba9b469386d38a73761b19694d2066e289efad8e528a7bcf08f19fce14f", size: 880,
}

// powerProfilesSpec independently pins the complete local qualification output.
var powerProfilesSpec = releaseSpec{
	component: PowerProfilesComponent,
	tag:       "local-qualification-0.30-2+sp11.1",
	files: []immutableFile{
		{name: "SHA256SUMS", sha256: "2ee944e94977745f28c719d3eaa8e6245c4bbcf2b188e39b57ebe7c785ae2e63", size: 346},
		{name: "power-profiles-daemon_0.30-2+sp11.1_arm64.buildinfo", sha256: "1fc67afe276ab76f906f73d72ee30fcb844fd981776330e0f9939243b33bac8d", size: 9175},
		{name: "power-profiles-daemon_0.30-2+sp11.1_arm64.changes", sha256: "e2f9cb46ec50077b0e0ca80ca97f60c925ed23faf91e1b822e1681e4565ea98d", size: 1769},
		{name: powerProfilesPackageName, sha256: "e6321ca68df9c395a641364d0eb7203cd233e0fe13758a6266446935f6492207", size: 57730},
	},
}

// powerProfilesActivationCommands is the bounded live service transition.
var powerProfilesActivationCommands = []Command{{
	Name: "/usr/bin/systemctl", Args: []string{"restart", "power-profiles-daemon.service"},
}}

// PowerProfiles installs the exact local SP11 qualification package from an
// OE checkout. Its compiled file identities are the independent authority;
// the repository is only a bounded discovery and provenance location.
func (installer *Installer) PowerProfiles(ctx context.Context, options Options) (Result, error) {
	root, err := normalizePowerProfilesRoot(options.Root)
	if err != nil {
		return Result{}, err
	}
	if root != string(filepath.Separator) {
		return Result{}, errors.New("power-profiles package installation is supported only for target root /")
	}
	repositoryRoot, err := resolvePowerProfilesRepositoryRoot(options.RepositoryRoot)
	if err != nil {
		return Result{}, err
	}
	directory := filepath.Join(repositoryRoot, filepath.FromSlash(powerProfilesBuildPath))
	packagePath, packageFile, err := verifyPowerProfilesInput(directory)
	if err != nil {
		return Result{}, err
	}
	installArgs := []string{"install", "--yes", "--no-install-recommends", "--", packagePath}
	result := Result{
		Component:          PowerProfilesComponent,
		Root:               root,
		DryRun:             options.DryRun,
		Command:            &Command{Name: "apt-get", Args: append([]string(nil), installArgs...)},
		Commands:           cloneInstallCommands(powerProfilesActivationCommands),
		ActivationRequired: true,
	}
	if options.DryRun {
		return result, nil
	}
	if err := installer.requireRoot(false); err != nil {
		return Result{}, err
	}
	stage, err := createPrivateInstallStaging("lexr-power-profiles-install-*")
	if err != nil {
		return Result{}, fmt.Errorf("create private power-profiles staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	stagedPackage := filepath.Join(stage, packageFile.name)
	if err := atomicCopyVerified(packagePath, stagedPackage, 0o600, packageFile.sha256, packageFile.size); err != nil {
		return Result{}, fmt.Errorf("stage verified power-profiles package: %w", err)
	}
	executionArgs := []string{"install", "--yes", "--no-install-recommends", "--", stagedPackage}
	if err := installer.runner.Run(ctx, platform.Command{Name: "apt-get", Args: executionArgs}); err != nil {
		return result, fmt.Errorf("install exact SP11 power-profiles package: %w", err)
	}
	result.FilesInstalled = true
	if err := installer.activateCommands(ctx, result.Commands); err != nil {
		result.ActivationError = err.Error()
		return result, fmt.Errorf("activate SP11 power-profiles integration: %w", err)
	}
	result.ActivationComplete = true
	return result, nil
}

// normalizePowerProfilesRoot canonicalises the package-manager target boundary.
func normalizePowerProfilesRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = string(filepath.Separator)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve userspace target root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve userspace target root: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("userspace target root is not a directory: %s", canonical)
	}
	return filepath.Clean(canonical), nil
}

// resolvePowerProfilesRepositoryRoot finds a checkout with the compiled source identity.
func resolvePowerProfilesRepositoryRoot(configured string) (string, error) {
	current := strings.TrimSpace(configured)
	explicit := current != ""
	if !explicit {
		var err error
		current, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("read current directory: %w", err)
		}
	}
	absolute, err := filepath.Abs(current)
	if err != nil {
		return "", fmt.Errorf("resolve OE repository root: %w", err)
	}
	for {
		info, statErr := os.Lstat(absolute)
		if statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			identity := filepath.Join(absolute, filepath.FromSlash(powerProfilesSourceIdentityPath))
			digest, identityInfo, hashErr := hashRegularNoFollowBounded(identity, 16<<10)
			if hashErr == nil && identityInfo.Size() == powerProfilesSourceIdentity.size && digest == powerProfilesSourceIdentity.sha256 {
				canonical, evalErr := filepath.EvalSymlinks(absolute)
				if evalErr == nil && filepath.Clean(canonical) == filepath.Clean(absolute) {
					return filepath.Clean(absolute), nil
				}
			}
		}
		if explicit {
			return "", fmt.Errorf("configured repository root does not contain the authenticated SP11 power-profiles source: %s", absolute)
		}
		parent := filepath.Dir(absolute)
		if parent == absolute || strings.TrimSpace(parent) == "" {
			return "", errors.New("could not find the OE repository; run from its checkout or pass --repository-root")
		}
		absolute = parent
	}
}

// verifyPowerProfilesInput proves the closed build output against compiled authority.
func verifyPowerProfilesInput(directory string) (string, immutableFile, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", immutableFile{}, fmt.Errorf("power-profiles build output must be a real directory: %s", directory)
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil || filepath.Clean(canonical) != filepath.Clean(directory) {
		return "", immutableFile{}, fmt.Errorf("power-profiles build output route contains a symbolic link: %s", directory)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", immutableFile{}, fmt.Errorf("read power-profiles build output: %w", err)
	}
	expected := make(map[string]immutableFile, len(powerProfilesSpec.files))
	for _, file := range powerProfilesSpec.files {
		expected[file.name] = file
	}
	if len(entries) != len(expected) {
		return "", immutableFile{}, fmt.Errorf("power-profiles build output contains %d files, expected %d", len(entries), len(expected))
	}
	for _, entry := range entries {
		file, ok := expected[entry.Name()]
		if !ok {
			return "", immutableFile{}, fmt.Errorf("power-profiles build output contains unexpected file %q", entry.Name())
		}
		path, err := requireRegularBundleFile(directory, entry.Name())
		if err != nil {
			return "", immutableFile{}, err
		}
		digest, fileInfo, err := hashRegularNoFollowBounded(path, file.size)
		if err != nil || fileInfo.Size() != file.size || digest != file.sha256 {
			return "", immutableFile{}, fmt.Errorf("power-profiles build output identity mismatch for %s", entry.Name())
		}
	}
	if err := verifyChecksums(filepath.Join(directory, "SHA256SUMS"), powerProfilesSpec); err != nil {
		return "", immutableFile{}, err
	}
	packageFile := expected[powerProfilesPackageName]
	return filepath.Join(directory, powerProfilesPackageName), packageFile, nil
}
