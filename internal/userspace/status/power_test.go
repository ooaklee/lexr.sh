package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPowerProfilesFedoraTuneD covers Fedora's actual desktop-provider layout,
// which has no powerprofilesctl and no dpkg database.
func TestPowerProfilesFedoraTuneD(t *testing.T) {
	root := t.TempDir()
	writeTuneDPowerFixture(t, root)
	report, err := Inspect(Options{Root: root, Features: []Feature{FeaturePower}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || len(report.Checks) != 2 || report.Checks[0].State != StateSkip || report.Checks[1].State != StatePass {
		t.Fatalf("expected complete native provider with unverified package ownership: %#v", report)
	}
	if !strings.Contains(report.Checks[1].Detail, "tuned-ppd") || !strings.Contains(report.Checks[1].Detail, "not verified") {
		t.Fatalf("static scope and provider missing: %#v", report.Checks[1])
	}
	if strings.Contains(report.Checks[1].Remediation, "install power-profiles-daemon") {
		t.Fatalf("must not replace Fedora's provider: %#v", report.Checks[1])
	}
}

// TestPowerProfilesRequireCompleteProvider rejects TuneD without its desktop
// bridge, mixed partial providers, and a non-executable bridge.
func TestPowerProfilesRequireCompleteProvider(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*testing.T, string)
	}{
		{"no bridge", func(t *testing.T, root string) {
			removePowerFixtureFile(t, root, "usr/sbin/tuned-ppd")
			removePowerFixtureFile(t, root, "usr/lib/systemd/system/tuned-ppd.service")
		}},
		{"mixed partial providers", func(t *testing.T, root string) {
			removePowerFixtureFile(t, root, "usr/sbin/tuned-ppd")
			writeFile(t, root, "usr/bin/powerprofilesctl", 0o755, "client\n")
		}},
		{"non executable bridge", func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, "usr/sbin/tuned-ppd"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTuneDPowerFixture(t, root)
			test.edit(t, root)
			report, err := Inspect(Options{Root: root, Features: []Feature{FeaturePower}})
			if err != nil {
				t.Fatal(err)
			}
			if report.Ready || report.Checks[1].State != StateFail || !strings.Contains(report.Checks[1].Remediation, "tuned-ppd") {
				t.Fatalf("partial TuneD must require its own repair: %#v", report)
			}
		})
	}
}

// TestPowerProfilesExistingPPD preserves the Ubuntu/Arch file contract even
// when an unrelated incomplete TuneD installation is present.
func TestPowerProfilesExistingPPD(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "usr/bin/powerprofilesctl", 0o755, "client\n")
	writeFile(t, root, "usr/lib/systemd/system/power-profiles-daemon.service", 0o644, "[Service]\n")
	writeFile(t, root, "usr/sbin/tuned", 0o755, "partial tuned\n")
	writeFile(t, root, "var/lib/dpkg/status", 0o644, "Package: power-profiles-daemon\nStatus: install ok installed\nVersion: 1\nArchitecture: arm64\n\n")
	report, err := Inspect(Options{Root: root, Features: []Feature{FeaturePower}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || report.Checks[0].State != StatePass || report.Checks[1].State != StatePass || !strings.Contains(report.Checks[1].Detail, "power-profiles-daemon") {
		t.Fatalf("existing provider regressed: %#v", report)
	}
}

// TestPowerProfilesAmbiguousProvider does not infer active service selection
// or change packages when both providers' files exist.
func TestPowerProfilesAmbiguousProvider(t *testing.T) {
	root := t.TempDir()
	writeTuneDPowerFixture(t, root)
	writeFile(t, root, "usr/bin/powerprofilesctl", 0o755, "client\n")
	writeFile(t, root, "usr/lib/systemd/system/power-profiles-daemon.service", 0o644, "[Service]\n")
	report, err := Inspect(Options{Root: root, Features: []Feature{FeaturePower}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Checks[0].State != StateSkip || report.Checks[1].State != StateWarn || !strings.Contains(report.Checks[1].Detail, "both") {
		t.Fatalf("must report unresolved service selection: %#v", report)
	}
}

// TestPowerProfilesTuneDPackagePair keeps package checks honest on dpkg-based
// systems: the backend package alone does not prove the bridge was installed.
func TestPowerProfilesTuneDPackagePair(t *testing.T) {
	root := t.TempDir()
	writeTuneDPowerFixture(t, root)
	writeFile(t, root, "var/lib/dpkg/status", 0o644, "Package: tuned\nStatus: install ok installed\nVersion: 1\nArchitecture: all\n\n")
	report, err := Inspect(Options{Root: root, Features: []Feature{FeaturePower}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready || report.Checks[0].State != StateFail || !strings.Contains(report.Checks[0].Detail, "tuned-ppd") {
		t.Fatalf("missing bridge package not reported: %#v", report)
	}
}

// TestPowerProfilesContainedAliases covers merged root and /usr/sbin aliases
// while ensuring an escaping parent link is never resolved on the host.
func TestPowerProfilesContainedAliases(t *testing.T) {
	root := t.TempDir()
	writeTuneDPowerFixture(t, root)
	if err := os.Rename(filepath.Join(root, "usr/sbin"), filepath.Join(root, "usr/bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("bin", filepath.Join(root, "usr/sbin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("usr/lib", filepath.Join(root, "lib")); err != nil {
		t.Fatal(err)
	}
	report, err := Inspect(Options{Root: root, Features: []Feature{FeaturePower}})
	if err != nil || !report.Ready {
		t.Fatalf("contained aliases failed: %#v, %v", report, err)
	}
	if err := os.Remove(filepath.Join(root, "usr/sbin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../outside", filepath.Join(root, "usr/sbin")); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(Options{Root: root, Features: []Feature{FeaturePower}}); err == nil || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("escaping parent link accepted: %v", err)
	}
}

// writeTuneDPowerFixture records the native Fedora client/daemon paths rather
// than deriving fixtures from the implementation's alternatives.
func writeTuneDPowerFixture(t *testing.T, root string) {
	t.Helper()
	for _, path := range []string{"usr/sbin/tuned", "usr/sbin/tuned-adm", "usr/sbin/tuned-ppd"} {
		writeFile(t, root, path, 0o755, "#!/usr/bin/python3\n")
	}
	writeFile(t, root, "usr/lib/systemd/system/tuned.service", 0o644, "[Service]\nExecStart=/usr/sbin/tuned -l -P\n")
	writeFile(t, root, "usr/lib/systemd/system/tuned-ppd.service", 0o644, "[Service]\nExecStart=/usr/sbin/tuned-ppd -l\n")
}

// removePowerFixtureFile makes a failed removal fatal to a partial-layout test.
func removePowerFixtureFile(t *testing.T, root, relative string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, relative)); err != nil {
		t.Fatal(err)
	}
}
