//go:build linux || darwin

package offline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/packageplan"
	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
)

// TestRetainFreshNativeLoaderFixture is an explicit preparation seam for a
// separately reviewed read-only native proof. It cannot adopt a target root.
// Outputs are deliberately retained; it neither executes a target binary nor
// runs Docker, publishes a package or grants live installation authority.
func TestRetainFreshNativeLoaderFixture(t *testing.T) {
	parent := os.Getenv("LEXR_TEST_SENSOR_RETAIN_PARENT")
	if parent == "" {
		t.Skip("explicit new private fixture parent required")
	}
	name := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE")
	expected := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_SHA256")
	if name == "" || expected == "" || !filepath.IsAbs(name) || filepath.Clean(name) != name {
		t.Fatal("paired canonical package path and independently retained SHA-256 required")
	}
	input, err := openAbsoluteDirectory(filepath.Dir(name), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.close()
	data, _, err := readRegular(input, filepath.Base(name), runtimepkg.MaximumPackageBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := runtimepkg.Decode(data, runtimepkg.Reference{Bytes: int64(len(data)), SHA256: expected})
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := os.Mkdir(filepath.Join(session.Path(), "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session.Path(), "etc", "os-release"), []byte("ID=ubuntu\nVERSION_ID=26.04\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := session.Plan(pkg, syntheticRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Install(pkg, syntheticRequest()); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(session.Path(), filepath.FromSlash(packageplan.PrivatePrefix[1:]))
	receipt, err := json.MarshalIndent(struct {
		Schema string `json:"schema"`
		Prefix string `json:"prefix"`
		Plan   Plan   `json:"plan"`
	}{"lexr-retained-native-loader-fixture-v1", prefix, plan}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(parent, "fixture.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(receipt, '\n')); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	t.Log("retained a fresh passive qualification fixture; native execution remains separately gated")
}
