package status

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// platformProfileClassDevice accepts only the kernel class's flat numbered
// handler names before constructing contained target-root paths.
var platformProfileClassDevice = regexp.MustCompile(`^platform-profile-[0-9]+$`)

// inspectPowerProfileClass requires one complete native handler. SP11 exposes
// exactly one Surface handler; silently selecting one of several devices would
// make a machine-wide userspace profile nondeterministic.
func inspectPowerProfileClass(fs *rootedFS, required bool) (Check, error) {
	check := Check{
		ID:          "power-profile-class-interface",
		Feature:     FeaturePower,
		Required:    required,
		Remediation: "boot the paired SP11 kernel and verify one complete /sys/class/platform-profile/platform-profile-* device",
	}
	classPath, err := fs.resolve("sys/class/platform-profile", true)
	if err != nil {
		return Check{}, err
	}
	entries, err := os.ReadDir(classPath)
	if os.IsNotExist(err) {
		check.State = optionalState(required)
		check.Detail = "native platform-profile class directory is absent"
		return check, nil
	}
	if err != nil {
		return Check{}, fmt.Errorf("inspect native platform-profile class: %w", err)
	}

	complete := make([]string, 0, 1)
	incomplete := 0
	for _, entry := range entries {
		if !platformProfileClassDevice.MatchString(entry.Name()) {
			continue
		}
		base := filepath.ToSlash(filepath.Join("sys/class/platform-profile", entry.Name()))
		profile, _, profileErr := fs.regular(filepath.Join(base, "profile"), true)
		choices, _, choicesErr := fs.regular(filepath.Join(base, "choices"), true)
		if profileErr == nil && choicesErr == nil && profile != "" && choices != "" {
			complete = append(complete, entry.Name())
			continue
		}
		if profileErr != nil && strings.Contains(profileErr.Error(), "target-root link") {
			return Check{}, profileErr
		}
		if choicesErr != nil && strings.Contains(choicesErr.Error(), "target-root link") {
			return Check{}, choicesErr
		}
		incomplete++
	}
	sort.Strings(complete)
	if len(complete) == 1 && incomplete == 0 {
		check.State = StatePass
		check.Detail = "one complete native platform-profile class device is available: " + complete[0]
		check.Remediation = ""
		return check, nil
	}
	check.State = optionalState(required)
	check.Detail = fmt.Sprintf("expected one complete native platform-profile class device; found %d complete and %d incomplete", len(complete), incomplete)
	return check, nil
}
