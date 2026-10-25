package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path"
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// Accepted package bytes are pinned only for this explicit retained replay;
// Render itself still consumes an opaque, strictly verified runtime package.
const (
	acceptedPackageBytes  = int64(51598336)
	acceptedPackageSHA256 = "ca05c65c6658c11f4fc9f2711290e063b186ac4c904fd0a6a7cd96718027eb08"
)

// readAcceptedPackage performs a bounded read of unchanged saved evidence,
// checking regular-file identity and complete retained byte authority.
func readAcceptedPackage(t *testing.T, name string) []byte {
	t.Helper()
	before, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Mode().IsRegular() || before.Size() != acceptedPackageBytes {
		t.Fatal("retained qualification package size or type differs")
	}
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != before.Size() || !opened.ModTime().Equal(before.ModTime()) {
		t.Fatal("retained package identity changed before reading")
	}
	data, err := io.ReadAll(io.LimitReader(f, acceptedPackageBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.Lstat(name)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, after) || !os.SameFile(opened, current) || after.Size() != opened.Size() || current.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) || !current.ModTime().Equal(opened.ModTime()) || int64(len(data)) != acceptedPackageBytes || digest(data) != acceptedPackageSHA256 {
		t.Fatal("retained package bytes or identity changed while reading")
	}
	return data
}

// TestSavedPackageIntegration is a read-only opt-in replay of the
// accepted package and authenticated corresponding source. It renders only
// passive in-memory assets; no registration, activation or target writes occur.
func TestSavedPackageIntegration(t *testing.T) {
	name, expected, oeRoot := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE"), os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_SHA256"), os.Getenv("LEXR_TEST_SENSOR_INTEGRATION_OE_ROOT")
	if name == "" && expected == "" && oeRoot == "" {
		t.Skip("explicit accepted runtime package and captured OE root required")
	}
	if name == "" || expected != acceptedPackageSHA256 || oeRoot == "" {
		t.Fatal("accepted package SHA and explicit OE source root are required together")
	}
	data := readAcceptedPackage(t, name)
	pkg, err := runtimepkg.Decode(data, runtimepkg.Reference{Bytes: acceptedPackageBytes, SHA256: acceptedPackageSHA256})
	if err != nil {
		t.Fatal(err)
	}
	sources, err := source.Capture(oeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(pkg, new(source.Captured)); err == nil {
		t.Fatal("verified package displaced missing source authority")
	}
	if _, err := Render(new(runtimepkg.VerifiedPackage), sources); err == nil {
		t.Fatal("captured source displaced missing runtime authority")
	}
	bundle, err := Render(pkg, sources)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := bundle.Assets()
	if err != nil || len(assets) != 6 {
		t.Fatal("inert integration asset closure differs")
	}
	snapshot, err := bundle.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	unit, err := bundle.Unit()
	if err != nil || !bytes.Equal(unit, []byte(renderedUnit)) || snapshot.UnitSHA256 != digest(unit) || snapshot.UnitBytes != int64(len(unit)) {
		t.Fatal("actual integration unit differs from compiled manual policy")
	}
	if snapshot.RuntimePackage != pkg.Reference() || snapshot.SourceLineage != sources.Composition().SourceCommit || !snapshot.QualificationOnly || snapshot.ActivationEnabled || snapshot.RegistrationEnabled || snapshot.UnitName != UnitName || snapshot.VendorUnitName != VendorUnitName || snapshot.BusName != BusName || snapshot.ReadinessKind != "external_bus_owner_verification_required" || !reflect.DeepEqual(snapshot.Argv, fixedArgv()) {
		t.Fatal("inert integration snapshot acquired registration or support authority")
	}
	fileMap := map[string][]byte{}
	for _, file := range pkg.Files() {
		fileMap[file.Path] = file.Data
	}
	if snapshot.ProgramSHA256 != digest(fileMap["libexec/iio-sensor-proxy"]) {
		t.Fatal("rendered program identity was not derived from the verified package")
	}
	sourceMap := map[string][]byte{}
	for _, entry := range sources.Entries() {
		sourceMap[entry.Path] = entry.Data
	}
	assetMap := map[string][]byte{}
	previous := ""
	for _, asset := range assets {
		if asset.Path <= previous || path.IsAbs(asset.Path) || path.Clean(asset.Path) != asset.Path || asset.Mode != 0o644 || len(asset.Data) == 0 {
			t.Fatal("actual integration passive path, mode or order differs")
		}
		previous = asset.Path
		assetMap[asset.Path] = asset.Data
	}
	for _, policy := range templatePolicies() {
		if !bytes.Equal(assetMap[policy.assetPath], sourceMap[policy.sourcePath]) || digest(assetMap[policy.assetPath]) != policy.sha256 {
			t.Fatal("raw integration template was changed or unauthenticated")
		}
	}
	if !bytes.Equal(assetMap["licenses/COPYING.iio-sensor-proxy"], fileMap["share/licenses/COPYING.iio-sensor-proxy"]) {
		t.Fatal("original proxy licence was not preserved")
	}
	second, err := Render(pkg, sources)
	if err != nil {
		t.Fatal(err)
	}
	secondAssets, _ := second.Assets()
	secondSnapshot, _ := second.Snapshot()
	if !reflect.DeepEqual(assets, secondAssets) || !reflect.DeepEqual(snapshot, secondSnapshot) {
		t.Fatal("inert integration rendering is not deterministic")
	}
	assets[0].Data[0] ^= 1
	unit[0] ^= 1
	snapshot.Argv[0] = "changed"
	unchangedAssets, _ := bundle.Assets()
	unchangedUnit, _ := bundle.Unit()
	unchangedSnapshot, _ := bundle.Snapshot()
	if !reflect.DeepEqual(unchangedAssets, secondAssets) || !bytes.Equal(unchangedUnit, []byte(renderedUnit)) || !reflect.DeepEqual(unchangedSnapshot, secondSnapshot) || !bytes.Equal(pkg.Bytes(), data) {
		t.Fatal("rendering exposed or mutated package, source or bundle buffers")
	}
	canonical, err := json.Marshal(secondSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("inert_integration_snapshot=%s", canonical)
	t.Logf("read-only retained package %s rendered six inert assets; unit=%s; Type=exec with external owner verification, no registration, execution or activation", acceptedPackageSHA256, secondSnapshot.UnitSHA256)
}
