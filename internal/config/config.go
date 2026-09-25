// Package config loads the Lexr YAML configuration file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// Config contains Lexr command settings.
type Config struct {
	// Version is the configuration schema version.
	Version int `yaml:"version"`
	// Profile selects a canonical hardware identity or automatic live detection.
	Profile string `yaml:"profile" config:"verbatim"`
	// Global contains the global settings.
	Global GlobalConfig `yaml:"global"`
	// Catalog contains the catalog settings.
	Catalog CatalogConfig `yaml:"catalog"`
	// Clean contains the clean settings.
	Clean CleanConfig `yaml:"clean"`
	// Doctor contains the doctor settings.
	Doctor DoctorConfig `yaml:"doctor"`
	// Handoff contains the handoff settings.
	Handoff HandoffConfig `yaml:"handoff"`
	// Image contains the image settings.
	Image ImageConfig `yaml:"image"`
	// Kernel contains the kernel settings.
	Kernel KernelConfig `yaml:"kernel"`
	// Userspace contains the userspace settings.
	Userspace UserspaceConfig `yaml:"userspace"`
	// Wizard contains the wizard settings.
	Wizard WizardConfig `yaml:"wizard"`
}

// GlobalConfig contains settings for global.
type GlobalConfig struct {
	// Catalog specifies the catalog.
	Catalog string `yaml:"catalog"`
	// UserspaceCatalog specifies the userspace catalog.
	UserspaceCatalog string `yaml:"userspace_catalog"`
}

// CatalogConfig contains settings for catalog.
type CatalogConfig struct {
	// List contains the list settings.
	List CatalogListConfig `yaml:"list"`
	// Show contains the show settings.
	Show CatalogShowConfig `yaml:"show"`
	// Validate contains the validate settings.
	Validate CatalogValidateConfig `yaml:"validate"`
}

// CatalogListConfig contains settings for catalog list.
type CatalogListConfig struct {
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// CatalogShowConfig contains settings for catalog show.
type CatalogShowConfig struct {
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// CatalogValidateConfig contains settings for catalog validate.
type CatalogValidateConfig struct {
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// CleanConfig contains settings for clean.
type CleanConfig struct {
	// Scan contains the scan settings.
	Scan CleanScanConfig `yaml:"scan"`
	// Plan contains the plan settings.
	Plan CleanPlanConfig `yaml:"plan"`
	// Apply contains the apply settings.
	Apply CleanApplyConfig `yaml:"apply"`
	// Restore contains the restore settings.
	Restore CleanRestoreConfig `yaml:"restore"`
}

// CleanScanConfig contains settings for clean scan.
type CleanScanConfig struct {
	// Root is the target filesystem root.
	Root string `yaml:"root"`
	// UserHome is the target user home directory.
	UserHome string `yaml:"user_home"`
	// Feature selects the requested features.
	Feature []string `yaml:"feature"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// CleanPlanConfig contains settings for clean plan.
type CleanPlanConfig struct {
	// Root is the target filesystem root.
	Root string `yaml:"root"`
	// UserHome is the target user home directory.
	UserHome string `yaml:"user_home"`
	// Feature selects the requested features.
	Feature []string `yaml:"feature"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
	// Output is the output path.
	Output string `yaml:"output"`
}

// CleanApplyConfig contains settings for clean apply.
type CleanApplyConfig struct {
	// Root is the target filesystem root.
	Root string `yaml:"root"`
	// UserHome is the target user home directory.
	UserHome string `yaml:"user_home"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
	// Plan specifies the plan.
	Plan string `yaml:"plan"`
}

// CleanRestoreConfig contains settings for clean restore.
type CleanRestoreConfig struct {
	// Root is the target filesystem root.
	Root string `yaml:"root"`
	// UserHome is the target user home directory.
	UserHome string `yaml:"user_home"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// DoctorConfig contains settings for doctor.
type DoctorConfig struct {
	// Workspace specifies the workspace.
	Workspace string `yaml:"workspace"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
	// Boot contains the boot settings.
	Boot DoctorBootConfig `yaml:"boot"`
	// Hardware contains the hardware settings.
	Hardware DoctorHardwareConfig `yaml:"hardware"`
	// Userspace contains the userspace settings.
	Userspace DoctorUserspaceConfig `yaml:"userspace"`
}

// DoctorBootConfig contains settings for doctor boot.
type DoctorBootConfig struct {
	// Root is the target filesystem root.
	Root string `yaml:"root"`
	// TargetABI specifies the target ABI.
	TargetABI string `yaml:"target_abi"`
	// FallbackABI specifies the fallback ABI.
	FallbackABI string `yaml:"fallback_abi"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// DoctorHardwareConfig contains settings for doctor hardware.
type DoctorHardwareConfig struct {
	// Root is the target filesystem root.
	Root string `yaml:"root"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// DoctorUserspaceConfig contains settings for doctor userspace.
type DoctorUserspaceConfig struct {
	// Root is the target filesystem root.
	Root string `yaml:"root"`
	// UserHome is the target user home directory.
	UserHome string `yaml:"user_home"`
	// Kernel specifies the kernel.
	Kernel string `yaml:"kernel"`
	// Feature selects the requested features.
	Feature []string `yaml:"feature"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// HandoffConfig contains settings for handoff.
type HandoffConfig struct {
	// Restore contains the restore settings.
	Restore HandoffRestoreConfig `yaml:"restore"`
	// Apply contains the apply settings.
	Apply HandoffApplyConfig `yaml:"apply"`
	// Import contains the import settings.
	Import HandoffImportConfig `yaml:"import"`
	// List contains the list settings.
	List HandoffListConfig `yaml:"list"`
	// Purge contains the purge settings.
	Purge HandoffPurgeConfig `yaml:"purge"`
}

// HandoffRestoreConfig contains settings for handoff restore.
type HandoffRestoreConfig struct {
	// TargetRoot specifies the target root.
	TargetRoot string `yaml:"target_root"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// HandoffApplyConfig contains settings for handoff apply.
type HandoffApplyConfig struct {
	// Store specifies the store.
	Store string `yaml:"store"`
	// IdentityRoot specifies the identity root.
	IdentityRoot string `yaml:"identity_root"`
	// TargetRoot specifies the target root.
	TargetRoot string `yaml:"target_root"`
	// Feature selects the requested features.
	Feature []string `yaml:"feature"`
	// ADSPPolicy specifies the ADSP policy.
	ADSPPolicy string `yaml:"adsp_policy"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// HandoffImportConfig contains settings for handoff import.
type HandoffImportConfig struct {
	// Store specifies the store.
	Store string `yaml:"store"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// HandoffListConfig contains settings for handoff list.
type HandoffListConfig struct {
	// Store specifies the store.
	Store string `yaml:"store"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// HandoffPurgeConfig contains settings for handoff purge.
type HandoffPurgeConfig struct {
	// Store specifies the store.
	Store string `yaml:"store"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// ImageConfig contains settings for image.
type ImageConfig struct {
	// Devices contains the devices settings.
	Devices ImageDevicesConfig `yaml:"devices"`
	// Write contains the write settings.
	Write ImageWriteConfig `yaml:"write"`
	// Create contains the create settings.
	Create ImageCreateConfig `yaml:"create"`
	// Validate contains the validate settings.
	Validate ImageValidateConfig `yaml:"validate"`
	// Release contains the release settings.
	Release ImageReleaseConfig `yaml:"release"`
}

// ImageDevicesConfig contains settings for image devices.
type ImageDevicesConfig struct {
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// ImageWriteConfig contains settings for image write. The destructive target
// and its confirmation are command-line-only so a file can never consent to
// overwriting removable media.
type ImageWriteConfig struct {
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// ImageCreateConfig contains settings for image create.
type ImageCreateConfig struct {
	// CatalogID specifies the catalog ID.
	CatalogID string `yaml:"catalog_id"`
	// Source specifies the source.
	Source string `yaml:"source"`
	// SourceSHA256 specifies the source SHA-256 digest.
	SourceSHA256 string `yaml:"source_sha256"`
	// RefreshSource refreshes the cached source.
	RefreshSource bool `yaml:"refresh_source"`
	// KernelDir specifies the kernel directory.
	KernelDir string `yaml:"kernel_dir"`
	// KernelRepository specifies the kernel repository.
	KernelRepository string `yaml:"kernel_repository"`
	// KernelRelease specifies the kernel release.
	KernelRelease string `yaml:"kernel_release"`
	// CacheDir specifies the cache directory.
	CacheDir string `yaml:"cache_dir"`
	// WorkspaceDir specifies the workspace directory.
	WorkspaceDir string `yaml:"workspace_dir"`
	// CompanionSourceDir specifies the companion source directory.
	CompanionSourceDir string `yaml:"companion_source_dir"`
	// CompanionUserspace specifies the companion userspace.
	CompanionUserspace []string `yaml:"companion_userspace"`
	// Output is the output path.
	Output string `yaml:"output"`
	// KeepWorkspace retains the build workspace.
	KeepWorkspace bool `yaml:"keep_workspace"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// ImageValidateConfig contains settings for image validate.
type ImageValidateConfig struct {
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// ImageReleaseConfig contains settings for image release.
type ImageReleaseConfig struct {
	// Prepare contains the prepare settings.
	Prepare ImageReleasePrepareConfig `yaml:"prepare"`
	// Validate contains the validate settings.
	Validate ImageReleaseValidateConfig `yaml:"validate"`
}

// ImageReleasePrepareConfig contains settings for image release prepare.
type ImageReleasePrepareConfig struct {
	// RepositoryRoot specifies the repository root.
	RepositoryRoot string `yaml:"repository_root"`
	// ReleaseName specifies the release name.
	ReleaseName string `yaml:"release_name"`
	// OutDir specifies the out directory.
	OutDir string `yaml:"out_dir" config:"verbatim"`
	// PartSizeBytes sets the release part size in bytes.
	PartSizeBytes int64 `yaml:"part_size_bytes"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// ImageReleaseValidateConfig contains settings for image release validate.
type ImageReleaseValidateConfig struct {
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// KernelConfig contains settings for kernel.
type KernelConfig struct {
	// Boot contains the boot settings.
	Boot KernelBootConfig `yaml:"boot"`
	// Release contains the release settings.
	Release KernelReleaseConfig `yaml:"release"`
	// Inspect contains local kernel bundle inspection defaults.
	Inspect KernelInspectConfig `yaml:"inspect"`
	// Preflight contains the preflight settings.
	Preflight KernelPreflightConfig `yaml:"preflight"`
	// Install contains the install settings.
	Install KernelInstallConfig `yaml:"install"`
	// Build contains the build settings.
	Build KernelBuildConfig `yaml:"build"`
}

// KernelBootConfig contains settings for kernel boot.
type KernelBootConfig struct {
	// Refresh contains the refresh settings.
	Refresh KernelBootRefreshConfig `yaml:"refresh"`
	// RegisterArch contains the register-arch settings.
	RegisterArch KernelBootRegisterArchConfig `yaml:"register-arch"`
}

// KernelBootRefreshConfig contains settings for kernel boot refresh.
type KernelBootRefreshConfig struct {
	// Root is the target filesystem root.
	Root string `yaml:"root"`
	// ABI specifies the ABI.
	ABI string `yaml:"abi"`
}

// KernelBootRegisterArchConfig contains settings for kernel boot register-arch.
type KernelBootRegisterArchConfig struct {
	// ArchRoot specifies the arch root.
	ArchRoot string `yaml:"arch_root"`
	// GrubDirectory specifies the GRUB directory.
	GrubDirectory string `yaml:"grub_directory"`
	// ESP specifies the EFI system partition.
	ESP string `yaml:"esp"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
}

// KernelReleaseConfig contains settings for kernel release.
type KernelReleaseConfig struct {
	// Prepare contains the prepare settings.
	Prepare KernelReleasePrepareConfig `yaml:"prepare"`
	// Validate contains the validate settings.
	Validate KernelReleaseValidateConfig `yaml:"validate"`
	// List contains the list settings.
	List KernelReleaseListConfig `yaml:"list"`
	// Download contains the download settings.
	Download KernelReleaseDownloadConfig `yaml:"download"`
}

// KernelReleasePrepareConfig contains settings for kernel release prepare.
type KernelReleasePrepareConfig struct {
	// BuildDir specifies the build directory.
	BuildDir string `yaml:"build_dir"`
	// OutputDir specifies the output directory.
	OutputDir string `yaml:"output_dir"`
	// ReleaseName specifies the release name.
	ReleaseName string `yaml:"release_name"`
	// Source specifies the source.
	Source []string `yaml:"source"`
	// Licence specifies the licence.
	Licence []string `yaml:"licence"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// KernelReleaseValidateConfig contains settings for kernel release validate.
type KernelReleaseValidateConfig struct {
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// KernelReleaseListConfig contains settings for kernel release list.
type KernelReleaseListConfig struct {
	// Repository specifies the repository.
	Repository string `yaml:"repository"`
	// Limit limits the number of releases listed.
	Limit int `yaml:"limit"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// KernelReleaseDownloadConfig contains settings for kernel release download.
type KernelReleaseDownloadConfig struct {
	// Repository specifies the repository.
	Repository string `yaml:"repository"`
	// OutputDir specifies the output directory.
	OutputDir string `yaml:"output_dir"`
	// Headers includes matching kernel header packages.
	Headers bool `yaml:"headers"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// KernelInspectConfig contains settings for kernel inspect.
type KernelInspectConfig struct {
	// PackageSet specifies the package set.
	PackageSet string `yaml:"package_set"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// KernelPreflightConfig contains settings for kernel preflight.
type KernelPreflightConfig struct {
	// Root is the target filesystem root.
	Root string `yaml:"root"`
	// FallbackABI specifies the fallback ABI.
	FallbackABI string `yaml:"fallback_abi"`
	// RunningABI specifies the running ABI.
	RunningABI string `yaml:"running_abi"`
	// PackageSet specifies the package set.
	PackageSet string `yaml:"package_set"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// KernelInstallConfig contains settings for kernel install.
type KernelInstallConfig struct {
	// Root is the target filesystem root.
	Root string `yaml:"root"`
	// FallbackABI specifies the fallback ABI.
	FallbackABI string `yaml:"fallback_abi"`
	// RunningABI specifies the running ABI.
	RunningABI string `yaml:"running_abi"`
	// PackageSet specifies the package set.
	PackageSet string `yaml:"package_set"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
}

// KernelBuildConfig contains settings for kernel build.
type KernelBuildConfig struct {
	// RepositoryRoot specifies the repository root.
	RepositoryRoot string `yaml:"repository_root"`
	// GitURL specifies the git URL.
	GitURL string `yaml:"git_url"`
	// GitBranch specifies the git branch.
	GitBranch string `yaml:"git_branch"`
	// SourceDir specifies a clean local Git worktree to snapshot.
	SourceDir string `yaml:"source_dir" config:"verbatim"`
	// BootImageMode specifies the boot image mode.
	BootImageMode string `yaml:"boot_image_mode"`
	// WorkDir specifies the work directory.
	WorkDir string `yaml:"work_dir" config:"verbatim"`
	// OutputDir specifies the output directory.
	OutputDir string `yaml:"output_dir" config:"verbatim"`
	// Jobs sets the number of build jobs.
	Jobs int `yaml:"jobs"`
	// ResetSource resets the build source.
	ResetSource bool `yaml:"reset_source"`
	// SkipClean skips cleaning the build source.
	SkipClean bool `yaml:"skip_clean"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
}

// UserspaceConfig contains settings for userspace.
type UserspaceConfig struct {
	// Catalog contains the catalog settings.
	Catalog UserspaceCatalogConfig `yaml:"catalog"`
	// List contains the list settings.
	List UserspaceListConfig `yaml:"list"`
	// Show contains the show settings.
	Show UserspaceShowConfig `yaml:"show"`
	// Status contains the status settings.
	Status UserspaceStatusConfig `yaml:"status"`
	// Pull contains the pull settings.
	Pull UserspacePullConfig `yaml:"pull"`
	// Build contains the build settings.
	Build UserspaceBuildConfig `yaml:"build"`
	// Install contains the install settings.
	Install UserspaceInstallConfig `yaml:"install"`
	// Audio contains the audio settings.
	Audio UserspaceAudioConfig `yaml:"audio"`
	// Camera contains the camera settings.
	Camera UserspaceCameraConfig `yaml:"camera"`
}

// UserspaceCatalogConfig contains settings for userspace catalog.
type UserspaceCatalogConfig struct {
	// Validate contains the validate settings.
	Validate UserspaceCatalogValidateConfig `yaml:"validate"`
}

// UserspaceCatalogValidateConfig has no settings for userspace catalog validate.
type UserspaceCatalogValidateConfig struct{}

// UserspaceListConfig contains settings for userspace list.
type UserspaceListConfig struct {
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// UserspaceShowConfig contains settings for userspace show.
type UserspaceShowConfig struct {
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// UserspaceStatusConfig contains settings for userspace status.
type UserspaceStatusConfig struct {
	// Root is the target filesystem root.
	Root string `yaml:"root"`
	// UserHome is the target user home directory.
	UserHome string `yaml:"user_home"`
	// Kernel specifies the kernel.
	Kernel string `yaml:"kernel"`
	// Feature selects the requested features.
	Feature []string `yaml:"feature"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// UserspacePullConfig contains settings for userspace pull.
type UserspacePullConfig struct {
	// CacheDir specifies the cache directory.
	CacheDir string `yaml:"cache_dir"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// UserspaceBuildConfig contains settings for userspace build.
type UserspaceBuildConfig struct {
	// RepositoryRoot specifies the repository root.
	RepositoryRoot string `yaml:"repository_root"`
	// OutputDir specifies the output directory.
	OutputDir string `yaml:"output_dir" config:"verbatim"`
	// Image specifies the image.
	Image string `yaml:"image"`
	// WorkVolume specifies the work volume.
	WorkVolume string `yaml:"work_volume"`
	// Jobs sets the number of build jobs.
	Jobs int `yaml:"jobs"`
	// MinimumFreeGiB sets the minimum free space in GiB.
	MinimumFreeGiB int `yaml:"minimum_free_gib"`
	// NoPull disables pulling the build image.
	NoPull bool `yaml:"no_pull"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// UserspaceInstallConfig contains settings for userspace install.
type UserspaceInstallConfig struct {
	// From is the installation source path.
	From string `yaml:"from"`
	// RepositoryRoot specifies the repository root.
	RepositoryRoot string `yaml:"repository_root"`
	// CameraAuthoritySHA256 specifies the camera authority SHA-256 digest.
	CameraAuthoritySHA256 string `yaml:"camera_authority_sha256"`
	// Root is the target filesystem root.
	Root string `yaml:"root"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
	// Activate activates the installed userspace component.
	Activate bool `yaml:"activate"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// UserspaceAudioConfig contains settings for userspace audio.
type UserspaceAudioConfig struct {
	// Release contains the release settings.
	Release UserspaceAudioReleaseConfig `yaml:"release"`
}

// UserspaceAudioReleaseConfig contains settings for userspace audio release.
type UserspaceAudioReleaseConfig struct {
	// Prepare contains the prepare settings.
	Prepare UserspaceAudioReleasePrepareConfig `yaml:"prepare"`
	// Validate contains the validate settings.
	Validate UserspaceAudioReleaseValidateConfig `yaml:"validate"`
}

// UserspaceAudioReleasePrepareConfig contains settings for userspace audio release prepare.
type UserspaceAudioReleasePrepareConfig struct {
	// RepositoryRoot specifies the repository root.
	RepositoryRoot string `yaml:"repository_root"`
	// SourceRoot specifies the source root.
	SourceRoot string `yaml:"source_root"`
	// Tag specifies the tag.
	Tag string `yaml:"tag"`
	// KernelTag specifies the kernel tag.
	KernelTag string `yaml:"kernel_tag"`
	// KernelABI specifies the kernel ABI.
	KernelABI string `yaml:"kernel_abi"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// UserspaceAudioReleaseValidateConfig contains settings for userspace audio release validate.
type UserspaceAudioReleaseValidateConfig struct {
	// RepositoryRoot specifies the repository root.
	RepositoryRoot string `yaml:"repository_root"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// UserspaceCameraConfig contains settings for userspace camera.
type UserspaceCameraConfig struct {
	// Capture contains the capture settings.
	Capture UserspaceCameraCaptureConfig `yaml:"capture"`
	// Render contains the render settings.
	Render UserspaceCameraRenderConfig `yaml:"render"`
	// Release contains the release settings.
	Release UserspaceCameraReleaseConfig `yaml:"release"`
}

// UserspaceCameraCaptureConfig contains settings for userspace camera capture.
type UserspaceCameraCaptureConfig struct {
	// Frames sets the number of frames to capture.
	Frames int `yaml:"frames"`
	// Output is the output path.
	Output string `yaml:"output"`
	// ExpectedRelease specifies the expected release.
	ExpectedRelease string `yaml:"expected_release"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// UserspaceCameraRenderConfig contains settings for userspace camera render.
type UserspaceCameraRenderConfig struct {
	// Frame selects the frame to render.
	Frame int `yaml:"frame"`
	// BayerOrder specifies the Bayer order.
	BayerOrder string `yaml:"bayer_order"`
	// Linear enables linear rendering.
	Linear bool `yaml:"linear"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// UserspaceCameraReleaseConfig contains settings for userspace camera release.
type UserspaceCameraReleaseConfig struct {
	// Prepare contains the prepare settings.
	Prepare UserspaceCameraReleasePrepareConfig `yaml:"prepare"`
	// Validate contains the validate settings.
	Validate UserspaceCameraReleaseValidateConfig `yaml:"validate"`
}

// UserspaceCameraReleasePrepareConfig contains settings for userspace camera release prepare.
type UserspaceCameraReleasePrepareConfig struct {
	// RepositoryRoot specifies the repository root.
	RepositoryRoot string `yaml:"repository_root"`
	// From is the native camera build directory.
	From string `yaml:"from"`
	// OutputDir specifies the output directory.
	OutputDir string `yaml:"output_dir" config:"verbatim"`
	// Tag specifies the tag.
	Tag string `yaml:"tag"`
	// KernelTag specifies the kernel tag.
	KernelTag string `yaml:"kernel_tag"`
	// KernelABI specifies the kernel ABI.
	KernelABI string `yaml:"kernel_abi"`
	// BuildAuthoritySHA256 specifies the build authority SHA-256 digest.
	BuildAuthoritySHA256 string `yaml:"build_authority_sha256"`
	// DryRun previews changes without applying them.
	DryRun bool `yaml:"dry_run"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// UserspaceCameraReleaseValidateConfig contains settings for userspace camera release validate.
type UserspaceCameraReleaseValidateConfig struct {
	// RepositoryRoot specifies the repository root.
	RepositoryRoot string `yaml:"repository_root"`
	// AuthoritySHA256 specifies the authority SHA-256 digest.
	AuthoritySHA256 string `yaml:"authority_sha256"`
	// JSON enables JSON output.
	JSON bool `yaml:"json"`
}

// WizardConfig contains settings for wizard.
type WizardConfig struct {
	// Output is the output path.
	Output string `yaml:"output"`
	// Source specifies the source.
	Source string `yaml:"source"`
	// SourceSHA256 specifies the source SHA-256 digest.
	SourceSHA256 string `yaml:"source_sha256"`
	// KernelDir specifies the kernel directory.
	KernelDir string `yaml:"kernel_dir"`
	// KernelRelease specifies the kernel release.
	KernelRelease string `yaml:"kernel_release"`
	// CacheDir specifies the cache directory.
	CacheDir string `yaml:"cache_dir"`
}

// ResolvePath selects an explicit path, the LEXR_CONFIG override, or the
// operating system's standard Lexr configuration path, in that order.
func ResolvePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		path = os.Getenv("LEXR_CONFIG")
	}
	if strings.TrimSpace(path) != "" {
		return expandUserHome(path)
	}
	home, err := configHomePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "lexr.yml"), nil
}

// Load preserves the optional single-file API: an absent file yields no overrides.
// Use LoadFiles when every selected file must exist.
func Load(path string) (Config, error) {
	resolvedPath, err := ResolvePath(path)
	if err != nil {
		return Config{}, err
	}
	_, err = os.Stat(resolvedPath)
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	return LoadFiles([]string{resolvedPath})
}

// expandConfigurationHome expands home prefixes in structs and lists, except
// fields whose repository-relative semantics require verbatim values.
func expandConfigurationHome(value reflect.Value) error {
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			// Repository-relative paths belong to their workflow, including literal ~.
			if value.Type().Field(i).Tag.Get("config") == "verbatim" {
				continue
			}
			if err := expandConfigurationHome(value.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			if err := expandConfigurationHome(value.Index(i)); err != nil {
				return err
			}
		}
	case reflect.String:
		expanded, err := expandUserHome(value.String())
		if err != nil {
			return err
		}
		value.SetString(expanded)
	}
	return nil
}

// expandUserHome expands the supported leading ~/ form without interpreting
// shell variables or other shell syntax.
func expandUserHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}
