package releaseprep

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ooaklee/lexr.sh/internal/kernel/build"
)

// These bounds mirror the committed-source snapshot policy. Release inputs
// retain the existing stricter per-asset eight-GiB limit.
const (
	maximumLocalSourceFiles = 2_000_000
	maximumLocalSourcePath  = 4096
)

// bindLocalSourceInput requires the retained source itself, exactly once through
// --source. An identically named copy elsewhere cannot replace the builder leaf.
func bindLocalSourceInput(directory string, provenance build.Provenance, inputs []PlannedAsset) error {
	if provenance.SourceKind != build.SourceKindLocalGitCommit {
		return nil
	}
	for _, input := range inputs {
		if input.Asset.Name == provenance.SourceArchiveName {
			if input.SourcePath != filepath.Join(directory, provenance.SourceArchiveName) {
				return errors.New("local corresponding source must name the retained builder archive")
			}
		}
	}
	assets := make([]Asset, len(inputs))
	for index, input := range inputs {
		assets[index] = input.Asset
	}
	return bindLocalSourceAsset(publicProvenance(provenance), assets)
}

// bindLocalSourceAsset binds public provenance to one closed source asset.
func bindLocalSourceAsset(provenance SourceProvenance, assets []Asset) error {
	if provenance.SourceKind != build.SourceKindLocalGitCommit {
		return nil
	}
	for _, asset := range assets {
		if asset.Name == provenance.SourceArchiveName && asset.Kind == AssetSource &&
			asset.SHA256 == provenance.SourceArchiveSHA256 && asset.Size == provenance.SourceArchiveSize {
			return nil
		}
	}
	return errors.New("local source provenance is not bound to its retained corresponding-source asset")
}

// inspectLocalSourceArchive never extracts source. It verifies the exact
// producer bytes and repeats bounded Git-archive namespace and file-count
// checks. The producer's recipe verified the Git tree; independent source
// publication and correspondence remain operator evidence, not an HTTPS build.
func inspectLocalSourceArchive(ctx context.Context, directory string, provenance build.Provenance) error {
	identity, err := inspectRegular(ctx, filepath.Join(directory, provenance.SourceArchiveName), "retained local source archive", maximumAssetBytes)
	if err != nil {
		return err
	}
	if identity.sha256 != provenance.SourceArchiveSHA256 || identity.size != provenance.SourceArchiveSize {
		return errors.New("retained local source archive differs from producer digest or size")
	}
	file, err := os.Open(identity.path)
	if err != nil {
		return err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(identity.info, opened) || !opened.Mode().IsRegular() {
		return errors.New("retained local source archive changed while opening")
	}
	hasher := sha256.New()
	reader := io.TeeReader(localSourceContextReader{ctx: ctx, reader: io.LimitReader(file, maximumAssetBytes+1)}, hasher)
	archive := tar.NewReader(reader)
	seen := make(map[string]bool)
	namespace := make(map[string]byte)
	count, headers := 0, 0
	commitSeen := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read retained local source archive: %w", err)
		}
		headers++
		if headers > 2*maximumLocalSourceFiles+1 {
			return errors.New("retained local source archive exceeds namespace limit")
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			if commitSeen || headers != 1 || header.PAXRecords["comment"] != provenance.Revision {
				return errors.New("retained local source archive has invalid Git commit comment")
			}
			commitSeen = true
			continue
		}
		name := header.Name
		if header.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if !safeLocalSourcePath(name) || seen[name] {
			return errors.New("retained local source archive contains unsafe or duplicate paths")
		}
		seen[name] = true
		switch header.Typeflag {
		case tar.TypeDir:
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maximumAssetBytes {
				return errors.New("retained local source archive has an oversized file")
			}
			count++
		case tar.TypeSymlink:
			target := header.Linkname
			resolved := path.Join(path.Dir(name), target)
			if target == "" || len(target) > maximumLocalSourcePath || !utf8.ValidString(target) ||
				path.IsAbs(target) || strings.ContainsAny(target, "\\\x00\r\n") ||
				resolved == ".." || strings.HasPrefix(resolved, "../") {
				return errors.New("retained local source archive contains an escaping symbolic link")
			}
			count++
		default:
			return errors.New("retained local source archive contains an unsupported member type")
		}
		if count > maximumLocalSourceFiles {
			return errors.New("retained local source archive exceeds file-count limit")
		}
		if kind, exists := namespace[name]; exists && (kind != tar.TypeDir || header.Typeflag != tar.TypeDir) {
			return errors.New("retained local source archive has a non-directory ancestor")
		}
		namespace[name] = header.Typeflag
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if kind, exists := namespace[parent]; exists && kind != tar.TypeDir {
				return errors.New("retained local source archive has a non-directory ancestor")
			}
			namespace[parent] = tar.TypeDir
			if len(namespace) > 2*maximumLocalSourceFiles {
				return errors.New("retained local source archive exceeds namespace limit")
			}
		}
	}
	if !commitSeen || count != provenance.SourceFileCount {
		return errors.New("retained local source archive commit comment or file count differs from producer")
	}
	// tar stops at its end markers; hash the entire immutable file, including
	// padding, and prove it did not change while parsing.
	if _, err := copyContext(ctx, io.Discard, reader, maximumAssetBytes); err != nil {
		return err
	}
	current, err := os.Lstat(identity.path)
	if err != nil || !os.SameFile(identity.info, current) || !current.Mode().IsRegular() ||
		current.Size() != identity.size || hex.EncodeToString(hasher.Sum(nil)) != identity.sha256 {
		return errors.New("retained local source archive changed while validating")
	}
	return nil
}

// safeLocalSourcePath keeps exact case while rejecting archive namespace escapes.
func safeLocalSourcePath(name string) bool {
	if name == "" || len(name) > maximumLocalSourcePath || !utf8.ValidString(name) || path.IsAbs(name) ||
		path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
		return false
	}
	for _, character := range name {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	for _, component := range strings.Split(name, "/") {
		if strings.EqualFold(component, ".git") {
			return false
		}
	}
	return true
}

// localSourceContextReader also checks cancellation while tar skips file data.
type localSourceContextReader struct {
	ctx    context.Context
	reader io.Reader
}

// Read honours cancellation before reading more archive bytes.
func (reader localSourceContextReader) Read(data []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(data)
}
