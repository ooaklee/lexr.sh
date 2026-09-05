#!/usr/bin/env python3
"""Add the exact live-media DTB and Lexr companion to Pop's recovery copy.

Distinst owns its Recovery entry and copies its kernel/initramfs before the
final installed-system initramfs update. This helper verifies those copies
against the original media contract and adds a separate Lexr recovery entry.
It never changes another entry, mounts a disk, or restarts hardware.
"""

import argparse
import hashlib
import importlib.machinery
import importlib.util
import json
import os
import re
import sys


def load_boot_helper():
    """Use the sibling installed helper's bounded filesystem operations."""
    path = os.path.join(os.path.dirname(os.path.realpath(__file__)), "pop-boot-refresh")
    loader = importlib.machinery.SourceFileLoader("lexr_pop_boot", path)
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


boot = load_boot_helper()
MEDIA_ROOT = "usr/share/lexr/pop-media"
SERIAL = re.compile(r"^[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}$")
PARTUUID = re.compile(r"^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$")


def strict_object(pairs):
    result = {}
    for key, value in pairs:
        boot.require(key not in result, "duplicate recovery metadata field")
        result[key] = value
    return result


def read_text(path, maximum=1 << 20):
    boot.canonical_regular(path, "recovery metadata")
    with open(path, "rb") as stream:
        data = stream.read(maximum + 1)
    boot.require(len(data) <= maximum, "recovery metadata exceeds its limit")
    return data.decode("utf-8", "strict")


def read_fields(text):
    """Read BLS or recovery.conf as data; never source installer content."""
    fields = {}
    for line in text.splitlines():
        if not line.strip() or line.startswith("#"):
            continue
        parts = line.split(None, 1)
        boot.require(len(parts) == 2 and parts[0] not in fields,
                     "ambiguous recovery loader entry")
        fields[parts[0]] = parts[1]
    return fields


def verify_file(path, record):
    boot.canonical_regular(path, "recovery artefact")
    boot.require(isinstance(record, dict) and boot.HEX64_RE.fullmatch(record.get("sha256", "")),
                 "invalid recovery artefact digest")
    boot.require(type(record.get("size_bytes")) is int and os.path.getsize(path) == record["size_bytes"],
                 "recovery artefact size differs from the original medium")
    boot.require(boot.sha256_file(path) == record["sha256"],
                 "recovery artefact differs from the original medium")


def companion_records(record):
    if not record["included"]:
        return []
    result = [record["executable"]["artifact"], record["source_archive"]]
    result += record["catalogues"] + record["licences"]
    for userspace in record["userspace"]:
        result += userspace["artifacts"]
    boot.require(0 < len(result) <= 4096, "invalid recovery companion inventory size")
    seen = set()
    for item in result:
        path = item["path"]
        boot.require(path.startswith("sp11/companion/") and "\\" not in path and
                     all(component not in ("", ".", "..") for component in path.split("/")) and
                     path not in seen, "unsafe or repeated recovery companion path")
        seen.add(path)
    return result


def copy_companion(media, recovery, records):
    """Preflight every source and destination before adding verified files."""
    for record in records:
        source, target = (os.path.join(base, record["path"]) for base in (media, recovery))
        verify_file(source, record)
        if os.path.lexists(target):
            verify_file(target, record)
    for record in records:
        source, target = (os.path.join(base, record["path"]) for base in (media, recovery))
        boot.ensure_directory_chain(os.path.dirname(target), "recovery companion directory")
        boot.copy_immutable(source, target, record["sha256"])


def refresh(root, abi):
    boot.require(boot.safe_abi(abi), "invalid installed kernel ABI")
    if boot.root_is_live(root):
        return 0
    _, filesystem = boot.find_mount(root, "/recovery")
    if filesystem is None:
        # Custom partition layouts may deliberately omit Pop recovery.
        return 0
    boot.require(filesystem == "vfat", "Pop recovery is not the declared FAT filesystem")
    profile, _ = boot.select_platform_state(root, abi, "auto")
    if profile is None:
        return 0  # A stock-kernel update has no SP11 identity to reconcile.
    root_uuid, _ = boot.parse_fstab(root)
    esp = boot.resolve_esp(root, None)
    boot.require(boot.esp_mounted(root, esp), "recovery ESP is unmounted")
    media = os.path.join(root, MEDIA_ROOT)
    recovery = os.path.join(root, "recovery")
    boot.ensure_directory(media, "original recovery payload")
    boot.ensure_directory(recovery, "recovery mount")
    manifest = json.loads(read_text(os.path.join(media, "lexr-manifest.json")), object_pairs_hook=strict_object)
    boot.require(manifest["adapter"] == "pop-casper" and manifest["schema_version"] == 5,
                 "unsupported recovery media contract")
    trees = [tree for tree in manifest["kernel_bundle"]["device_trees"] if tree["device"] == profile]
    boot.require(len(trees) == 1, "original recovery medium has no unique DTB for this device")
    tree = trees[0]
    basename = tree["basename"]
    boot.require(basename == os.path.basename(basename) and basename.endswith(".dtb"),
                 "unsafe recovery DTB basename")
    dtb = os.path.join(media, "sp11/dtb", basename)
    dtb_records = [record for record in manifest["boot_artifacts"]["device_trees"] if record["path"] == "sp11/dtb/" + basename]
    boot.require(len(dtb_records) == 1 and dtb_records[0]["sha256"] == tree["sha256"],
                 "recovery DTB differs from its original kernel bundle")
    verify_file(dtb, dtb_records[0])
    config = {}
    for line in read_text(os.path.join(recovery, "recovery.conf"), 1 << 16).splitlines():
        key, separator, value = line.partition("=")
        boot.require(separator and key not in config, "ambiguous recovery configuration")
        config[key] = value
    boot.require(config.get("ROOT_UUID") == root_uuid, "recovery belongs to a different installed root")
    partuuid = config.get("RECOVERY_UUID", "").removeprefix("PARTUUID=")
    boot.require(PARTUUID.fullmatch(partuuid), "invalid recovery partition UUID")
    # Check the actual recovery block device against the installer's identity.
    source, _ = boot.find_mount(root, "/recovery")
    expected_device = os.path.join(root, "dev/disk/by-partuuid", partuuid)
    device = boot.device_identity(expected_device)
    boot.require(device is not None and device == boot.device_identity(source),
                 "mounted recovery device does not match recovery.conf")
    entries = os.path.join(esp, "loader/entries")
    boot.ensure_directory(entries, "loader entries")
    matches = []
    for name in os.listdir(entries):
        if name.startswith("Recovery-") and name.endswith(".conf") and SERIAL.fullmatch(name[9:-5]):
            values = read_fields(read_text(os.path.join(entries, name), 1 << 16))
            options = values.get("options", "").split()
            if "live-media=/dev/disk/by-partuuid/" + partuuid in options:
                matches.append((name[9:-5], values, options))
    boot.require(len(matches) == 1, "no unique native recovery entry for the installed recovery device")
    serial, values, options = matches[0]
    live_path = "casper-" + serial
    expected_options = {"boot": "casper", "live-media-path": "/" + live_path,
                        "live-media": "/dev/disk/by-partuuid/" + partuuid}
    for key, value in expected_options.items():
        boot.require([word for word in options if word.startswith(key + "=")] == [key + "=" + value],
                     "native recovery selectors disagree")
    boot.require(not any(word.startswith("root=") or word.startswith("init=") or word.startswith("rdinit=") for word in options),
                 "native recovery entry overrides the live root")
    boot.require(values.get("linux") == "/EFI/Recovery-" + serial + "/vmlinuz.efi" and
                 values.get("initrd") == "/EFI/Recovery-" + serial + "/initrd.gz" and
                 set(values) == {"title", "linux", "initrd", "options"}, "native recovery boot paths changed")
    for role, name in (("kernel", "vmlinuz.efi"), ("initrd", "initrd.gz")):
        record = manifest["boot_artifacts"][role]
        verify_file(os.path.join(recovery, live_path, name), record)
        verify_file(os.path.join(esp, "EFI/Recovery-" + serial, name), record)
    identities = [item["value"] for item in manifest["media_discovery"]["evidence"] if item["role"] == "medium-identity"]
    boot.require(len(identities) == 1 and read_text(os.path.join(recovery, ".disk/casper-uuid-generic"), 64).strip() == identities[0],
                 "recovery medium does not match its Casper initramfs")
    # The installed kernel can advance independently. The recovery entry
    # continues to use the original live kernel's DTB, never the updated DTB.
    dtb_relative = "EFI/lexr-recovery/" + root_uuid + "/" + tree["sha256"] + "/devicetree.dtb"
    target_dtb = os.path.join(esp, dtb_relative)
    for argument in boot.SURFACE_KERNEL_ARGUMENTS.split():
        if argument not in options:
            options.append(argument)
    content = ("title Lexr Pop!_OS recovery\nlinux " + values["linux"] + "\ninitrd " + values["initrd"] +
               "\ndevicetree /" + dtb_relative + "\noptions " + " ".join(options) + "\n")
    name = "lexr-recovery-" + root_uuid + "-" + serial + ".conf"
    entry = os.path.join(entries, name)
    receipt = os.path.join(root, "var/lib/lexr/pop-recovery", name + ".sha256")
    entry_digest = hashlib.sha256(content.encode()).hexdigest()
    if os.path.lexists(entry):
        boot.canonical_regular(entry, "owned recovery entry")
        boot.require(read_text(receipt, 65).strip() == boot.sha256_file(entry), "recovery entry is unowned or modified")
        boot.require(boot.sha256_file(entry) == entry_digest, "existing recovery entry differs; explicit review is required")
    elif os.path.lexists(receipt):
        boot.require(read_text(receipt, 65).strip() == entry_digest, "recovery receipt differs from the prepared entry")
    if os.path.lexists(target_dtb):
        verify_file(target_dtb, dtb_records[0])
    copy_companion(media, recovery, companion_records(manifest["companion_bundle"]))
    boot.ensure_directory_chain(os.path.dirname(target_dtb), "recovery DTB directory")
    boot.copy_immutable(dtb, target_dtb, tree["sha256"])
    boot.ensure_directory_chain(os.path.dirname(receipt), "recovery receipt directory")
    boot.atomic_text(receipt, entry_digest + "\n")
    boot.atomic_text(entry, content)
    boot.note("verified Lexr recovery entry and companion are ready")
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default="/")
    parser.add_argument("--abi", required=True)
    arguments = parser.parse_args()
    try:
        boot.require(os.path.isabs(arguments.root) and not os.path.islink(arguments.root), "invalid recovery target root")
        return refresh(os.path.realpath(arguments.root), arguments.abi)
    except (boot.Fail, OSError, ValueError, KeyError, TypeError) as error:
        boot.note("recovery: " + str(error))
        return 65


if __name__ == "__main__":
    sys.exit(main())
