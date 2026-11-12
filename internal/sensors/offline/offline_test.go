//go:build linux || darwin

package offline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ooaklee/lexr.sh/internal/sensors/packageplan"
	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
	"golang.org/x/sys/unix"
)

// syntheticInput tests mechanics without forging an opaque verified package.
// It is not native producer, public package or physical sensor qualification.
func syntheticInput(t *testing.T) packageInput {
	t.Helper()
	m := &compatibility.Manifest{SchemaVersion: 1, Kind: "lexr.userspace-component-compatibility", ComponentID: packageplan.ComponentID, Release: packageplan.ProposedRelease, Capabilities: []string{"sensors"}, Lexr: compatibility.VersionRange{MinimumInclusive: "0.7.0"}, Targets: []compatibility.TargetRule{{Architectures: []string{"arm64"}, DeviceProfiles: []string{"x1e80100-microsoft-denali-oled"}, OperatingSystems: []compatibility.OperatingSystem{{ID: "ubuntu", VersionRanges: []compatibility.OSRange{{MinimumInclusive: "26.04", MaximumExclusive: "26.10"}}, TestedVersions: []string{}}}, Kernels: []compatibility.KernelRule{{PatchLine: "7.2.9", PlatformFlavour: "qcom-x1e", Scope: "sp11", ABIGeneration: compatibility.GenerationRange{MinimumInclusive: 2, MaximumExclusive: 3}}}}}}
	if err := compatibility.Validate(m); err != nil {
		t.Fatal(err)
	}
	declaration, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	in := packageInput{ref: runtimepkg.Reference{Bytes: 2048, SHA256: strings.Repeat("a", 64)}, declaration: declaration, assess: func(target compatibility.Target) compatibility.Decision { return compatibility.Evaluate(m, target) }}
	for _, name := range []string{"bin/ssccli", "bin/monitor-sensor", "libexec/iio-sensor-proxy", "lib/libqmi-glib.so.5.12.0", "lib/libssc.so.2", "share/licenses/COPYING.libqmi", "share/licenses/COPYING.LIB.libqmi", "share/licenses/LICENSE.libssc", "share/licenses/COPYING.iio-sensor-proxy"} {
		mode := int64(0o755)
		if strings.HasPrefix(name, "share/") {
			mode = 0o644
		}
		in.files = append(in.files, runtimepkg.File{Path: name, Data: []byte("fixture-only:" + name + "\n"), Mode: mode})
	}
	in.links = []runtimepkg.Link{{Path: "lib/libqmi-glib.so", Target: "libqmi-glib.so.5"}, {Path: "lib/libqmi-glib.so.5", Target: "libqmi-glib.so.5.12.0"}, {Path: "lib/libssc.so", Target: "libssc.so.2"}}
	return in
}

// syntheticRequest identifies a complete, explicitly unverified test tuple.
func syntheticRequest() Request {
	return Request{Target: compatibility.Target{LexrVersion: "0.7.0", Architecture: "arm64", DeviceProfile: "x1e80100-microsoft-denali-oled", OSID: "ubuntu", OSVersion: "26.04", KernelABI: "7.2.9-jg-0sp11v2-qcom-x1e"}, AllowUnverified: true}
}

// privateTestParent supplies a real canonical current-user private test parent.
func privateTestParent(t *testing.T) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// testSession creates a fresh root with only a synthetic offline OS fixture.
func testSession(t *testing.T) *Session {
	t.Helper()
	s, err := NewSession(privateTestParent(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := os.Mkdir(filepath.Join(s.Path(), "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Path(), "etc/os-release"), []byte("ID=ubuntu\nVERSION_ID=26.04\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return s
}

// snapshot records exact fixture paths, types, modes, bytes and alias targets.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		value := info.Mode().String()
		switch {
		case info.Mode().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			value += " " + Digest(data)
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			value += " " + target
		}
		result[rel] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// TestFreshPlanInstallRepeatRollback proves passive transaction byte closure.
func TestFreshPlanInstallRepeatRollback(t *testing.T) {
	s := testSession(t)
	in := syntheticInput(t)
	req := syntheticRequest()
	before := snapshot(t, s.Path())
	evidenceBefore := snapshot(t, filepath.Dir(s.Path()))
	p, err := s.plan(in, req)
	if err != nil {
		t.Fatal(err)
	}
	if p.Decision.Status != compatibility.Unverified || !p.QualificationOnly || p.Decision.TargetIndex != 0 || len(p.Decision.Reasons) != 3 {
		t.Fatalf("unknown evidence was not retained: %+v", p)
	}
	if !reflect.DeepEqual(before, snapshot(t, s.Path())) || !reflect.DeepEqual(evidenceBefore, snapshot(t, filepath.Dir(s.Path()))) {
		t.Fatal("plan wrote target or evidence")
	}
	h, err := s.install(in, req)
	if err != nil {
		t.Fatal(err)
	}
	if !h.published || !h.committed {
		t.Fatal("successful install was not committed")
	}
	if err := h.verifyFinal(); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(s.Path(), strings.TrimPrefix(packageplan.PrivatePrefix, "/"))
	data, err := os.ReadFile(filepath.Join(prefix, ReceiptFilename))
	if err != nil {
		t.Fatal(err)
	}
	var got receipt
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Transaction != h.tx || got.Plan.Package != in.ref || !got.Plan.QualificationOnly || !got.Plan.AllowUnverified || got.Plan.Decision.Status != compatibility.Unverified {
		t.Fatalf("receipt binding differs: %+v", got)
	}
	declaration, err := os.ReadFile(filepath.Join(prefix, compatibility.Filename))
	if err != nil || !bytes.Equal(declaration, in.declaration) {
		t.Fatal("exact declaration was not retained")
	}
	installed := snapshot(t, filepath.Dir(s.Path()))
	again, err := s.install(in, req)
	if err != nil || again != h {
		t.Fatalf("exact managed repeat not idempotent: %v", err)
	}
	if !reflect.DeepEqual(installed, snapshot(t, filepath.Dir(s.Path()))) {
		t.Fatal("managed repeat changed bytes or evidence")
	}
	if err := s.Rollback(h); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshot(t, s.Path())) {
		t.Fatalf("rollback did not restore preinstall snapshot: before=%v after=%v", before, snapshot(t, s.Path()))
	}
	if err := s.Rollback(h); err == nil {
		t.Fatal("replayed handle granted deletion authority")
	}
	if len(mustReadDir(t, filepath.Join(filepath.Dir(s.Path()), "evidence"))) != 4 {
		t.Fatal("immutable outside-root diagnostic phases differ")
	}
}

// mustReadDir reads fixture entries, failing without a secondary panic.
func mustReadDir(t *testing.T, name string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(name)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestNewSessionCannotAdoptRootsOrAliases protects the fresh-root-only API.
func TestNewSessionCannotAdoptRootsOrAliases(t *testing.T) {
	p := privateTestParent(t)
	if err := os.WriteFile(filepath.Join(p, "preserve"), []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "/", ".", p + "/.", p + "/.."} {
		if s, err := NewSession(bad); err == nil {
			s.Close()
			t.Fatalf("adopted unsafe parent %q", bad)
		}
	}
	alias := filepath.Join(p, "alias")
	if err := os.Symlink(p, alias); err != nil {
		t.Fatal(err)
	}
	if s, err := NewSession(alias); err == nil {
		s.Close()
		t.Fatal("accepted parent symlink")
	}
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if s, err := NewSession(p); err == nil {
		s.Close()
		t.Fatal("accepted public parent")
	}
	if err := os.Chmod(p, 0o700); err != nil {
		t.Fatal(err)
	}
	s1, err := NewSession(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	s2, err := NewSession(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s1.Path() == s2.Path() || s1.Path() == p || s2.Path() == p {
		t.Fatal("adopted or reused a root")
	}
	data, err := os.ReadFile(filepath.Join(p, "preserve"))
	if err != nil || string(data) != "unchanged" {
		t.Fatal("parent fixture changed")
	}
}

// TestPublicBoundaryRejectsUnverifiedPackages prevents synthetic fixture escape.
func TestPublicBoundaryRejectsUnverifiedPackages(t *testing.T) {
	s := testSession(t)
	for _, p := range []*runtimepkg.VerifiedPackage{nil, {}} {
		if _, err := s.Plan(p, syntheticRequest()); err == nil {
			t.Fatal("unverified package produced plan")
		}
		if _, err := s.Install(p, syntheticRequest()); err == nil {
			t.Fatal("unverified package installed")
		}
	}
}

// TestCompatibilityRefusalNeverWrites proves no force-like escape is present.
func TestCompatibilityRefusalNeverWrites(t *testing.T) {
	for _, name := range []string{"missing-override", "old-lexr", "wrong-kernel", "wrong-architecture", "wrong-device", "missing-kernel", "wrong-root-os", "explicit-os-mismatch", "development-identity"} {
		t.Run(name, func(t *testing.T) {
			s := testSession(t)
			in := syntheticInput(t)
			req := syntheticRequest()
			switch name {
			case "missing-override":
				req.AllowUnverified = false
			case "old-lexr":
				req.Target.LexrVersion = "0.6.0"
			case "wrong-kernel":
				req.Target.KernelABI = "7.2.9-jg-0sp11v3-qcom-x1e"
			case "wrong-architecture":
				req.Target.Architecture = "amd64"
			case "wrong-device":
				req.Target.DeviceProfile = "x1p64100-microsoft-denali"
			case "missing-kernel":
				req.Target.KernelABI = ""
			case "wrong-root-os":
				if err := os.WriteFile(filepath.Join(s.Path(), "etc/os-release"), []byte("ID=fedora\nVERSION_ID=44\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				req.Target.OSID = ""
				req.Target.OSVersion = ""
			case "explicit-os-mismatch":
				req.Target.OSVersion = "24.04"
			case "development-identity":
				req.Target.LexrVersion = "dev"
				req.AllowUnverified = false
			}
			before := snapshot(t, filepath.Dir(s.Path()))
			if _, err := s.install(in, req); err == nil {
				t.Fatal("refused compatibility tuple installed")
			}
			if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(s.Path()))) {
				t.Fatal("refused tuple wrote output")
			}
		})
	}
	// Omitted explicit OS fields use the selected root, never runtime.GOOS.
	s := testSession(t)
	req := syntheticRequest()
	req.Target.OSID = ""
	req.Target.OSVersion = ""
	p, err := s.plan(syntheticInput(t), req)
	if err != nil || p.Target.OSID != "ubuntu" || p.Target.OSVersion != "26.04" {
		t.Fatalf("selected-root identity missing: %+v %v", p, err)
	}
}

// TestOSReleaseUnsafeTypesNeverBlock rejects redirected or multiply linked OS data.
func TestOSReleaseUnsafeTypesNeverBlock(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "etc-symlink"} {
		t.Run(kind, func(t *testing.T) {
			s := testSession(t)
			file := filepath.Join(s.Path(), "etc/os-release")
			if kind == "etc-symlink" {
				if err := os.Rename(filepath.Join(s.Path(), "etc"), filepath.Join(s.Path(), "saved-etc")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("saved-etc", filepath.Join(s.Path(), "etc")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Rename(file, file+".saved"); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "symlink":
					if err := os.Symlink("os-release.saved", file); err != nil {
						t.Fatal(err)
					}
				case "hardlink":
					if err := os.Link(file+".saved", file); err != nil {
						t.Fatal(err)
					}
				case "fifo":
					if err := unix.Mkfifo(file, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := s.plan(syntheticInput(t), syntheticRequest()); err == nil {
				t.Fatal("unsafe OS path was read")
			}
		})
	}
}

// TestUnmanagedAndUnsafePrefixConflictsRefuse prevents no-overwrite regressions.
func TestUnmanagedAndUnsafePrefixConflictsRefuse(t *testing.T) {
	for _, kind := range []string{"unmanaged", "ancestor-symlink", "ancestor-file", "ancestor-fifo", "wrong-mode"} {
		t.Run(kind, func(t *testing.T) {
			s := testSession(t)
			usr := filepath.Join(s.Path(), "usr")
			switch kind {
			case "unmanaged":
				if err := os.MkdirAll(filepath.Join(s.Path(), strings.TrimPrefix(packageplan.PrivatePrefix, "/")), 0o755); err != nil {
					t.Fatal(err)
				}
			case "ancestor-symlink":
				if err := os.Mkdir(filepath.Join(s.Path(), "other"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("other", usr); err != nil {
					t.Fatal(err)
				}
			case "ancestor-file":
				if err := os.WriteFile(usr, []byte("untouched"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "ancestor-fifo":
				if err := unix.Mkfifo(usr, 0o600); err != nil {
					t.Fatal(err)
				}
			case "wrong-mode":
				if err := os.Mkdir(usr, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshot(t, filepath.Dir(s.Path()))
			if _, err := s.install(syntheticInput(t), syntheticRequest()); err == nil {
				t.Fatal("unsafe/unmanaged prefix overwritten")
			}
			if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(s.Path()))) {
				t.Fatal("prefix conflict changed target or evidence")
			}
		})
	}
}

// TestManagedMutationRefusesRepeatAndRollback preserves externally altered data.
func TestManagedMutationRefusesRepeatAndRollback(t *testing.T) {
	for _, kind := range []string{"bytes", "mode", "extra", "symlink", "hardlink", "fifo", "alias", "receipt", "declaration", "directory-symlink", "directory-mode"} {
		t.Run(kind, func(t *testing.T) {
			s := testSession(t)
			in := syntheticInput(t)
			h, err := s.install(in, syntheticRequest())
			if err != nil {
				t.Fatal(err)
			}
			prefix := filepath.Join(s.Path(), strings.TrimPrefix(packageplan.PrivatePrefix, "/"))
			member := filepath.Join(prefix, "bin/ssccli")
			switch kind {
			case "bytes":
				err = os.WriteFile(member, []byte("changed"), 0o755)
			case "mode":
				err = os.Chmod(member, 0o644)
			case "extra":
				err = os.WriteFile(filepath.Join(prefix, "extra"), []byte("retain"), 0o600)
			case "symlink", "hardlink", "fifo":
				if err = os.Rename(member, member+".saved"); err == nil {
					switch kind {
					case "symlink":
						err = os.Symlink("ssccli.saved", member)
					case "hardlink":
						err = os.Link(member+".saved", member)
					case "fifo":
						err = unix.Mkfifo(member, 0o600)
					}
				}
			case "alias":
				a := filepath.Join(prefix, "lib/libssc.so")
				if err = os.Remove(a); err == nil {
					err = os.Symlink("../../outside", a)
				}
			case "receipt":
				err = os.WriteFile(filepath.Join(prefix, ReceiptFilename), []byte("{}"), 0o644)
			case "declaration":
				err = os.WriteFile(filepath.Join(prefix, compatibility.Filename), []byte("{}"), 0o644)
			case "directory-symlink":
				if err = os.Rename(filepath.Join(prefix, "bin"), filepath.Join(prefix, "saved-bin")); err == nil {
					err = os.Symlink("saved-bin", filepath.Join(prefix, "bin"))
				}
			case "directory-mode":
				err = os.Chmod(filepath.Join(prefix, "bin"), 0o700)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, filepath.Dir(s.Path()))
			if _, err := s.install(in, syntheticRequest()); err == nil {
				t.Fatal("modified managed tree accepted")
			}
			if err := s.Rollback(h); err == nil {
				t.Fatal("modified managed tree erased")
			}
			if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(s.Path()))) {
				t.Fatal("refusal changed mutated tree or evidence")
			}
		})
	}
}

// TestDifferentReferenceRefusesUpgrade preserves the single-release contract.
func TestDifferentReferenceRefusesUpgrade(t *testing.T) {
	s := testSession(t)
	in := syntheticInput(t)
	if _, err := s.install(in, syntheticRequest()); err != nil {
		t.Fatal(err)
	}
	in.ref.SHA256 = strings.Repeat("b", 64)
	before := snapshot(t, filepath.Dir(s.Path()))
	if _, err := s.install(in, syntheticRequest()); err == nil {
		t.Fatal("different package overwrote managed prefix")
	}
	if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(s.Path()))) {
		t.Fatal("refused upgrade wrote output")
	}
}

// TestPathAndHandleSwapsRefuse ensures retained identity gates detect replacements.
func TestPathAndHandleSwapsRefuse(t *testing.T) {
	for _, kind := range []string{"target", "base", "parent", "installed-parent", "installed-prefix"} {
		t.Run(kind, func(t *testing.T) {
			s := testSession(t)
			in := syntheticInput(t)
			var h *Installation
			var err error
			if strings.HasPrefix(kind, "installed") {
				h, err = s.install(in, syntheticRequest())
				if err != nil {
					t.Fatal(err)
				}
			}
			var name string
			switch kind {
			case "target":
				name = s.Path()
			case "base":
				name = filepath.Dir(s.Path())
			case "parent":
				name = s.parentPath
			case "installed-parent":
				name = filepath.Join(s.Path(), "usr/lib/lexr/sensors")
			case "installed-prefix":
				name = filepath.Join(s.Path(), strings.TrimPrefix(packageplan.PrivatePrefix, "/"))
			}
			if err := os.Rename(name, name+".moved"); err != nil {
				t.Fatal(err)
			}
			mode := os.FileMode(0o700)
			if strings.HasPrefix(kind, "installed") {
				mode = 0o755
			}
			if err := os.Mkdir(name, mode); err != nil {
				t.Fatal(err)
			}
			if _, err := s.install(in, syntheticRequest()); err == nil {
				t.Fatal("replacement directory accepted")
			}
			if h != nil {
				if err := s.Rollback(h); err == nil {
					t.Fatal("replacement directory rolled back")
				}
			}
			if len(mustReadDir(t, name)) != 0 {
				t.Fatal("replacement directory changed")
			}
		})
	}
}

// TestFailureRetentionAndExclusiveRename exercises real platform publication.
func TestFailureRetentionAndExclusiveRename(t *testing.T) {
	for _, point := range []string{"file-synced:bin/ssccli", "sync:stage", "before-publish", "sync:published-parent", "after-publish"} {
		t.Run(point, func(t *testing.T) {
			s := testSession(t)
			s.hook = func(got string) error {
				if got == point {
					return errors.New("injected " + point)
				}
				return nil
			}
			h, err := s.install(syntheticInput(t), syntheticRequest())
			if err == nil || h == nil {
				t.Fatal("injected failure did not retain handle")
			}
			if h.committed {
				t.Fatal("failed transaction claimed committed")
			}
			if h.tree == nil {
				t.Fatal("partial tree missing")
			}
			if point != "file-synced:bin/ssccli" {
				if err := h.verifyTree(); err != nil {
					t.Fatalf("completed retained tree is not closed: %v", err)
				}
			} else if _, err := os.Stat(filepath.Join(s.Path(), "usr/lib/lexr/sensors", h.tree.name, "bin/ssccli")); err != nil {
				t.Fatal("early partial stage was not retained")
			}
			if _, err := s.install(syntheticInput(t), syntheticRequest()); err == nil {
				t.Fatal("incomplete managed transaction was treated as success")
			}
			s.hook = nil
			if h.published {
				if err := s.Rollback(h); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.Rollback(h); err == nil {
					t.Fatal("partial stage granted published rollback authority")
				}
				if _, err := os.Stat(filepath.Join(s.Path(), "usr/lib/lexr/sensors", h.tree.name)); err != nil {
					t.Fatal("partial stage not retained")
				}
			}
		})
	}
	// Create the destination at the final hook. The kernel primitive, not a
	// precheck, must refuse replacement on actual Darwin and Linux filesystems.
	s := testSession(t)
	s.hook = func(point string) error {
		if point == "before-publish" {
			return os.Mkdir(filepath.Join(s.Path(), strings.TrimPrefix(packageplan.PrivatePrefix, "/")), 0o755)
		}
		return nil
	}
	h, err := s.install(syntheticInput(t), syntheticRequest())
	if err == nil || h == nil || h.published {
		t.Fatal("exclusive directory rename replaced conflict")
	}
	if len(mustReadDir(t, filepath.Join(s.Path(), strings.TrimPrefix(packageplan.PrivatePrefix, "/")))) != 0 {
		t.Fatal("conflicting directory was modified")
	}
	if _, err := os.Stat(filepath.Join(s.Path(), "usr/lib/lexr/sensors", h.tree.name)); err != nil {
		t.Fatal("refused stage was not retained")
	}
}

// TestRollbackFailureRetainsQuarantine checks no recursive uncertain cleanup.
func TestRollbackFailureRetainsQuarantine(t *testing.T) {
	for _, point := range []string{"sync:quarantine-parent", "after-quarantine"} {
		t.Run(point, func(t *testing.T) {
			s := testSession(t)
			h, err := s.install(syntheticInput(t), syntheticRequest())
			if err != nil {
				t.Fatal(err)
			}
			s.hook = func(got string) error {
				if got == point {
					return errors.New("injected rollback failure")
				}
				return nil
			}
			if err := s.Rollback(h); err == nil {
				t.Fatal("rollback fault ignored")
			}
			if !strings.HasSuffix(h.tree.name, ".rollback") {
				t.Fatal("quarantined state not retained")
			}
			if err := h.verifyTree(); err != nil {
				t.Fatal(err)
			}
			if err := s.Rollback(h); err == nil {
				t.Fatal("uncertain quarantine replay granted deletion")
			}
		})
	}
}

// TestClosedSessionAndForeignHandleRefuse prevents handle replays across roots.
func TestClosedSessionAndForeignHandleRefuse(t *testing.T) {
	s := testSession(t)
	other := testSession(t)
	h, err := s.install(syntheticInput(t), syntheticRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Rollback(h); err == nil {
		t.Fatal("foreign session accepted handle")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.plan(syntheticInput(t), syntheticRequest()); err == nil {
		t.Fatal("closed session planned")
	}
	if err := s.Rollback(h); err == nil {
		t.Fatal("closed session rolled back")
	}
}

// TestTraversalAndRosterAreClosed independently closes all payload paths.
func TestTraversalAndRosterAreClosed(t *testing.T) {
	for _, name := range []string{"../escape", "/etc/escape", "bin/../ssccli", "bin//ssccli", "bin\\ssccli", "bin/unknown"} {
		t.Run(fmt.Sprintf("path-%q", name), func(t *testing.T) {
			s := testSession(t)
			in := syntheticInput(t)
			in.files[0].Path = name
			if _, err := s.install(in, syntheticRequest()); err == nil {
				t.Fatal("unsafe payload member installed")
			}
		})
	}
	for _, name := range []string{".", "..", "../x", "/x", "x//y", "x\\y"} {
		if _, err := splitMember(name); err == nil {
			t.Fatalf("unsafe member accepted: %q", name)
		}
	}
}

// TestExistingAncestorsPreserved proves rollback removes only created parents.
func TestExistingAncestorsPreserved(t *testing.T) {
	s := testSession(t)
	if err := os.MkdirAll(filepath.Join(s.Path(), "usr/lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Path(), "usr/lib/distribution-owned"), []byte("retain"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, s.Path())
	h, err := s.install(syntheticInput(t), syntheticRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Rollback(h); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, snapshot(t, s.Path())) {
		t.Fatal("rollback changed preexisting ancestors")
	}
}

// TestSpaceAdmissionRefusesBeforeMutation tests the full staged payload budget
// without filling a filesystem, lowering guards or reserving hidden copies.
func TestSpaceAdmissionRefusesBeforeMutation(t *testing.T) {
	for _, kind := range []string{"insufficient", "observation-error"} {
		t.Run(kind, func(t *testing.T) {
			s := testSession(t)
			s.spaceProbe = func(*os.File) (int64, error) {
				if kind == "observation-error" {
					return 0, errors.New("unavailable space observation")
				}
				return 0, nil
			}
			before := snapshot(t, filepath.Dir(s.Path()))
			if h, err := s.install(syntheticInput(t), syntheticRequest()); err == nil || h != nil {
				t.Fatal("space refusal did not happen before mutation")
			}
			if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(s.Path()))) {
				t.Fatal("space refusal changed output")
			}
		})
	}
}

// TestTargetMetadataIsBounded rejects oversized or control-bearing identities
// even when unknown compatibility is explicitly permitted for qualification.
func TestTargetMetadataIsBounded(t *testing.T) {
	for _, kind := range []string{"huge-version", "newline-version", "nul-version", "invalid-utf8", "unicode-control", "huge-kernel", "huge-profile", "huge-os"} {
		t.Run(kind, func(t *testing.T) {
			s := testSession(t)
			req := syntheticRequest()
			switch kind {
			case "huge-version":
				req.Target.LexrVersion = strings.Repeat("a", 257)
			case "newline-version":
				req.Target.LexrVersion = "dev\ninjected"
			case "nul-version":
				req.Target.LexrVersion = "dev\x00"
			case "invalid-utf8":
				req.Target.LexrVersion = "dev\xff"
			case "unicode-control":
				req.Target.LexrVersion = "dev\u0085"
			case "huge-kernel":
				req.Target.KernelABI = strings.Repeat("a", 257)
			case "huge-profile":
				req.Target.DeviceProfile = strings.Repeat("a", 129)
			case "huge-os":
				req.Target.OSVersion = strings.Repeat("a", 65)
			}
			before := snapshot(t, filepath.Dir(s.Path()))
			if _, err := s.install(syntheticInput(t), req); err == nil {
				t.Fatal("unbounded target installed")
			}
			if !reflect.DeepEqual(before, snapshot(t, filepath.Dir(s.Path()))) {
				t.Fatal("bounded target refusal changed output")
			}
		})
	}
}
