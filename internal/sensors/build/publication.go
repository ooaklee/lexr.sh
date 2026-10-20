package build

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
	"path/filepath"
	"strings"
)

const publicationReceiptName = "lexr-sensors-source-build.json"

// Publication describes filesystem outcomes, including failures after rename.
// Published is not an installation or hardware-qualification authority.
type Publication struct {
	Directory        string
	StagingDirectory string
	ReceiptSHA256    string
	Published        bool
	Durable          bool
	LocationVerified bool
}

// These per-call hooks are deliberately private and are never controller input.
type publicationHooks struct {
	beforeRename  func() error
	afterRename   func() error
	syncFile      func(*os.File) error
	syncDirectory func(*os.File, string) error
}

type publicationMember struct {
	artifact Artifact
	data     []byte
}

func publishArtifacts(parent, buildID string, artifacts *VerifiedArtifacts, receipt []byte) (Publication, error) {
	return publishArtifactsWithHooks(parent, buildID, artifacts, receipt, publicationHooks{})
}

func publishArtifactsWithHooks(parent, buildID string, artifacts *VerifiedArtifacts, receipt []byte, hooks publicationHooks) (result Publication, resultErr error) {
	if err := publicationSupported(); err != nil {
		return result, err
	}
	if len(buildID) != 24 || strings.Trim(buildID, "0123456789abcdef") != "" {
		return result, errors.New("publication build ID must be exactly 24 lowercase hexadecimal digits")
	}
	members, links, err := publicationInputs(artifacts, receipt)
	if err != nil {
		return result, err
	}
	if !filepath.IsAbs(parent) || filepath.Clean(parent) != parent {
		return result, errors.New("publication parent must be an existing canonical absolute directory")
	}
	canonical, err := filepath.EvalSymlinks(parent)
	if err != nil || canonical != parent {
		return result, errors.New("publication parent must not contain symlinks")
	}
	before, err := os.Lstat(parent)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("publication parent is not a real directory")
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return result, fmt.Errorf("open publication parent: %w", err)
	}
	defer root.Close()
	parentFD, err := root.Open(".")
	if err != nil {
		return result, fmt.Errorf("open publication parent descriptor: %w", err)
	}
	defer parentFD.Close()
	opened, err := parentFD.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return result, errors.New("publication parent changed while opening")
	}
	if err := publicationParentMatches(parent, parentFD); err != nil {
		return result, err
	}
	stageName, finalName := ".sensors-"+buildID+".partial", "sensors-"+buildID
	if err := root.Mkdir(stageName, 0o700); err != nil {
		return result, fmt.Errorf("exclusively create publication staging directory: %w", err)
	}
	result.StagingDirectory = filepath.Join(parent, stageName)
	stageInfo, err := root.Lstat(stageName)
	if err != nil || !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
		return result, errors.New("new publication staging directory changed; partial retained")
	}
	stage, err := root.OpenRoot(stageName)
	if err != nil {
		return result, fmt.Errorf("open publication staging directory; partial retained: %w", err)
	}
	defer stage.Close()
	stageFD, err := stage.Open(".")
	if err != nil {
		return result, fmt.Errorf("open staging descriptor; partial retained: %w", err)
	}
	defer stageFD.Close()
	openedStage, err := stageFD.Stat()
	if err != nil || !os.SameFile(stageInfo, openedStage) {
		return result, errors.New("staging identity changed; partial retained")
	}
	for _, member := range members {
		if err := writePublicationMember(stage, member, hooks); err != nil {
			return result, fmt.Errorf("publication partial retained: %w", err)
		}
	}
	for _, link := range links {
		if err := stage.Symlink(link.Target, link.Name); err != nil {
			return result, fmt.Errorf("create publication symlink; partial retained: %w", err)
		}
	}
	if err := verifyPublicationMembers(stage, stageFD, members, links); err != nil {
		return result, err
	}
	if err := publicationSyncDirectory(stageFD, "staging", hooks); err != nil {
		return result, fmt.Errorf("sync publication staging directory; partial retained: %w", err)
	}
	if hooks.beforeRename != nil {
		if err := hooks.beforeRename(); err != nil {
			return result, err
		}
	}
	if err := publicationParentMatches(parent, parentFD); err != nil {
		return result, err
	}
	if err := publicationDirectoryMatches(root, stageName, stageFD); err != nil {
		return result, err
	}
	// Re-read the complete closed set immediately before publication. Neither a
	// manifest nor a successful earlier write is authority for current bytes.
	if err := verifyPublicationMembers(stage, stageFD, members, links); err != nil {
		return result, err
	}
	if err := renamePublicationNoReplace(parentFD, stageName, finalName); err != nil {
		return result, fmt.Errorf("atomically publish without replacement; partial retained: %w", err)
	}
	result.Published = true
	result.Directory = filepath.Join(parent, finalName)
	result.StagingDirectory = ""
	result.ReceiptSHA256 = members[len(members)-1].artifact.SHA256
	if hooks.afterRename != nil {
		if err := hooks.afterRename(); err != nil {
			return result, err
		}
	}
	if err := publicationParentMatches(parent, parentFD); err != nil {
		return result, err
	}
	if err := publicationDirectoryMatches(root, finalName, stageFD); err != nil {
		return result, err
	}
	if err := verifyPublicationMembers(stage, stageFD, members, links); err != nil {
		return result, err
	}
	result.LocationVerified = true
	if err := publicationSyncDirectory(parentFD, "parent", hooks); err != nil {
		return result, fmt.Errorf("publication exists but parent sync failed: %w", err)
	}
	result.Durable = true
	if err := publicationParentMatches(parent, parentFD); err != nil {
		result.LocationVerified = false
		return result, err
	}
	if err := publicationDirectoryMatches(root, finalName, stageFD); err != nil {
		result.LocationVerified = false
		return result, err
	}
	return result, nil
}

func publicationInputs(verified *VerifiedArtifacts, receipt []byte) ([]publicationMember, []Link, error) {
	trimmed := bytes.TrimSpace(receipt)
	if len(receipt) > 1<<20 || len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(receipt) {
		return nil, nil, errors.New("publication receipt must be a JSON object no larger than 1 MiB")
	}
	if verified == nil || !verified.valid {
		return nil, nil, errors.New("publication requires verified artifacts")
	}
	wanted := expectedFiles()
	files := verified.Files()
	if len(files) != len(wanted) {
		return nil, nil, errors.New("publication artifact roster differs")
	}
	members := make([]publicationMember, 0, len(files)+1)
	seen := make(map[string]bool, len(files))
	var total int64
	for _, entry := range files {
		rule, ok := wanted[entry.Name]
		data, present := verified.Data(entry.Name)
		if !ok || seen[entry.Name] || !present || entry.Bytes <= 0 || entry.Bytes > rule.maximum || int64(len(data)) != entry.Bytes || entry.Mode != rule.mode || publicationSHA256(data) != entry.SHA256 {
			return nil, nil, errors.New("publication artifact metadata or bytes differ")
		}
		seen[entry.Name] = true
		if entry.Bytes > MaximumArchiveBytes-total {
			return nil, nil, errors.New("publication artifacts exceed the compiled byte bound")
		}
		total += entry.Bytes
		members = append(members, publicationMember{entry, data})
	}
	links := verified.Links()
	wantedLinks := ArtifactLinks()
	if len(links) != len(wantedLinks) {
		return nil, nil, errors.New("publication symlink roster differs")
	}
	linkSet := make(map[string]string, len(wantedLinks))
	for _, link := range wantedLinks {
		linkSet[link.Name] = link.Target
	}
	for _, link := range links {
		target, ok := linkSet[link.Name]
		if !ok || link.Target != target {
			return nil, nil, errors.New("publication symlink target differs")
		}
		delete(linkSet, link.Name)
	}
	if len(linkSet) != 0 {
		return nil, nil, errors.New("publication symlink roster is incomplete")
	}
	// Capture caller-owned receipt bytes too; later mutation is not authority.
	data := append([]byte(nil), receipt...)
	members = append(members, publicationMember{Artifact{publicationReceiptName, int64(len(data)), publicationSHA256(data), 0o644}, data})
	return members, links, nil
}

func writePublicationMember(root *os.Root, member publicationMember, hooks publicationHooks) error {
	f, err := root.OpenFile(member.artifact.Name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("exclusively create %s: %w", member.artifact.Name, err)
	}
	defer f.Close()
	if n, err := f.Write(member.data); err != nil || int64(n) != member.artifact.Bytes {
		return fmt.Errorf("write publication member %s: %w", member.artifact.Name, errors.Join(err, io.ErrShortWrite))
	}
	if err := f.Chmod(os.FileMode(member.artifact.Mode)); err != nil {
		return err
	}
	if err := verifyPublicationFD(f, member.artifact); err != nil {
		return err
	}
	if hooks.syncFile != nil {
		err = hooks.syncFile(f)
	} else {
		err = f.Sync()
	}
	if err != nil {
		return fmt.Errorf("sync publication member %s: %w", member.artifact.Name, err)
	}
	return f.Close()
}

func verifyPublicationFD(f *os.File, expected Artifact) error {
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Mode().Perm() != os.FileMode(expected.Mode) || info.Size() != expected.Bytes {
		return fmt.Errorf("publication member type, size or mode differs: %s", expected.Name)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(f, expected.Bytes+1))
	if err != nil || n != expected.Bytes || hex.EncodeToString(digest.Sum(nil)) != expected.SHA256 {
		return fmt.Errorf("publication member digest differs: %s", expected.Name)
	}
	return nil
}

func verifyPublicationMembers(root *os.Root, directory *os.File, members []publicationMember, links []Link) error {
	wanted := make(map[string]bool, len(members)+len(links))
	for _, member := range members {
		wanted[member.artifact.Name] = true
		f, err := openPublicationRegular(directory, member.artifact.Name)
		if err != nil {
			return fmt.Errorf("open staged regular member %s: %w", member.artifact.Name, err)
		}
		verifyErr := verifyPublicationFD(f, member.artifact)
		closeErr := f.Close()
		if err := errors.Join(verifyErr, closeErr); err != nil {
			return err
		}
	}
	for _, link := range links {
		wanted[link.Name] = true
		info, err := root.Lstat(link.Name)
		target, readErr := root.Readlink(link.Name)
		if err != nil || readErr != nil || info.Mode()&os.ModeSymlink == 0 || target != link.Target {
			return fmt.Errorf("publication symlink differs: %s", link.Name)
		}
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	if len(entries) != len(wanted) {
		return errors.New("publication directory is not the compiled closed set")
	}
	for _, entry := range entries {
		if !wanted[entry.Name()] {
			return errors.New("publication directory contains an extra member")
		}
	}
	return nil
}

func publicationParentMatches(parent string, opened *os.File) error {
	canonical, err := filepath.EvalSymlinks(parent)
	current, pathErr := os.Lstat(parent)
	identity, fdErr := opened.Stat()
	if err != nil || canonical != parent || pathErr != nil || fdErr != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(current, identity) {
		return errors.New("publication parent pathname changed; owned output retained")
	}
	return nil
}

func publicationDirectoryMatches(parent *os.Root, name string, opened *os.File) error {
	current, err := parent.Lstat(name)
	identity, fdErr := opened.Stat()
	if err != nil || fdErr != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(current, identity) {
		return errors.New("publication directory pathname changed; owned output retained")
	}
	return nil
}

func publicationSyncDirectory(directory *os.File, phase string, hooks publicationHooks) error {
	if hooks.syncDirectory != nil {
		return hooks.syncDirectory(directory, phase)
	}
	return directory.Sync()
}

func publicationSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
