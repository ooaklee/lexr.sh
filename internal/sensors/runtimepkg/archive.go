package runtimepkg

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
)

// archiveEntry is an in-memory member, never an extraction destination.
type archiveEntry struct {
	Path       string
	Data       []byte
	Mode       int64
	LinkTarget string
}

// safePath recognises canonical relative archive names without traversal,
// platform separators or control characters.
func safePath(name string) bool {
	if name == "." || !fs.ValidPath(name) || path.Clean(name) != name || strings.Contains(name, "\\") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// writeArchive admits only this module's canonical, uncompressed USTAR form.
// There are no directory members, extended headers or host-derived metadata.
func writeArchive(entries []archiveEntry, maximum int64) ([]byte, error) {
	entries = append([]archiveEntry(nil), entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	var buffer bytes.Buffer
	w := tar.NewWriter(&buffer)
	previous := ""
	for _, entry := range entries {
		if !safePath(entry.Path) || entry.Path == previous {
			return nil, errors.New("archive has an unsafe or duplicate member")
		}
		previous = entry.Path
		h := &tar.Header{Name: entry.Path, Mode: entry.Mode, ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR, Typeflag: tar.TypeReg, Size: int64(len(entry.Data))}
		if entry.LinkTarget != "" {
			if !safePath(entry.LinkTarget) || len(entry.Data) != 0 || entry.Mode != 0o777 {
				return nil, errors.New("archive has an invalid relative symlink")
			}
			h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, entry.LinkTarget, 0
		} else if entry.Mode != 0o644 && entry.Mode != 0o755 {
			return nil, errors.New("archive regular member mode differs")
		}
		if err := w.WriteHeader(h); err != nil {
			return nil, fmt.Errorf("write canonical USTAR header: %w", err)
		}
		if _, err := w.Write(entry.Data); err != nil {
			return nil, err
		}
		if int64(buffer.Len()) > maximum-1024 {
			return nil, errors.New("archive exceeds its compiled byte bound")
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if int64(buffer.Len()) > maximum {
		return nil, errors.New("archive exceeds its compiled byte bound")
	}
	return buffer.Bytes(), nil
}

// readArchive bounds work before reading member bytes and re-encodes to require
// exact headers, sorted order, padding and exactly two zero end records.
func readArchive(data []byte, maximum, memberMaximum int64, countMaximum int) ([]archiveEntry, error) {
	if len(data) < 1024 || int64(len(data)) > maximum || len(data)%512 != 0 {
		return nil, errors.New("archive is empty, truncated or oversized")
	}
	r := tar.NewReader(bytes.NewReader(data))
	var entries []archiveEntry
	previous := ""
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(entries) >= countMaximum || !safePath(h.Name) || h.Name <= previous || h.Format != tar.FormatUSTAR || h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" || !h.ModTime.Equal(time.Unix(0, 0)) || !h.AccessTime.IsZero() || !h.ChangeTime.IsZero() || len(h.PAXRecords) != 0 || len(h.Xattrs) != 0 || h.Devmajor != 0 || h.Devminor != 0 || h.Size < 0 || h.Size > memberMaximum {
			return nil, errors.New("archive header is outside the canonical closed contract")
		}
		previous = h.Name
		entry := archiveEntry{Path: h.Name, Mode: h.Mode}
		switch h.Typeflag {
		case tar.TypeReg:
			if h.Linkname != "" || h.Mode != 0o644 && h.Mode != 0o755 {
				return nil, errors.New("archive regular member header differs")
			}
			entry.Data, err = io.ReadAll(io.LimitReader(r, h.Size+1))
			if err != nil || int64(len(entry.Data)) != h.Size {
				return nil, errors.New("archive regular member is truncated")
			}
		case tar.TypeSymlink:
			if h.Size != 0 || h.Mode != 0o777 || !safePath(h.Linkname) {
				return nil, errors.New("archive symlink header differs")
			}
			entry.LinkTarget = h.Linkname
		default:
			return nil, errors.New("archive contains a forbidden member type")
		}
		entries = append(entries, entry)
	}
	canonical, err := writeArchive(entries, maximum)
	if err != nil || !bytes.Equal(data, canonical) {
		return nil, errors.New("archive spelling, order, padding or end records are not canonical")
	}
	return entries, nil
}
