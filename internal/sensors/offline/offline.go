// Package offline qualifies passive sensor packages in exclusively new roots.
// It is not a catalogue installer, service activator or existing-root API.
// Journals are diagnostic evidence, not crash-recovery or deletion authority.
package offline

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/ooaklee/lexr.sh/internal/sensors/packageplan"
	"github.com/ooaklee/lexr.sh/internal/sensors/runtimepkg"
	"github.com/ooaklee/lexr.sh/internal/userspace/compatibility"
)

// ReceiptFilename is passive metadata within the closed private prefix.
const ReceiptFilename = "offline-qualification-receipt.json"

// Request records an explicit synthetic qualification target, not host identity
// or evidence of production support. OS identity is read from the fresh root.
type Request struct {
	Target          compatibility.Target
	AllowUnverified bool
}

// Plan is a descriptive, non-executable observation. Install reassesses it.
type Plan struct {
	Package           runtimepkg.Reference    `json:"package"`
	Declaration       compatibility.Reference `json:"declaration"`
	Target            compatibility.Target    `json:"target"`
	Decision          compatibility.Decision  `json:"decision"`
	AllowUnverified   bool                    `json:"allow_unverified"`
	PrivatePrefix     string                  `json:"private_prefix"`
	QualificationOnly bool                    `json:"qualification_only"`
}

// receipt is descriptive only; decoding it never grants mutation authority.
type receipt struct {
	Schema      string `json:"schema"`
	Transaction string `json:"transaction"`
	Plan        Plan   `json:"plan"`
}

// directory retains both a rooted handle and a no-follow directory descriptor.
type directory struct {
	root        *os.Root
	file        *os.File
	parent      *directory
	name        string
	mode        fs.FileMode
	ownedParent bool
}

// Session owns a fresh scratch root and separate diagnostic evidence. It may
// not adopt an existing root, install on the host, or execute target programs.
type Session struct {
	mu         sync.Mutex
	parent     *directory
	base       *directory
	target     *directory
	evidence   *directory
	parentPath string
	targetPath string
	closed     bool
	managed    *Installation
	hook       func(string) error
	spaceProbe func(*os.File) (int64, error)
}

// Installation is process-local authority bound to its exact package, retained
// root, parent and tree. A journal or caller-supplied path cannot create one.
type Installation struct {
	session    *Session
	input      packageInput
	plan       Plan
	tx         string
	parent     *directory
	tree       *directory
	created    []*directory
	retained   []*directory
	files      []runtimepkg.File
	links      []runtimepkg.Link
	published  bool
	rolledBack bool
	committed  bool
}

// packageInput is an internal mechanical snapshot, never a public constructor.
// Tests can exercise filesystem failures without forging a VerifiedPackage.
type packageInput struct {
	ref         runtimepkg.Reference
	files       []runtimepkg.File
	links       []runtimepkg.Link
	declaration []byte
	assess      func(compatibility.Target) compatibility.Decision
}

// NewSession exclusively creates a fresh target and evidence beneath a
// canonical, current-user-owned 0700 directory. It never adopts an old root.
func NewSession(parent string) (*Session, error) {
	if err := supported(); err != nil {
		return nil, err
	}
	if parent == "" || !filepath.IsAbs(parent) || filepath.Clean(parent) != parent || parent == string(filepath.Separator) {
		return nil, errors.New("offline qualification requires an explicit canonical private parent, not a root")
	}
	real, err := filepath.EvalSymlinks(parent)
	if err != nil || real != parent {
		return nil, errors.New("offline parent must have no symlink or root alias")
	}
	if err := privateParent(parent); err != nil {
		return nil, err
	}
	p, err := openAbsoluteDirectory(parent, 0o700)
	if err != nil {
		return nil, err
	}
	s := &Session{parent: p, parentPath: parent}
	if err := s.verifyParent(); err != nil {
		p.close()
		return nil, err
	}
	name, err := uniqueName("sensor-offline-")
	if err != nil {
		p.close()
		return nil, err
	}
	if err := p.root.Mkdir(name, 0o700); err != nil {
		p.close()
		return nil, err
	}
	// Any newly created partial session is retained after failure for inspection.
	fail := func(e error) (*Session, error) {
		s.closeHandles()
		return nil, fmt.Errorf("offline session retained at %s: %w", filepath.Join(parent, name), e)
	}
	s.base, err = p.openDir(name, 0o700)
	if err != nil {
		return fail(err)
	}
	for _, child := range []string{"target", "evidence"} {
		if err := s.base.root.Mkdir(child, 0o700); err != nil {
			return fail(err)
		}
		d, err := s.base.openDir(child, 0o700)
		if err != nil {
			return fail(err)
		}
		if child == "target" {
			s.target = d
		} else {
			s.evidence = d
		}
	}
	s.targetPath = filepath.Join(parent, name, "target")
	for _, d := range []*directory{s.target, s.evidence, s.base, s.parent} {
		if err := d.file.Sync(); err != nil {
			return fail(err)
		}
	}
	if err := s.verify(); err != nil {
		return fail(err)
	}
	return s, nil
}

// Path exposes only this session's newly owned qualification root for fixtures.
func (s *Session) Path() string {
	if s == nil {
		return ""
	}
	return s.targetPath
}

// Close releases handles without deleting targets, evidence or partial stages.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.closeHandles()
	return nil
}

// closeHandles releases only descriptors created by this process.
func (s *Session) closeHandles() {
	if s.managed != nil {
		s.managed.closeDirectories()
	}
	for _, d := range []*directory{s.target, s.evidence, s.base, s.parent} {
		if d != nil {
			d.close()
		}
	}
}

// Plan reads the selected offline root and performs no filesystem writes.
func (s *Session) Plan(pkg *runtimepkg.VerifiedPackage, req Request) (Plan, error) {
	if s == nil {
		return Plan{}, errors.New("offline session is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	in, err := capturePackage(pkg)
	if err != nil {
		return Plan{}, err
	}
	return s.plan(in, req)
}

// Install stages and publishes one closed passive prefix, never global policy.
// A non-nil handle returned with an error describes retained partial state; it
// is not a claim that installation or rollback succeeded.
func (s *Session) Install(pkg *runtimepkg.VerifiedPackage, req Request) (*Installation, error) {
	if s == nil {
		return nil, errors.New("offline session is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	in, err := capturePackage(pkg)
	if err != nil {
		return nil, err
	}
	return s.install(in, req)
}

// capturePackage takes defensive copies through the opaque verified boundary.
func capturePackage(pkg *runtimepkg.VerifiedPackage) (packageInput, error) {
	if pkg == nil {
		return packageInput{}, errors.New("verified sensor runtime package is required")
	}
	in := packageInput{ref: pkg.Reference(), files: pkg.Files(), links: pkg.Links(), declaration: pkg.Declaration(), assess: pkg.Assess}
	if err := in.validate(); err != nil {
		return packageInput{}, err
	}
	return in, nil
}

// validate independently closes the only fixed destinations and modes.
func (in packageInput) validate() error {
	if in.ref.Bytes <= 0 || in.ref.Bytes > runtimepkg.MaximumPackageBytes || len(in.ref.SHA256) != 64 || strings.ToLower(in.ref.SHA256) != in.ref.SHA256 || in.assess == nil {
		return errors.New("invalid verified sensor package reference")
	}
	if _, err := hex.DecodeString(in.ref.SHA256); err != nil {
		return err
	}
	rules := map[string]int64{"bin/ssccli": 0o755, "bin/monitor-sensor": 0o755, "libexec/iio-sensor-proxy": 0o755, "lib/libqmi-glib.so.5.12.0": 0o755, "lib/libssc.so.2": 0o755, "share/licenses/COPYING.libqmi": 0o644, "share/licenses/COPYING.LIB.libqmi": 0o644, "share/licenses/LICENSE.libssc": 0o644, "share/licenses/COPYING.iio-sensor-proxy": 0o644}
	if len(in.files) != len(rules) {
		return errors.New("sensor payload file roster differs")
	}
	for _, f := range in.files {
		mode, ok := rules[f.Path]
		if !ok || mode != f.Mode || len(f.Data) == 0 {
			return errors.New("sensor payload path, mode or bytes differ")
		}
		delete(rules, f.Path)
	}
	want := map[string]string{"lib/libqmi-glib.so": "libqmi-glib.so.5", "lib/libqmi-glib.so.5": "libqmi-glib.so.5.12.0", "lib/libssc.so": "libssc.so.2"}
	if len(in.links) != len(want) {
		return errors.New("sensor payload alias roster differs")
	}
	for _, l := range in.links {
		target, ok := want[l.Path]
		if !ok || l.Target != target {
			return errors.New("sensor payload alias differs")
		}
		delete(want, l.Path)
	}
	if len(in.declaration) == 0 || len(in.declaration) > compatibility.MaxBytes {
		return errors.New("sensor declaration is absent or exceeds bound")
	}
	return nil
}

// plan checks root-derived OS and the complete selected compatibility tuple.
func (s *Session) plan(in packageInput, req Request) (Plan, error) {
	if err := s.verify(); err != nil {
		return Plan{}, err
	}
	if err := in.validate(); err != nil {
		return Plan{}, err
	}
	if err := boundedTarget(req.Target); err != nil {
		return Plan{}, err
	}
	id, version, err := s.readOS()
	if err != nil {
		return Plan{}, err
	}
	t := req.Target
	if t.OSID != "" && t.OSID != id || t.OSVersion != "" && t.OSVersion != version {
		return Plan{}, errors.New("explicit OS identity differs from the selected offline root")
	}
	t.OSID, t.OSVersion = id, version
	d := in.assess(t)
	if err := compatibility.RequireAllowed(d, req.AllowUnverified); err != nil {
		return Plan{}, err
	}
	return Plan{Package: in.ref, Declaration: compatibility.ReferenceFor(in.declaration), Target: t, Decision: d, AllowUnverified: req.AllowUnverified, PrivatePrefix: packageplan.PrivatePrefix, QualificationOnly: true}, nil
}

// readOS parses the exact no-follow, singly linked descriptor-captured bytes.
// A second descriptor read checks stability without reopening a host pathname.
func (s *Session) readOS() (string, string, error) {
	etc, err := s.target.openDir("etc", 0)
	if err != nil {
		return "", "", err
	}
	defer etc.close()
	before, info, err := readRegular(etc, "os-release", compatibility.MaxBytes, 0)
	if err != nil {
		return "", "", err
	}
	id, version, err := compatibility.ParseOSRelease(before)
	if err != nil {
		return "", "", err
	}
	after, afterInfo, err := readRegular(etc, "os-release", compatibility.MaxBytes, 0)
	if err != nil {
		return "", "", err
	}
	if !os.SameFile(info, afterInfo) || !bytes.Equal(before, after) {
		return "", "", errors.New("offline os-release changed during assessment")
	}
	if err := s.verify(); err != nil {
		return "", "", err
	}
	if err := etc.verify(); err != nil {
		return "", "", err
	}
	return id, version, nil
}

// install is the internal mechanical transaction after the opaque input gate.
func (s *Session) install(in packageInput, req Request) (*Installation, error) {
	p, err := s.plan(in, req)
	if err != nil {
		return nil, err
	}
	if s.managed != nil {
		h := s.managed
		if !h.committed || h.rolledBack || h.input.ref != in.ref || !reflect.DeepEqual(h.plan, p) {
			return nil, errors.New("different or incomplete managed sensor installation exists")
		}
		if err := h.verifyFinal(); err != nil {
			return nil, err
		}
		return h, nil
	}
	// Preflight the full existing ancestor chain and final absence before writes.
	if err := s.preflightPrefix(); err != nil {
		return nil, err
	}
	if err := s.space(in); err != nil {
		return nil, err
	}
	tx, err := uniqueName("")
	if err != nil {
		return nil, err
	}
	h := &Installation{session: s, input: in, plan: p, tx: tx}
	s.managed = h
	if err := s.journal(h, "prepared"); err != nil {
		return h, err
	}
	parent := s.target
	for _, name := range []string{"usr", "lib", "lexr", "sensors"} {
		if err := s.verify(); err != nil {
			return h, err
		}
		_, err := parent.root.Lstat(name)
		created := false
		if errors.Is(err, fs.ErrNotExist) {
			if err = parent.root.Mkdir(name, 0o755); err != nil {
				return h, err
			}
			created = true
			if err = s.syncDir(parent, "created-ancestor:"+name); err != nil {
				return h, err
			}
		} else if err != nil {
			return h, err
		}
		d, err := parent.openDir(name, 0o755)
		if err != nil {
			return h, err
		}
		if created {
			h.created = append(h.created, d)
		}
		// Retain every ancestor, including fixture-owned existing directories.
		h.retained = append(h.retained, d)
		parent = d
	}
	h.parent = parent
	stage := "." + packageplan.ProposedRelease + "-" + tx + ".partial"
	if err := parent.root.Mkdir(stage, 0o755); err != nil {
		return h, err
	}
	h.tree, err = parent.openDir(stage, 0o755)
	if err != nil {
		return h, err
	}
	h.files = append([]runtimepkg.File(nil), in.files...)
	h.links = append([]runtimepkg.Link(nil), in.links...)
	receiptData, err := json.Marshal(receipt{Schema: "lexr-sensors-offline-qualification-v1", Transaction: tx, Plan: p})
	if err != nil {
		return h, err
	}
	receiptData = append(receiptData, '\n')
	if len(receiptData) > compatibility.MaxBytes {
		return h, errors.New("offline receipt exceeds bound")
	}
	h.files = append(h.files, runtimepkg.File{Path: ReceiptFilename, Data: receiptData, Mode: 0o644}, runtimepkg.File{Path: compatibility.Filename, Data: append([]byte(nil), in.declaration...), Mode: 0o644})
	if err := h.writeTree(); err != nil {
		return h, err
	}
	if err := s.checkpoint("before-publish"); err != nil {
		return h, err
	}
	if _, err := s.plan(in, req); err != nil {
		return h, err
	}
	if err := h.verifyTree(); err != nil {
		return h, err
	}
	if err := s.verify(); err != nil {
		return h, err
	}
	if err := parent.verify(); err != nil {
		return h, err
	}
	if err := renameNoReplace(parent.file, stage, packageplan.ProposedRelease); err != nil {
		return h, err
	}
	h.tree.name = packageplan.ProposedRelease
	h.published = true
	if err := s.syncDir(parent, "published-parent"); err != nil {
		return h, err
	}
	if err := s.checkpoint("after-publish"); err != nil {
		return h, err
	}
	if err := h.verifyFinal(); err != nil {
		return h, err
	}
	if err := s.journal(h, "committed"); err != nil {
		return h, err
	}
	h.committed = true
	return h, nil
}

// preflightPrefix refuses all unmanaged final entries and unsafe ancestors.
func (s *Session) preflightPrefix() error {
	d := s.target
	var opened []*directory
	defer func() {
		for _, v := range opened {
			v.close()
		}
	}()
	for _, name := range []string{"usr", "lib", "lexr", "sensors"} {
		_, err := d.root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		next, err := d.openDir(name, 0o755)
		if err != nil {
			return err
		}
		opened = append(opened, next)
		d = next
	}
	if _, err := d.root.Lstat(packageplan.ProposedRelease); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return err
		}
		return errors.New("unmanaged sensor prefix already exists")
	}
	return nil
}

// writeTree creates only closed private directories, files and relative aliases.
func (h *Installation) writeTree() error {
	dirs := treeDirectories(h.files, h.links)
	for _, name := range dirs {
		if err := mkdirTree(h.tree, name); err != nil {
			return err
		}
	}
	for _, f := range h.files {
		d, base, err := memberParent(h.tree, f.Path)
		if err != nil {
			return err
		}
		err = writeExclusive(d, base, f.Data, fs.FileMode(f.Mode))
		d.close()
		if err != nil {
			return err
		}
		if err := h.session.checkpoint("file-synced:" + f.Path); err != nil {
			return err
		}
	}
	for _, l := range h.links {
		d, base, err := memberParent(h.tree, l.Path)
		if err != nil {
			return err
		}
		err = d.root.Symlink(l.Target, base)
		d.close()
		if err != nil {
			return err
		}
	}
	if err := h.verifyTree(); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		d, err := openTree(h.tree, dirs[i])
		if err != nil {
			return err
		}
		err = h.session.syncDir(d, "stage:"+dirs[i])
		d.close()
		if err != nil {
			return err
		}
	}
	return h.session.syncDir(h.tree, "stage")
}

// Rollback removes only an exact process-owned installation after quarantine.
// Unknown, altered or partially staged content is retained, never recursively
// deleted. It does not restore services because no activation is available.
func (s *Session) Rollback(h *Installation) error {
	if s == nil {
		return errors.New("offline session is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if h == nil || h.session != s || s.managed != h || !h.published || h.rolledBack {
		return errors.New("no process-owned published installation to roll back")
	}
	if err := h.verifyFinal(); err != nil {
		return err
	}
	if err := s.checkpoint("before-rollback"); err != nil {
		return err
	}
	if err := h.verifyFinal(); err != nil {
		return err
	}
	quarantine := "." + packageplan.ProposedRelease + "-" + h.tx + ".rollback"
	if err := renameNoReplace(h.parent.file, packageplan.ProposedRelease, quarantine); err != nil {
		return err
	}
	h.tree.name = quarantine
	if err := s.syncDir(h.parent, "quarantine-parent"); err != nil {
		return err
	}
	if err := s.checkpoint("after-quarantine"); err != nil {
		return err
	}
	if err := h.verifyTree(); err != nil {
		return err
	}
	if err := s.journal(h, "quarantined"); err != nil {
		return err
	}
	for _, f := range h.files {
		if err := h.removeMember(f.Path, false); err != nil {
			return err
		}
	}
	for _, l := range h.links {
		if err := h.removeMember(l.Path, true); err != nil {
			return err
		}
	}
	dirs := treeDirectories(h.files, h.links)
	for i := len(dirs) - 1; i >= 0; i-- {
		d, base, err := memberParent(h.tree, dirs[i])
		if err != nil {
			return err
		}
		err = removeDirectory(d.file, base)
		d.close()
		if err != nil {
			return err
		}
	}
	if err := h.tree.verify(); err != nil {
		return err
	}
	if err := removeDirectory(h.parent.file, quarantine); err != nil {
		return err
	}
	h.tree.close()
	h.tree = nil
	if err := s.syncDir(h.parent, "removed-parent"); err != nil {
		return err
	}
	for i := len(h.created) - 1; i >= 0; i-- {
		d := h.created[i]
		if err := d.verify(); err != nil {
			return err
		}
		if err := removeDirectory(d.parent.file, d.name); err != nil {
			return fmt.Errorf("created ancestor retained: %w", err)
		}
		if err := s.syncDir(d.parent, "removed-ancestor:"+d.name); err != nil {
			return err
		}
	}
	h.rolledBack = true
	s.managed = nil
	if err := s.journal(h, "rolled-back"); err != nil {
		return err
	}
	h.closeDirectories()
	return nil
}

// removeMember rechecks one exact known entry immediately before unlinking it.
func (h *Installation) removeMember(name string, link bool) error {
	if err := h.session.verify(); err != nil {
		return err
	}
	if err := h.tree.verify(); err != nil {
		return err
	}
	d, base, err := memberParent(h.tree, name)
	if err != nil {
		return err
	}
	defer d.close()
	if link {
		var target string
		for _, l := range h.links {
			if l.Path == name {
				target = l.Target
			}
		}
		if err := verifyLink(d, base, target); err != nil {
			return err
		}
	} else {
		var expected *runtimepkg.File
		for i := range h.files {
			if h.files[i].Path == name {
				expected = &h.files[i]
			}
		}
		if expected == nil {
			return errors.New("unknown rollback member")
		}
		if err := verifyFile(d, base, *expected); err != nil {
			return err
		}
	}
	return removeFile(d.file, base)
}

// verifyFinal requires the original prefix name and a complete exact tree.
func (h *Installation) verifyFinal() error {
	if h.tree == nil || !h.published || h.tree.name != packageplan.ProposedRelease {
		return errors.New("sensor installation is not in its committed prefix")
	}
	if err := h.session.verify(); err != nil {
		return err
	}
	return h.verifyTree()
}

// verifyTree closes all paths, bytes, modes, aliases and directory identities.
func (h *Installation) verifyTree() error {
	if h.tree == nil {
		return errors.New("sensor tree handle is absent")
	}
	if err := h.tree.verify(); err != nil {
		return err
	}
	want := map[string]string{}
	for _, d := range treeDirectories(h.files, h.links) {
		want[d] = "directory"
	}
	for _, f := range h.files {
		want[f.Path] = "file"
		d, base, err := memberParent(h.tree, f.Path)
		if err != nil {
			return err
		}
		err = verifyFile(d, base, f)
		d.close()
		if err != nil {
			return err
		}
	}
	for _, l := range h.links {
		want[l.Path] = "link"
		d, base, err := memberParent(h.tree, l.Path)
		if err != nil {
			return err
		}
		err = verifyLink(d, base, l.Target)
		d.close()
		if err != nil {
			return err
		}
	}
	got := map[string]string{}
	if err := inventory(h.tree, "", got); err != nil {
		return err
	}
	if !reflect.DeepEqual(want, got) {
		return errors.New("sensor prefix contains missing, unknown or changed members")
	}
	return h.tree.verify()
}

// closeDirectories releases retained installation handles without removing data.
func (h *Installation) closeDirectories() {
	if h.tree != nil {
		h.tree.close()
	}
	for i := len(h.retained) - 1; i >= 0; i-- {
		h.retained[i].close()
	}
}

// journal writes immutable descriptive phase evidence outside the target root.
func (s *Session) journal(h *Installation, phase string) error {
	if err := s.verify(); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Schema      string `json:"schema"`
		Phase       string `json:"phase"`
		Transaction string `json:"transaction"`
		Plan        Plan   `json:"plan"`
	}{"lexr-sensors-offline-diagnostic-v1", phase, h.tx, h.plan})
	if err != nil {
		return err
	}
	if len(data)+1 > compatibility.MaxBytes {
		return errors.New("offline diagnostic journal exceeds bound")
	}
	if err := writeExclusive(s.evidence, h.tx+"-"+phase+".json", append(data, '\n'), 0o600); err != nil {
		return err
	}
	return s.syncDir(s.evidence, "evidence:"+phase)
}

// checkpoint is a private failure-injection seam, not caller execution policy.
func (s *Session) checkpoint(point string) error {
	if s.hook != nil {
		return s.hook(point)
	}
	return nil
}

// syncDir checks directory identity before and after a durable directory sync.
func (s *Session) syncDir(d *directory, label string) error {
	if err := d.verify(); err != nil {
		return err
	}
	if err := s.checkpoint("sync:" + label); err != nil {
		return err
	}
	if err := d.file.Sync(); err != nil {
		return err
	}
	return d.verify()
}

// verify binds every session route to its retained inode, never a path alone.
func (s *Session) verify() error {
	if s == nil || s.closed || s.target == nil || s.evidence == nil {
		return errors.New("offline session is absent or closed")
	}
	if err := s.verifyParent(); err != nil {
		return err
	}
	for _, d := range []*directory{s.base, s.target, s.evidence} {
		if err := d.verify(); err != nil {
			return err
		}
	}
	return nil
}

// verifyParent detects path replacement and requires the original private parent.
func (s *Session) verifyParent() error {
	if err := privateParent(s.parentPath); err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(s.parentPath)
	if err != nil || real != s.parentPath {
		return errors.New("offline parent route changed")
	}
	info, err := os.Lstat(s.parentPath)
	if err != nil {
		return err
	}
	fd, err := s.parent.file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, fd) {
		return errors.New("offline parent identity changed")
	}
	return s.parent.verify()
}

// openAbsoluteDirectory binds the initial canonical parent to root and FD.
func openAbsoluteDirectory(name string, mode fs.FileMode) (*directory, error) {
	f, err := openDirectory(nil, name)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(name)
	if err != nil {
		f.Close()
		return nil, err
	}
	d := &directory{root: r, file: f, mode: mode}
	if err := d.verify(); err != nil {
		d.close()
		return nil, err
	}
	return d, nil
}

// openDir opens one relative component without following a directory symlink.
func (d *directory) openDir(name string, mode fs.FileMode) (*directory, error) {
	if !component(name) {
		return nil, errors.New("unsafe offline directory component")
	}
	if err := d.verify(); err != nil {
		return nil, err
	}
	f, err := openDirectory(d.file, name)
	if err != nil {
		return nil, err
	}
	r, err := d.root.OpenRoot(name)
	if err != nil {
		f.Close()
		return nil, err
	}
	child := &directory{root: r, file: f, parent: d, name: name, mode: mode}
	if err := child.verify(); err != nil {
		child.close()
		return nil, err
	}
	return child, nil
}

// verify compares both handles and the current parent entry without following it.
func (d *directory) verify() error {
	if d == nil || d.root == nil || d.file == nil {
		return errors.New("offline directory handle is absent")
	}
	i, err := d.file.Stat()
	if err != nil {
		return err
	}
	if !i.IsDir() || i.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 || d.mode != 0 && i.Mode().Perm() != d.mode || !currentOwner(i) {
		return errors.New("offline directory type, owner or mode changed")
	}
	f, err := d.root.Open(".")
	if err != nil {
		return err
	}
	ri, err := f.Stat()
	f.Close()
	if err != nil {
		return err
	}
	if !os.SameFile(i, ri) {
		return errors.New("offline rooted directory identity differs")
	}
	if d.parent != nil {
		if err := d.parent.verify(); err != nil {
			return err
		}
		p, err := d.parent.root.Lstat(d.name)
		if err != nil {
			return err
		}
		if !p.IsDir() || !os.SameFile(i, p) {
			return errors.New("offline directory parent mapping changed")
		}
	}
	return nil
}

// close only releases descriptors; it never recursively deletes paths.
func (d *directory) close() {
	if d == nil {
		return
	}
	if d.root != nil {
		d.root.Close()
		d.root = nil
	}
	if d.file != nil {
		d.file.Close()
		d.file = nil
	}
	if d.ownedParent && d.parent != nil {
		d.parent.close()
		d.ownedParent = false
	}
}

// uniqueName creates an unpredictable basename without filesystem side effects.
func uniqueName(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]), nil
}

// component excludes aliases, separators and traversal from fd-relative calls.
func component(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}

// splitMember requires a clean fixed-prefix-relative POSIX member path.
func splitMember(name string) ([]string, error) {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name {
		return nil, errors.New("unsafe sensor member path")
	}
	parts := strings.Split(name, "/")
	for _, p := range parts {
		if !component(p) {
			return nil, errors.New("unsafe sensor member component")
		}
	}
	return parts, nil
}

// openTree traverses only no-follow retained directory components.
func openTree(root *directory, name string) (*directory, error) {
	parts, err := splitMember(name)
	if err != nil {
		return nil, err
	}
	// Duplicate the starting descriptor so callers may always close the result.
	d, err := duplicateDirectory(root)
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		next, err := d.openDir(part, 0o755)
		if err != nil {
			d.close()
			return nil, err
		}
		next.ownedParent = true
		d = next
	}
	return d, nil
}

// duplicateDirectory opens a separate handle while preserving parent identity.
func duplicateDirectory(d *directory) (*directory, error) {
	if err := d.verify(); err != nil {
		return nil, err
	}
	f, err := openDirectory(d.file, ".")
	if err != nil {
		return nil, err
	}
	r, err := d.root.OpenRoot(".")
	if err != nil {
		f.Close()
		return nil, err
	}
	return &directory{root: r, file: f, parent: d.parent, name: d.name, mode: d.mode}, nil
}

// memberParent returns an owned directory handle for one safe relative member.
func memberParent(root *directory, name string) (*directory, string, error) {
	parts, err := splitMember(name)
	if err != nil {
		return nil, "", err
	}
	if len(parts) == 1 {
		d, err := duplicateDirectory(root)
		return d, parts[0], err
	}
	d, err := openTree(root, strings.Join(parts[:len(parts)-1], "/"))
	return d, parts[len(parts)-1], err
}

// mkdirTree creates one known directory, requiring existing parents to be real.
func mkdirTree(root *directory, name string) error {
	d, base, err := memberParent(root, name)
	if err != nil {
		return err
	}
	defer d.close()
	if err := d.root.Mkdir(base, 0o755); err != nil {
		return err
	}
	child, err := d.openDir(base, 0o755)
	if err != nil {
		return err
	}
	child.close()
	return nil
}

// treeDirectories derives the exact closed directory roster in parent-first order.
func treeDirectories(files []runtimepkg.File, links []runtimepkg.Link) []string {
	seen := map[string]bool{}
	add := func(name string) {
		for d := path.Dir(name); d != "."; d = path.Dir(d) {
			seen[d] = true
		}
	}
	for _, f := range files {
		add(f.Path)
	}
	for _, l := range links {
		add(l.Path)
	}
	result := make([]string, 0, len(seen))
	for d := range seen {
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := strings.Count(result[i], "/"), strings.Count(result[j], "/")
		if a != b {
			return a < b
		}
		return result[i] < result[j]
	})
	return result
}

// writeExclusive writes and syncs one new regular file without overwriting.
func writeExclusive(d *directory, name string, data []byte, mode fs.FileMode) error {
	if !component(name) {
		return errors.New("unsafe offline file component")
	}
	if err := d.verify(); err != nil {
		return err
	}
	f, err := d.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if n, err := f.Write(data); err != nil {
		return err
	} else if n != len(data) {
		return io.ErrShortWrite
	}
	if err := f.Sync(); err != nil {
		return err
	}
	got, info, err := readRegular(d, name, int64(len(data)), mode)
	if err != nil {
		return err
	}
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) || !bytes.Equal(data, got) {
		return errors.New("offline file changed while staging")
	}
	return d.verify()
}

// readRegular performs a bounded no-follow, non-blocking, single-link read.
func readRegular(d *directory, name string, maximum int64, mode fs.FileMode) ([]byte, fs.FileInfo, error) {
	if !component(name) || maximum < 0 {
		return nil, nil, errors.New("unsafe offline read")
	}
	if err := d.verify(); err != nil {
		return nil, nil, err
	}
	f, err := openRegular(d.file, name)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 || mode != 0 && info.Mode().Perm() != mode || !currentOwner(info) || info.Size() > maximum {
		return nil, nil, errors.New("offline regular member mode, owner or size differs")
	}
	data, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) != info.Size() || int64(len(data)) > maximum {
		return nil, nil, errors.New("offline regular member changed length")
	}
	after, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	named, err := d.root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(info, after) || !os.SameFile(info, named) || !named.Mode().IsRegular() || !singleLink(after) || !singleLink(named) || info.Size() != after.Size() || info.Mode() != after.Mode() {
		return nil, nil, errors.New("offline regular member identity changed")
	}
	if err := d.verify(); err != nil {
		return nil, nil, err
	}
	return data, info, nil
}

// verifyFile compares every known byte and exact mode, not a receipt claim.
func verifyFile(d *directory, name string, f runtimepkg.File) error {
	data, _, err := readRegular(d, name, int64(len(f.Data)), fs.FileMode(f.Mode))
	if err != nil {
		return err
	}
	if !bytes.Equal(data, f.Data) {
		return errors.New("sensor member bytes differ")
	}
	return nil
}

// verifyLink recognises only an exact fixed relative library alias.
func verifyLink(d *directory, name, target string) error {
	if err := d.verify(); err != nil {
		return err
	}
	info, err := d.root.Lstat(name)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink == 0 || !currentOwner(info) || !singleLink(info) {
		return errors.New("sensor alias is not the expected symbolic link")
	}
	got, err := d.root.Readlink(name)
	if err != nil {
		return err
	}
	if got != target {
		return errors.New("sensor alias target differs")
	}
	after, err := d.root.Lstat(name)
	if err != nil || !os.SameFile(info, after) || !singleLink(after) {
		return errors.New("sensor alias identity changed")
	}
	return d.verify()
}

// inventory recursively enumerates without following links or special files.
func inventory(d *directory, prefix string, out map[string]string) error {
	if err := d.verify(); err != nil {
		return err
	}
	f, err := openDirectory(d.file, ".")
	if err != nil {
		return err
	}
	entries, err := f.ReadDir(-1)
	f.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !component(e.Name()) {
			return errors.New("unsafe sensor directory member")
		}
		name := e.Name()
		if prefix != "" {
			name = prefix + "/" + name
		}
		info, err := d.root.Lstat(e.Name())
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			out[name] = "directory"
			child, err := d.openDir(e.Name(), 0o755)
			if err != nil {
				return err
			}
			err = inventory(child, name, out)
			child.close()
			if err != nil {
				return err
			}
		case info.Mode().IsRegular():
			out[name] = "file"
		case info.Mode()&fs.ModeSymlink != 0:
			out[name] = "link"
		default:
			return errors.New("special file in sensor prefix")
		}
	}
	return d.verify()
}

// Digest is a diagnostic SHA-256 helper used by the opt-in proof, not authority.
func Digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// space budgets one complete staged prefix plus 64 MiB headroom. Rollback is
// a same-filesystem rename, not a second payload copy. This is an admission
// check, not a reservation or a promise against another process filling disk.
func (s *Session) space(in packageInput) error {
	needed := int64(64<<20) + int64(len(in.declaration)) + int64(compatibility.MaxBytes)
	for _, f := range in.files {
		needed += int64(len(f.Data))
	}
	probe := s.spaceProbe
	if probe == nil {
		probe = availableSpace
	}
	available, err := probe(s.target.file)
	if err != nil {
		return err
	}
	if available < needed {
		return fmt.Errorf("offline staging needs %d bytes including reserve; only %d available", needed, available)
	}
	return nil
}

// boundedTarget keeps request records and diagnostics within the staged metadata
// budget. Unknown build identities may be explicitly unverified, not unbounded
// strings or control-bearing log/receipt content.
func boundedTarget(t compatibility.Target) error {
	for _, field := range []struct {
		name, value string
		maximum     int
	}{{"Lexr version", t.LexrVersion, 256}, {"architecture", t.Architecture, 64}, {"device profile", t.DeviceProfile, 128}, {"OS ID", t.OSID, 64}, {"OS version", t.OSVersion, 64}, {"kernel ABI", t.KernelABI, 256}} {
		if !utf8.ValidString(field.value) {
			return fmt.Errorf("offline target %s is not UTF-8", field.name)
		}
		if len(field.value) > field.maximum {
			return fmt.Errorf("offline target %s exceeds bound", field.name)
		}
		for _, r := range field.value {
			if unicode.IsControl(r) {
				return fmt.Errorf("offline target %s contains a control character", field.name)
			}
		}
	}
	return nil
}
