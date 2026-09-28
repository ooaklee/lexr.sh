//go:build linux

package bootidentity

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// mountInfoPath is the authoritative mounted-filesystem source. The udev
// metadata directories are the persistent-identifier sources. All are
// variables for test injection only.
var (
	mountInfoPath   = "/proc/self/mountinfo"
	uuidMetaDir     = "/dev/disk/by-uuid"
	partUUIDMetaDir = "/dev/disk/by-partuuid"
)

// Sanitised resolver errors: no paths, device names, or identifiers.
var (
	errRootNotMounted      = errors.New("boot ownership found no mount for the selected root")
	errMetadataUnavailable = errors.New("boot ownership could not inspect mounted filesystem metadata")
)

// resolvePlatform is the authoritative read-only identity resolver for Linux.
// It derives identity exclusively from live kernel metadata (mountinfo, stat,
// udev by-uuid/by-partuuid symlinks) and never consults source manifests.
func resolvePlatform(ctx context.Context, root string) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return Identity{}, errRootNotMountpoint
	}
	data, err := readFileBounded(ctx, mountInfoPath, maxMountInfoBytes)
	if err != nil {
		if ctx.Err() != nil {
			return Identity{}, ctx.Err()
		}
		return Identity{}, errMetadataUnavailable
	}
	entries, err := parseMountInfo(data)
	if err != nil {
		return Identity{}, err
	}
	rootEntry, bootEntry, bootWithinRoot, err := selectRootAndBoot(entries, root)
	if err != nil {
		if errors.Is(err, errRootNotMountpoint) && !underAnyMount(entries, root) {
			return Identity{}, errRootNotMounted
		}
		return Identity{}, err
	}
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}

	// Stat the selected root and boot directory to confirm the kernel view is
	// live. When boot belongs to root, both must sit on the same filesystem;
	// a divergent boot directory means the mount table was raced or lies.
	rootStat, err := statUnix(root)
	if err != nil {
		return Identity{}, errMetadataUnavailable
	}
	bootStat, err := statUnix(filepath.Join(root, "boot"))
	if err != nil {
		return Identity{}, errMetadataUnavailable
	}
	if rootStat.Mode&unix.S_IFMT == unix.S_IFLNK || bootStat.Mode&unix.S_IFMT == unix.S_IFLNK {
		return Identity{}, errors.New("boot ownership requires non-symlink root and boot directories")
	}
	if bootWithinRoot && rootEntry.fstype == "btrfs" && statMatchesMount(rootStat, rootEntry) && !statMatchesMount(bootStat, bootEntry) {
		return Identity{}, errors.New("boot ownership cannot attribute a boot directory spanning an unmounted btrfs subvolume; mount an explicit boot view")
	}
	if !statMatchesMount(rootStat, rootEntry) || !statMatchesMount(bootStat, bootEntry) {
		return Identity{}, errMetadataUnavailable
	}

	identity := Identity{}
	identity.Root, err = filesystemFromEntry(ctx, rootEntry)
	if err != nil {
		return Identity{}, err
	}
	if bootWithinRoot {
		identity.Boot = identity.Root
	} else {
		identity.Boot, err = filesystemFromEntry(ctx, bootEntry)
		if err != nil {
			return Identity{}, err
		}
	}
	// Re-read the selected views: unrelated mount activity is harmless, but a
	// root/boot remount while resolving identifiers invalidates this evidence.
	again, err := readFileBounded(ctx, mountInfoPath, maxMountInfoBytes)
	if ctx.Err() != nil {
		return Identity{}, ctx.Err()
	}
	if err != nil {
		return Identity{}, errMetadataUnavailable
	}
	againEntries, err := parseMountInfo(again)
	if err != nil {
		return Identity{}, err
	}
	againRoot, againBoot, _, err := selectRootAndBoot(againEntries, root)
	if err != nil || againRoot != rootEntry || againBoot != bootEntry {
		return Identity{}, errAmbiguousIdentity
	}
	againRootStat, err := statUnix(root)
	if err != nil || againRootStat.Dev != rootStat.Dev || againRootStat.Ino != rootStat.Ino {
		return Identity{}, errAmbiguousIdentity
	}
	againBootStat, err := statUnix(filepath.Join(root, "boot"))
	if err != nil || againBootStat.Dev != bootStat.Dev || againBootStat.Ino != bootStat.Ino {
		return Identity{}, errAmbiguousIdentity
	}
	return identity, nil
}

// statMatchesMount verifies that the selected directory is still the view
// described by mountinfo, including virtual device numbers used by btrfs.
func statMatchesMount(st unix.Stat_t, entry mountEntry) bool {
	return st.Mode&unix.S_IFMT == unix.S_IFDIR &&
		int(unix.Major(uint64(st.Dev))) == entry.devMajor && int(unix.Minor(uint64(st.Dev))) == entry.devMinor
}

// filesystemFromEntry resolves persistent identifiers for a mountinfo record
// using /dev/disk/by-uuid and /dev/disk/by-partuuid metadata.
func filesystemFromEntry(ctx context.Context, entry mountEntry) (Filesystem, error) {
	if err := ctx.Err(); err != nil {
		return Filesystem{}, err
	}
	major, minor := entry.devMajor, entry.devMinor
	// Btrfs mountinfo reports a virtual filesystem device, not the block
	// device's rdev. Its kernel-provided source identifies the backing member.
	if entry.fstype == "btrfs" {
		if !strings.HasPrefix(entry.source, "/dev/") {
			return Filesystem{}, errNoDeviceIdentity
		}
		var err error
		major, minor, err = statDeviceFn(entry.source)
		if err != nil {
			return Filesystem{}, errNoDeviceIdentity
		}
	}
	id, err := resolveDeviceIdentity(ctx, uuidMetaDir, partUUIDMetaDir, major, minor)
	if err != nil {
		return Filesystem{}, err
	}
	return Filesystem{
		UUID:       id.uuid,
		PartUUID:   id.partUUID,
		Device:     id.device,
		FSType:     entry.fstype,
		FSRoot:     filepath.Clean(entry.root),
		Mountpoint: entry.mountPoint,
	}, nil
}

// readFileBounded reads a file with a hard size cap, failing on truncation
// risk and respecting context cancellation between chunk reads.
func readFileBounded(ctx context.Context, path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 0, 64*1024)
	chunk := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := f.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if len(buf) > limit {
			return nil, errMalformedMountInfo
		}
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// statUnix exposes the link mode so the resolver can reject redirected views.
func statUnix(path string) (unix.Stat_t, error) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return st, err
	}
	return st, nil
}

// underAnyMount reports whether any mountinfo record is an ancestor of (or
// equal to) the path, used to distinguish an unmounted directory from a
// subdirectory of a mounted filesystem.
func underAnyMount(entries []mountEntry, path string) bool {
	for _, e := range entries {
		if e.mountPoint == path {
			return true
		}
		if e.mountPoint == "/" || (len(e.mountPoint) < len(path) &&
			path[len(e.mountPoint)] == '/' && path[:len(e.mountPoint)] == e.mountPoint) {
			return true
		}
	}
	return false
}
