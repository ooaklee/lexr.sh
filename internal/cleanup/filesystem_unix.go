//go:build !windows

package cleanup

import (
	"errors"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// directoryIdentity extracts the stable Unix device and inode pair shared by
// plans, receipts, and opened-root revalidation.
func directoryIdentity(info os.FileInfo) (DirectoryIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return DirectoryIdentity{}, errors.New("filesystem metadata has no Unix directory identity")
	}
	identity := DirectoryIdentity{Device: uint64(stat.Dev), Inode: uint64(stat.Ino)}
	if !identity.valid() {
		return DirectoryIdentity{}, errors.New("filesystem directory identity is invalid")
	}
	return identity, nil
}

// unsupportedAnchoredRecoveryMetadata reports metadata that the recovery
// format cannot reproduce exactly, including hard links, special mode bits,
// extended attributes, capabilities, ACLs, and security labels.
func unsupportedAnchoredRecoveryMetadata(root *os.Root, name string, inspected os.FileInfo, expectedLinks uint64) (string, error) {
	if inspected.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return "special permission bits cannot be reproduced by automatic clean-up", nil
	}
	stat, ok := inspected.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("filesystem metadata has no Unix link count")
	}
	if uint64(stat.Nlink) != expectedLinks {
		return "hard-linked files cannot be reproduced exactly by automatic clean-up", nil
	}
	file, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(inspected, opened) {
		return "", errors.Join(err, errors.New("recovery metadata target changed while it was opened"))
	}
	attributeNames, err := anchoredExtendedAttributeNames(file)
	if err != nil {
		return "", err
	}
	for _, attributeName := range attributeNames {
		if attributeName == "com.apple.provenance" {
			continue
		}
		return "extended attributes, capabilities, ACLs, or security labels cannot be reproduced by automatic clean-up", nil
	}
	after, err := root.Lstat(name)
	if err != nil || !os.SameFile(inspected, after) {
		return "", errors.Join(err, errors.New("recovery metadata target changed during inspection"))
	}
	return "", nil
}

// anchoredExtendedAttributeNames lists descriptor metadata without reading
// attribute values and treats filesystems without xattr support as empty.
func anchoredExtendedAttributeNames(file *os.File) ([]string, error) {
	size, err := unix.Flistxattr(int(file.Fd()), nil)
	if errors.Is(err, unix.ENOTSUP) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}
	buffer := make([]byte, size)
	written, err := unix.Flistxattr(int(file.Fd()), buffer)
	if err != nil {
		return nil, err
	}
	if written < 0 || written > len(buffer) {
		return nil, errors.New("extended-attribute list has an invalid length")
	}
	names := make([]string, 0)
	for _, encoded := range strings.Split(string(buffer[:written]), "\x00") {
		if encoded != "" {
			names = append(names, encoded)
		}
	}
	return names, nil
}

// fileOwnership returns portable numeric ownership from Unix file metadata.
func fileOwnership(info os.FileInfo) (uint32, uint32, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, errors.New("filesystem metadata has no Unix ownership")
	}
	return stat.Uid, stat.Gid, nil
}

// renameAnchoredDirectories moves one entry between stable directory
// descriptors without resolving either directory through a mutable pathname.
func renameAnchoredDirectories(source *os.File, sourceName string, destination *os.File, destinationName string) error {
	return unix.Renameat(int(source.Fd()), sourceName, int(destination.Fd()), destinationName)
}

// linkAnchoredDirectories creates a no-overwrite hard link between two stable
// directory descriptors without resolving either directory by pathname.
func linkAnchoredDirectories(source *os.File, sourceName string, destination *os.File, destinationName string) error {
	return unix.Linkat(int(source.Fd()), sourceName, int(destination.Fd()), destinationName, 0)
}
