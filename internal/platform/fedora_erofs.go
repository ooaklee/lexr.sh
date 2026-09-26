package platform

// fedoraEROFSExtractorDockerfile builds only the corrected extractor from the
// matching signed Fedora source package. The distribution's mkfs and dump
// executables remain unchanged. Source, notices and the generated patch stay
// inside the tool image so the locally built binary has explicit provenance.
const fedoraEROFSExtractorDockerfile = `ADD --checksum=sha256:45cc43606bddd4a5642871d94d9269721131c289dc52267c982682b097248d5b https://kojipkgs.fedoraproject.org/packages/erofs-utils/1.9.4/1.fc44/data/signed/6d9f90a6/src/erofs-utils-1.9.4-1.fc44.src.rpm /tmp/lexr-erofs.src.rpm
RUN rpm --checksig /tmp/lexr-erofs.src.rpm \
 && test "$(rpm -qp --qf '%{NAME}-%{VERSION}-%{RELEASE}' /tmp/lexr-erofs.src.rpm)" = erofs-utils-1.9.4-1.fc44 \
 && test "$(rpm -qp --qf '%{SOURCEPACKAGE}' /tmp/lexr-erofs.src.rpm)" = 1 \
 && dnf install -y --setopt=install_weak_deps=False \
	  acl autoconf automake libtool make libdeflate-devel libselinux-devel libuuid-devel \
	  libzstd-devel lz4-devel xxhash-devel xz-devel zlib-ng-compat-devel \
 && mkdir -p /usr/local/src/lexr-erofs /usr/local/libexec/lexr \
 && mv /tmp/lexr-erofs.src.rpm /usr/local/src/lexr-erofs/erofs-utils-1.9.4-1.fc44.src.rpm \
 && cd /usr/local/src/lexr-erofs \
 && rpm2cpio erofs-utils-1.9.4-1.fc44.src.rpm > source.cpio \
 && cpio -idm --quiet < source.cpio \
 && rm source.cpio \
 && tar -xzf erofs-utils-1.9.4.tar.gz \
 && dnf clean all
RUN python3 - <<'LEXR_EROFS_PATCH'
` + fedoraEROFSExtractorPatch + `
LEXR_EROFS_PATCH
RUN cd /usr/local/src/lexr-erofs/erofs-utils-1.9.4 \
 && autoreconf -fi \
 && ./configure --disable-fuse --disable-ublk --disable-oci --disable-s3 --disable-fanotify \
	  --without-libcurl --without-json-c --without-libxml2 --without-libnl3 --without-openssl --without-qpl \
	  --enable-lzma --enable-lz4 --with-libzstd --with-libdeflate --with-zlib --with-uuid --with-selinux --with-xxhash \
 && make -j2 -C lib \
 && make -j2 -C fsck \
 && install -m 0755 fsck/fsck.erofs /usr/local/libexec/lexr/fsck.erofs \
 && /usr/local/libexec/lexr/fsck.erofs --version | grep -Fx 'fsck.erofs (erofs-utils) 1.9.4-lexr1' \
 && /usr/local/libexec/lexr/fsck.erofs --help 2>&1 | grep -F -- '--path=X' \
 && make clean
`

// fedoraEROFSExtractorPatch changes only the order of upstream metadata
// restoration. lchown clears file capabilities and chmod can change ACL masks;
// apply source xattrs last for every inode, including subsequent hardlinks and
// directories after their children. A full source digest and unique anchors
// reject drift or repeated application before any patched binary is built.
const fedoraEROFSExtractorPatch = `from pathlib import Path
import difflib
import hashlib

root = Path('/usr/local/src/lexr-erofs/erofs-utils-1.9.4')
path = root / 'fsck/main.c'
original = path.read_bytes()
expected = 'e5a543497f7a90654c9a905eed60baabf8d88ae732925a1a1b087ea219afe40e'
if hashlib.sha256(original).hexdigest() != expected:
    raise SystemExit('EROFS extractor source differs from the reviewed Fedora 1.9.4 source')
source = original.decode()
definition = 'static int erofsfsck_check_inode(erofs_nid_t pnid, erofs_nid_t nid)\n{'
start = source.index(definition)
end = source.index('\n#ifdef FUZZING', start)
function = source[start:end]
block_start = function.index('\n\tif (fsckcfg.check_decomp && fsckcfg.dump_xattrs) {')
block_end = function.index('\n\tif (S_ISDIR(inode.i_mode))', block_start)
block = function[block_start:block_end]
if function.count(block) != 1 or block.count('erofsfsck_dump_xattrs(&inode)') != 1:
    raise SystemExit('EROFS xattr restoration block is not unique')
function = function[:block_start] + function[block_end:]
anchor = '\n\tif (ret == -ECANCELED)\n\t\tret = 0;\nout:\n'
if function.count(anchor) != 1:
    raise SystemExit('EROFS metadata completion point is not unique')
block = block.replace('if (fsckcfg.check_decomp', 'if (!ret && fsckcfg.check_decomp', 1)
function = function.replace(anchor, anchor.removesuffix('out:\n') + block + 'out:\n', 1)
patched = source[:start] + function + source[end:]
path.write_text(patched)
(root.parent / 'fsck-preserve-xattrs.patch').write_text(''.join(difflib.unified_diff(
    source.splitlines(keepends=True), patched.splitlines(keepends=True),
    fromfile='a/fsck/main.c', tofile='b/fsck/main.c')))
version = root / 'VERSION'
lines = version.read_text().splitlines(keepends=True)
if lines[0].strip() != '1.9.4':
    raise SystemExit('EROFS source version is not the reviewed 1.9.4 release')
lines[0] = '1.9.4-lexr1\n'
version.write_text(''.join(lines))
(root.parent / 'LEXR_PATCH.txt').write_text(
    'Locally built erofs-utils 1.9.4-lexr1 extractor; not a Fedora-signed binary.\n'
    'Source: signed erofs-utils-1.9.4-1.fc44.src.rpm, retained alongside this file.\n'
    'Lexr restores source xattrs after ownership and mode updates for every inode.\n'
    'This preserves file capabilities and ACLs, including non-RPM-owned paths.\n'
    'Upstream source, copyright and licence files remain in erofs-utils-1.9.4/.\n')
`
