//go:build linux || darwin

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/build"
	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
	"golang.org/x/sys/unix"
)

// The successor pin names only independently retained qualification evidence,
// not arbitrary caller authority or a production-release trust chain.
const (
	accepted048PackageBytes  = int64(51671040)
	accepted048PackageSHA256 = "289fa8f2977cd398e8e9547fbcda98260193eb6dc333fc895c02ff4da27842ae"
)

// readPinnedIntegrationPackage reads one bounded single-link regular evidence
// file without following its final symlink, waiting on a FIFO or reopening it.
func readPinnedIntegrationPackage(t *testing.T, name string, size int64, sha256 string) []byte {
	t.Helper()
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		t.Fatal("retained package path must be explicit and canonical")
	}
	real, err := filepath.EvalSymlinks(name)
	if err != nil || real != name {
		t.Fatal("retained package path contains a symlink")
	}
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var before unix.Stat_t
	if err := unix.Fstat(fd, &before); err != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 || before.Size != size || size <= 0 || size > runtimepkg.MaximumPackageBytes {
		t.Fatal("retained package is not the pinned bounded single-link regular file")
	}
	opened, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(f, size+1))
	var after unix.Stat_t
	statErr := unix.Fstat(fd, &after)
	current, currentErr := os.Lstat(name)
	ended, endedErr := f.Stat()
	if err != nil || statErr != nil || currentErr != nil || endedErr != nil || int64(len(data)) != size || digest(data) != sha256 || before.Dev != after.Dev || before.Ino != after.Ino || after.Nlink != 1 || after.Mode != before.Mode || after.Size != before.Size || !current.Mode().IsRegular() || !os.SameFile(opened, current) || !os.SameFile(opened, ended) || current.Size() != size || !current.ModTime().Equal(opened.ModTime()) || !ended.ModTime().Equal(opened.ModTime()) {
		t.Fatal("retained package bytes or regular-file identity changed")
	}
	return data
}

// TestSavedPackageIntegration048 renders actual accepted old/new evidence with
// explicitly selected constructors. All assets stay in memory; no payload,
// systemd command, registration or target filesystem write is performed.
func TestSavedPackageIntegration048(t *testing.T) {
	oldName, oldPin, oldRoot := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE"), os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_SHA256"), os.Getenv("LEXR_TEST_SENSOR_INTEGRATION_OE_ROOT")
	newName, newPin, newRoot := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_048"), os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_048_SHA256"), os.Getenv("LEXR_TEST_SENSOR_METADATA048_OE_ROOT")
	if oldName == "" && oldPin == "" && oldRoot == "" && newName == "" && newPin == "" && newRoot == "" {
		t.Skip("explicit retained old/new package pins and both authenticated source roots required")
	}
	if oldName == "" || oldPin != acceptedPackageSHA256 || oldRoot == "" || newName == "" || newPin != accepted048PackageSHA256 || newRoot == "" {
		t.Fatal("all fixed retained package pins and both source roots are required together")
	}
	oldBytes := readPinnedIntegrationPackage(t, oldName, acceptedPackageBytes, acceptedPackageSHA256)
	newBytes := readPinnedIntegrationPackage(t, newName, accepted048PackageBytes, accepted048PackageSHA256)
	oldPackage, err := runtimepkg.Decode(oldBytes, runtimepkg.Reference{Bytes: acceptedPackageBytes, SHA256: acceptedPackageSHA256})
	if err != nil {
		t.Fatal(err)
	}
	newPackage, err := runtimepkg.Decode048(newBytes, runtimepkg.Reference{Bytes: accepted048PackageBytes, SHA256: accepted048PackageSHA256})
	if err != nil {
		t.Fatal(err)
	}
	oldSources, err := source.Capture(oldRoot)
	if err != nil {
		t.Fatal(err)
	}
	newSources, err := source.Capture048(newRoot)
	if err != nil {
		t.Fatal(err)
	}
	if oldPackage.IsMetadata048() || oldSources.IsMetadata048() || !newPackage.IsMetadata048() || !newSources.IsMetadata048() {
		t.Fatal("retained source or package profile differs")
	}
	var oldBundle, newBundle *Bundle
	for _, row := range []struct {
		name    string
		render  func(*runtimepkg.VerifiedPackage, *source.Captured) (*Bundle, error)
		pkg     *runtimepkg.VerifiedPackage
		src     *source.Captured
		allowed bool
	}{
		{"old-default", Render, oldPackage, oldSources, true},
		{"new-explicit", Render048, newPackage, newSources, true},
		{"new-refused-by-default", Render, newPackage, newSources, false},
		{"old-refused-by-successor", Render048, oldPackage, oldSources, false},
		{"default-old-runtime-new-source", Render, oldPackage, newSources, false},
		{"default-new-runtime-old-source", Render, newPackage, oldSources, false},
		{"successor-old-runtime-new-source", Render048, oldPackage, newSources, false},
		{"successor-new-runtime-old-source", Render048, newPackage, oldSources, false},
	} {
		bundle, err := row.render(row.pkg, row.src)
		if (err == nil) != row.allowed || row.allowed && bundle == nil {
			t.Fatalf("actual profile matrix %s: %v", row.name, err)
		}
		if row.name == "old-default" {
			oldBundle = bundle
		}
		if row.name == "new-explicit" {
			newBundle = bundle
		}
	}
	if oldBundle.IsMetadata048() || !oldBundle.IsLegacy047() || !newBundle.IsMetadata048() || newBundle.IsLegacy047() {
		t.Fatal("rendered opaque seals do not match the explicitly selected constructors")
	}
	for _, row := range []struct {
		name    string
		bundle  *Bundle
		pkg     *runtimepkg.VerifiedPackage
		src     *source.Captured
		lineage string
		render  func(*runtimepkg.VerifiedPackage, *source.Captured) (*Bundle, error)
	}{
		{"old", oldBundle, oldPackage, oldSources, build.SourceCommit, Render},
		{"new", newBundle, newPackage, newSources, build.SourceCommit048, Render048},
	} {
		assets, err := row.bundle.Assets()
		if err != nil || len(assets) != 6 {
			t.Fatal("actual passive asset closure differs")
		}
		unit, err := row.bundle.Unit()
		if err != nil || !bytes.Equal(unit, []byte(renderedUnit)) || digest(unit) != "2fc2d8ef2e555c65ee168be7d37f9919f0f0cfe22effbb1648849223e0936c2d" {
			t.Fatal("actual static unit bytes changed across profiles")
		}
		snapshot, err := row.bundle.Snapshot()
		if err != nil || snapshot.RuntimePackage != row.pkg.Reference() || snapshot.SourceLineage != row.lineage || !snapshot.QualificationOnly || snapshot.RegistrationEnabled || snapshot.ActivationEnabled || snapshot.UnitSHA256 != digest(unit) || !reflect.DeepEqual(snapshot.Argv, fixedArgv()) {
			t.Fatal("actual integration snapshot profile or inert boundary differs")
		}
		var proxy []byte
		for _, file := range row.pkg.Files() {
			if file.Path == "libexec/iio-sensor-proxy" {
				proxy = file.Data
			}
		}
		if len(proxy) == 0 || snapshot.ProgramSHA256 != digest(proxy) {
			t.Fatal("actual proxy identity was not derived from the selected verified package")
		}
		second, err := row.render(row.pkg, row.src)
		if err != nil {
			t.Fatal(err)
		}
		secondAssets, _ := second.Assets()
		secondSnapshot, _ := second.Snapshot()
		if !reflect.DeepEqual(assets, secondAssets) || !reflect.DeepEqual(snapshot, secondSnapshot) {
			t.Fatal("actual rendering is not deterministic")
		}
		assets[0].Data[0] ^= 1
		unit[0] ^= 1
		snapshot.Argv[0] = "changed"
		snapshot.RawTemplates[0].SHA256 = "changed"
		snapshot.Assets[0].SHA256 = "changed"
		unchangedAssets, _ := row.bundle.Assets()
		unchangedUnit, _ := row.bundle.Unit()
		unchangedSnapshot, _ := row.bundle.Snapshot()
		if !reflect.DeepEqual(unchangedAssets, secondAssets) || !bytes.Equal(unchangedUnit, []byte(renderedUnit)) || !reflect.DeepEqual(unchangedSnapshot, secondSnapshot) {
			t.Fatal("actual integration accessors exposed retained mutable state")
		}
		canonical, err := json.Marshal(secondSnapshot)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s_inert_integration_snapshot=%s", row.name, canonical)
	}
	oldAssets, _ := oldBundle.Assets()
	newAssets, _ := newBundle.Assets()
	if !reflect.DeepEqual(oldAssets, newAssets) || !bytes.Equal(oldPackage.Bytes(), oldBytes) || !bytes.Equal(newPackage.Bytes(), newBytes) {
		t.Fatal("profile rendering changed static assets or accepted package bytes")
	}
}
