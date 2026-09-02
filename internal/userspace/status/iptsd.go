package status

import (
	"os"
	"path/filepath"
	"strings"
)

// portableIPTSDELFBinaries preserves the original /usr/local runtime contract
// when no Fedora-native package layout is observed.
var portableIPTSDELFBinaries = []string{
	"usr/local/libexec/sp11-iptsd",
	"usr/local/libexec/sp11-iptsd-check-device",
}

// inspectIPTSDIntegration selects one coherent installation topology. Any
// Fedora-native path selects the /usr contract so a partial native package is
// diagnosed as such instead of being misreported as an absent portable install.
func (inspector *Inspector) inspectIPTSDIntegration(fs *rootedFS, required bool) (Check, []string, bool, error) {
	nativePresent, err := iptsdPathsPresent(fs, fedoraNativeIPTSDDistinctFiles)
	if err != nil {
		return Check{}, nil, false, err
	}
	if nativePresent == 0 {
		check, err := inspector.checkFileSet(fs, "iptsd-v1-integration", FeatureIPTSD, required, iptsdV1Files, false)
		return check, append([]string(nil), portableIPTSDELFBinaries...), false, err
	}

	portablePresent, err := iptsdPathsPresent(fs, portableIPTSDDistinctFiles)
	if err != nil {
		return Check{}, nil, true, err
	}
	check, err := inspector.checkFileSet(fs, "iptsd-v1-integration", FeatureIPTSD, required, fedoraNativeIPTSDFiles, false)
	if err != nil {
		return Check{}, nil, true, err
	}
	check.Detail = "Fedora-native /usr layout: " + check.Detail
	if portablePresent != 0 {
		check.State = optionalState(required)
		check.Detail += "; stale portable /usr/local IPTSD integration paths are also present"
	}
	return check, append([]string(nil), fedoraNativeIPTSDELFBinaries...), true, nil
}

// iptsdPathsPresent counts a fixed set without following leaf links. A hostile
// intermediate link still fails target-root containment in rootedFS.
func iptsdPathsPresent(fs *rootedFS, requirements []fileRequirement) (int, error) {
	present := 0
	for _, requirement := range requirements {
		_, _, err := fs.lstat(requirement.Path)
		if missing(err) {
			continue
		}
		if err != nil {
			return 0, err
		}
		present++
	}
	return present, nil
}

// inspectNativeIPTSDGenericService proves the native RPM has no generic vendor
// unit to contend for the device. An exact administrator /dev/null mask is also
// safe, but the Fedora package does not need to create one because it conflicts
// with the generic iptsd package.
func inspectNativeIPTSDGenericService(fs *rootedFS, required bool) (Check, error) {
	issues := make([]string, 0)
	overridePath, info, err := fs.lstat(genericIPTSDMask)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			issues = append(issues, "/"+genericIPTSDMask+" is not an exact /dev/null mask")
		} else {
			target, readErr := os.Readlink(overridePath)
			if readErr != nil {
				return Check{}, readErr
			}
			if target != "/dev/null" {
				issues = append(issues, "/"+genericIPTSDMask+" has an unexpected link target")
			}
		}
	} else if !missing(err) {
		return Check{}, err
	}

	for _, logical := range []string{
		"usr/lib/systemd/system/iptsd@.service",
		"lib/systemd/system/iptsd@.service",
	} {
		_, _, inspectErr := fs.lstat(logical)
		if missing(inspectErr) {
			continue
		}
		if inspectErr != nil {
			return Check{}, inspectErr
		}
		issues = append(issues, "/"+filepath.ToSlash(logical)+" is installed")
	}

	if len(issues) != 0 {
		return Check{
			ID: "iptsd-generic-service-conflict", Feature: FeatureIPTSD,
			State: optionalState(required), Required: required,
			Detail:      "generic iptsd service can compete with the Fedora-native SP11 integration: " + strings.Join(issues, ", "),
			Remediation: "remove the generic iptsd package or mask its unit before using lexr-sp11-iptsd",
		}, nil
	}
	return Check{
		ID: "iptsd-generic-service-conflict", Feature: FeatureIPTSD,
		State: StatePass, Required: required,
		Detail: "no generic iptsd vendor unit conflicts with the Fedora-native SP11 integration",
	}, nil
}
