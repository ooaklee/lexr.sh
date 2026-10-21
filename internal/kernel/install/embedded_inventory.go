package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DTB limits mirror the build producer; directory limits additionally bound
// discovery work when no closed bundle inventory is available to the consumer.
const (
	maximumInstalledDTBBytes       int64 = 4 << 20
	maximumInstalledDTBTotal       int64 = 512 << 20
	maximumInstalledDTBs                 = 4096
	maximumInstalledDTBEntries           = 8192
	maximumInstalledDTBDirectories       = 4096
	maximumInstalledDTBDepth             = 8
	maximumInstalledDTBPathBytes         = 1024
	maximumEmbeddedDTBSections           = 64
)

// installedDTBComponent matches the producer's portable ASCII path grammar.
var installedDTBComponent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~,-]*$`)

// installedDTBLimits permits small, isolated bound fixtures without changing
// production limits or allocating hundreds of MiB during regression tests.
type installedDTBLimits struct {
	fileBytes, totalBytes                         int64
	files, entries, directories, depth, pathBytes int
}

// defaultInstalledDTBLimits keeps runtime admission aligned with the producer.
func defaultInstalledDTBLimits() installedDTBLimits {
	return installedDTBLimits{maximumInstalledDTBBytes, maximumInstalledDTBTotal,
		maximumInstalledDTBs, maximumInstalledDTBEntries, maximumInstalledDTBDirectories,
		maximumInstalledDTBDepth, maximumInstalledDTBPathBytes}
}

// discoverInstalledDeviceTrees attributes embedded payloads only to regular
// files in this ABI's installed firmware subtree. This proves installed-byte
// identity, not package provenance or a claim that every packaged board is
// supported. Explicit bundle inventories continue through their closed path.
func discoverInstalledDeviceTrees(ctx context.Context, root, abi string, limits installedDTBLimits) ([]FileEvidence, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateABI("installed device-tree", abi); err != nil {
		return nil, err
	}
	canonical, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}
	if limits.fileBytes < 1 || limits.totalBytes < 1 || limits.files < 1 || limits.entries < 1 ||
		limits.directories < 1 || limits.depth < 1 || limits.pathBytes < 1 {
		return nil, errors.New("installed device-tree discovery limits must be positive")
	}
	relative := "usr/lib/firmware/" + abi + "/device-tree"
	base, err := rootPath(canonical, relative)
	if err != nil {
		return nil, err
	}
	if err := validateTargetRoute(canonical, base, false); err != nil {
		return nil, err
	}
	directory, err := openInstalledDTBDirectory(canonical, relative)
	if err != nil {
		return nil, fmt.Errorf("open same-ABI device-tree inventory: %w", err)
	}
	defer directory.Close()
	var installed []FileEvidence
	var total int64
	entries, directories := 0, 0
	var walk func(*os.File, string, int) error
	walk = func(dir *os.File, prefix string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		directories++
		if directories > limits.directories || depth > limits.depth {
			return errors.New("installed device-tree inventory exceeds directory or depth limit")
		}
		opened, err := dir.Stat()
		if err != nil {
			return err
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			// One extra entry detects overflow without reading an unbounded directory.
			batch, readErr := dir.ReadDir(min(64, limits.entries-entries+1))
			for _, entry := range batch {
				if err := ctx.Err(); err != nil {
					return err
				}
				entries++
				if entries > limits.entries {
					return errors.New("installed device-tree inventory exceeds entry limit")
				}
				name := entry.Name()
				if !installedDTBComponent.MatchString(name) || name == "." || name == ".." {
					return errors.New("installed device-tree inventory has an unsafe path component")
				}
				member := name
				if prefix != "" {
					member = prefix + "/" + name
				}
				if len(relative)+1+len(member) > limits.pathBytes {
					return errors.New("installed device-tree inventory exceeds path limit")
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
					return errors.New("installed device-tree inventory contains a symbolic link or non-regular member")
				}
				target := filepath.Join(base, filepath.FromSlash(member))
				if err := validateTargetRoute(canonical, target, false); err != nil {
					return err
				}
				if info.IsDir() {
					child, err := openInstalledDTBChild(dir, name, true)
					if err != nil {
						return err
					}
					childInfo, statErr := child.Stat()
					if statErr != nil || !os.SameFile(info, childInfo) {
						_ = child.Close()
						return errors.Join(statErr, errors.New("installed device-tree directory changed before inspection"))
					}
					walkErr := walk(child, member, depth+1)
					closeErr := child.Close()
					if walkErr != nil || closeErr != nil {
						return errors.Join(walkErr, closeErr)
					}
					continue
				}
				// Overlays and other ordinary packaged files do not attribute a DTB.
				if !strings.HasSuffix(name, ".dtb") {
					continue
				}
				if prefix == "" {
					return errors.New("installed device-tree must have a vendor-relative path")
				}
				if len(installed) >= limits.files || info.Size() <= 0 || info.Size() > limits.fileBytes ||
					info.Size() > limits.totalBytes-total {
					return errors.New("installed device-tree inventory exceeds DTB count or byte limit")
				}
				file, err := openInstalledDTBChild(dir, name, false)
				if err != nil {
					return err
				}
				pinned, statErr := file.Stat()
				if statErr != nil || !os.SameFile(info, pinned) || !pinned.Mode().IsRegular() || pinned.Size() != info.Size() {
					_ = file.Close()
					return errors.Join(statErr, errors.New("installed device-tree file changed before inspection"))
				}
				digest, size, hashErr := digestReader(ctx, file, nil, limits.fileBytes)
				final, finalErr := file.Stat()
				closeErr := file.Close()
				current, pathErr := os.Lstat(target)
				if hashErr != nil || finalErr != nil || closeErr != nil || pathErr != nil {
					return errors.Join(hashErr, finalErr, closeErr, pathErr)
				}
				if err := validateTargetRoute(canonical, target, false); err != nil {
					return err
				}
				if !os.SameFile(pinned, current) || !current.Mode().IsRegular() ||
					size != info.Size() || final.Size() != size || current.Size() != size ||
					!final.ModTime().Equal(pinned.ModTime()) || !current.ModTime().Equal(pinned.ModTime()) {
					return errors.New("installed device-tree file changed during inspection")
				}
				total += size
				installed = append(installed, FileEvidence{Kind: "installed device-tree", Path: target, SHA256: digest, Size: size})
			}
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) {
					return readErr
				}
				break
			}
			if len(batch) == 0 {
				return io.ErrNoProgress
			}
		}
		target := base
		if prefix != "" {
			target = filepath.Join(base, filepath.FromSlash(prefix))
		}
		if err := validateTargetRoute(canonical, target, false); err != nil {
			return err
		}
		current, err := os.Lstat(target)
		if err != nil || !current.IsDir() || !os.SameFile(opened, current) || !current.ModTime().Equal(opened.ModTime()) {
			return errors.Join(err, errors.New("installed device-tree directory changed during inspection"))
		}
		return ctx.Err()
	}
	if err := walk(directory, "", 0); err != nil {
		return nil, err
	}
	sort.Slice(installed, func(i, j int) bool { return installed[i].Path < installed[j].Path })
	return installed, nil
}
