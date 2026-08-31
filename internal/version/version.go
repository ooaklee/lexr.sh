// Package version contains build metadata populated by trusted linker flags.
package version

import "runtime/debug"

var (
	// Version is the semantic release version injected at link time.
	Version = "dev"
	// Commit is the source revision injected at link time.
	Commit = "unknown"
	// Date is the build timestamp injected at link time.
	Date = "unknown"
)

// Info returns linker-provided build metadata and may use only the main Go
// module version as a local-build fallback. Automatic VCS commit and timestamp
// settings are deliberately ignored because Go can discover a containing
// superproject rather than a Git submodule's pointer-file repository.
func Info() (version, commit, date string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version, Commit, Date
	}
	return resolve(Version, Commit, Date, info)
}

// resolve applies the narrow trusted fallback to explicit metadata values.
func resolve(version, commit, date string, info *debug.BuildInfo) (string, string, string) {
	if info != nil && version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
	return version, commit, date
}
