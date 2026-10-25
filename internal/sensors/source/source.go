// Package source authenticates the approved, source-only sensor composition.
// It does not invoke Git, a build system, a transport, or an installer.
package source

import (
	"bytes"
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
	"sort"
	"strings"
	"unicode"
)

// Source authority and size limits are compiled policy, not checkout metadata.
const (
	approvedCommit     = "649cc875d08c04ce33bc8afc2a0ff7a22ce362e0"
	sourcePrefix       = "userspace/sensors"
	maximumFileBytes   = int64(32 << 20)
	maximumSourceBytes = int64(64 << 20)
)

// The metadata successor is a second exact source composition, not a version
// range or permission to substitute arbitrary neighbouring source metadata.
const (
	Metadata048SourceCommit    = "037e249831b34c3786dd95453269821bd15b1a3e"
	Metadata048SourceFileCount = 955
)

// File identifies authenticated bytes and the Git executable-bit class.
// Path is relative to a staged composition, never a local host pathname.
type File struct {
	Path    string
	SHA256  string
	Bytes   int64
	GitMode string
}

// Symlink identifies an exact, relative link to a regular source member.
type Symlink struct {
	Path   string
	Target string
}

// Composition describes approved source lineage, not an observed checkout HEAD,
// a successful build, installability, or physical-device qualification.
type Composition struct {
	SourceCommit       string
	SourceFileCount    int
	SourceSymlinkCount int
	SourceBytes        int64
	files              []File
	metadata           []File
	links              []Symlink
}

// SourceFiles returns a defensive copy of the authenticated source roster.
func (c Composition) SourceFiles() []File { return append([]File(nil), c.files...) }

// Metadata returns a defensive copy of the four independently pinned records.
func (c Composition) Metadata() []File { return append([]File(nil), c.metadata...) }

// Symlinks returns a defensive copy of the exact internal source links.
func (c Composition) Symlinks() []Symlink { return append([]Symlink(nil), c.links...) }

// metadataPin binds one metadata record to its expected digest, size and roster.
type metadataPin struct {
	name   string
	sha256 string
	bytes  int64
	files  int
	links  int
}

// authority contains the fixed lineage and private hooks used by controlled tests.
type authority struct {
	commit      string
	components  []metadataPin
	checksums   metadataPin
	metadata048 bool
	// Only unexported fixture policies use this deterministic race-test seam.
	beforeOpen func(*os.Root, string) error
	beforeCopy func(*os.Root, string) error
}

// approvedAuthority constructs a fresh policy; callers cannot inject authority.
func approvedAuthority() authority {
	return authority{commit: approvedCommit, components: []metadataPin{
		{name: "libqmi", sha256: "ab6008235d2f691ce7a8fca2d82aa072ea12bc9874f337f0ebbe1effd82f3083", bytes: 157815, files: 793, links: 6},
		{name: "libssc", sha256: "6411dd017ba6aa0d177972706158636a2945ae19db808c549785e6ebc1e2f98c", bytes: 14556, files: 77},
		{name: "iio-sensor-proxy", sha256: "aa9ac39d11cfdb6f9e1306624458e98579e7340c976bbe573cfb5adf105f8303", bytes: 14377, files: 79},
	}, checksums: metadataPin{name: "SOURCE.sha256", sha256: "4fd055d4d21700047d2970fdd0ae5ddf9deda350d4de7cc298a2fc7a64ae796d", bytes: 119728}}
}

// approved048Authority retains unchanged QMI/proxy identities and admits only
// the independently frozen metadata-diagnostics SSC successor composition.
func approved048Authority() authority {
	policy := approvedAuthority()
	policy.commit = Metadata048SourceCommit
	policy.metadata048 = true
	policy.components[1] = metadataPin{name: "libssc", sha256: "a4f8b204fae4fed651d0a4f01c6825e23b1f52f29f576ba1a7d80e28a3db01ab", bytes: 15731, files: 83}
	policy.checksums = metadataPin{name: "SOURCE.sha256", sha256: "79d220b28c676b1f0174d29beaf84c8697d06a7237b988cf55ac1e33a08eab35", bytes: 120400}
	return policy
}

// inventoryFile is one file description decoded from an authenticated inventory.
type inventoryFile struct {
	Bytes   int64  `json:"bytes"`
	SHA256  string `json:"sha256"`
	GitMode string `json:"git_mode"`
}

// inventory describes one component's closed file and symlink roster.
type inventory struct {
	Schema        string                   `json:"schema"`
	Component     string                   `json:"component"`
	Status        string                   `json:"status"`
	InventoryRoot string                   `json:"inventory_root"`
	Files         map[string]inventoryFile `json:"files"`
	Symlinks      map[string]string        `json:"symlinks"`
}

// capturedFile retains verified bytes alongside their expected source identity.
type capturedFile struct {
	identity File
	data     []byte
}

// snapshot groups the authenticated composition and its captured regular files.
type snapshot struct {
	composition Composition
	files       []capturedFile
}

// Verify reads only the fixed public sensor input set. Metadata is authenticated
// before parsing. A .git directory is neither required nor consulted. Each
// source file is verified from an opened regular-file descriptor; no claim of
// an atomic view of a concurrently mutable filesystem is made.
func Verify(repositoryRoot string) (Composition, error) {
	s, err := capture(repositoryRoot, approvedAuthority())
	return s.composition, err
}

// Verify048 authenticates only the independently compiled metadata successor.
// The default Verify remains restricted to the original 0.4.7 composition.
func Verify048(repositoryRoot string) (Composition, error) {
	s, err := capture(repositoryRoot, approved048Authority())
	return s.composition, err
}

// Stage makes an exclusively new destination containing the three components,
// their SOURCE.json records and SOURCE.sha256. It copies only captured verified
// bytes, rehashes the written descriptors, and never mounts mutable live source.
// The destination must not overlap the repository. No cleanup is performed:
// any newly owned partial destination remains available for diagnosis.
func Stage(repositoryRoot, destination string) (Composition, error) {
	return stage(repositoryRoot, destination, approvedAuthority())
}

// Stage048 copies only the exact metadata successor into an exclusively new
// destination, retaining Stage's descriptor checks and no-cleanup semantics.
func Stage048(repositoryRoot, destination string) (Composition, error) {
	return stage(repositoryRoot, destination, approved048Authority())
}

// capture opens the repository root and reads the composition under fixed policy.
func capture(repositoryRoot string, policy authority) (snapshot, error) {
	if err := regularReadSupported(); err != nil {
		return snapshot{}, err
	}
	r, err := os.OpenRoot(repositoryRoot)
	if err != nil {
		return snapshot{}, sourceError("open", "repository root", err)
	}
	defer r.Close()
	return captureRoot(r, policy)
}

// captureRoot authenticates metadata and source bytes through one confined root.
func captureRoot(r *os.Root, policy authority) (snapshot, error) {
	for _, name := range []string{"userspace", sourcePrefix} {
		if err := requireDirectory(r, name); err != nil {
			return snapshot{}, err
		}
	}
	s := snapshot{composition: Composition{SourceCommit: policy.commit}}
	for _, pin := range policy.components {
		componentRoot := path.Join(sourcePrefix, pin.name)
		if !safeRelative(pin.name) || strings.Contains(pin.name, "/") {
			return snapshot{}, errors.New("invalid compiled component name")
		}
		if err := requireDirectory(r, componentRoot); err != nil {
			return snapshot{}, err
		}
		metadata := File{Path: path.Join(pin.name, "SOURCE.json"), SHA256: pin.sha256, Bytes: pin.bytes, GitMode: "100644"}
		data, err := readVerified(r, path.Join(sourcePrefix, metadata.Path), metadata, policy.beforeOpen)
		if err != nil {
			return snapshot{}, err
		}
		// Unused provenance fields remain authenticated by the complete metadata
		// digest; only the fixed inventory fields below drive filesystem access.
		var inv inventory
		if err := json.Unmarshal(data, &inv); err != nil {
			return snapshot{}, fmt.Errorf("parse authenticated %s: %w", metadata.Path, err)
		}
		if inv.Schema != "oe-sensor-source-inventory-v1" || inv.Component != pin.name || inv.Status != "experimental-source-only" || inv.InventoryRoot != "source" || len(inv.Files) != pin.files || len(inv.Symlinks) != pin.links {
			return snapshot{}, fmt.Errorf("authenticated inventory contract differs for %s", pin.name)
		}
		wanted, err := inventoryNodes(inv)
		if err != nil {
			return snapshot{}, fmt.Errorf("invalid %s inventory: %w", pin.name, err)
		}
		base := path.Join(componentRoot, "source")
		if err := verifyTree(r, base, wanted); err != nil {
			return snapshot{}, err
		}
		s.files = append(s.files, capturedFile{metadata, data})
		s.composition.metadata = append(s.composition.metadata, metadata)
		for _, name := range sortedKeys(inv.Files) {
			entry := inv.Files[name]
			if entry.Bytes > maximumSourceBytes-s.composition.SourceBytes {
				return snapshot{}, errors.New("source composition exceeds its compiled byte bound")
			}
			member := File{Path: path.Join(pin.name, "source", name), SHA256: entry.SHA256, Bytes: entry.Bytes, GitMode: entry.GitMode}
			data, err := readVerified(r, path.Join(sourcePrefix, member.Path), member, policy.beforeOpen)
			if err != nil {
				return snapshot{}, err
			}
			s.files = append(s.files, capturedFile{member, data})
			s.composition.files = append(s.composition.files, member)
			s.composition.SourceBytes += member.Bytes
		}
		for _, name := range sortedKeys(inv.Symlinks) {
			target, err := r.Readlink(path.Join(base, name))
			if err != nil || target != inv.Symlinks[name] {
				return snapshot{}, fmt.Errorf("source symlink differs: %s", path.Join(pin.name, "source", name))
			}
			s.composition.links = append(s.composition.links, Symlink{path.Join(pin.name, "source", name), target})
		}
		if err := verifyTree(r, base, wanted); err != nil {
			return snapshot{}, err
		}
	}
	checksum := File{Path: policy.checksums.name, SHA256: policy.checksums.sha256, Bytes: policy.checksums.bytes, GitMode: "100644"}
	if checksum.Path != "SOURCE.sha256" {
		return snapshot{}, errors.New("invalid compiled checksum record name")
	}
	data, err := readVerified(r, path.Join(sourcePrefix, checksum.Path), checksum, policy.beforeOpen)
	if err != nil {
		return snapshot{}, err
	}
	s.files = append(s.files, capturedFile{checksum, data})
	s.composition.metadata = append(s.composition.metadata, checksum)
	s.composition.SourceFileCount = len(s.composition.files)
	s.composition.SourceSymlinkCount = len(s.composition.links)
	sort.Slice(s.files, func(i, j int) bool { return s.files[i].identity.Path < s.files[j].identity.Path })
	return s, nil
}

// inventoryNodes validates inventory paths and derives their complete node set.
func inventoryNodes(inv inventory) (map[string]byte, error) {
	wanted := map[string]byte{".": 'd'}
	for name, file := range inv.Files {
		if !safeRelative(name) || file.Bytes < 0 || file.Bytes > maximumFileBytes || !validSHA256(file.SHA256) || (file.GitMode != "100644" && file.GitMode != "100755") {
			return nil, errors.New("unsafe regular-file record")
		}
		if err := addNode(wanted, name, 'f'); err != nil {
			return nil, err
		}
	}
	for name, target := range inv.Symlinks {
		if !safeRelative(name) || !safeRelative(target) {
			return nil, errors.New("unsafe symlink record")
		}
		resolved := path.Clean(path.Join(path.Dir(name), target))
		if !safeRelative(resolved) {
			return nil, errors.New("symlink escapes its source component")
		}
		if _, regular := inv.Files[resolved]; !regular {
			return nil, errors.New("symlink target is not an authenticated regular member")
		}
		if err := addNode(wanted, name, 'l'); err != nil {
			return nil, err
		}
	}
	return wanted, nil
}

// addNode records a node and its parent directories, refusing conflicting kinds.
func addNode(wanted map[string]byte, name string, kind byte) error {
	if _, exists := wanted[name]; exists {
		return errors.New("duplicate or overlapping source member")
	}
	wanted[name] = kind
	for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
		if existing, ok := wanted[parent]; ok && existing != 'd' {
			return errors.New("source member overlaps a directory")
		}
		wanted[parent] = 'd'
	}
	return nil
}

// safeRelative admits canonical relative names without unsafe separators or bytes.
func safeRelative(name string) bool {
	if name == "." || !fs.ValidPath(name) || strings.Contains(name, "\\") || path.Clean(name) != name {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// validSHA256 recognises an exact lowercase hexadecimal SHA-256 digest.
func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}

// requireDirectory requires the selected confined entry to be a real directory.
func requireDirectory(r *os.Root, name string) error {
	info, err := r.Lstat(name)
	if err != nil {
		return sourceError("inspect directory", name, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source directory is not a real directory: %s", name)
	}
	return nil
}

// verifyTree compares the complete observed tree with the permitted node roster.
func verifyTree(r *os.Root, base string, wanted map[string]byte) error {
	seen := make(map[string]bool, len(wanted))
	err := fs.WalkDir(r.FS(), base, func(name string, _ fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return sourceError("walk", name, walkErr)
		}
		relative := "."
		if name != base {
			relative = strings.TrimPrefix(name, base+"/")
		}
		kind, ok := wanted[relative]
		if !ok {
			return fmt.Errorf("unexpected source member: %s", name)
		}
		info, err := r.Lstat(name)
		if err != nil {
			return sourceError("inspect", name, err)
		}
		valid := kind == 'd' && info.IsDir() || kind == 'f' && info.Mode().IsRegular() || kind == 'l' && info.Mode()&os.ModeSymlink != 0
		if !valid || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("source member has unexpected type or special mode: %s", name)
		}
		seen[relative] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(wanted) {
		return fmt.Errorf("source tree is missing approved members: %s", base)
	}
	return nil
}

// gitMode projects filesystem permissions onto Git's regular executable-bit class.
func gitMode(mode os.FileMode) string {
	if mode.Perm()&0o111 != 0 {
		return "100755"
	}
	return "100644"
}

// readVerified checks regular-file identity, size, mode and digest around its read.
func readVerified(r *os.Root, name string, expected File, beforeOpen func(*os.Root, string) error) ([]byte, error) {
	if !safeRelative(name) || expected.Bytes < 0 || expected.Bytes > maximumFileBytes || !validSHA256(expected.SHA256) {
		return nil, errors.New("invalid compiled file authority")
	}
	before, err := r.Lstat(name)
	if err != nil {
		return nil, sourceError("inspect", name, err)
	}
	if !before.Mode().IsRegular() || before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || before.Size() != expected.Bytes || gitMode(before.Mode()) != expected.GitMode {
		return nil, fmt.Errorf("source file type, size or executable mode differs: %s", name)
	}
	if beforeOpen != nil {
		if err := beforeOpen(r, name); err != nil {
			return nil, err
		}
	}
	file, err := openRegularLeaf(r, name)
	if err != nil {
		return nil, sourceError("open regular member", name, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || !os.SameFile(before, opened) || opened.Size() != expected.Bytes || gitMode(opened.Mode()) != expected.GitMode {
		return nil, fmt.Errorf("source member changed before opened-descriptor validation: %s", name)
	}
	data, err := io.ReadAll(io.LimitReader(file, expected.Bytes+1))
	if err != nil {
		return nil, sourceError("read", name, err)
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != expected.Bytes || hex.EncodeToString(digest[:]) != expected.SHA256 {
		return nil, fmt.Errorf("source byte identity differs: %s", name)
	}
	after, err := r.Lstat(name)
	fdAfter, fdErr := file.Stat()
	if err != nil || fdErr != nil || validateRegularFD(file) != nil || !after.Mode().IsRegular() || after.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || !os.SameFile(opened, after) || !os.SameFile(opened, fdAfter) || after.Size() != expected.Bytes || fdAfter.Size() != expected.Bytes || gitMode(after.Mode()) != expected.GitMode || gitMode(fdAfter.Mode()) != expected.GitMode {
		return nil, fmt.Errorf("source member changed while being captured: %s", name)
	}
	return data, nil
}

// stage copies captured bytes into a fresh confined destination without reuse.
func stage(repositoryRoot, destination string, policy authority) (Composition, error) {
	if err := regularReadSupported(); err != nil {
		return Composition{}, err
	}
	repository, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return Composition{}, errors.New("invalid repository root")
	}
	repository, err = filepath.EvalSymlinks(repository)
	if err != nil {
		return Composition{}, sourceError("resolve", "repository root", err)
	}
	if strings.TrimSpace(destination) == "" {
		return Composition{}, errors.New("snapshot destination is required")
	}
	selected, err := filepath.Abs(destination)
	if err != nil {
		return Composition{}, errors.New("invalid snapshot destination")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(selected))
	if err != nil {
		return Composition{}, sourceError("resolve", "destination parent", err)
	}
	name := filepath.Base(selected)
	selected = filepath.Join(parent, name)
	if name == "." || name == string(filepath.Separator) || overlap(repository, selected) {
		return Composition{}, errors.New("snapshot source and destination must not overlap")
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return Composition{}, sourceError("open", "destination parent", err)
	}
	defer parentRoot.Close()
	repositoryHandle, err := os.OpenRoot(repository)
	if err != nil {
		return Composition{}, sourceError("open", "repository root", err)
	}
	defer repositoryHandle.Close()
	// Lexical paths alone do not detect differently cased aliases on Darwin.
	// Directory identities also bind the exact source object used for capture.
	contained, err := directoryContains(repositoryHandle, parentRoot)
	if err != nil {
		return Composition{}, sourceError("check identity ancestry", "snapshot parent", err)
	}
	if contained {
		return Composition{}, errors.New("snapshot source and destination identities must not overlap")
	}
	if _, err := parentRoot.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		return Composition{}, errors.New("snapshot destination must not already exist")
	}
	s, err := captureRoot(repositoryHandle, policy)
	if err != nil {
		return Composition{}, err
	}
	if err := parentRoot.Mkdir(name, 0o700); err != nil {
		return Composition{}, sourceError("exclusively create", "snapshot destination", err)
	}
	destinationRoot, err := parentRoot.OpenRoot(name)
	if err != nil {
		return Composition{}, sourceError("open", "new partial snapshot retained", err)
	}
	defer destinationRoot.Close()
	opened, statErr := destinationRoot.Stat(".")
	current, pathErr := parentRoot.Lstat(name)
	if statErr != nil || pathErr != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) {
		return Composition{}, errors.New("new snapshot directory identity changed; partial retained")
	}
	wanted := map[string]byte{".": 'd'}
	for _, captured := range s.files {
		if err := addNode(wanted, captured.identity.Path, 'f'); err != nil {
			return Composition{}, err
		}
		if err := destinationRoot.MkdirAll(path.Dir(captured.identity.Path), 0o700); err != nil {
			return Composition{}, sourceError("create directory", "partial snapshot retained", err)
		}
		if policy.beforeCopy != nil {
			if err := policy.beforeCopy(destinationRoot, captured.identity.Path); err != nil {
				return Composition{}, fmt.Errorf("partial snapshot retained: %w", err)
			}
		}
		if err := writeCaptured(destinationRoot, captured); err != nil {
			return Composition{}, fmt.Errorf("partial snapshot retained: %w", err)
		}
	}
	for _, link := range s.composition.links {
		if err := addNode(wanted, link.Path, 'l'); err != nil {
			return Composition{}, err
		}
		if err := destinationRoot.Symlink(link.Target, link.Path); err != nil {
			return Composition{}, sourceError("create link", "partial snapshot retained", err)
		}
		actual, err := destinationRoot.Readlink(link.Path)
		if err != nil || actual != link.Target {
			return Composition{}, errors.New("staged symlink identity differs; partial snapshot retained")
		}
	}
	if err := verifyTree(destinationRoot, ".", wanted); err != nil {
		return Composition{}, fmt.Errorf("partial snapshot retained: %w", err)
	}
	// An anchored root remains usable after a rename. Do not endorse it while
	// the pathname a later builder will mount identifies a different directory.
	if err := directoryPathMatches(parent, parentRoot); err != nil {
		return Composition{}, fmt.Errorf("snapshot parent pathname changed; partial retained: %w", err)
	}
	if err := directoryPathMatches(repository, repositoryHandle); err != nil {
		return Composition{}, fmt.Errorf("source root pathname changed; partial retained: %w", err)
	}
	current, pathErr = parentRoot.Lstat(name)
	if pathErr != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, current) {
		return Composition{}, errors.New("snapshot destination pathname changed; partial retained")
	}
	contained, err = directoryContains(repositoryHandle, parentRoot)
	if err != nil || contained {
		return Composition{}, errors.New("snapshot source/output identity ancestry changed; partial retained")
	}
	return s.composition, nil
}

// writeCaptured exclusively creates one verified source file and rechecks its bytes.
func writeCaptured(r *os.Root, captured capturedFile) error {
	entry := captured.identity
	file, err := r.OpenFile(entry.Path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return sourceError("create snapshot member", entry.Path, err)
	}
	defer file.Close()
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, digest), bytes.NewReader(captured.data))
	if err != nil || written != entry.Bytes || hex.EncodeToString(digest.Sum(nil)) != entry.SHA256 {
		return fmt.Errorf("captured bytes changed during snapshot copy: %s", entry.Path)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return sourceError("rewind snapshot member", entry.Path, err)
	}
	digest.Reset()
	read, err := io.Copy(digest, io.LimitReader(file, entry.Bytes+1))
	if err != nil || read != entry.Bytes || hex.EncodeToString(digest.Sum(nil)) != entry.SHA256 {
		return fmt.Errorf("written snapshot bytes differ: %s", entry.Path)
	}
	mode := os.FileMode(0o644)
	if entry.GitMode == "100755" {
		mode = 0o755
	}
	if err := file.Chmod(mode); err != nil {
		return sourceError("set snapshot mode", entry.Path, err)
	}
	if err := file.Sync(); err != nil {
		return sourceError("sync snapshot member", entry.Path, err)
	}
	opened, err := file.Stat()
	current, currentErr := r.Lstat(entry.Path)
	if err != nil || currentErr != nil || validateRegularFD(file) != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) || current.Size() != entry.Bytes || gitMode(current.Mode()) != entry.GitMode {
		return fmt.Errorf("snapshot member identity changed: %s", entry.Path)
	}
	return file.Close()
}

// overlap reports equal paths or ancestry in either pathname direction.
func overlap(first, second string) bool {
	within := func(base, candidate string) bool {
		rel, err := filepath.Rel(base, candidate)
		return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
	}
	return within(first, second) || within(second, first)
}

// sortedKeys returns deterministic lexical ordering for a map's keys.
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sourceError labels a failed operation without exposing nested filesystem paths.
func sourceError(action, member string, err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	return fmt.Errorf("%s %s: %w", action, member, err)
}
