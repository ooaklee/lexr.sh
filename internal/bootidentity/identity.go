// Package bootidentity binds boot evidence to mounted filesystems without
// evaluating bootloader scripts or exposing device identifiers in reports.
package bootidentity

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// Filesystem describes the mounted view of one filesystem. These identifiers
// are private comparison inputs, not fields to serialise in CLI receipts.
type Filesystem struct {
	// UUID is the filesystem identifier resolved for this mounted device.
	UUID string
	// PartUUID is the partition identifier, when the filesystem has one.
	PartUUID string
	// Device is the canonical block-device path, when available.
	Device string
	// FSType identifies filesystem-specific root selection such as btrfs.
	FSType string
	// FSRoot is the mount's root within its filesystem, including a subvolume.
	FSRoot string
	// Mountpoint is the absolute directory exposing this view to the inspector.
	Mountpoint string
}

// Identity records the selected Linux root and the filesystem serving /boot.
type Identity struct {
	Root Filesystem
	Boot Filesystem
}

// MarshalJSON forbids accidentally publishing private mounted-device evidence.
func (Identity) MarshalJSON() ([]byte, error) {
	return nil, errors.New("mounted filesystem identity must not be serialised")
}

// MarshalJSON enforces the same privacy boundary for a detached filesystem view.
func (Filesystem) MarshalJSON() ([]byte, error) {
	return nil, errors.New("mounted filesystem identifiers must not be serialised")
}

// Resolver supplies fresh mounted-filesystem evidence. It is injectable through
// the internal Go API for portable fixtures, never through a CLI flag or file.
type Resolver func(context.Context, string) (Identity, error)

// resolverKey prevents collisions with unrelated context dependencies.
type resolverKey struct{}

// expectedKey carries the private identity pinned for one transaction.
type expectedKey struct{}

// WithExpected requires all subsequent resolutions to describe the reviewed
// mounted root and boot views. It adds a check, never an identity override.
func WithExpected(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, expectedKey{}, identity)
}

// WithResolver supplies an explicit inspection dependency to internal callers.
// Production CLI entrypoints use Resolve's native mounted-filesystem reader.
func WithResolver(ctx context.Context, resolver Resolver) context.Context {
	return context.WithValue(ctx, resolverKey{}, resolver)
}

// Resolve obtains fresh identity for an exact mounted Linux root. An unpacked
// image directory is not proof of the destination disk's runtime identity.
func Resolve(ctx context.Context, root string) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	resolver, _ := ctx.Value(resolverKey{}).(Resolver)
	if resolver == nil {
		resolver = resolvePlatform
	}
	identity, err := resolver(ctx, root)
	if err != nil {
		return Identity{}, err
	}
	if err := identity.validate(root); err != nil {
		return Identity{}, err
	}
	if expected, pinned := ctx.Value(expectedKey{}).(Identity); pinned && identity != expected {
		return Identity{}, errors.New("mounted root or boot identity changed since preflight; stop and review the mount layout")
	}
	return identity, nil
}

// validate rejects unbounded identities and views outside the selected root.
func (identity Identity) validate(root string) error {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || identity.Root.Mountpoint != root ||
		(identity.Boot.Mountpoint != root && identity.Boot.Mountpoint != filepath.Join(root, "boot")) {
		return errors.New("boot ownership requires the selected root and its own mounted boot directory")
	}
	for _, fs := range []Filesystem{identity.Root, identity.Boot} {
		if fs.FSRoot == "" || !strings.HasPrefix(fs.FSRoot, "/") || filepath.Clean(fs.FSRoot) != fs.FSRoot {
			return errors.New("boot ownership has an unsupported filesystem root")
		}
		for _, value := range []string{fs.UUID, fs.PartUUID, fs.Device, fs.FSType, fs.FSRoot, fs.Mountpoint} {
			if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
				return errors.New("boot ownership has malformed filesystem evidence")
			}
		}
		if fs.UUID == "" && fs.PartUUID == "" && fs.Device == "" {
			return errors.New("boot ownership has no mounted filesystem identity")
		}
	}
	return nil
}
