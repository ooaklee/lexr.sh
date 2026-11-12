//go:build linux || darwin

package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/sensors/integration"
	"github.com/ooaklee/lexr.sh/internal/sensors/packageplan"
	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
	"golang.org/x/sys/unix"
)

// fixtureBackend is the only backend here: a newly owned private test root and
// simulated manager/process/bus state. It has no process runner or live root.
type fixtureBackend struct {
	root         *os.Root
	path         string
	observation  Observation
	claim        *Claim
	calls        []string
	faults       map[string]bool
	before       func(string)
	after        func(string)
	observe      func(*Observation)
	block        string
	staleReload  bool
	releaseDelay int
	readyDelay   int
	maskInfo     fs.FileInfo
	unitInfo     fs.FileInfo
}

// syntheticBinding is a mechanical fixture, not an opaque package forgery.
func syntheticBinding() (integration.Snapshot, []byte) {
	unit := []byte("[Unit]\nRequires=dbus.socket\nAfter=dbus.socket\n[Service]\nType=exec\nExecStart=fixture-only\nRestart=no\n")
	b := integration.Snapshot{QualificationOnly: true, UnitName: integration.UnitName, VendorUnitName: integration.VendorUnitName, BusName: integration.BusName, LoaderPath: integration.LoaderPath, ProgramPath: packageplan.PrivatePrefix + "/libexec/iio-sensor-proxy", LibraryPath: packageplan.PrivatePrefix + "/lib", ProgramSHA256: strings.Repeat("c", 64), RuntimePackage: runtimepkg.Reference{Bytes: 4096, SHA256: strings.Repeat("a", 64)}, UnitBytes: int64(len(unit)), UnitSHA256: hash(unit)}
	b.Argv = []string{b.LoaderPath, "--library-path", b.LibraryPath, b.ProgramPath}
	return b, unit
}

// freshBackend exclusively creates fixture files beneath a new private root.
func freshBackend(t *testing.T, active bool) *fixtureBackend {
	t.Helper()
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p, "run/systemd/system"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	f := &fixtureBackend{root: root, path: p, faults: map[string]bool{}, observation: Observation{BootID: "fixture-boot", ManagerID: "fixture-manager", KnownVendor: true, VendorDefinitionSHA256: strings.Repeat("b", 64), VendorFileState: "static", Vendor: Unit{Name: integration.VendorUnitName, LoadState: "loaded", ActiveState: "inactive", SubState: "dead", CGroupEmpty: true, DefinitionSHA256: strings.Repeat("b", 64)}, Native: Unit{Name: integration.UnitName, LoadState: "not-found", ActiveState: "inactive", SubState: "dead", CGroupEmpty: true}, Owner: BusOwner{Name: integration.BusName}}}
	if active {
		f.vendorActive()
	}
	return f
}

// engineFixture composes the real engine with only the explicitly fake backend.
func engineFixture(t *testing.T, active bool) (*Qualification, *fixtureBackend) {
	t.Helper()
	f := freshBackend(t, active)
	b, u := syntheticBinding()
	q, err := newQualification(b, u, f)
	if err != nil {
		t.Fatal(err)
	}
	q.operationTimeout = 30 * time.Millisecond
	q.cleanupTimeout = 100 * time.Millisecond
	q.poll = time.Millisecond
	return q, f
}

// hash returns a fixture digest, never a package authentication constructor.
func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// effect injects one-shot before/after failures, including error-after-effect.
func (f *fixtureBackend) effect(ctx context.Context, name string, fn func() error) error {
	f.calls = append(f.calls, name)
	if f.before != nil {
		f.before(name)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.block == name {
		<-ctx.Done()
		return ctx.Err()
	}
	if f.faults[name+":before"] {
		delete(f.faults, name+":before")
		return errors.New("fixture before " + name)
	}
	if err := fn(); err != nil {
		return err
	}
	if f.after != nil {
		f.after(name)
	}
	if f.faults[name+":after"] {
		delete(f.faults, name+":after")
		return errors.New("fixture after " + name)
	}
	return nil
}

// Observe derives exact fixture-file evidence without target service commands.
func (f *fixtureBackend) Observe(ctx context.Context) (Observation, error) {
	var result Observation
	err := f.effect(ctx, "Observe", func() error {
		info, err := os.Lstat(f.path)
		if err != nil {
			return err
		}
		opened, err := f.root.Stat(".")
		if err != nil || !os.SameFile(info, opened) || !info.IsDir() || info.Mode().Perm() != 0o700 {
			return errors.New("fixture root identity changed")
		}
		f.observation.Inhibition = f.fileEvidence("run/systemd/system/"+integration.VendorUnitName, true)
		f.observation.NativeFile = f.fileEvidence("run/systemd/system/"+integration.UnitName, false)
		if f.releaseDelay > 0 {
			f.releaseDelay--
			if f.releaseDelay == 0 {
				f.observation.Owner = BusOwner{Name: integration.BusName}
			}
		}
		if f.readyDelay > 0 {
			f.readyDelay--
			if f.readyDelay == 0 {
				f.nativeOwner()
			}
		}
		result = cloneObservation(f.observation)
		if f.observe != nil {
			f.observe(&result)
		}
		return nil
	})
	return result, err
}

// fileEvidence never treats a marker string as an adopted ownership handle.
func (f *fixtureBackend) fileEvidence(name string, mask bool) Fixture {
	info, err := f.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return Fixture{}
	}
	state := Fixture{Present: true}
	if err != nil || f.claim == nil {
		return state
	}
	if mask {
		target, err := f.root.Readlink(name)
		state.Exact = err == nil && info.Mode()&fs.ModeSymlink != 0 && target == "/dev/null" && sameFixtureFile(info, f.maskInfo)
		state.Effective = f.observation.Inhibition.Effective
	} else {
		unit, _ := f.claim.Unit()
		state.Exact = info.Mode().IsRegular() && info.Mode().Perm() == 0o644 && sameFixtureFile(info, f.unitInfo) && f.exactUnitBytes(name, info, unit)
		state.Effective = f.observation.NativeFile.Effective
	}
	if state.Exact {
		state.ClaimID = f.claim.ID()
	}
	return state
}

// requireClaim accepts only the same in-process opaque effect handle.
func (f *fixtureBackend) requireClaim(c *Claim) error {
	if c == nil || c.ID() == "" || f.claim != nil && c != f.claim {
		return errors.New("foreign fixture claim")
	}
	return nil
}

// InhibitVendor creates only the exact runtime /dev/null mask without replace.
func (f *fixtureBackend) InhibitVendor(ctx context.Context, c *Claim) error {
	return f.effect(ctx, "InhibitVendor", func() error {
		if err := f.requireClaim(c); err != nil {
			return err
		}
		if err := f.root.Symlink("/dev/null", "run/systemd/system/"+integration.VendorUnitName); err != nil {
			return err
		}
		f.claim = c
		info, err := f.root.Lstat("run/systemd/system/" + integration.VendorUnitName)
		if err != nil {
			return err
		}
		f.maskInfo = info
		return nil
	})
}

// InstallNative exclusively writes and synchronises one exact static unit.
func (f *fixtureBackend) InstallNative(ctx context.Context, c *Claim) error {
	return f.effect(ctx, "InstallNative", func() error {
		if err := f.requireClaim(c); err != nil {
			return err
		}
		unit, err := c.Unit()
		if err != nil {
			return err
		}
		file, err := f.root.OpenFile("run/systemd/system/"+integration.UnitName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer file.Close()
		if n, err := file.Write(unit); err != nil || n != len(unit) {
			return errors.Join(err, errors.New("fixture short write"))
		}
		if err := file.Sync(); err != nil {
			return err
		}
		f.unitInfo, err = file.Stat()
		return err
	})
}

// RemoveNative refuses to delete changed or foreign unit content.
func (f *fixtureBackend) RemoveNative(ctx context.Context, c *Claim) error {
	return f.effect(ctx, "RemoveNative", func() error {
		if err := f.requireClaim(c); err != nil {
			return err
		}
		if state := f.fileEvidence("run/systemd/system/"+integration.UnitName, false); !state.Present || !state.Exact || state.ClaimID != c.ID() {
			return errors.New("changed fixture unit")
		}
		if err := f.removalBoundary(c); err != nil {
			return err
		}
		return f.root.Remove("run/systemd/system/" + integration.UnitName)
	})
}

// ReleaseVendor removes only this backend's unchanged exact runtime mask.
func (f *fixtureBackend) ReleaseVendor(ctx context.Context, c *Claim) error {
	return f.effect(ctx, "ReleaseVendor", func() error {
		if err := f.requireClaim(c); err != nil {
			return err
		}
		if state := f.fileEvidence("run/systemd/system/"+integration.VendorUnitName, true); !state.Present || !state.Exact || state.ClaimID != c.ID() {
			return errors.New("changed fixture mask")
		}
		if !quiescent(f.observation.Native) || f.fileEvidence("run/systemd/system/"+integration.UnitName, false).Present {
			return errors.New("native fixture remains at exclusion release boundary")
		}
		if f.observation.Native.LoadState != "not-found" && (f.observation.Native.LoadState != "loaded" || f.observation.Native.DefinitionSHA256 != c.snapshot.UnitSHA256) {
			return errors.New("unknown effective native definition at exclusion release boundary")
		}
		return f.root.Remove("run/systemd/system/" + integration.VendorUnitName)
	})
}

// Reload models effective manager state from the real fixture files.
func (f *fixtureBackend) Reload(ctx context.Context) error {
	return f.effect(ctx, "Reload", func() error {
		if f.staleReload {
			return nil
		}
		mask := f.fileEvidence("run/systemd/system/"+integration.VendorUnitName, true)
		f.observation.Inhibition.Effective = mask.Present && mask.Exact
		if mask.Present {
			f.observation.Vendor.LoadState = "masked"
			f.observation.Vendor.DefinitionSHA256 = ""
		} else {
			f.observation.Vendor.LoadState = "loaded"
			f.observation.Vendor.DefinitionSHA256 = f.observation.VendorDefinitionSHA256
		}
		unit := f.fileEvidence("run/systemd/system/"+integration.UnitName, false)
		f.observation.NativeFile.Effective = unit.Present && unit.Exact
		if unit.Present && unit.Exact {
			f.observation.Native.LoadState = "loaded"
			f.observation.Native.DefinitionSHA256 = f.claim.snapshot.UnitSHA256
		} else {
			f.observation.Native.LoadState = "not-found"
			f.observation.Native.DefinitionSHA256 = ""
		}
		return nil
	})
}

// StopVendor models full cgroup quiescence, with optional asynchronous bus release.
func (f *fixtureBackend) StopVendor(ctx context.Context) error {
	return f.effect(ctx, "StopVendor", func() error {
		f.observation.Vendor.ActiveState = "inactive"
		f.observation.Vendor.SubState = "dead"
		f.observation.Vendor.MainPID = 0
		f.observation.Vendor.Process = Process{}
		f.observation.Vendor.CGroupEmpty = true
		if f.releaseDelay == 0 {
			f.observation.Owner = BusOwner{Name: integration.BusName}
		}
		return nil
	})
}

// StartNative models an exact owned invocation, not an executed target program.
func (f *fixtureBackend) StartNative(ctx context.Context) error {
	return f.effect(ctx, "StartNative", func() error {
		if !f.observation.Inhibition.Effective || !quiescent(f.observation.Vendor) || f.observation.Owner.UniqueName != "" {
			return errors.New("fixture competitor was not excluded")
		}
		b := f.claim.snapshot
		p := Process{PID: 230, StartTime: 400, InvocationID: "native-invocation", UnitName: integration.UnitName, CGroup: "/system.slice/" + integration.UnitName, RuntimePackage: b.RuntimePackage, ProgramPath: b.ProgramPath, ProgramSHA256: b.ProgramSHA256, LoaderPath: b.LoaderPath, LibraryPath: b.LibraryPath, Argv: slices.Clone(b.Argv)}
		f.observation.Native.ActiveState = "active"
		f.observation.Native.SubState = "running"
		f.observation.Native.MainPID = p.PID
		f.observation.Native.Process = p
		f.observation.Native.CGroupEmpty = false
		if f.readyDelay == 0 {
			f.nativeOwner()
		}
		return nil
	})
}

// nativeOwner publishes the typed fixture's simulated bus-name acquisition.
func (f *fixtureBackend) nativeOwner() {
	p := f.observation.Native.Process
	f.observation.Owner = BusOwner{Name: integration.BusName, UniqueName: ":native.1", PID: p.PID, StartTime: p.StartTime, InvocationID: p.InvocationID, UnitName: p.UnitName}
}

// StopNative models complete native cgroup and bus-name release.
func (f *fixtureBackend) StopNative(ctx context.Context) error {
	return f.effect(ctx, "StopNative", func() error {
		f.observation.Native.ActiveState = "inactive"
		f.observation.Native.SubState = "dead"
		f.observation.Native.MainPID = 0
		f.observation.Native.Process = Process{}
		f.observation.Native.CGroupEmpty = true
		if f.observation.Owner.UnitName == integration.UnitName {
			f.observation.Owner = BusOwner{Name: integration.BusName}
		}
		return nil
	})
}

// StartVendor refuses overlap, then restores a new recognised functional epoch.
func (f *fixtureBackend) StartVendor(ctx context.Context) error {
	return f.effect(ctx, "StartVendor", func() error {
		if !quiescent(f.observation.Native) || f.observation.Inhibition.Effective || f.observation.Owner.UniqueName != "" {
			return errors.New("fixture native process or inhibition remains")
		}
		f.vendorActive()
		return nil
	})
}

// vendorActive does not pretend that the original vendor PID can be restored.
func (f *fixtureBackend) vendorActive() {
	p := Process{PID: 120, StartTime: 200, InvocationID: "vendor-invocation", UnitName: integration.VendorUnitName, CGroup: "/system.slice/" + integration.VendorUnitName}
	f.observation.Vendor.ActiveState = "active"
	f.observation.Vendor.SubState = "running"
	f.observation.Vendor.MainPID = p.PID
	f.observation.Vendor.Process = p
	f.observation.Vendor.CGroupEmpty = false
	f.observation.Owner = BusOwner{Name: integration.BusName, UniqueName: ":vendor.1", PID: p.PID, StartTime: p.StartTime, InvocationID: p.InvocationID, UnitName: p.UnitName}
}

// fixtureSnapshot records all fixture bytes/modes/links without following links.
func fixtureSnapshot(t *testing.T, p string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(p, func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		i, e := os.Lstat(name)
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(p, name)
		if e != nil {
			return e
		}
		value := i.Mode().String()
		if i.Mode().IsRegular() {
			data, e := os.ReadFile(name)
			if e != nil {
				return e
			}
			value += " " + hash(data)
		} else if i.Mode()&fs.ModeSymlink != 0 {
			target, e := os.Readlink(name)
			if e != nil {
				return e
			}
			value += " " + target
		}
		m[relative] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestApplyRepeatRollbackExecutesFixtureLifecycle proves actual file operations
// and simulated exclusion/ownership/compensation, not merely a rendered plan.
func TestApplyRepeatRollbackExecutesFixtureLifecycle(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			q, f := engineFixture(t, active)
			before := fixtureSnapshot(t, f.path)
			tx, err := q.Apply(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if phase, _ := tx.Status(); phase != Active {
				t.Fatalf("phase %s", phase)
			}
			calls := len(f.calls)
			again, err := q.Apply(context.Background())
			if err != nil || again != tx {
				t.Fatalf("managed repeat %v", err)
			}
			for _, call := range f.calls[calls:] {
				if call != "Observe" {
					t.Fatal("repeat changed files or manager")
				}
			}
			if err := q.Rollback(context.Background(), tx); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, fixtureSnapshot(t, f.path)) {
				t.Fatal("rollback did not restore exact fixture files")
			}
			if f.observation.Vendor.ActiveState == "active" != active {
				t.Fatal("prior active state changed")
			}
			if !active && slices.Contains(f.calls, "StartVendor") {
				t.Fatal("inactive vendor was started")
			}
			if err := q.Rollback(context.Background(), tx); err == nil {
				t.Fatal("rollback handle replayed")
			}
		})
	}
}

// TestApplyFaultsBeforeAndAfterEffects covers every forward operation and the
// distinction between observed side effects and command-return success.
func TestApplyFaultsBeforeAndAfterEffects(t *testing.T) {
	for _, operation := range []string{"InhibitVendor", "Reload", "StopVendor", "InstallNative", "StartNative"} {
		for _, when := range []string{"before", "after"} {
			t.Run(operation+"/"+when, func(t *testing.T) {
				q, f := engineFixture(t, true)
				before := fixtureSnapshot(t, f.path)
				f.faults[operation+":"+when] = true
				tx, err := q.Apply(context.Background())
				if err == nil || tx == nil {
					t.Fatal("fault ignored")
				}
				phase, _ := tx.Status()
				if operation == "StopVendor" {
					if phase != RecoveryRequired || slices.Contains(f.calls, "StartNative") || slices.Contains(f.calls, "StartVendor") {
						t.Fatal("stop fault allowed competitor")
					}
					if !f.fileEvidence("run/systemd/system/"+integration.VendorUnitName, true).Present {
						t.Fatal("stop fault lost inhibition")
					}
				} else {
					if phase != Restored {
						t.Fatalf("compensation failed: %v %s", err, phase)
					}
					if !reflect.DeepEqual(before, fixtureSnapshot(t, f.path)) {
						t.Fatal("compensated files differ")
					}
				}
			})
		}
	}
}

// TestRollbackFaultsRetainTruthfulState checks every compensation operation.
func TestRollbackFaultsRetainTruthfulState(t *testing.T) {
	for _, operation := range []string{"StopNative", "RemoveNative", "ReleaseVendor", "Reload", "StartVendor"} {
		for _, when := range []string{"before", "after"} {
			t.Run(operation+"/"+when, func(t *testing.T) {
				q, f := engineFixture(t, true)
				tx, err := q.Apply(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				f.faults[operation+":"+when] = true
				calls := len(f.calls)
				err = q.Rollback(context.Background(), tx)
				if err == nil {
					t.Fatal("rollback fault ignored")
				}
				phase, _ := tx.Status()
				if phase != RecoveryRequired {
					t.Fatal("failed compensation claimed restored")
				}
				if operation == "StopNative" {
					if !f.fileEvidence("run/systemd/system/"+integration.VendorUnitName, true).Present || slices.Contains(f.calls[calls:], "StartVendor") {
						t.Fatal("uncertain native stop released inhibition or started competitor")
					}
				}
				if operation == "ReleaseVendor" && when == "after" {
					if f.fileEvidence("run/systemd/system/"+integration.VendorUnitName, true).Present {
						t.Fatal("error-after-release effect was hidden")
					}
					if strings.Contains(err.Error(), "inhibition and uncertain fixtures retained") {
						t.Fatal("error falsely claimed removed mask remained")
					}
				}
			})
		}
	}
}

// TestAsyncReadinessAndReleaseWaits verifies start/stop returns are not proof.
func TestAsyncReadinessAndReleaseWaits(t *testing.T) {
	q, f := engineFixture(t, true)
	f.after = func(name string) {
		if name == "StopVendor" {
			f.observation.Owner = q.transaction.baseline.Owner
			f.releaseDelay = 5
		}
		if name == "StartNative" {
			f.observation.Owner = BusOwner{Name: integration.BusName}
			f.readyDelay = 5
		}
	}
	tx, err := q.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if phase, _ := tx.Status(); phase != Active {
		t.Fatal("asynchronous readiness not attested")
	}
	if err := q.Rollback(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
}

// TestCancelAfterStartCompensatesWithFreshContext requires cleanup independent
// of the cancelled caller while retaining bounded cooperative operations.
func TestCancelAfterStartCompensatesWithFreshContext(t *testing.T) {
	q, f := engineFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	f.after = func(name string) {
		if name == "StartNative" {
			cancel()
		}
	}
	tx, err := q.Apply(ctx)
	if !errors.Is(err, context.Canceled) || tx == nil {
		t.Fatalf("cancel result %v", err)
	}
	if phase, _ := tx.Status(); phase != Restored {
		t.Fatalf("fresh-context compensation failed: %s %v", phase, err)
	}
	if !slices.Contains(f.calls, "StopNative") || !slices.Contains(f.calls, "StartVendor") {
		t.Fatal("cancelled context skipped compensation")
	}
}

// TestCooperativeTimeoutStopsForwardProgress checks a blocked start safely
// returns to the pre-experiment functional state without repeated starts.
func TestCooperativeTimeoutStopsForwardProgress(t *testing.T) {
	q, f := engineFixture(t, true)
	f.block = "StartNative"
	tx, err := q.Apply(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || tx == nil {
		t.Fatalf("timeout result %v", err)
	}
	if phase, _ := tx.Status(); phase != Restored {
		t.Fatalf("timeout cleanup %s: %v", phase, err)
	}
	if count(f.calls, "StartNative") != 1 {
		t.Fatal("timed-out start retried")
	}
}

// TestInitialUnknownStatesRefuseBeforeMutation prevents implicit service adoption.
func TestInitialUnknownStatesRefuseBeforeMutation(t *testing.T) {
	for _, kind := range []string{"unknown-vendor", "dropin", "alias", "masked", "foreign-owner", "pending-job", "failed", "native-existing"} {
		t.Run(kind, func(t *testing.T) {
			q, f := engineFixture(t, true)
			switch kind {
			case "unknown-vendor":
				f.observation.KnownVendor = false
			case "dropin":
				f.observation.VendorDropIns = true
			case "alias":
				f.observation.VendorAliases = true
			case "masked":
				f.observation.VendorFileState = "masked"
			case "foreign-owner":
				f.observation.Owner.UnitName = "other.service"
			case "pending-job":
				f.observation.Vendor.PendingJob = true
			case "failed":
				f.observation.Vendor.ActiveState = "failed"
			case "native-existing":
				f.observation.Native.LoadState = "loaded"
			}
			before := fixtureSnapshot(t, f.path)
			if _, err := q.Apply(context.Background()); err == nil {
				t.Fatal("unknown initial state admitted")
			}
			for _, call := range f.calls {
				if call != "Observe" {
					t.Fatal("unknown state changed manager")
				}
			}
			if !reflect.DeepEqual(before, fixtureSnapshot(t, f.path)) {
				t.Fatal("unknown state changed files")
			}
		})
	}
}

// TestNativeIdentityDriftNeverStopsUnknownProcess checks failure compensation
// does not use a failed start or unchanged filename as authority over a stranger.
func TestNativeIdentityDriftNeverStopsUnknownProcess(t *testing.T) {
	for _, kind := range []string{"program", "unit-definition", "invocation", "owner", "start-time", "cgroup", "loader", "argv"} {
		t.Run(kind, func(t *testing.T) {
			q, f := engineFixture(t, true)
			f.after = func(name string) {
				if name != "StartNative" {
					return
				}
				switch kind {
				case "program":
					f.observation.Native.Process.ProgramSHA256 = strings.Repeat("d", 64)
				case "unit-definition":
					f.observation.Native.DefinitionSHA256 = strings.Repeat("d", 64)
				case "invocation":
					f.observation.Native.Process.InvocationID = "foreign"
				case "owner":
					f.observation.Owner.UnitName = "foreign.service"
				case "start-time":
					f.observation.Native.Process.StartTime++
				case "cgroup":
					f.observation.Native.Process.CGroup = "/foreign"
				case "loader":
					f.observation.Native.Process.LoaderPath = "/foreign-loader"
				case "argv":
					f.observation.Native.Process.Argv = []string{"foreign"}
				}
			}
			tx, err := q.Apply(context.Background())
			if err == nil || tx == nil {
				t.Fatal("foreign native state accepted")
			}
			if phase, _ := tx.Status(); phase != RecoveryRequired {
				t.Fatal("unknown process was treated as compensated")
			}
			if slices.Contains(f.calls, "StopNative") || slices.Contains(f.calls, "StartVendor") {
				t.Fatal("unknown process stopped or competitor started")
			}
			if !f.fileEvidence("run/systemd/system/"+integration.VendorUnitName, true).Present {
				t.Fatal("unknown state lost inhibition")
			}
		})
	}
}

// TestRepeatedApplyDriftTerminatesActiveHandle prevents continuing to advertise
// readiness or retry after an attested invocation is replaced.
func TestRepeatedApplyDriftTerminatesActiveHandle(t *testing.T) {
	q, f := engineFixture(t, true)
	tx, err := q.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.observation.Native.Process.InvocationID = "new-invocation"
	f.observation.Owner.InvocationID = "new-invocation"
	calls := len(f.calls)
	if _, err := q.Apply(context.Background()); err == nil {
		t.Fatal("replaced active epoch accepted")
	}
	if phase, _ := tx.Status(); phase != RecoveryRequired {
		t.Fatal("failed repeat remained active")
	}
	for _, call := range f.calls[calls:] {
		if call != "Observe" {
			t.Fatal("repeat identity drift caused mutations")
		}
	}
}

// TestChangedFilesAndManagerDriftPreserveUnknownState prevents cleanup clobber.
func TestChangedFilesAndManagerDriftPreserveUnknownState(t *testing.T) {
	for _, kind := range []string{"mask", "unit", "boot", "manager", "root"} {
		t.Run(kind, func(t *testing.T) {
			q, f := engineFixture(t, true)
			tx, err := q.Apply(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "mask":
				name := "run/systemd/system/" + integration.VendorUnitName
				if err := f.root.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := f.root.Symlink("/foreign", name); err != nil {
					t.Fatal(err)
				}
			case "unit":
				if err := f.root.WriteFile("run/systemd/system/"+integration.UnitName, []byte("foreign"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "boot":
				f.observation.BootID = "new-boot"
			case "manager":
				f.observation.ManagerID = "new-manager"
			case "root":
				if err := os.Rename(f.path, f.path+".moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(f.path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			calls := len(f.calls)
			if err := q.Rollback(context.Background(), tx); err == nil {
				t.Fatal("unknown state erased")
			}
			for _, call := range f.calls[calls:] {
				if call != "Observe" {
					t.Fatal("unknown state caused service or file changes")
				}
			}
		})
	}
}

// TestQuiescenceRequiresEmptyCgroups blocks a native start after a nominally
// successful vendor stop that leaves a hidden process holding resources.
func TestQuiescenceRequiresEmptyCgroups(t *testing.T) {
	q, f := engineFixture(t, true)
	f.after = func(name string) {
		if name == "StopVendor" {
			f.observation.Vendor.CGroupEmpty = false
		}
	}
	if _, err := q.Apply(context.Background()); err == nil {
		t.Fatal("surviving vendor cgroup accepted")
	}
	if slices.Contains(f.calls, "StartNative") {
		t.Fatal("native started beside leftover vendor process")
	}
}

// TestRestorationRequiresEffectiveVendorDefinition rejects no-effect reloads.
func TestRestorationRequiresEffectiveVendorDefinition(t *testing.T) {
	for _, kind := range []string{"stale-mask", "wrong-definition"} {
		t.Run(kind, func(t *testing.T) {
			q, f := engineFixture(t, false)
			tx, err := q.Apply(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			f.after = func(name string) {
				if name == "ReleaseVendor" {
					f.staleReload = true
				}
				if name == "Reload" && kind == "wrong-definition" {
					f.observation.Vendor.LoadState = "loaded"
					f.observation.Vendor.DefinitionSHA256 = strings.Repeat("d", 64)
				}
			}
			if err := q.Rollback(context.Background(), tx); err == nil {
				t.Fatal("ineffective vendor restoration accepted")
			}
			if phase, _ := tx.Status(); phase != RecoveryRequired {
				t.Fatal("ineffective restoration claimed restored")
			}
		})
	}
}

// TestOpaquePublicBoundaryAndForeignHandles rejects zero bundles and replays.
func TestOpaquePublicBoundaryAndForeignHandles(t *testing.T) {
	for _, bundle := range []*integration.Bundle{nil, {}} {
		if _, err := NewQualification(bundle, freshBackend(t, false)); err == nil {
			t.Fatal("unverified integration bundle admitted")
		}
	}
	q, f := engineFixture(t, false)
	tx, err := q.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	other, _ := engineFixture(t, false)
	calls := len(f.calls)
	if err := other.Rollback(context.Background(), tx); err == nil {
		t.Fatal("foreign engine accepted transaction")
	}
	if len(f.calls) != calls {
		t.Fatal("foreign handle changed original backend")
	}
}

// count counts fixed fixture operations for no-retry assertions.
func count(values []string, want string) int {
	n := 0
	for _, v := range values {
		if v == want {
			n++
		}
	}
	return n
}

// sameFixtureFile retains inode and metadata ownership, not byte-only adoption.
// It is a test backend guard, not a claim of production fd-relative atomicity.
func sameFixtureFile(current, created fs.FileInfo) bool {
	if current == nil || created == nil || !os.SameFile(current, created) || current.Mode() != created.Mode() || current.Size() != created.Size() || !current.ModTime().Equal(created.ModTime()) {
		return false
	}
	stat, ok := current.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink == 1
}

// exactUnitBytes checks type and original identity before any read, then opens
// without following links or blocking on special files. Every read is bounded
// by the exact expected unit length and rechecks descriptor/path identity.
func (f *fixtureBackend) exactUnitBytes(name string, before fs.FileInfo, want []byte) bool {
	if !before.Mode().IsRegular() || !sameFixtureFile(before, f.unitInfo) || before.Size() != int64(len(want)) {
		return false
	}
	parent, err := f.root.Open("run/systemd/system")
	if err != nil {
		return false
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), filepath.Base(name), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Nlink != 1 || stat.Size != int64(len(want)) {
		return false
	}
	opened, err := file.Stat()
	if err != nil || !sameFixtureFile(opened, f.unitInfo) || !os.SameFile(before, opened) {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(len(want))+1))
	if err != nil || !slices.Equal(data, want) {
		return false
	}
	after, err := file.Stat()
	if err != nil || !sameFixtureFile(after, f.unitInfo) {
		return false
	}
	named, err := f.root.Lstat(name)
	return err == nil && sameFixtureFile(named, f.unitInfo) && os.SameFile(after, named)
}

// removalBoundary models the backend's own effect-boundary admission rather
// than assuming the engine's previous observation remains current forever.
func (f *fixtureBackend) removalBoundary(c *Claim) error {
	mask := f.fileEvidence("run/systemd/system/"+integration.VendorUnitName, true)
	unit := f.fileEvidence("run/systemd/system/"+integration.UnitName, false)
	if !mask.Present || !mask.Exact || !mask.Effective || mask.ClaimID != c.ID() || f.observation.Vendor.LoadState != "masked" || !quiescent(f.observation.Vendor) || !quiescent(f.observation.Native) || f.observation.Owner.UniqueName != "" {
		return errors.New("fixture quiescence or exclusion changed at removal boundary")
	}
	if !unit.Present || !unit.Exact || unit.ClaimID != c.ID() {
		return errors.New("fixture unit ownership changed at removal boundary")
	}
	if unit.Effective {
		if f.observation.Native.LoadState != "loaded" || f.observation.Native.DefinitionSHA256 != c.snapshot.UnitSHA256 {
			return errors.New("fixture loaded definition changed at removal boundary")
		}
	} else if f.observation.Native.LoadState != "not-found" || f.observation.Native.DefinitionSHA256 != "" {
		return errors.New("unknown partial fixture registration at removal boundary")
	}
	return nil
}

// TestUnsafeUnitReplacementsRefuseWithoutBlocking covers the fixture observer's
// early type/identity guard, bounded reads and no-follow special-file handling.
func TestUnsafeUnitReplacementsRefuseWithoutBlocking(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink", "hardlink", "oversized-regular"} {
		t.Run(kind, func(t *testing.T) {
			q, f := engineFixture(t, true)
			tx, err := q.Apply(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			name := "run/systemd/system/" + integration.UnitName
			saved := name + ".original"
			if err := f.root.Rename(name, saved); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "fifo":
				if err := unix.Mkfifo(filepath.Join(f.path, name), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := f.root.Symlink(filepath.Base(saved), name); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(filepath.Join(f.path, saved), filepath.Join(f.path, name)); err != nil {
					t.Fatal(err)
				}
			case "oversized-regular":
				file, err := f.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(32 << 20); err != nil {
					file.Close()
					t.Fatal(err)
				}
				file.Close()
			}
			calls := len(f.calls)
			done := make(chan error, 1)
			go func() { done <- q.Rollback(context.Background(), tx) }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("unsafe replacement admitted")
				}
			case <-time.After(time.Second):
				t.Fatal("unsafe replacement blocked outside the bounded observer contract")
			}
			for _, call := range f.calls[calls:] {
				if call != "Observe" {
					t.Fatal("unsafe replacement caused mutations")
				}
			}
		})
	}
}

// TestPostStopRegistrationDriftRetainsInhibition refuses an unknown definition
// before deletion or release, even though the native process has exited.
func TestPostStopRegistrationDriftRetainsInhibition(t *testing.T) {
	for _, kind := range []string{"definition", "effective"} {
		t.Run(kind, func(t *testing.T) {
			q, f := engineFixture(t, true)
			tx, err := q.Apply(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			f.after = func(name string) {
				if name == "StopNative" {
					if kind == "definition" {
						f.observation.Native.DefinitionSHA256 = strings.Repeat("d", 64)
					} else {
						f.observation.NativeFile.Effective = false
					}
				}
			}
			calls := len(f.calls)
			if err := q.Rollback(context.Background(), tx); err == nil {
				t.Fatal("post-stop registration drift admitted")
			}
			for _, forbidden := range []string{"RemoveNative", "ReleaseVendor", "StartVendor"} {
				if slices.Contains(f.calls[calls:], forbidden) {
					t.Fatalf("post-stop drift allowed %s", forbidden)
				}
			}
			if !f.fileEvidence("run/systemd/system/"+integration.VendorUnitName, true).Present {
				t.Fatal("post-stop drift released inhibition")
			}
		})
	}
}

// TestRemovalEffectBoundaryRechecksDefinition covers a mutation occurring after
// the engine's observation but before the backend's actual file removal.
func TestRemovalEffectBoundaryRechecksDefinition(t *testing.T) {
	q, f := engineFixture(t, true)
	tx, err := q.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.before = func(name string) {
		if name == "RemoveNative" {
			f.observation.Native.DefinitionSHA256 = strings.Repeat("d", 64)
		}
	}
	calls := len(f.calls)
	if err := q.Rollback(context.Background(), tx); err == nil {
		t.Fatal("effect-boundary drift admitted")
	}
	if !f.fileEvidence("run/systemd/system/"+integration.UnitName, false).Present || !f.fileEvidence("run/systemd/system/"+integration.VendorUnitName, true).Present || slices.Contains(f.calls[calls:], "ReleaseVendor") || slices.Contains(f.calls[calls:], "StartVendor") {
		t.Fatal("effect-boundary drift erased fixtures or allowed competitor")
	}
}

// TestByteIdenticalReplacementIsNotOwned ensures matching bytes or /dev/null
// targets cannot adopt a foreign inode and turn a receipt into deletion rights.
func TestByteIdenticalReplacementIsNotOwned(t *testing.T) {
	for _, kind := range []string{"mask", "unit"} {
		t.Run(kind, func(t *testing.T) {
			q, f := engineFixture(t, true)
			tx, err := q.Apply(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			name := "run/systemd/system/" + integration.UnitName
			if kind == "mask" {
				name = "run/systemd/system/" + integration.VendorUnitName
			}
			saved := name + ".original"
			if err := f.root.Rename(name, saved); err != nil {
				t.Fatal(err)
			}
			if kind == "mask" {
				if err := f.root.Symlink("/dev/null", name); err != nil {
					t.Fatal(err)
				}
			} else {
				unit, _ := tx.claim.Unit()
				if err := f.root.WriteFile(name, unit, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			before := fixtureSnapshot(t, f.path)
			calls := len(f.calls)
			if err := q.Rollback(context.Background(), tx); err == nil {
				t.Fatal("byte-identical foreign file was adopted")
			}
			if !reflect.DeepEqual(before, fixtureSnapshot(t, f.path)) {
				t.Fatal("foreign replacement erased")
			}
			for _, call := range f.calls[calls:] {
				if call != "Observe" {
					t.Fatal("foreign file caused manager mutation")
				}
			}
		})
	}
}

// TestSecondReloadFaultCompensates covers the distinct post-registration reload.
func TestSecondReloadFaultCompensates(t *testing.T) {
	for _, when := range []string{"before", "after"} {
		t.Run(when, func(t *testing.T) {
			q, f := engineFixture(t, true)
			before := fixtureSnapshot(t, f.path)
			f.after = func(name string) {
				if name == "InstallNative" {
					f.faults["Reload:"+when] = true
				}
			}
			tx, err := q.Apply(context.Background())
			if err == nil || tx == nil {
				t.Fatal("second reload fault ignored")
			}
			if phase, _ := tx.Status(); phase != Restored {
				t.Fatalf("second reload compensation %s: %v", phase, err)
			}
			if slices.Contains(f.calls, "StartNative") || !reflect.DeepEqual(before, fixtureSnapshot(t, f.path)) {
				t.Fatal("second reload fault started native or changed baseline")
			}
		})
	}
}

// TestPendingNativeWorkRetainsInhibition models reported late work without
// permitting competitor restoration after a superficially successful stop.
func TestPendingNativeWorkRetainsInhibition(t *testing.T) {
	q, f := engineFixture(t, true)
	tx, err := q.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.after = func(name string) {
		if name == "StopNative" {
			f.observation.Native.PendingJob = true
		}
	}
	calls := len(f.calls)
	if err := q.Rollback(context.Background(), tx); err == nil {
		t.Fatal("pending native work was hidden")
	}
	if !f.fileEvidence("run/systemd/system/"+integration.VendorUnitName, true).Present || slices.Contains(f.calls[calls:], "ReleaseVendor") || slices.Contains(f.calls[calls:], "StartVendor") {
		t.Fatal("pending native work allowed competitor")
	}
}

// TestContradictoryEffectiveMaskNeverStartsNative refuses a backend observation
// claiming both effective exclusion and an unmasked loaded vendor definition.
func TestContradictoryEffectiveMaskNeverStartsNative(t *testing.T) {
	q, f := engineFixture(t, true)
	f.observe = func(o *Observation) {
		if o.Inhibition.Effective {
			o.Vendor.LoadState = "loaded"
		}
	}
	if _, err := q.Apply(context.Background()); err == nil {
		t.Fatal("contradictory mask observation admitted")
	}
	if slices.Contains(f.calls, "StartNative") {
		t.Fatal("native started without proved effective mask")
	}
}
