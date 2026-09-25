package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// maximumGitIdentityBytes bounds commit metadata and diagnostics.
	maximumGitIdentityBytes = 4 << 20
	// maximumGitTreeListBytes bounds the complete committed path inventory.
	maximumGitTreeListBytes = 64 << 20
	// maximumGitAttributeBytes bounds two archive attributes for every path.
	maximumGitAttributeBytes = 192 << 20
	// maximumSourceArchiveSize bounds one retained local kernel source payload.
	maximumSourceArchiveSize = int64(16 << 30)
	// maximumSourceFiles bounds committed tree and archive traversal.
	maximumSourceFiles = 2_000_000
	// maximumSourcePathBytes bounds each committed repository-relative path.
	maximumSourcePathBytes = 4096
	// maximumSymlinkBytes bounds a committed symbolic-link target.
	maximumSymlinkBytes = 4096
	// localSourceArchiveFile is the private transaction payload filename.
	localSourceArchiveFile = "local-source.tar"
	// localSourceCommitFile retains the raw commit object for reconstruction.
	localSourceCommitFile = "local-source.commit"
)

// localSourceIdentity is private host evidence for one immutable committed tree.
type localSourceIdentity struct {
	revision   string
	tree       string
	commitTime time.Time
	fileCount  int
}

// sourceArchiveIdentity records one completed private snapshot payload.
type sourceArchiveIdentity struct {
	sha256 string
	size   int64
}

// inspectLocalSource proves that directory is one clean canonical worktree and
// returns the exact committed identity which may be archived without reading
// mutable working-tree file bytes.
func inspectLocalSource(ctx context.Context, directory string) (localSourceIdentity, error) {
	top, err := runGitOutput(ctx, directory, maximumGitIdentityBytes, "rev-parse", "--show-toplevel")
	if err != nil {
		return localSourceIdentity{}, fmt.Errorf("inspect kernel local source worktree: %w", err)
	}
	canonicalTop, err := filepath.EvalSymlinks(strings.TrimSpace(string(top)))
	if err != nil || filepath.Clean(canonicalTop) != filepath.Clean(directory) {
		return localSourceIdentity{}, errors.New("kernel --source-dir must name the canonical Git worktree root")
	}
	status, err := runGitOutput(ctx, directory, maximumGitTreeListBytes, "status", "--porcelain=v1", "--untracked-files=all", "-z")
	if err != nil {
		return localSourceIdentity{}, fmt.Errorf("inspect kernel local source status: %w", err)
	}
	if len(status) != 0 {
		return localSourceIdentity{}, errors.New("kernel local source has staged, unstaged, or untracked changes; commit the intended source locally before building")
	}
	revision, err := gitObjectOutput(ctx, directory, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return localSourceIdentity{}, fmt.Errorf("resolve kernel local source commit: %w", err)
	}
	tree, err := gitObjectOutput(ctx, directory, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return localSourceIdentity{}, fmt.Errorf("resolve kernel local source tree: %w", err)
	}
	committedText, err := runGitOutput(ctx, directory, maximumGitIdentityBytes, "show", "-s", "--format=%cI", revision)
	if err != nil {
		return localSourceIdentity{}, fmt.Errorf("resolve kernel local source commit time: %w", err)
	}
	committed, err := time.Parse(time.RFC3339, strings.TrimSpace(string(committedText)))
	if err != nil {
		return localSourceIdentity{}, fmt.Errorf("parse kernel local source commit time: %w", err)
	}
	listing, err := runGitOutput(ctx, directory, maximumGitTreeListBytes, "ls-tree", "-r", "-z", "--full-tree", revision)
	if err != nil {
		return localSourceIdentity{}, fmt.Errorf("inspect kernel local source tree: %w", err)
	}
	count, err := validateLocalTree(ctx, directory, listing)
	if err != nil {
		return localSourceIdentity{}, err
	}
	return localSourceIdentity{revision: revision, tree: tree, commitTime: committed, fileCount: count}, nil
}

// validateLocalTree rejects source forms which cannot be represented by the
// initial committed-tree snapshot contract.
func validateLocalTree(ctx context.Context, directory string, listing []byte) (int, error) {
	records := bytes.Split(listing, []byte{0})
	seenFolded := make(map[string]string, len(records))
	var attributePaths bytes.Buffer
	count := 0
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		count++
		if count > maximumSourceFiles {
			return 0, errors.New("kernel local source exceeds the committed file-count limit")
		}
		header, nameBytes, found := bytes.Cut(record, []byte{'\t'})
		fields := bytes.Fields(header)
		if !found || len(fields) != 3 || len(nameBytes) == 0 || len(nameBytes) > maximumSourcePathBytes || !utf8.Valid(nameBytes) {
			return 0, errors.New("kernel local source contains a malformed or oversized Git path")
		}
		name := string(nameBytes)
		if strings.IndexByte(name, 0) >= 0 || path.IsAbs(name) || path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") {
			return 0, fmt.Errorf("kernel local source contains an unsafe Git path %q", name)
		}
		for _, character := range name {
			if character < 0x20 || character == 0x7f {
				return 0, fmt.Errorf("kernel local source contains a control character in Git path %q", name)
			}
		}
		folded := strings.ToLower(name)
		if previous, exists := seenFolded[folded]; exists && previous != name {
			return 0, fmt.Errorf("kernel local source contains a case-folded path collision between %q and %q", previous, name)
		}
		seenFolded[folded] = name
		_, _ = attributePaths.Write(nameBytes)
		_ = attributePaths.WriteByte(0)
		mode, objectType, objectID := string(fields[0]), string(fields[1]), string(fields[2])
		if mode == "160000" || objectType == "commit" || name == ".gitmodules" {
			return 0, errors.New("kernel local source contains a submodule contract, which local snapshots do not support")
		}
		if objectType != "blob" || (mode != "100644" && mode != "100755" && mode != "120000") || !gitObjectExpression.MatchString(objectID) {
			return 0, fmt.Errorf("kernel local source contains unsupported Git mode %q at %q", mode, name)
		}
		if mode == "120000" {
			target, err := runGitOutput(ctx, directory, maximumSymlinkBytes, "cat-file", "blob", objectID)
			if err != nil {
				return 0, fmt.Errorf("read kernel local source link %q: %w", name, err)
			}
			if err := validateSnapshotLink(name, string(target)); err != nil {
				return 0, err
			}
		}
	}
	if count == 0 {
		return 0, errors.New("kernel local source committed tree is empty")
	}
	attributes, err := runGitInputOutput(ctx, directory, attributePaths.Bytes(), maximumGitAttributeBytes,
		"check-attr", "--cached", "-z", "--stdin", "export-ignore", "export-subst")
	if err != nil {
		return 0, fmt.Errorf("inspect kernel local source archive attributes: %w", err)
	}
	fields := bytes.Split(attributes, []byte{0})
	if len(fields) == 0 || len(fields[len(fields)-1]) != 0 || (len(fields)-1)%3 != 0 {
		return 0, errors.New("Git returned malformed local source archive attributes")
	}
	for index := 0; index+2 < len(fields)-1; index += 3 {
		value := string(fields[index+2])
		if value != "unspecified" && value != "unset" {
			return 0, fmt.Errorf("kernel local source path %q uses %s, which would change its committed archive tree", fields[index], fields[index+1])
		}
	}
	return count, nil
}

// validateSnapshotLink accepts only links which remain inside the extracted tree.
func validateSnapshotLink(name, target string) error {
	if target == "" || len(target) > maximumSymlinkBytes || !utf8.ValidString(target) || strings.IndexByte(target, 0) >= 0 || path.IsAbs(target) {
		return fmt.Errorf("kernel local source contains an unsafe symbolic link %q", name)
	}
	for _, character := range target {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("kernel local source contains an unsafe symbolic link %q", name)
		}
	}
	resolved := path.Clean(path.Join(path.Dir(name), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") || path.IsAbs(resolved) {
		return fmt.Errorf("kernel local source symbolic link escapes the committed tree: %q", name)
	}
	return nil
}

// captureLocalSourceSnapshot writes a deterministic Git archive and raw commit
// object into the private transaction, then proves the selected worktree did
// not change while capture was in progress.
func captureLocalSourceSnapshot(ctx context.Context, transaction, directory string, expected localSourceIdentity) (sourceArchiveIdentity, error) {
	current, err := inspectLocalSource(ctx, directory)
	if err != nil {
		return sourceArchiveIdentity{}, err
	}
	if current != expected {
		return sourceArchiveIdentity{}, errors.New("kernel local source identity changed after planning")
	}
	commit, err := runGitOutput(ctx, directory, maximumGitIdentityBytes, "cat-file", "commit", expected.revision)
	if err != nil {
		return sourceArchiveIdentity{}, fmt.Errorf("read kernel local source commit object: %w", err)
	}
	commitPath := filepath.Join(transaction, localSourceCommitFile)
	if err := writeExclusiveSynced(commitPath, commit, 0o600); err != nil {
		return sourceArchiveIdentity{}, fmt.Errorf("write kernel local source commit object: %w", err)
	}
	archivePath := filepath.Join(transaction, localSourceArchiveFile)
	archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return sourceArchiveIdentity{}, fmt.Errorf("create kernel local source archive: %w", err)
	}
	hasher := sha256.New()
	limited := &maximumWriter{destination: io.MultiWriter(archive, hasher), maximum: maximumSourceArchiveSize}
	command := exec.CommandContext(ctx, "git", "-C", directory, "archive", "--format=tar", expected.revision)
	stderr := &boundedWriter{limit: maximumGitIdentityBytes}
	command.Stdout = limited
	command.Stderr = stderr
	command.Env = append(os.Environ(), "LC_ALL=C")
	runErr := command.Run()
	syncErr := archive.Sync()
	closeErr := archive.Close()
	if err := errors.Join(runErr, syncErr, closeErr); err != nil {
		_ = os.Remove(archivePath)
		diagnostic := strings.TrimSpace(string(stderr.data))
		if diagnostic != "" {
			return sourceArchiveIdentity{}, fmt.Errorf("capture kernel local source archive: %w: %s", err, diagnostic)
		}
		return sourceArchiveIdentity{}, fmt.Errorf("capture kernel local source archive: %w", err)
	}
	if limited.written == 0 {
		_ = os.Remove(archivePath)
		return sourceArchiveIdentity{}, errors.New("kernel local source archive is empty")
	}
	after, err := inspectLocalSource(ctx, directory)
	if err != nil || after != expected {
		_ = os.Remove(archivePath)
		return sourceArchiveIdentity{}, errors.New("kernel local source changed while its committed snapshot was captured")
	}
	return sourceArchiveIdentity{sha256: hex.EncodeToString(hasher.Sum(nil)), size: limited.written}, nil
}

// maximumWriter fails before a snapshot can exceed its public size contract.
type maximumWriter struct {
	destination io.Writer
	maximum     int64
	written     int64
}

// Write implements io.Writer with an aggregate byte bound.
func (writer *maximumWriter) Write(data []byte) (int, error) {
	if writer.written+int64(len(data)) > writer.maximum {
		return 0, errors.New("kernel local source archive exceeds the size limit")
	}
	written, err := writer.destination.Write(data)
	writer.written += int64(written)
	return written, err
}

// runGitOutput executes one fixed Git binary with bounded output.
func runGitOutput(ctx context.Context, directory string, maximum int, arguments ...string) ([]byte, error) {
	return runGitInputOutput(ctx, directory, nil, maximum, arguments...)
}

// runGitInputOutput executes one fixed Git binary with bounded input and output.
func runGitInputOutput(ctx context.Context, directory string, input []byte, maximum int, arguments ...string) ([]byte, error) {
	stdout := &boundedWriter{limit: maximum}
	stderr := &boundedWriter{limit: maximumGitIdentityBytes}
	commandArguments := append([]string{"-C", directory}, arguments...)
	command := exec.CommandContext(ctx, "git", commandArguments...)
	command.Stdout = stdout
	command.Stderr = stderr
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	command.Env = append(os.Environ(), "LC_ALL=C")
	if err := command.Run(); err != nil {
		return nil, gitCommandError(err, stderr)
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, errors.New("Git output exceeded the kernel local source safety limit")
	}
	return append([]byte(nil), stdout.data...), nil
}

// gitCommandError adds one bounded Git diagnostic without exposing source paths.
func gitCommandError(err error, stderr *boundedWriter) error {
	if stderr != nil {
		diagnostic := strings.TrimSpace(string(stderr.data))
		if diagnostic != "" {
			return fmt.Errorf("%w: %s", err, diagnostic)
		}
	}
	return err
}

// gitObjectOutput returns one validated full Git object identifier.
func gitObjectOutput(ctx context.Context, directory string, arguments ...string) (string, error) {
	output, err := runGitOutput(ctx, directory, maximumGitIdentityBytes, arguments...)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if !gitObjectExpression.MatchString(value) {
		return "", errors.New("Git returned a malformed object identity")
	}
	return value, nil
}

// writeExclusiveSynced writes one private file without replacement.
func writeExclusiveSynced(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	written, writeErr := file.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	return errors.Join(writeErr, file.Sync(), file.Close())
}

// parsePositiveIdentityInt validates one decimal provenance quantity.
func parsePositiveIdentityInt(value string, maximum int64) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 || parsed > maximum || strconv.FormatInt(parsed, 10) != value {
		return 0, errors.New("malformed positive provenance quantity")
	}
	return parsed, nil
}
