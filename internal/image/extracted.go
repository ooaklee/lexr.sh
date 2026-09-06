package image

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ValidateExtractedRegularFiles rejects any extracted member whose path is
// non-canonical, escapes the private root, traverses a symbolic link, or ends
// in anything other than a regular file.
func ValidateExtractedRegularFiles(root string, paths []string) error {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect extracted root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return errors.New("extracted root is not a non-symbolic-link directory")
	}
	for _, relative := range paths {
		if relative == "" || path.IsAbs(relative) || path.Clean(relative) != relative || relative == "." || strings.HasPrefix(relative, "../") {
			return fmt.Errorf("extracted path %q is not canonical and relative", relative)
		}
		current := root
		components := strings.Split(relative, "/")
		for index, component := range components {
			if component == "" || component == "." || component == ".." {
				return fmt.Errorf("extracted path %q contains an invalid component", relative)
			}
			current = filepath.Join(current, filepath.FromSlash(component))
			info, err := os.Lstat(current)
			if err != nil {
				return fmt.Errorf("inspect extracted path %q: %w", relative, err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("extracted path %q traverses a symbolic link", relative)
			}
			if index < len(components)-1 {
				if !info.IsDir() {
					return fmt.Errorf("extracted path %q traverses a non-directory", relative)
				}
				continue
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("extracted path %q is not a regular file", relative)
			}
		}
	}
	return nil
}

// ReadBoundedExtractedFile reads one regular extracted file through an
// os.Root descriptor after rejecting symbolic-link traversal. The descriptor
// identity and size are rechecked so untrusted contents cannot escape their
// private extraction root or produce unbounded validation evidence.
func ReadBoundedExtractedFile(rootPath, relative string, maximumBytes int64) (data []byte, resultErr error) {
	if maximumBytes < 0 {
		return nil, errors.New("extracted file size bound must not be negative")
	}
	if err := ValidateExtractedRegularFiles(rootPath, []string{relative}); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open extracted root: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	listed, err := root.Lstat(relative)
	if err != nil || listed.Mode()&os.ModeSymlink != 0 || !listed.Mode().IsRegular() || listed.Size() < 0 || listed.Size() > maximumBytes {
		return nil, errors.Join(fmt.Errorf("extracted path %q is not a bounded regular file", relative), err)
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, fmt.Errorf("open extracted path %q: %w", relative, err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(listed, opened) || opened.Size() != listed.Size() {
		return nil, errors.Join(fmt.Errorf("extracted path %q changed while opening", relative), err)
	}
	data, err = io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read extracted path %q: %w", relative, err)
	}
	afterRead, statErr := file.Stat()
	current, lstatErr := root.Lstat(relative)
	if statErr != nil || lstatErr != nil || int64(len(data)) != opened.Size() || int64(len(data)) > maximumBytes ||
		!afterRead.Mode().IsRegular() || !os.SameFile(opened, afterRead) || afterRead.Size() != opened.Size() ||
		current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) || current.Size() != opened.Size() {
		return nil, errors.Join(fmt.Errorf("extracted path %q changed while reading", relative), statErr, lstatErr)
	}
	return data, nil
}
