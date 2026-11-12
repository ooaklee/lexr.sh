//go:build linux || darwin

package lifecycle

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/integration"
	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
	"github.com/ooaklee/lexr.sh/internal/sensors/source"
	"golang.org/x/sys/unix"
)

// acceptedPackageBytes reads a bounded single-link regular file without a
// symlink, FIFO or pathname reopening race, for the opt-in qualification test.
func acceptedPackageBytes(t *testing.T, name string) []byte {
	t.Helper()
	if !filepath.IsAbs(name) || filepath.Clean(name) != name {
		t.Fatal("accepted package path must be explicit and canonical")
	}
	real, err := filepath.EvalSymlinks(name)
	if err != nil || real != name {
		t.Fatal("accepted package path contains a symlink")
	}
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Size <= 0 || st.Size > runtimepkg.MaximumPackageBytes {
		t.Fatal("accepted package is not a bounded single-link regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, runtimepkg.MaximumPackageBytes+1))
	if err != nil || int64(len(data)) != st.Size {
		t.Fatal(errors.Join(err, errors.New("accepted package changed length")))
	}
	var after unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil || st.Dev != after.Dev || st.Ino != after.Ino || after.Nlink != 1 || after.Size != st.Size || after.Mode != st.Mode {
		t.Fatal("accepted package identity changed")
	}
	return data
}

// TestSavedPackageLifecycle renders a real authenticated package/source bundle,
// then executes the same apply/repeat/rollback engine using only a fresh-root
// file backend and simulated manager/process/bus observations. No package code
// or target service is executed and no physical sensor support is established.
func TestSavedPackageLifecycle(t *testing.T) {
	name, want, repository := os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE"), os.Getenv("LEXR_TEST_SENSOR_RUNTIME_PACKAGE_SHA256"), os.Getenv("LEXR_TEST_SENSOR_INTEGRATION_OE_ROOT")
	if name == "" && want == "" && repository == "" {
		t.Skip("set accepted runtime package, retained SHA-256 and approved OE root for lifecycle qualification")
	}
	if name == "" || want == "" || repository == "" {
		t.Fatal("saved lifecycle qualification requires all three package/source inputs")
	}
	data := acceptedPackageBytes(t, name)
	pkg, err := runtimepkg.Decode(data, runtimepkg.Reference{Bytes: int64(len(data)), SHA256: want})
	if err != nil {
		t.Fatal(err)
	}
	captured, err := source.Capture(repository)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := integration.Render(pkg, captured)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.IsMetadata048() {
		t.Fatal("original bundle acquired successor profile authority")
	}
	refusal := freshBackend(t, false)
	if _, err := NewQualification048(bundle, refusal); err == nil || len(refusal.calls) != 0 {
		t.Fatal("metadata constructor accepted original bundle or touched backend")
	}
	for _, active := range []bool{false, true} {
		f := freshBackend(t, active)
		before := fixtureSnapshot(t, f.path)
		q, err := NewQualification(bundle, f)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := q.Apply(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if phase, _ := tx.Status(); phase != Active {
			t.Fatal("actual bundle did not reach simulated ownership acceptance")
		}
		again, err := q.Apply(context.Background())
		if err != nil || again != tx {
			t.Fatalf("actual-bundle exact repeat: %v", err)
		}
		if err := q.Rollback(context.Background(), tx); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, fixtureSnapshot(t, f.path)) {
			t.Fatal("actual-bundle fixture rollback changed baseline")
		}
		if (f.observation.Vendor.ActiveState == "active") != active {
			t.Fatal("actual-bundle prior vendor functional state changed")
		}
	}
	t.Logf("accepted package %s: real integration bytes exercised by fresh-root apply/repeat/compensation with simulated services only", want)
}
