package source

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
)

// RepositorySnapshot is an authenticated, fresh read-only copy whose mountable
// path retains the ordinary userspace/sensors layout. It is not an install or
// execution grant. The owner must Verify immediately around later use and Close
// the retained directory handles. Methods are intended for one sequential owner.
type RepositorySnapshot struct {
	name, parentName string
	root, parent     *os.Root
	composition      Composition
	policy           authority
	closed           bool
}

// Path returns the snapshot pathname, or an empty value for a nil snapshot.
func (s *RepositorySnapshot) Path() string {
	if s == nil {
		return ""
	}
	return s.name
}

// Composition returns the authenticated lineage associated with this snapshot.
func (s *RepositorySnapshot) Composition() Composition {
	if s == nil {
		return Composition{}
	}
	return s.composition
}

// Close releases handles only. Successful and failed snapshots remain on disk.
func (s *RepositorySnapshot) Close() error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.root != nil {
		err = errors.Join(err, s.root.Close())
	}
	if s.parent != nil {
		err = errors.Join(err, s.parent.Close())
	}
	return err
}

// StageRepository wraps Stage's flat component layout in a fresh repository
// layout. Only the new copy is made readable to the unprivileged container UID:
// directories 0555, regular data 0444, executable source members 0555. The caller
// must keep the existing destination parent owner-private (mode 0700).
// No directory, source byte or permission in repositoryRoot is changed.
func StageRepository(repositoryRoot, destination string) (*RepositorySnapshot, error) {
	return stageRepository(repositoryRoot, destination, approvedAuthority())
}

// stageRepository stages the exact approved tree and retains its directory handles.
func stageRepository(repositoryRoot, destination string, policy authority) (result *RepositorySnapshot, resultErr error) {
	if err := regularReadSupported(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(repositoryRoot) == "" || strings.TrimSpace(destination) == "" {
		return nil, errors.New("source and snapshot repository paths are required")
	}
	repository, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return nil, err
	}
	repository, err = filepath.EvalSymlinks(repository)
	if err != nil {
		return nil, err
	}
	selected, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	parentName, err := filepath.EvalSymlinks(filepath.Dir(selected))
	if err != nil {
		return nil, err
	}
	name := filepath.Base(selected)
	selected = filepath.Join(parentName, name)
	if name == "." || name == string(filepath.Separator) || overlap(repository, selected) {
		return nil, errors.New("snapshot repository and source must not overlap")
	}
	parent, err := os.OpenRoot(parentName)
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained {
			_ = parent.Close()
		}
	}()
	if err := requirePrivateSnapshotParent(parent); err != nil {
		return nil, err
	}
	original, err := os.OpenRoot(repository)
	if err != nil {
		return nil, err
	}
	defer original.Close()
	contained, err := directoryContains(original, parent)
	if err != nil || contained {
		return nil, errors.New("snapshot parent must not identify a directory within the source")
	}
	if _, err := parent.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("snapshot repository destination must be new")
	}
	if err := directoryPathMatches(parentName, parent); err != nil {
		return nil, err
	}
	if err := parent.Mkdir(name, 0o700); err != nil {
		return nil, err
	}
	r, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	s := &RepositorySnapshot{name: selected, parentName: parentName, root: r, parent: parent, policy: policy}
	retained = true
	defer func() {
		if resultErr != nil {
			_ = s.Close()
		}
	}()
	if err := r.Mkdir("userspace", 0o700); err != nil {
		return nil, err
	}
	if err := s.pathsMatch(); err != nil {
		return nil, err
	}
	composition, err := stage(repository, filepath.Join(selected, filepath.FromSlash(sourcePrefix)), policy)
	if err != nil {
		return nil, fmt.Errorf("snapshot repository partial retained: %w", err)
	}
	s.composition = composition
	nodes, err := s.nodes()
	if err != nil {
		return nil, err
	}
	if err := verifyTree(r, ".", nodes); err != nil {
		return nil, err
	}
	// All bytes already came from verified descriptors. Apply permissions to
	// anchored copy descriptors only, never to links or the original checkout.
	for _, member := range append(composition.SourceFiles(), composition.Metadata()...) {
		name := path.Join(sourcePrefix, member.Path)
		file, err := openRegularLeaf(r, name)
		if err != nil {
			return nil, err
		}
		mode := os.FileMode(0o444)
		if member.GitMode == "100755" {
			mode = 0o555
		}
		err = file.Chmod(mode)
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
	}
	for _, name := range sortedKeys(nodes) {
		if nodes[name] != 'd' {
			continue
		}
		before, err := r.Lstat(name)
		if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("snapshot directory identity differs before readonly mode")
		}
		dir, err := r.OpenRoot(name)
		if err != nil {
			return nil, err
		}
		opened, err := dir.Stat(".")
		if err != nil || !os.SameFile(before, opened) {
			_ = dir.Close()
			return nil, errors.New("snapshot directory changed before readonly mode")
		}
		file, err := dir.Open(".")
		if err == nil {
			err = file.Chmod(0o555)
			err = errors.Join(err, file.Close())
		}
		err = errors.Join(err, dir.Close())
		if err != nil {
			return nil, err
		}
	}
	if err := s.Verify(); err != nil {
		return nil, err
	}
	return s, nil
}

// pathsMatch rechecks both snapshot pathnames against their retained directories.
func (s *RepositorySnapshot) pathsMatch() error {
	if s == nil || s.closed || s.root == nil || s.parent == nil {
		return errors.New("snapshot repository is closed or unavailable")
	}
	if err := directoryPathMatches(s.parentName, s.parent); err != nil {
		return err
	}
	if err := requirePrivateSnapshotParent(s.parent); err != nil {
		return err
	}
	return directoryPathMatches(s.name, s.root)
}

// nodes constructs the closed set of directories, files and links in the snapshot.
func (s *RepositorySnapshot) nodes() (map[string]byte, error) {
	wanted := map[string]byte{".": 'd', "userspace": 'd', sourcePrefix: 'd'}
	for _, member := range append(s.composition.SourceFiles(), s.composition.Metadata()...) {
		if err := addNode(wanted, path.Join(sourcePrefix, member.Path), 'f'); err != nil {
			return nil, err
		}
	}
	for _, link := range s.composition.Symlinks() {
		if err := addNode(wanted, path.Join(sourcePrefix, link.Path), 'l'); err != nil {
			return nil, err
		}
	}
	return wanted, nil
}

// Verify re-authenticates the complete copied composition and wrapper roster,
// including read-only modes and current path-to-directory identities. It does
// not consult the mutable original repository or invoke Docker/build tools.
func (s *RepositorySnapshot) Verify() error {
	if err := s.pathsMatch(); err != nil {
		return err
	}
	wanted, err := s.nodes()
	if err != nil {
		return err
	}
	if err := verifyTree(s.root, ".", wanted); err != nil {
		return err
	}
	for name, kind := range wanted {
		if kind == 'l' {
			continue
		}
		info, err := s.root.Lstat(name)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		if kind == 'd' || info.Mode()&0o111 != 0 {
			mode = 0o555
		}
		if info.Mode().Perm() != mode {
			return fmt.Errorf("snapshot readonly mode differs: %s", name)
		}
	}
	current, err := captureRoot(s.root, s.policy)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current.composition, s.composition) {
		return errors.New("snapshot repository composition changed")
	}
	if err := verifyTree(s.root, ".", wanted); err != nil {
		return err
	}
	return s.pathsMatch()
}
