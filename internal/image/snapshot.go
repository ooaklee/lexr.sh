package image

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
)

// SnapshotFile copies and hashes a bounded, descriptor-pinned regular file
// into an exclusively created private path. The optional inspection callback
// permits deterministic race tests; normal callers pass nil. Later tooling
// consumes the snapshot rather than reopening a mutable external input.
func SnapshotFile(ctx context.Context, sourcePath, destinationPath string, maximumBytes int64, afterInspection func() error) (digest string, size int64, resultErr error) {
	if maximumBytes <= 0 {
		return "", 0, errors.New("snapshot size bound must be positive")
	}
	listed, err := os.Lstat(sourcePath)
	if err != nil {
		return "", 0, fmt.Errorf("inspect file: %w", err)
	}
	if listed.Mode()&os.ModeSymlink != 0 || !listed.Mode().IsRegular() || listed.Size() <= 0 || listed.Size() > maximumBytes {
		return "", 0, fmt.Errorf("file path %q is not a bounded non-symbolic-link regular file", sourcePath)
	}
	if afterInspection != nil {
		if err := afterInspection(); err != nil {
			return "", 0, err
		}
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", 0, fmt.Errorf("open file snapshot source: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, source.Close()) }()
	opened, err := source.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(listed, opened) ||
		opened.Size() != listed.Size() || opened.Size() <= 0 || opened.Size() > maximumBytes {
		return "", 0, errors.Join(errors.New("file identity changed while opening its snapshot"), err)
	}
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("create private file snapshot: %w", err)
	}
	keepDestination := false
	defer func() {
		if !keepDestination {
			resultErr = errors.Join(resultErr, os.Remove(destinationPath))
		}
	}()
	hasher := sha256.New()
	buffer := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			_ = destination.Close()
			return "", size, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			size += int64(count)
			if size > opened.Size() || size > maximumBytes {
				_ = destination.Close()
				return "", size, errors.New("file grew while its snapshot was copied")
			}
			written, writeErr := io.MultiWriter(destination, hasher).Write(buffer[:count])
			if writeErr != nil {
				_ = destination.Close()
				return "", size, writeErr
			}
			if written != count {
				_ = destination.Close()
				return "", size, io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = destination.Close()
			return "", size, readErr
		}
	}
	afterRead, statErr := source.Stat()
	syncErr := destination.Sync()
	closeErr := destination.Close()
	if err := errors.Join(statErr, syncErr, closeErr); err != nil {
		return "", size, err
	}
	if size != opened.Size() || !afterRead.Mode().IsRegular() || !os.SameFile(opened, afterRead) || afterRead.Size() != opened.Size() {
		return "", size, errors.New("file changed while its snapshot was copied")
	}
	keepDestination = true
	return fmt.Sprintf("%x", hasher.Sum(nil)), size, nil
}
