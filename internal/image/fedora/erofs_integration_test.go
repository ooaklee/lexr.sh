package fedora

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ooaklee/lexr.sh/internal/platform"
)

// TestFedoraEROFSMetadataIntegration checks the production extractor against a
// packed, LZMA-compressed filesystem with capabilities unrelated to RPM metadata.
// It uses an existing tools image and a fresh disposable volume, and compares
// both extraction and a second repack/extraction with the original fixture.
func TestFedoraEROFSMetadataIntegration(t *testing.T) {
	image := os.Getenv("LEXR_TEST_FEDORA_EROFS_IMAGE")
	if image == "" {
		t.Skip("set LEXR_TEST_FEDORA_EROFS_IMAGE to an existing Fedora tools image")
	}
	// Docker Desktop may require an explicitly shared exchange parent. All
	// filesystem metadata and image data stay inside the disposable Linux volume.
	workspace, err := os.MkdirTemp(os.Getenv("LEXR_TEST_FEDORA_EROFS_WORKSPACE"), ".fedora-erofs-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(workspace); err != nil {
			t.Errorf("remove EROFS test exchange directory: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	docker := platform.NewDocker(nil)
	if _, err := docker.Runner.Capture(ctx, platform.Command{Name: "docker", Args: []string{"image", "inspect", image}}); err != nil {
		t.Fatal(err)
	}
	volume, err := docker.CreateWorkVolume(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		if err := docker.RemoveWorkVolume(cleanup, volume); err != nil {
			t.Errorf("remove disposable EROFS test volume %s: %v", volume, err)
		}
	})
	run := func(stage, script string, arguments ...string) {
		t.Helper()
		args := append([]string{"bash", "-ceu", script, "fedora-erofs-test"}, arguments...)
		if err := docker.RunInWorkspaceVolumePreservingXattrs(ctx, image, workspace, volume, args...); err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
	}
	run("create source fixture", fedoraEROFSFixtureScript)
	for _, round := range []struct{ source, image, destination string }{
		{"/linux-work/source", "/linux-work/source.erofs", "/linux-work/extracted"},
		{"/linux-work/extracted", "/linux-work/repacked.erofs", "/linux-work/reextracted"},
	} {
		run("pack "+round.source, fedoraEROFSPackFixtureScript, round.source, round.image)
		if err := docker.RunInWorkspaceVolumePreservingXattrs(ctx, image, workspace, volume,
			erofsExtractionArguments(round.image, round.destination)...); err != nil {
			t.Fatalf("extract %s: %v", round.image, err)
		}
		run("compare "+round.destination, fedoraEROFSCompareFixtureScript, "/linux-work/source", round.destination)
	}
}

// fedoraEROFSFixtureScript creates ordinary files and metadata directly, so no
// package database can supply or repair the capabilities being tested.
const fedoraEROFSFixtureScript = `python3 - <<'PY'
import errno
import os
from pathlib import Path
import struct

root = Path('/linux-work/source')
root.mkdir()
for name in ('bin', 'fragments', 'acl-dir', 'acl-dir/inner', 'z-last-alias'):
    (root / name).mkdir()

# Distinct small files exercise the packed-fragment inode, including data with
# embedded NULs which cannot be compared faithfully through shell variables.
for number in range(384):
    payload = (f'fragment-{number:03d}:'.encode() + bytes(range(256))) * 7
    (root / 'fragments' / f'{number:03d}').write_bytes(payload)

for name in ('root-cap', 'nonroot-cap', 'hardlink-cap', 'v3-cap', 'plain'):
    path = root / 'bin' / name
    path.write_bytes(b'#!/bin/sh\nexit 0\n' + name.encode() + b'\x00\xff\n')
    os.chmod(path, 0o751)
(root / 'acl-dir/plain').write_bytes(b'no inherited source ACL\n')
(root / 'acl-dir/inner/plain').write_bytes(b'nested directory payload\n')
os.chown(root / 'bin/nonroot-cap', 12345, 23456)
os.chown(root / 'bin/hardlink-cap', 12345, 23456)
os.chmod(root / 'bin/hardlink-cap', 0o4751)
os.link(root / 'bin/hardlink-cap', root / 'acl-dir/cap-alias')
os.link(root / 'bin/hardlink-cap', root / 'z-last-alias/cap-alias')

# VFS capability revision 2 carries effective/permitted/inheritable bitmaps.
capabilities = {
    'root-cap': struct.pack('<5I', 0x02000001, 1 << 13, 1 << 10, 0, 0),
    'nonroot-cap': struct.pack('<5I', 0x02000001, 1 << 10, 0, 0, 0),
    'hardlink-cap': struct.pack('<5I', 0x02000001, 1 << 7, 1 << 6, 0, 0),
}
for name, value in capabilities.items():
    path = root / 'bin' / name
    os.setxattr(path, 'security.capability', value)
    assert os.getxattr(path, 'security.capability') == value, name + ': source v2 capability differs'

# A nonzero rootid must survive byte for byte when this Linux namespace can
# represent it. Read the source after setting it to account for VFS conversion.
v3 = struct.pack('<6I', 0x03000001, 1 << 10, 0, 0, 0, 12345)
try:
    os.setxattr(root / 'bin/v3-cap', 'security.capability', v3)
except OSError as error:
    if error.errno not in (errno.EINVAL, errno.EOPNOTSUPP, errno.EPERM):
        raise
    print('Revision-3 capability unavailable in this Linux namespace:', error, flush=True)
else:
    actual = os.getxattr(root / 'bin/v3-cap', 'security.capability')
    assert actual == v3, 'source nonzero-rootid v3 capability was not preserved'
    print('Revision-3 capability with rootid 12345 included', flush=True)

# Linux POSIX ACL xattrs consist of a version followed by tag/permissions/id
# entries. Add defaults after creating children: extraction must not introduce
# inherited ACLs absent from the source tree.
undefined = 0xffffffff
access = struct.pack('<I', 2) + b''.join(struct.pack('<HHI', *entry) for entry in (
    (1, 7, undefined), (2, 5, 12345), (4, 5, undefined),
    (8, 4, 23456), (16, 5, undefined), (32, 0, undefined),
))
default = struct.pack('<I', 2) + b''.join(struct.pack('<HHI', *entry) for entry in (
    (1, 7, undefined), (2, 7, 12345), (4, 5, undefined),
    (8, 4, 23456), (16, 7, undefined), (32, 1, undefined),
))
for name in ('acl-dir', 'acl-dir/inner'):
    path = root / name
    os.chown(path, 12345, 23456)
    os.chmod(path, 0o2750)
    os.setxattr(path, 'system.posix_acl_access', access)
    os.setxattr(path, 'system.posix_acl_default', default)
    os.setxattr(path, 'user.lexr.fixture', b'directory\x00metadata\xff')
    assert os.getxattr(path, 'system.posix_acl_access') == access, name + ': source access ACL differs'
    assert os.getxattr(path, 'system.posix_acl_default') == default, name + ': source default ACL differs'

os.setxattr(root / 'bin/plain', 'user.lexr.fixture', b'regular\x00metadata\xff')
os.symlink('../bin/nonroot-cap', root / 'z-last-alias/symlink')
os.chown(root / 'z-last-alias/symlink', 34567, 45678, follow_symlinks=False)

# Exercise labels on every node, including symlinks; no host SELinux policy is
# needed because the extraction boundary disables container label confinement.
paths = [root, *sorted(root.rglob('*'))]
for path in paths:
    kind = b'lnk_file_t' if path.is_symlink() else b'usr_t'
    os.setxattr(path, 'security.selinux', b'system_u:object_r:' + kind + b':s0\x00', follow_symlinks=False)
    os.utime(path, ns=(1700000001123456789, 1700000001123456789), follow_symlinks=False)
assert len(list((root / 'fragments').iterdir())) == 384
assert not (root / 'var/lib/rpm').exists() and not (root / 'usr/lib/sysimage/rpm').exists()
print('Created 384 fragments, capability executables, hardlinks, ACLs, symlink and SELinux labels', flush=True)
PY
`

// fedoraEROFSPackFixtureScript uses production compression settings and rejects
// a fixture which does not actually exercise the special packed-fragment inode.
const fedoraEROFSPackFixtureScript = `mkfs.erofs -Efragments -C 1048576 -z lzma,level=6 "$2" "$1"
python3 - "$2" <<'PY'
import subprocess
import sys

image = sys.argv[1]
output = subprocess.check_output(['dump.erofs', '-s', image], text=True)
fields = dict(line.split(':', 1) for line in output.splitlines() if ':' in line)
assert int(fields.get('Filesystem packed nid', '0').strip()) > 0, image + ': no packed-fragment inode\n' + output
assert 'lzma' in fields.get('Filesystem compr_algs', ''), image + ': no LZMA compression\n' + output
print(image + ': packed-fragment inode and LZMA compression verified', flush=True)
PY
`

// fedoraEROFSCompareFixtureScript compares every source path and xattr, including
// unexpected inherited ACLs, without following symbolic links or executing data.
const fedoraEROFSCompareFixtureScript = `python3 - "$1" "$2" <<'PY'
import os
from pathlib import Path
import stat
import sys

source, destination = map(Path, sys.argv[1:])

def inventory(root):
    """Enumerate directories without following links outside the fixture."""
    result = {'.': root}
    for directory, directories, files in os.walk(root, followlinks=False):
        for name in directories + files:
            path = Path(directory) / name
            result[str(path.relative_to(root))] = path
    return result

expected, actual = inventory(source), inventory(destination)
assert expected.keys() == actual.keys(), (
    f'{destination}: missing paths {sorted(expected.keys() - actual.keys())}; '
    f'unexpected paths {sorted(actual.keys() - expected.keys())}'
)
source_links, extracted_links = {}, {}
capability_paths = []
for name in sorted(expected):
    left, right = expected[name], actual[name]
    before, after = left.lstat(), right.lstat()
    for field in ('st_mode', 'st_uid', 'st_gid', 'st_mtime_ns'):
        want, got = getattr(before, field), getattr(after, field)
        assert want == got, f'{destination}/{name}: {field}: expected {want}, got {got}'
    if stat.S_ISREG(before.st_mode):
        assert left.read_bytes() == right.read_bytes(), f'{destination}/{name}: contents differ'
        assert before.st_nlink == after.st_nlink, f'{destination}/{name}: hardlink count differs'
        source_links.setdefault((before.st_dev, before.st_ino), []).append(name)
        extracted_links.setdefault((after.st_dev, after.st_ino), []).append(name)
    elif stat.S_ISLNK(before.st_mode):
        assert os.readlink(left) == os.readlink(right), f'{destination}/{name}: symlink target differs'
    before_xattrs = {key: os.getxattr(left, key, follow_symlinks=False)
                     for key in os.listxattr(left, follow_symlinks=False)}
    after_xattrs = {key: os.getxattr(right, key, follow_symlinks=False)
                    for key in os.listxattr(right, follow_symlinks=False)}
    assert before_xattrs.keys() == after_xattrs.keys(), (
        f'{destination}/{name}: xattr names: expected {sorted(before_xattrs)}, got {sorted(after_xattrs)}'
    )
    for key, want in before_xattrs.items():
        got = after_xattrs[key]
        assert want == got, f'{destination}/{name}: {key}: expected {want.hex()}, got {got.hex()}'
    if 'security.capability' in before_xattrs:
        capability_paths.append(name)

# Compare groups by pathname, since inode numbers necessarily change on extract.
assert sorted(source_links.values()) == sorted(extracted_links.values()), f'{destination}: hardlink groups differ'
required = {'bin/root-cap', 'bin/nonroot-cap', 'bin/hardlink-cap', 'acl-dir/cap-alias', 'z-last-alias/cap-alias'}
assert required.issubset(capability_paths), f'{destination}: source capability fixture is incomplete'
print(f'{destination}: {len(expected)} paths match contents, modes, owners, timestamps, links and raw xattrs; '
      f'{len(capability_paths)} capability paths verified', flush=True)
PY
`
