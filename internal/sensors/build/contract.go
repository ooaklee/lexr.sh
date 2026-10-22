// Package build owns the native, offline sensor source-build contract.
// Its artefacts are development outputs, not installable or qualified sensors.
package build

import "sort"

// The source-build contract fixes archive names, source authority, private
// provider location and the maximum accepted development archive size.
const (
	ArchiveName         = "sensor-source-artifacts.tar"
	ReportName          = "build-report.json"
	SourceCommit        = "649cc875d08c04ce33bc8afc2a0ff7a22ce362e0"
	ProviderDirectory   = "/work/job/providers"
	MaximumArchiveBytes = 128 << 20
)

// fileRule bounds one regular archive member and optionally pins licence bytes.
type fileRule struct {
	maximum       int64
	mode          int64
	licenseSHA256 string
}

// expectedFiles returns the closed regular-member policy compiled into Lexr.
func expectedFiles() map[string]fileRule {
	return map[string]fileRule{
		"libqmi-glib.so.5.12.0":    {64 << 20, 0o755, ""},
		"libssc.so.2":              {16 << 20, 0o755, ""},
		"ssccli":                   {8 << 20, 0o755, ""},
		"iio-sensor-proxy":         {16 << 20, 0o755, ""},
		"monitor-sensor":           {8 << 20, 0o755, ""},
		"COPYING.libqmi":           {18092, 0o644, "8177f97513213526df2cf6184d8ff986c675afb514d4e68a404010521b880643"},
		"COPYING.LIB.libqmi":       {26530, 0o644, "dc626520dcd53a22f727af3ee42c770e56c97a64fe3adb063799d8ab032fe551"},
		"LICENSE.libssc":           {35149, 0o644, "3972dc9744f6499f0f9b2dbf76696f2ae7ad8af9b23dde66d6af86c9dfb36986"},
		"COPYING.iio-sensor-proxy": {35147, 0o644, "8ceb4b9ee5adedde47b31e975c1d90c73ad27b6b165a1dcd80c7c545eb65b903"},
		"testlog.json":             {8 << 20, 0o644, ""},
		"testlog.txt":              {8 << 20, 0o644, ""},
		"qmi-dependencies.json":    {2 << 20, 0o644, ""},
		"ssc-dependencies.json":    {2 << 20, 0o644, ""},
		"proxy-dependencies.json":  {2 << 20, 0o644, ""},
		"loader-evidence.txt":      {2 << 20, 0o644, ""},
		ReportName:                 {1 << 20, 0o644, ""},
	}
}

// ArtifactFiles returns the compiled closed regular-member roster, not a roster
// discovered from an archive or editable manifest at run time.
func ArtifactFiles() []string {
	files := expectedFiles()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Link records one allowed flat symlink and its exact relative target.
type Link struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

// ArtifactLinks returns the fixed private-provider symlinks allowed in outputs.
func ArtifactLinks() []Link {
	return []Link{{"libqmi-glib.so", "libqmi-glib.so.5"}, {"libqmi-glib.so.5", "libqmi-glib.so.5.12.0"}, {"libssc.so", "libssc.so.2"}}
}

// FixtureTargets lists the software-only proxy tests required by the build.
func FixtureTargets() []string {
	return []string{"test-mount-matrix", "test-orientation", "test-ssc-drivers", "test-iio-buffer", "test-iio-acquisition", "test-iio-sysfs-write", "test-sensor-selection"}
}

// ProxyInstallTargets lists expected staged paths without granting installation.
func ProxyInstallTargets() []string {
	return []string{"/usr/bin/monitor-sensor", "/usr/lib/systemd/system/iio-sensor-proxy.service", "/usr/lib/udev/rules.d/80-iio-sensor-proxy.rules", "/usr/libexec/iio-sensor-proxy", "/usr/share/dbus-1/system.d/net.hadess.SensorProxy.conf", "/usr/share/polkit-1/actions/net.hadess.SensorProxy.policy"}
}

// Artifact binds one output member to its byte count, digest and execution mode.
type Artifact struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	Mode   int64  `json:"mode"`
}

// Provider records the private dependency selected by the compiled build recipe.
type Provider struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	PCFileDirectory string `json:"pcfile_directory"`
	Library         string `json:"library"`
	SHA256          string `json:"sha256"`
}

// Loader records resolved dependencies for one binary in the closed output set.
type Loader struct {
	Artifact  string            `json:"artifact"`
	Providers map[string]string `json:"providers"`
}

// BuildReport describes observations made by the compiled container recipe.
// It is not independent release/install authority; host checks must compare its
// complete roster and claims with the actual received bytes and compiled policy.
type BuildReport struct {
	Schema                        string     `json:"schema"`
	SourceCommit                  string     `json:"source_commit"`
	Jobs                          int        `json:"jobs"`
	Products                      []Artifact `json:"products"`
	Links                         []Link     `json:"links"`
	Providers                     []Provider `json:"providers"`
	Loaders                       []Loader   `json:"loaders"`
	Fixtures                      []string   `json:"fixtures"`
	ProxyInstallTargets           []string   `json:"proxy_install_targets"`
	VolumeFreeBeforeBytes         int64      `json:"volume_free_before_bytes"`
	JobApparentBeforeArchiveBytes int64      `json:"job_apparent_before_archive_bytes"`
	MemoryPeakBytes               *int64     `json:"memory_peak_bytes"`
}
