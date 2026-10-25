package integration

import (
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
)

// TestMetadata048CannotRenderLegacyIntegration is a passive opt-in boundary
// check with an actual new package and the independently authenticated old
// source. It does not extend integration, register services or execute payloads.
func TestMetadata048CannotRenderLegacyIntegration(t *testing.T) {
	name := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_048")
	pin := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_048_SHA256")
	legacyRoot := os.Getenv("LEXR_TEST_SENSOR_INTEGRATION_LEGACY_OE_ROOT_048")
	if name == "" && pin == "" && legacyRoot == "" {
		t.Skip("explicit new package and legacy source boundary opt-in required")
	}
	d, err := hex.DecodeString(pin)
	if name == "" || legacyRoot == "" || !filepath.IsAbs(name) || err != nil ||
		len(d) != 32 || hex.EncodeToString(d) != pin {
		t.Fatal("paired canonical package reference and explicit legacy source root required")
	}
	before, err := os.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 ||
		before.Size() > runtimepkg.MaximumPackageBytes {
		t.Fatal("new package is not a bounded regular input")
	}
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() {
		t.Fatal("new package identity changed before read")
	}
	raw, err := io.ReadAll(io.LimitReader(f, runtimepkg.MaximumPackageBytes+1))
	after, statErr := f.Stat()
	current, lstatErr := os.Lstat(name)
	if err != nil || statErr != nil || lstatErr != nil || int64(len(raw)) != before.Size() ||
		!current.Mode().IsRegular() || !os.SameFile(opened, after) || !os.SameFile(opened, current) ||
		after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("new package changed during bounded read")
	}
	pkg, err := runtimepkg.Decode048(raw, runtimepkg.Reference{Bytes: int64(len(raw)), SHA256: pin})
	if err != nil {
		t.Fatal(err)
	}
	oldSources, err := source.Capture(legacyRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(pkg, oldSources); err == nil {
		t.Fatal("default integration renderer adopted metadata0.4.8 package")
	}
	t.Log("metadata0.4.8 remains outside the legacy passive integration/service contract")
}
