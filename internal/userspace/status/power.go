package status

import "strings"

// tunedPowerProfileFiles is one complete TuneD desktop-provider contract.
// TuneD alone does not implement the PowerProfiles API; tuned-ppd supplies it.
// Keep the set separate from PPD so unrelated partial installations cannot
// satisfy one another's requirements. These are static files, not service state.
var tunedPowerProfileFiles = [][]fileRequirement{
	{{Path: "usr/sbin/tuned", Executable: true}, {Path: "usr/bin/tuned", Executable: true}},
	{{Path: "usr/sbin/tuned-adm", Executable: true}, {Path: "usr/bin/tuned-adm", Executable: true}},
	{{Path: "usr/sbin/tuned-ppd", Executable: true}, {Path: "usr/bin/tuned-ppd", Executable: true}},
	{{Path: "usr/lib/systemd/system/tuned.service"}, {Path: "lib/systemd/system/tuned.service"}},
	{{Path: "usr/lib/systemd/system/tuned-ppd.service"}, {Path: "lib/systemd/system/tuned-ppd.service"}},
}

// inspectPowerProfiles accepts either complete desktop provider without
// replacing the distribution's choice or executing tools in the selected root.
func (inspector *Inspector) inspectPowerProfiles(fs *rootedFS, dpkg dpkgDatabase, required bool) ([]Check, error) {
	ppd, err := inspector.checkAlternativeSets(fs, "power-profiles-files", FeaturePower, required, powerProfileFiles, true)
	if err != nil {
		return nil, err
	}
	tuned, err := inspector.checkAlternativeSets(fs, "power-profiles-files", FeaturePower, required, tunedPowerProfileFiles, true)
	if err != nil {
		return nil, err
	}
	ppd.Remediation = "install or repair the distribution's power-profile provider: power-profiles-daemon, or tuned with tuned-ppd"
	tuned.Remediation = "repair the distribution's tuned and tuned-ppd packages; inspect tuned.service and tuned-ppd.service without replacing the power backend"
	if ppd.State == StatePass && tuned.State == StatePass {
		return []Check{
			{ID: "power-profiles-package", Feature: FeaturePower, Required: required, State: StateSkip, Detail: "both desktop provider file sets are present; the active provider cannot be selected from static files"},
			{ID: "power-profiles-files", Feature: FeaturePower, Required: required, State: StateWarn, Detail: "power-profiles-daemon and tuned with tuned-ppd both have complete file sets; service activity and kernel profile support are not verified", Remediation: "inspect the active power-profile service before changing packages or service selection"},
		}, nil
	}
	if ppd.State == StatePass {
		ppd.Detail = "power-profiles-daemon client and service files are installed; service activity and kernel profile support are not verified"
		return []Check{inspectPackage(dpkg, "power-profiles-package", FeaturePower, "power-profiles-daemon", "", required), ppd}, nil
	}
	tunedPresent, err := powerProfileFilesPresent(fs, tunedPowerProfileFiles)
	if err != nil {
		return nil, err
	}
	if tuned.State == StatePass || tunedPresent {
		if tuned.State == StatePass {
			tuned.Detail = "tuned and tuned-ppd client and service files are installed; service activity and kernel profile support are not verified"
		}
		return []Check{inspectTunedPackages(dpkg, required), tuned}, nil
	}
	return []Check{inspectPackage(dpkg, "power-profiles-package", FeaturePower, "power-profiles-daemon", "", required), ppd}, nil
}

// powerProfileFilesPresent preserves a partial provider's own repair advice,
// including when a present file is empty, non-executable or a leaf symlink.
func powerProfileFilesPresent(fs *rootedFS, sets [][]fileRequirement) (bool, error) {
	for _, alternatives := range sets {
		for _, requirement := range alternatives {
			_, _, err := fs.lstat(requirement.Path)
			if missing(err) {
				continue
			}
			if err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

// inspectTunedPackages checks both packages where a dpkg database is available.
// Other package databases are deliberately not inferred from executable files.
func inspectTunedPackages(db dpkgDatabase, required bool) Check {
	result := inspectPackage(db, "power-profiles-package", FeaturePower, "tuned", "", required)
	if !db.present {
		result.Detail = "tuned and tuned-ppd package ownership is not checked: dpkg status is unavailable in the selected root"
		return result
	}
	bridge := inspectPackage(db, "power-profiles-package", FeaturePower, "tuned-ppd", "", required)
	if bridge.State != StatePass {
		result.State = bridge.State
	}
	result.Detail = strings.Join([]string{result.Detail, bridge.Detail}, "; ")
	result.Remediation = "repair the distribution's tuned and tuned-ppd packages"
	return result
}
