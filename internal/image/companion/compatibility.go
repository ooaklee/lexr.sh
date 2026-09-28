package companion

import (
	"context"
	"errors"
	imagecontract "github.com/ooaklee/lexr.sh/internal/image"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/ooaklee/lexr.sh/internal/platform"
	"github.com/ooaklee/lexr.sh/internal/userspace/assessment"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// NeedsTarget reports whether this companion contains a manifest-aware release.
func NeedsTarget(request BuildRequest) bool {
	for _, bundle := range request.UserspaceBundles {
		if request.UserspaceCatalog == nil {
			continue
		}
		component, found := request.UserspaceCatalog.Get(bundle.Component)
		if found && component.Compatibility != nil {
			return true
		}
	}
	return false
}

// validateCompatibilityRecords reproduces recorded decisions from compiled
// manifest pins, never from an image receipt's self-declared authority.
func validateCompatibilityRecords(record imagecontract.CompanionBundleRecord, directory string) error {
	for _, component := range record.Userspace {
		if component.Compatibility == nil {
			continue
		}
		if record.Tool == nil || component.Compatibility.Decision.Target.LexrVersion != record.Tool.Version {
			return errors.New("image compatibility target does not name the bundled Lexr version")
		}
		if component.Component != iptsdOfflineReleaseContract.Component || iptsdOfflineReleaseContract.Compatibility == nil {
			return errors.New("component has no compiled offline compatibility contract")
		}
		relative := strings.TrimPrefix(component.Root, ISOFilesystemRoot+"/")
		fresh, err := assessment.Evaluate(filepath.Join(directory, filepath.FromSlash(relative)), *iptsdOfflineReleaseContract.Compatibility, component.Component, component.Release, component.Compatibility.Decision.Target, component.Compatibility.AllowUnverified)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(fresh, *component.Compatibility) {
			return errors.New("image compatibility decision disagrees with authenticated manifest evidence")
		}
	}
	return nil
}

// ObserveImageTarget reads the extracted image's operating-system identity
// using only tools-container Python; it never executes an image binary.
func ObserveImageTarget(ctx context.Context, docker *platform.Docker, image, workspace, volume string, target compatibility.Target) (compatibility.Target, error) {
	directory, err := os.MkdirTemp(workspace, ".component-target-")
	if err != nil {
		return target, err
	}
	defer os.RemoveAll(directory)
	if err := os.Mkdir(filepath.Join(directory, "etc"), 0o755); err != nil {
		return target, err
	}
	const export = `import os, pathlib, stat, sys
root=pathlib.Path('/linux-work/rootfs')
source=(root/'etc/os-release').resolve(strict=True)
if not source.is_relative_to(root): raise SystemExit('image os-release escapes target root')
fd=os.open(source,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
try:
 if not stat.S_ISREG(os.fstat(fd).st_mode): raise SystemExit('image os-release is not regular')
 data=os.read(fd,65537)
 if len(data)>65536: raise SystemExit('image os-release exceeds bound')
finally: os.close(fd)
with open(sys.argv[1],'xb') as output: output.write(data)
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume, "python3", "-c", export, "/work/"+filepath.Base(directory)+"/etc/os-release"); err != nil {
		return target, err
	}
	target.OSID, target.OSVersion, err = compatibility.ReadOSRelease(directory)
	return target, err
}

// assessCompanion authenticates each manifest and binds it to the selected
// image tuple before any component is included in a published image.
func assessCompanion(request BuildRequest, bundles []preparedUserspaceBundle) error {
	for index := range bundles {
		prepared := &bundles[index]
		if prepared.component.Compatibility == nil {
			if prepared.bundle.Compatibility != nil {
				return errors.New("legacy companion bundle carries unauthorised compatibility metadata")
			}
			continue
		}
		record, err := assessment.Evaluate(prepared.bundle.Directory, *prepared.component.Compatibility, prepared.component.ID, prepared.bundle.Release, request.Target, request.AllowUnverifiedCompatibility)
		if err != nil {
			return err
		}
		if err := compatibility.RequireAllowed(record.Decision, request.AllowUnverifiedCompatibility); err != nil {
			return err
		}
		prepared.compatibility = &record
	}
	return nil
}

// ValidateImageRecord binds companion decisions to the enclosing image's
// kernel, architecture and distribution instead of accepting a valid tuple
// copied from another image.
func ValidateImageRecord(manifest imagecontract.Manifest) error {
	if err := ValidateRecord(manifest.CompanionBundle); err != nil {
		return err
	}
	systems := map[string]string{"ubuntu-casper": "ubuntu", "elementary-casper": "elementary", "fedora-live": "fedora", "debian-live": "debian", "archlinux-arm-live": "arch"}
	for _, component := range manifest.CompanionBundle.Userspace {
		if component.Compatibility == nil {
			continue
		}
		target := component.Compatibility.Decision.Target
		if target.KernelABI != manifest.KernelBundle.ABI || target.Architecture != manifest.KernelBundle.Architecture || target.OSID != systems[manifest.Adapter] || target.LexrVersion != manifest.ToolVersion {
			return errors.New("component compatibility target differs from enclosing image")
		}
	}
	return nil
}
