package build

import "errors"

// ValidateRuntimeELFs applies the unchanged strict native-artefact ELF checks
// to exactly the five fixed runtime roles. This performs static validation
// only: it does not construct producer authority or execute target programs.
func ValidateRuntimeELFs(files map[string][]byte) error {
	return validateRuntimeELFsWithProfile(files, nativeSSC047)
}

// ValidateRuntimeELFs048 statically validates exactly the five fixed runtime
// roles against the closed metadata successor, without producer authority.
func ValidateRuntimeELFs048(files map[string][]byte) error {
	return validateRuntimeELFsWithProfile(files, nativeSSC048)
}

// validateRuntimeELFsWithProfile retains exact role closure for one fixed ABI.
func validateRuntimeELFsWithProfile(files map[string][]byte, profile nativeSSCProfile) error {
	if len(files) != len(coreELFNames) {
		return errors.New("sensor runtime ELF role count differs")
	}
	for _, name := range coreELFNames {
		if len(files[name]) == 0 {
			return errors.New("sensor runtime ELF role is missing")
		}
	}
	return validateNativeELFsWithProfile(files, profile)
}
