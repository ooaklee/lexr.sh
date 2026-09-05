#!/usr/bin/env python3
"""Lexr Pop!_OS additive systemd-boot loader lifecycle helper.

Original, self-contained Python 3 (standard library only).  It manages
*additive* Type-1 BLS entries and immutable per-generation kernel, initramfs
and device-tree artefacts on a Pop!_OS ESP, coexisting with kernelstub: it
never modifies or diverts kernelstub, never sets EFI variables, and never
deletes entries or files it does not own.

Ownership scope:
  * entries:   ESP/loader/entries/lexr-<rootUUID>-<ABI>.conf
  * artefacts: ESP/EFI/lexr/<rootUUID>/<ABI>/<generation>/
  * receipts:  /var/lib/lexr/pop-boot/<rootUUID>/<ABI>.json

Exit codes: 0 success or bounded no-op; 65 hard failure; 75 deferrable (no
generic boot-support identity for the ABI); 76 deferrable (ESP not mounted).

The sibling pop-recovery-refresh helper handles Distinst recovery using the
original live-media kernel, initramfs and device tree.
"""

import argparse
import functools
import hashlib
import json
import os
import re
import stat
import subprocess
import sys
import tempfile

SURFACE_KERNEL_ARGUMENTS = (
    "clk_ignore_unused pd_ignore_unused arm64.nopauth systemd.tpm2_wait=0"
)
STOCK_CURRENT_ENTRY = "Pop_OS-current"
STOCK_OLDKERN_ENTRY = "Pop_OS-oldkern"
ROOT_UUID_RE = re.compile(r"^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$")
FAT_UUID_RE = re.compile(r"^[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}$")
HEX64_RE = re.compile(r"^[0-9a-f]{64}$")
ABI_RE = re.compile(r"^[a-z0-9][a-z0-9.+-]{0,126}$")
COMPONENT_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$")
MAX_TEXT_BYTES = 1 << 16
MAX_FILE_BYTES = 1 << 30
LIVE_FILESYSTEMS = ("overlay", "squashfs")


class Fail(Exception):
    """A hard, non-deferrable failure reported on standard error."""


def note(message):
    sys.stderr.write("lexr pop-boot-refresh: %s\n" % message)


def require(condition, message):
    if not condition:
        raise Fail(message)


def safe_abi(abi):
    return bool(abi) and len(abi) <= 127 and ABI_RE.match(abi) is not None


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as stream:
        while True:
            block = stream.read(1 << 20)
            if not block:
                break
            digest.update(block)
    return digest.hexdigest()


def canonical_regular(path, label):
    require(not os.path.islink(path), "%s is redirected by a symbolic link" % label)
    require(os.path.isfile(path), "%s is not a regular file" % label)
    require(os.path.realpath(path) == path, "%s is redirected" % label)
    size = os.path.getsize(path)
    require(0 < size <= MAX_FILE_BYTES, "%s size is outside its bounded range" % label)


def ensure_directory(path, label):
    require(not os.path.islink(path), "%s is redirected by a symbolic link" % label)
    require(os.path.isdir(path), "%s is not a real directory" % label)
    require(os.path.realpath(path) == path, "%s is redirected" % label)


def ensure_directory_chain(path, label):
    """Create missing components one by one, rejecting redirected ones."""
    partial = os.sep
    for component in path.split(os.sep)[1:]:
        if not component or component in (".", ".."):
            raise Fail("unsafe component in %s" % label)
        partial = os.path.join(partial, component)
        if os.path.lexists(partial):
            ensure_directory(partial, "%s component" % label)
        else:
            os.mkdir(partial, 0o755)


def read_single_line(path, label):
    canonical_regular(path, label)
    with open(path, "r", encoding="utf-8", errors="strict") as stream:
        data = stream.read(MAX_TEXT_BYTES + 1)
    require(len(data) <= MAX_TEXT_BYTES, "%s exceeds its size bound" % label)
    lines = data.splitlines()
    require(len(lines) == 1 and lines[0].strip(), "%s is not one non-empty line" % label)
    return lines[0].strip()


def fsync_and_rename(temporary, destination):
    """Durably persist a temporary file before its atomic rename."""
    handle = os.open(temporary, os.O_RDONLY)
    try:
        os.fsync(handle)
    finally:
        os.close(handle)
    os.rename(temporary, destination)
    directory = os.open(os.path.dirname(destination), os.O_RDONLY)
    try:
        os.fsync(directory)
    except OSError:
        pass
    finally:
        os.close(directory)


def atomic_text(destination, contents, mode=0o644):
    handle, temporary = tempfile.mkstemp(dir=os.path.dirname(destination), prefix=".lexr.")
    try:
        with os.fdopen(handle, "w", encoding="utf-8") as stream:
            stream.write(contents)
        os.chmod(temporary, mode)
        fsync_and_rename(temporary, destination)
    except BaseException:
        if os.path.lexists(temporary):
            os.unlink(temporary)
        raise


# --- mount table -----------------------------------------------------------

def read_mounts(root):
    """Return [(source, mountpoint, fstype)] from the root's own mount table.

    A fixture root may supply hermetic <root>/proc/mounts; the real installed
    system uses /proc/mounts.  Only this table is consulted, never device
    contents.
    """
    packaged = os.path.join(root, "proc", "mounts")
    path = packaged if os.path.lexists(packaged) and not os.path.islink(packaged) else "/proc/mounts"
    with open(path, "r", encoding="utf-8", errors="strict") as stream:
        data = stream.read((1 << 20) + 1)
    require(len(data) <= 1 << 20, "mount table exceeds its bound")
    def unescape(value):
        return re.sub(r"\\(040|011|012|134)",
                      lambda match: chr(int(match.group(1), 8)), value)
    entries = []
    for line in data.splitlines():
        fields = line.split()
        require(len(fields) == 6, "malformed mount table record")
        entries.append(tuple(unescape(value) for value in fields[:3]))
    return entries


def find_mount(root, mountpoint):
    """Locate the topmost matching mount, decoding procfs path escapes."""
    wanted = os.path.realpath(mountpoint if root == "/"
                              else os.path.join(root, mountpoint.lstrip("/")))
    for source, point, fstype in reversed(read_mounts(root)):
        if os.path.realpath(os.path.normpath(point)) == wanted:
            return source, fstype
    return None, None


def root_is_live(root):
    """Report whether the supplied root is itself a live media filesystem.

    An installer chroot inherits the live media's /proc/cmdline, so
    boot=casper alone must never suppress work on a mounted installed root:
    liveness is decided by the root's own mount identity (overlay or
    squashfs), never by the command line.
    """
    _, fstype = find_mount(root, "/")
    return fstype in LIVE_FILESYSTEMS


# --- fstab and ESP ---------------------------------------------------------

def parse_fstab(root):
    """Return (root UUID, ESP source) declared by the root's own fstab."""
    fstab = os.path.join(root, "etc", "fstab")
    canonical_regular(fstab, "fstab")
    root_uuid = None
    esp_source = None
    with open(fstab, "r", encoding="utf-8", errors="strict") as stream:
        for line in stream:
            fields = line.split("#", 1)[0].split()
            if len(fields) < 2:
                continue
            source, mountpoint = fields[0], fields[1]
            if mountpoint == "/":
                require(root_uuid is None, "fstab declares the root filesystem twice")
                require(source.startswith("UUID="), "fstab root must be identified by UUID")
                candidate = source[5:].strip().lower()
                require(ROOT_UUID_RE.match(candidate), "fstab root UUID is malformed")
                root_uuid = candidate
            elif mountpoint == "/boot/efi":
                require(esp_source is None, "fstab declares /boot/efi twice")
                esp_source = source
    require(root_uuid is not None, "fstab does not identify the root filesystem by UUID")
    require(esp_source is not None, "fstab does not declare a /boot/efi ESP")
    return root_uuid, esp_source


def device_identity(path):
    """Return the stat identity of a block device, or None when unavailable."""
    try:
        info = os.stat(path)
    except OSError:
        return None
    if not stat.S_ISBLK(info.st_mode):
        return None
    return (os.major(info.st_rdev), os.minor(info.st_rdev))


def blkid_uuid(device):
    """Return the exact filesystem UUID of a block device via blkid.

    Only blkid's structured output is used; the device is never read as text.
    """
    try:
        result = subprocess.run(
            ["blkid", "-s", "UUID", "-o", "value", device],
            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL)
    except OSError:
        return None
    if result.returncode != 0:
        return None
    return result.stdout.decode("utf-8", "strict").strip().lower()


def resolve_esp(root, esp_override):
    """Return the mounted vfat ESP directory beneath root, validated.

    The fstab /boot/efi source must match the block device actually mounted
    there. A vfat FAT serial (e.g. ABCD-1234) is not an RFC UUID, so it is
    compared by block-device rdev through /dev/disk/by-uuid; an RFC UUID is
    compared exactly via blkid. An explicit --esp override must name the same
    real, non-redirected directory; device contents are never read as text.
    """
    _, esp_source = parse_fstab(root)
    source, fstype = find_mount(root, "/boot/efi")
    require(source is not None, "declared ESP is not mounted at /boot/efi")
    require(fstype == "vfat", "/boot/efi is not a vfat ESP")
    declared = os.path.join(root, "boot", "efi")
    ensure_directory(declared, "declared ESP directory")
    if esp_override is not None:
        require(esp_override.startswith("/") and len(esp_override) <= 4096,
                "explicit ESP path must be absolute and bounded")
        require(not os.path.islink(esp_override),
                "explicit ESP path is redirected by a symbolic link")
        require(os.path.realpath(esp_override) == declared,
                "explicit ESP path does not match the declared /boot/efi mount")
    if esp_source.startswith("/dev/"):
        require(device_identity(esp_source) is not None and
                device_identity(esp_source) == device_identity(source),
                "mounted ESP device does not match the fstab declaration")
    elif esp_source.startswith("UUID="):
        serial = esp_source[5:]
        if FAT_UUID_RE.match(serial):
            by_uuid = os.path.join(root, "dev/disk/by-uuid", serial)
            require(device_identity(by_uuid) is not None and
                    device_identity(by_uuid) == device_identity(source),
                    "mounted ESP device does not match the fstab FAT serial")
        elif ROOT_UUID_RE.match(serial.lower()):
            require(blkid_uuid(source) == serial.lower(),
                    "mounted ESP filesystem UUID does not match the fstab declaration")
        else:
            raise Fail("fstab ESP UUID is malformed")
    else:
        raise Fail("fstab ESP source is neither a device nor a UUID")
    return declared


def esp_mounted(root, esp):
    """Report whether the ESP directory is a real mountpoint."""
    _, fstype = find_mount(root, "/boot/efi")
    if fstype is not None:
        return True
    wanted = os.path.realpath(os.path.normpath(esp))
    for _, point, _ in read_mounts(root):
        if os.path.realpath(os.path.normpath(point)) == wanted:
            return True
    return False


def resolve_esp_quiet(root):
    """Return the candidate ESP path or None, without raising on unmounted."""
    if not os.path.lexists(os.path.join(root, "etc", "fstab")):
        return None
    try:
        return resolve_esp(root, None)
    except Fail:
        return None


# --- generic boot-support state --------------------------------------------

def select_platform_state(root, abi, requested):
    """Return the single pinned (platform, dtb sha256) from generic state."""
    state_root = os.path.join(root, "var", "lib", "lexr", "kernel-boot", abi)
    if not os.path.lexists(state_root):
        return None, None
    ensure_directory(state_root, "generic boot-support state")
    profiles = sorted(entry for entry in os.listdir(state_root)
                      if COMPONENT_RE.match(entry) is not None and
                      os.path.isdir(os.path.join(state_root, entry)))
    require(len(profiles) == 1,
            "generic state for ABI %s is ambiguous: %d profiles" % (abi, len(profiles)))
    platform = profiles[0]
    if requested != "auto":
        require(requested == platform,
                "requested platform %s does not match pinned state %s" % (requested, platform))
    state = os.path.join(state_root, platform)
    ensure_directory(state, "platform state")
    dtb_path = read_single_line(os.path.join(state, "dtb-path"), "state dtb-path")
    dtb_sha = read_single_line(os.path.join(state, "dtb-sha256"), "state dtb-sha256")
    require(HEX64_RE.match(dtb_sha), "state DTB digest is malformed")
    require(dtb_path.endswith(".dtb") and not dtb_path.startswith("/") and
            ".." not in dtb_path.split("/"), "state DTB path is unsafe")
    return platform, dtb_sha


# --- options ---------------------------------------------------------------

def native_options(esp, root_uuid):
    """Reuse validated root options from the native current entry."""
    entry = os.path.join(esp, "loader", "entries", STOCK_CURRENT_ENTRY + ".conf")
    if not os.path.lexists(entry):
        return None
    canonical_regular(entry, "native Pop current entry")
    with open(entry, "r", encoding="utf-8", errors="strict") as stream:
        lines = stream.read(MAX_TEXT_BYTES + 1).splitlines()
    require(sum(len(line) + 1 for line in lines) <= MAX_TEXT_BYTES, "native current entry exceeds its bound")
    options = None
    for line in lines:
        if line.startswith("options "):
            require(options is None, "native current entry repeats its options")
            options = line[len("options "):].strip()
    require(options is not None, "native current entry has no options line")
    require("\n" not in options and "\r" not in options, "native options contain line breaks")
    roots = [token for token in options.split() if token.startswith("root=")]
    require(roots == ["root=UUID=" + root_uuid],
            "native current entry does not use the fstab root UUID exactly once")
    return options


def combine_options(base):
    merged = base.split()
    for argument in SURFACE_KERNEL_ARGUMENTS.split():
        if argument not in merged:
            merged.append(argument)
    options = " ".join(merged)
    require(len(options.encode("utf-8")) <= MAX_TEXT_BYTES, "combined options exceed their bound")
    roots = [token for token in merged if token.startswith("root=")]
    require(len(roots) == 1, "combined options must contain exactly one root argument")
    return options


# --- version ordering ------------------------------------------------------

def dpkg_compare_versions(left, right):
    """Return a negative, zero or positive value for left vs right ordering.

    Ordering is delegated to ``dpkg --compare-versions``, the authoritative
    implementation of Debian version semantics. Both operands are validated
    as bounded ABI tokens before they reach argv, so no option, path or shell
    interpretation is possible.
    """
    require(safe_abi(left) and safe_abi(right), "unsafe version operand")
    if left == right:
        return 0
    for operator, sign in (("lt", -1), ("gt", 1)):
        try:
            result = subprocess.run(
                ["dpkg", "--compare-versions", left, operator, right],
                stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL)
        except OSError:
            raise Fail("dpkg is unavailable to order kernel versions")
        if result.returncode == 0:
            return sign
        if result.returncode != 1:
            raise Fail("dpkg could not order kernel versions %s and %s" % (left, right))
    return 0


# --- receipts and entries --------------------------------------------------

def receipt_path(root, root_uuid, abi):
    return os.path.join(root, "var", "lib", "lexr", "pop-boot", root_uuid, abi + ".json")


RECEIPT_FIELDS = frozenset((
    "entry", "generation", "kernel_sha256", "initrd_sha256",
    "dtb_sha256", "entry_sha256",
))


def generation_identity(kernel_sha, initrd_sha, dtb_sha):
    """Derive the canonical generation from all three artefact digests."""
    return hashlib.sha256(
        (kernel_sha + initrd_sha + dtb_sha).encode("ascii")).hexdigest()[:16]


def strict_object(pairs):
    """Reject duplicate JSON fields in ownership receipts."""
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate ownership receipt field")
        result[key] = value
    return result


def load_receipt(root, root_uuid, abi):
    path = receipt_path(root, root_uuid, abi)
    if not os.path.lexists(path):
        return None
    canonical_regular(path, "ownership receipt")
    with open(path, "r", encoding="utf-8", errors="strict") as stream:
        data = stream.read(MAX_TEXT_BYTES + 1)
    require(len(data) <= MAX_TEXT_BYTES, "ownership receipt exceeds its bound")
    receipt = json.loads(data, object_pairs_hook=strict_object)
    require(isinstance(receipt, dict), "ownership receipt is not an object")
    require(set(receipt) == RECEIPT_FIELDS,
            "ownership receipt has unexpected or missing fields")
    for field in ("entry", "generation", "kernel_sha256", "initrd_sha256",
                  "dtb_sha256", "entry_sha256"):
        value = receipt.get(field)
        require(isinstance(value, str) and value, "receipt field %s is malformed" % field)
    for field in ("kernel_sha256", "initrd_sha256", "dtb_sha256", "entry_sha256"):
        require(HEX64_RE.match(receipt[field]), "receipt field %s is not a canonical digest" % field)
    require(COMPONENT_RE.match(receipt["generation"]), "receipt generation is not canonical")
    require(receipt["generation"] == generation_identity(
        receipt["kernel_sha256"], receipt["initrd_sha256"], receipt["dtb_sha256"]),
        "receipt generation is not derived from the artefact digests")
    require(receipt["entry"] == entry_name(root_uuid, abi), "receipt entry name does not match its owner")
    return receipt


def entry_name(root_uuid, abi):
    return "lexr-%s-%s.conf" % (root_uuid, abi)


def entry_content(root_uuid, abi, generation, options):
    relative = os.path.join("EFI", "lexr", root_uuid, abi, generation)
    content = (
        "title Lexr Pop!_OS kernel %s\n"
        "linux /%s/vmlinuz\n"
        "initrd /%s/initrd.img\n"
        "devicetree /%s/devicetree.dtb\n"
        "options %s\n"
    ) % (abi, relative, relative, relative, options)
    require("\n" not in options, "options contain line breaks")
    require(len(content.encode("utf-8")) <= MAX_TEXT_BYTES, "generated entry exceeds its bound")
    return content


def copy_immutable(source, destination, expected_digest):
    """Copy one verified artefact; a differing existing copy fails closed."""
    if os.path.lexists(destination):
        canonical_regular(destination, "published artefact")
        require(sha256_file(destination) == expected_digest,
                "published artefact digest differs from the verified source")
        return
    ensure_directory(os.path.dirname(destination), "generation directory")
    handle, temporary = tempfile.mkstemp(dir=os.path.dirname(destination), prefix=".lexr-copy.")
    os.close(handle)
    try:
        with open(source, "rb") as reader, open(temporary, "wb") as writer:
            while True:
                block = reader.read(1 << 20)
                if not block:
                    break
                writer.write(block)
        os.chmod(temporary, 0o644)
        require(sha256_file(temporary) == expected_digest, "copied artefact digest mismatch")
        fsync_and_rename(temporary, destination)
    except BaseException:
        if os.path.lexists(temporary):
            os.unlink(temporary)
        raise


def publish_entry(esp, root_uuid, abi, content):
    """Atomically publish the owned entry (guards live in do_refresh)."""
    entry_dir = os.path.join(esp, "loader", "entries")
    ensure_directory_chain(entry_dir, "entry directory")
    destination = os.path.join(entry_dir, entry_name(root_uuid, abi))
    digest = hashlib.sha256(content.encode("utf-8")).hexdigest()
    atomic_text(destination, content)
    return digest


# --- loader.conf default ---------------------------------------------------

def loader_default(esp):
    loader = os.path.join(esp, "loader", "loader.conf")
    if not os.path.lexists(loader):
        return loader, None, None
    canonical_regular(loader, "loader configuration")
    with open(loader, "r", encoding="utf-8", errors="strict") as stream:
        data = stream.read(MAX_TEXT_BYTES + 1)
    require(len(data) <= MAX_TEXT_BYTES, "loader configuration exceeds its bound")
    defaults = []
    for position, line in enumerate(data.splitlines()):
        fields = line.split()
        if fields and fields[0] == "default":
            require(len(fields) == 2, "malformed loader default")
            defaults.append((position, fields[1]))
    require(len(defaults) <= 1, "loader configuration repeats its default")
    return (loader, *defaults[0]) if defaults else (loader, None, None)


def write_loader_default(esp, value):
    loader, position, _ = loader_default(esp)
    if loader is None or position is None:
        return
    with open(loader, "r", encoding="utf-8", errors="strict") as stream:
        original = stream.read(MAX_TEXT_BYTES + 1)
    lines = original.splitlines()
    if value is None:
        del lines[position]
    else:
        lines[position] = "default " + value
    updated = "\n".join(lines) + ("\n" if original.endswith("\n") or not original else "")
    atomic_text(loader, updated)


def is_verified_owned_abi(esp, root, root_uuid, abi):
    """Report whether an ABI's kernel, receipt, entry and artefacts are intact."""
    kernel = os.path.join(root, "boot", "vmlinuz-" + abi)
    if not os.path.lexists(kernel):
        return False
    try:
        canonical_regular(kernel, "exact-ABI kernel")
        receipt = load_receipt(root, root_uuid, abi)
        if receipt is None:
            return False
        entry_file = os.path.join(esp, "loader", "entries", receipt["entry"])
        if not os.path.lexists(entry_file):
            return False
        canonical_regular(entry_file, "owned entry")
        if sha256_file(entry_file) != receipt["entry_sha256"]:
            return False
        generation = os.path.join(esp, "EFI", "lexr", root_uuid, abi, receipt["generation"])
        for member, field in (("vmlinuz", "kernel_sha256"),
                              ("initrd.img", "initrd_sha256"),
                              ("devicetree.dtb", "dtb_sha256")):
            path = os.path.join(generation, member)
            if not os.path.lexists(path):
                return False
            canonical_regular(path, "owned artefact %s" % member)
            if sha256_file(path) != receipt[field]:
                return False
    except (Fail, OSError, ValueError, TypeError):
        return False
    return True


def verified_owned_abis(esp, root, root_uuid):
    """Return the ABIs whose kernel, receipt, entry and artefacts are intact."""
    return [abi for abi in owned_abis(root, root_uuid)
            if is_verified_owned_abi(esp, root, root_uuid, abi)]


def bounded_loader_default(esp, root, root_uuid):
    """Repoint the default to the highest verified custom ABI when permitted.

    An unrelated user default is preserved untouched. A stock default (which
    kernelstub may reset) is repointed to the highest verified custom ABI, so
    update-initramfs -k all cannot downgrade to an older ABI processed last.
    A lexr default must name a fully verified ABI: receipt, entry and every
    artefact.
    """
    loader, position, default = loader_default(esp)
    if loader is None or position is None or default is None:
        return
    if default != STOCK_CURRENT_ENTRY:
        match = re.match(r"^lexr-%s-(.+)$" % re.escape(root_uuid), default)
        if match is None:
            return
        previous = match.group(1)
        require(safe_abi(previous), "default names a malformed lexr ABI")
        require(is_verified_owned_abi(esp, root, root_uuid, previous),
                "default names an unowned or modified lexr entry")
    candidates = verified_owned_abis(esp, root, root_uuid)
    if not candidates:
        return
    candidates.sort(key=functools.cmp_to_key(dpkg_compare_versions))
    target = "lexr-%s-%s" % (root_uuid, candidates[-1])
    if default != target:
        write_loader_default(esp, target)


# --- refresh ---------------------------------------------------------------

def do_refresh(root, abi, image, platform, esp_override):
    require(safe_abi(abi), "unsafe exact ABI")
    require(image == "/boot/vmlinuz-" + abi, "image does not match the exact ABI")
    if root_is_live(root):
        note("live root filesystem; ESP publication deferred")
        return 0
    _, dtb_sha = select_platform_state(root, abi, platform)
    if dtb_sha is None:
        # A registered custom ABI (kernel-build ABI list) that has lost its
        # per-ABI state is tampering, not a deferral, once a real ESP target
        # is mounted; offline image preparation may still defer.
        registered = os.path.join(root, "usr", "lib", "lexr", "kernel-build", "abi")
        candidate = resolve_esp_quiet(root)
        mounted = candidate is not None and esp_mounted(root, candidate)
        if os.path.lexists(registered) and mounted:
            with open(registered, "r", encoding="utf-8", errors="strict") as stream:
                known = stream.read(MAX_TEXT_BYTES + 1).split()
            if abi in known:
                raise Fail("registered ABI %s has no generic boot-support state on a mounted target" % abi)
        note("no generic boot-support state for ABI %s; deferring" % abi)
        return 75
    kernel = os.path.join(root, "boot", "vmlinuz-" + abi)
    initrd = os.path.join(root, "boot", "initrd.img-" + abi)
    dtb = os.path.join(root, "boot", "dtb-" + abi)
    if not os.path.lexists(kernel):
        note("kernel image for ABI %s is absent; nothing to publish" % abi)
        return 0
    canonical_regular(kernel, "exact-ABI kernel")
    canonical_regular(initrd, "exact-ABI initramfs")
    if not os.path.lexists(dtb):
        # On a mounted installed target this must fail meaningfully rather
        # than silently swallow the missing device tree.
        if esp_override is not None:
            raise Fail("verified DTB for ABI %s is missing on a mounted target" % abi)
        _, fstype = find_mount(root, "/boot/efi")
        if fstype == "vfat":
            raise Fail("verified DTB for ABI %s is missing on a mounted target" % abi)
        note("verified DTB for ABI %s is not staged and no ESP is mounted; deferring" % abi)
        return 76
    canonical_regular(dtb, "staged exact-ABI DTB")
    require(sha256_file(dtb) == dtb_sha, "staged DTB digest differs from generic state")
    root_uuid, _ = parse_fstab(root)
    # An unmounted ESP defers (76) rather than failing; only a mounted ESP
    # may receive publication, and the fstab identity is then validated.
    _, esp_fstype = find_mount(root, "/boot/efi")
    if esp_override is None and esp_fstype is None:
        note("ESP is not mounted; ESP publication deferred")
        return 76
    esp = resolve_esp(root, esp_override)
    base = native_options(esp, root_uuid)
    require(base is not None,
            "native %s entry is unavailable for option reuse" % STOCK_CURRENT_ENTRY)
    options = combine_options(base)

    kernel_sha = sha256_file(kernel)
    initrd_sha = sha256_file(initrd)
    # The generation identity covers kernel, initramfs and DTB so an
    # initramfs refresh under the same ABI yields a new immutable generation.
    generation = generation_identity(kernel_sha, initrd_sha, dtb_sha)
    for component in (root_uuid, abi, generation):
        require(COMPONENT_RE.match(component), "unsafe artefact component")
    destination = os.path.join(esp, "EFI", "lexr", root_uuid, abi, generation)
    ensure_directory_chain(destination, "generation directory")

    content = entry_content(root_uuid, abi, generation, options)
    entry_digest = hashlib.sha256(content.encode("utf-8")).hexdigest()
    entry_file = os.path.join(esp, "loader", "entries", entry_name(root_uuid, abi))
    previous = load_receipt(root, root_uuid, abi)
    if os.path.lexists(entry_file):
        canonical_regular(entry_file, "owned entry")
        existing = sha256_file(entry_file)
        require(previous is not None, "pre-existing entry has no ownership receipt")
        require(existing == previous["entry_sha256"] or existing == entry_digest,
                "existing entry was modified outside this lifecycle")

    copy_immutable(kernel, os.path.join(destination, "vmlinuz"), kernel_sha)
    copy_immutable(initrd, os.path.join(destination, "initrd.img"), initrd_sha)
    copy_immutable(dtb, os.path.join(destination, "devicetree.dtb"), dtb_sha)

    receipt = {
        "entry": entry_name(root_uuid, abi),
        "generation": generation,
        "kernel_sha256": kernel_sha,
        "initrd_sha256": initrd_sha,
        "dtb_sha256": dtb_sha,
        "entry_sha256": entry_digest,
    }
    # The entry is published last so any earlier failure retains the previous
    # bootable entry and its artefacts.
    publish_entry(esp, root_uuid, abi, content)
    store_receipt(root, root_uuid, abi, receipt)
    bounded_loader_default(esp, root, root_uuid)
    return 0


def store_receipt(root, root_uuid, abi, receipt):
    path = receipt_path(root, root_uuid, abi)
    ensure_directory_chain(os.path.dirname(path), "receipt directory")
    atomic_text(path, json.dumps(receipt, sort_keys=True, indent=1) + "\n")


# --- removal ---------------------------------------------------------------

def owned_abis(root, root_uuid):
    directory = os.path.join(root, "var", "lib", "lexr", "pop-boot", root_uuid)
    if not os.path.lexists(directory):
        return []
    ensure_directory(directory, "receipt directory")
    return sorted(name[:-5] for name in os.listdir(directory)
                  if name.endswith(".json") and safe_abi(name[:-5]))


def remove_one(root, abi, esp_override):
    require(safe_abi(abi), "unsafe exact ABI")
    image = os.path.join(root, "boot", "vmlinuz-" + abi)
    require(not os.path.lexists(image),
            "cannot remove boot support while the kernel image remains: %s" % abi)
    receipts = os.path.join(root, "var", "lib", "lexr", "pop-boot")
    if not os.path.lexists(receipts):
        return 0
    ensure_directory(receipts, "receipt directory")
    for root_uuid in sorted(os.listdir(receipts)):
        if not ROOT_UUID_RE.match(root_uuid):
            continue
        receipt = load_receipt(root, root_uuid, abi)
        if receipt is None:
            continue
        _, esp_fstype = find_mount(root, "/boot/efi")
        if esp_override is None and esp_fstype != "vfat":
            note("ESP is not mounted; removal deferred")
            return 0
        esp = resolve_esp(root, esp_override)
        entry_file = os.path.join(esp, "loader", "entries", receipt["entry"])
        generation = os.path.join(esp, "EFI", "lexr", root_uuid, abi, receipt["generation"])
        members = (
            ("vmlinuz", "kernel_sha256"),
            ("initrd.img", "initrd_sha256"),
            ("devicetree.dtb", "dtb_sha256"),
        )
        # Preflight: every owned artefact and the entry must exist and match
        # before any deletion, so a missing or tampered member never loses the
        # remaining data (and a missing entry is never unlinked after its
        # kernel artefacts have already been removed).
        for member, field in members:
            path = os.path.join(generation, member)
            require(os.path.lexists(path), "owned artefact %s is missing" % member)
            canonical_regular(path, "owned artefact %s" % member)
            require(sha256_file(path) == receipt[field],
                    "owned artefact %s changed before removal" % member)
        require(os.path.lexists(entry_file), "owned entry is missing")
        canonical_regular(entry_file, "owned entry")
        require(sha256_file(entry_file) == receipt["entry_sha256"],
                "owned entry changed before removal")
        # Repoint the default away from the entry being removed first, so a
        # later failure never leaves the default pointing at a removed entry.
        repair_default(esp, root, root_uuid, removed=abi)
        # Deletion phase: only the named files, never directory sweeps.
        for member, _ in members:
            os.unlink(os.path.join(generation, member))
        os.unlink(entry_file)
        os.unlink(receipt_path(root, root_uuid, abi))
        for directory in (generation,
                          os.path.join(esp, "EFI", "lexr", root_uuid, abi)):
            try:
                os.rmdir(directory)
            except OSError:
                pass
        note("removed owned entry and artefacts for ABI %s" % abi)
    return 0


def repair_default(esp, root, root_uuid, removed):
    """Never leave the default pointing at a removed or unverified entry."""
    _, _, default = loader_default(esp)
    if default != "lexr-%s-%s" % (root_uuid, removed):
        return
    candidates = [abi for abi in verified_owned_abis(esp, root, root_uuid)
                  if abi != removed]
    if candidates:
        candidates.sort(key=functools.cmp_to_key(dpkg_compare_versions))
        write_loader_default(esp, "lexr-%s-%s" % (root_uuid, candidates[-1]))
    elif os.path.lexists(os.path.join(esp, "loader", "entries", STOCK_CURRENT_ENTRY + ".conf")):
        write_loader_default(esp, STOCK_CURRENT_ENTRY)
    else:
        write_loader_default(esp, None)


def do_remove_all(root, esp_override):
    receipts = os.path.join(root, "var", "lib", "lexr", "pop-boot")
    if not os.path.lexists(receipts):
        return 0
    ensure_directory(receipts, "receipt directory")
    for root_uuid in sorted(os.listdir(receipts)):
        if not ROOT_UUID_RE.match(root_uuid):
            continue
        for abi in owned_abis(root, root_uuid):
            remove_one(root, abi, esp_override)
    return 0


def main(argv):
    parser = argparse.ArgumentParser(prog="pop-boot-refresh")
    parser.add_argument("operation", choices=["refresh", "remove", "remove-all"])
    parser.add_argument("--root", default="/")
    parser.add_argument("--abi")
    parser.add_argument("--image")
    parser.add_argument("--platform", default="auto")
    parser.add_argument("--defer-grub", action="store_true")
    parser.add_argument("--esp", default=None)
    arguments = parser.parse_args(argv)
    root = arguments.root.rstrip("/") or "/"
    require(root.startswith("/"), "absolute --root is required")
    require(not os.path.islink(root) and os.path.isdir(root), "root is not a real directory")
    root = os.path.realpath(root)
    try:
        if arguments.operation == "refresh":
            require(arguments.abi and arguments.image, "refresh requires --abi and --image")
            return do_refresh(root, arguments.abi, arguments.image,
                              arguments.platform, arguments.esp)
        if arguments.operation == "remove":
            require(arguments.abi, "remove requires --abi")
            require(not arguments.image and arguments.platform == "auto",
                    "remove received refresh-only arguments")
            return remove_one(root, arguments.abi, arguments.esp)
        require(not arguments.abi and not arguments.image and arguments.platform == "auto",
                "remove-all does not accept ABI, image, or platform")
        return do_remove_all(root, arguments.esp)
    except Fail as failure:
        note(str(failure))
        return 65
    except (OSError, ValueError, TypeError) as failure:
        note(str(failure))
        return 65


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
