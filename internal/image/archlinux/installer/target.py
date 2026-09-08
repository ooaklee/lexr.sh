"""Install the verified Surface payload into an Archinstall-created target.

This module never partitions or formats a device. Archinstall supplies the fresh
mounted root after its normal storage flow. All commands use argument arrays.
"""

import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import tempfile

ABI_PATTERN = re.compile(r"[a-z0-9][a-z0-9.+-]{0,126}\Z")
UUID_PATTERN = re.compile(r"[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}\Z")
PROFILE_TREES = {
    "surface-pro-11-x1e-oled": "x1e80100-microsoft-denali-oled.dtb",
    "surface-pro-11-x1p-lcd": "x1p64100-microsoft-denali.dtb",
}
FIRMWARE = (
    "qcom/gen70500_gmu.bin", "qcom/gen70500_sqe.fw",
    "ath12k/WCN7850/hw2.0/board.bin", "ath12k/WCN7850/hw2.0/board-2.bin",
    "ath12k/WCN7850/hw2.0/amss.bin", "ath12k/WCN7850/hw2.0/m3.bin",
)
ROOT_PLACEHOLDER = "00000000-0000-0000-0000-000000000000"
BOOT_LABEL = "Lexr Arch Linux ARM"
BOOT_LOADER = r"\EFI\LexrArch\grubaa64.efi"


def run(*args, capture=False):
    """Execute a checked command, never a shell string."""
    return subprocess.run([str(a) for a in args], check=True, text=True,
                          stdout=subprocess.PIPE if capture else None).stdout


def digest(path):
    """Hash a regular file without following a final symlink."""
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def confined(root, relative, *, regular=False):
    """Reject traversal and every symlink component in an owned payload path."""
    root = Path(root)
    rel = Path(relative)
    if not rel.parts or rel.is_absolute() or any(p in (".", "..") for p in rel.parts):
        raise ValueError("Invalid payload path")
    path = root / rel
    if root.resolve() != root or path.resolve() != path:
        raise ValueError(f"Symlinked payload path: {relative}")
    if regular and not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError(f"Expected a regular file: {relative}")
    return path


def payload_paths():
    """Enumerate the installer assets that must be bound by the manifest."""
    return {
        "lexr-kernel-sp11.pkg.tar.gz", "installer/installed.conf",
        "installer/lexr_sp11", "installer/runtime.json",
        *(f"installer/firmware/{name}" for name in FIRMWARE),
        *(f"installer/{profile}.cfg" for profile in PROFILE_TREES),
    }


def verify_payload(payload):
    """Verify the complete, fixed asset inventory before any target write."""
    payload = Path(payload)
    manifest = json.loads(confined(payload, "installer/payload.json", regular=True).read_text())
    if set(manifest) != {"schema", "abi", "profiles", "files"} or manifest["schema"] != 1:
        raise ValueError("Unsupported Surface installer payload")
    if not ABI_PATTERN.fullmatch(manifest["abi"]) or manifest["profiles"] != PROFILE_TREES:
        raise ValueError("Invalid Surface kernel identity")
    if set(manifest["files"]) != payload_paths():
        raise ValueError("Incomplete Surface installer payload")
    for name, record in manifest["files"].items():
        path = confined(payload, name, regular=True)
        if set(record) != {"sha256", "size"} or path.stat().st_size != record["size"] or digest(path) != record["sha256"]:
            raise ValueError(f"Surface installer payload changed: {name}")
    runtime = json.loads((payload / "installer/runtime.json").read_text())
    abi = manifest["abi"]
    expected = {f"boot/vmlinuz-{abi}", *(f"usr/lib/firmware/{abi}/device-tree/qcom/{tree}" for tree in PROFILE_TREES.values())}
    if set(runtime) != expected:
        raise ValueError("Incomplete installed kernel identities")
    for record in runtime.values():
        if set(record) != {"sha256", "size"} or not re.fullmatch(r"[0-9a-f]{64}", record["sha256"]) or not 0 < record["size"] < 1 << 30:
            raise ValueError("Invalid installed kernel identity")
    return manifest


def detect_profile():
    """Match the actual live device tree to one supported Surface variant."""
    compatible = Path("/sys/firmware/devicetree/base/compatible").read_bytes().split(b"\0")
    for name, profile in ((b"microsoft,denali-oled", "surface-pro-11-x1e-oled"),
                          (b"microsoft,denali", "surface-pro-11-x1p-lcd")):
        if name in compatible:
            return profile
    raise ValueError("Installation requires a recognised Surface Pro 11 device tree")


def mount_record(path):
    """Read an exact mount, refusing a parent mount masquerading as the target."""
    records = json.loads(run("findmnt", "--json", "--mountpoint", path,
                             "--output", "TARGET,SOURCE,FSTYPE,OPTIONS,UUID", capture=True))["filesystems"]
    if len(records) != 1 or records[0]["target"] != str(path):
        raise ValueError(f"Not a distinct mounted filesystem: {path}")
    return records[0]


def target_mounts(target):
    """Require the filesystems expected by the Surface boot payload."""
    target = Path(target)
    if not target.is_absolute() or target.resolve() != target or target in (Path("/"), Path("/boot"), Path("/home")):
        raise ValueError("Unsafe installation target")
    root = mount_record(target)
    esp = mount_record(target / "boot/efi")
    if root["fstype"] != "ext4" or esp["fstype"] != "vfat" or root["source"] == esp["source"]:
        raise ValueError("Require ext4 root and separate FAT ESP")
    for record in (root, esp):
        if "rw" not in record["options"].split(",") or not re.fullmatch(r"/dev/[A-Za-z0-9_/-]+", record["source"]):
            raise ValueError("Invalid or read-only target device")
    if not UUID_PATTERN.fullmatch(root["uuid"] or ""):
        raise ValueError("Root needs a valid ext4 UUID")
    return root, esp


def copy_asset(source, target, relative):
    """Copy a checked asset to a non-symlinked target location."""
    destination = confined(Path(target), relative)
    destination.parent.mkdir(parents=True, exist_ok=True)
    if destination.exists() and not destination.is_file():
        raise ValueError(f"Invalid target file: {relative}")
    shutil.copyfile(source, destination)
    destination.chmod(0o644)
    if digest(source) != digest(destination):
        raise ValueError(f"Copy verification failed: {relative}")


def check_runtime(target, payload, abi):
    """Check the installed kernel and both DTBs against build-time identities."""
    runtime = json.loads((payload / "installer/runtime.json").read_text())
    for name, record in runtime.items():
        path = confined(target, name, regular=True)
        if path.stat().st_size != record["size"] or digest(path) != record["sha256"]:
            raise ValueError(f"Installed Surface kernel differs: {name}")
    modules = confined(target, "usr/lib/modules")
    if {p.name for p in modules.iterdir() if p.is_dir()} != {abi}:
        raise ValueError("Installed kernel/module ABI mismatch")


def install_kernel(target, payload):
    """Install only the digest-verified native package into the new root."""
    target, payload = Path(target), Path(payload)
    manifest = verify_payload(payload)
    abi = manifest["abi"]
    target_mounts(target)
    if (target / "etc/sudoers.d/lexr-live").exists() or (target / "etc/systemd/system/getty@tty1.service.d/autologin.conf").exists():
        raise ValueError("Refusing an installed target containing the live account policy")
    copy_asset(payload / "installer/installed.conf", target, "etc/lexr/mkinitcpio-installed.conf")
    copy_asset(payload / "installer/lexr_sp11", target, "etc/initcpio/install/lexr_sp11")
    preset = confined(target, "etc/mkinitcpio.d/lexr-sp11.preset")
    preset.parent.mkdir(parents=True, exist_ok=True)
    preset.write_text(f"ALL_config='/etc/lexr/mkinitcpio-installed.conf'\nALL_kver='{abi}'\nPRESETS=('default')\ndefault_image='/boot/initramfs-{abi}.img'\n")
    for name in FIRMWARE:
        copy_asset(payload / "installer/firmware" / name, target, "usr/lib/firmware/" + name)
    # The package is local and unsigned. Its bytes were verified above. This
    # one no-repository transaction never changes the installed pacman policy.
    # arch-chroot overlays /tmp with tmpfs. Keep this private transaction in
    # /var/tmp so its inputs remain visible inside the installation chroot.
    staging = confined(target, "var/tmp")
    staging.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="lexr-kernel-", dir=staging) as temporary:
        temporary = Path(temporary)
        config = temporary / "pacman.conf"
        config.write_text("[options]\nArchitecture = aarch64\nSigLevel = Never\nLocalFileSigLevel = Never\n")
        archive = temporary / "lexr-kernel-sp11.pkg.tar.gz"
        shutil.copyfile(payload / archive.name, archive)
        if digest(archive) != manifest["files"][archive.name]["sha256"]:
            raise ValueError("Native kernel changed during copy")
        run("arch-chroot", target, "pacman", "--config", "/" + str(config.relative_to(target)),
            "-U", "--noconfirm", "/" + str(archive.relative_to(target)))
    check_runtime(target, payload, abi)
    # Keep the companion and original source available after removing the USB.
    retained = confined(target, "usr/share/lexr/arch-media/sp11")
    if retained.exists():
        raise ValueError("Surface recovery payload already exists in target")
    shutil.copytree(payload, retained, symlinks=True)
    verify_payload(retained)
    guide = payload.parent.parent / "LEXR_GETTING_STARTED.txt"
    if guide.is_file():
        copy_asset(guide, target, "usr/share/lexr/LEXR_GETTING_STARTED.txt")
    # Never copy the live user, autologin, passwords or connection secrets.
    run("arch-chroot", target, "depmod", "-a", abi)
    run("arch-chroot", target, "mkinitcpio", "-c", "/etc/lexr/mkinitcpio-installed.conf",
        "-k", abi, "-g", f"/boot/initramfs-{abi}.img")


def efi_snapshot(esp):
    """Record existing EFI files; Lexr's own new directory is excluded."""
    result = {}
    for path in sorted(Path(esp).rglob("*")):
        relative = path.relative_to(esp).as_posix()
        if relative.lower() == "efi/lexrarch" or relative.lower().startswith("efi/lexrarch/"):
            continue
        if path.is_symlink():
            raise ValueError("Unexpected symlink on EFI filesystem")
        if path.is_file():
            result[relative] = digest(path)
    return result


def efi_entries(text):
    """Parse the variables whose preservation matters to other OSes."""
    order = re.search(r"^BootOrder:\s*(.*)$", text, re.M)
    entries = dict(re.findall(r"^Boot([0-9A-Fa-f]{4})(\*?\s+.*)$", text, re.M))
    if not order:
        raise ValueError("Cannot read firmware BootOrder")
    return order.group(1).strip(), entries


def grub_config(payload, profile, root_uuid):
    """Render the same explicit version-bound GRUB entries as the image builder."""
    if profile not in PROFILE_TREES or not UUID_PATTERN.fullmatch(root_uuid):
        raise ValueError("Invalid installed boot identity")
    return (payload / f"installer/{profile}.cfg").read_text().replace(ROOT_PLACEHOLDER, root_uuid)


def require_uefi():
    """Require accessible UEFI variables with Secure Boot disabled."""
    secure = list(Path("/sys/firmware/efi/efivars").glob("SecureBoot-*"))
    if len(secure) != 1 or secure[0].read_bytes()[4:] != b"\0":
        raise ValueError("Surface installation requires UEFI with Secure Boot disabled")


def esp_identity(source):
    """Resolve GPT identity from the device, without relying on cached udev fields."""
    source = Path(source).resolve()
    info = json.loads(run("lsblk", "--json", "--paths", "--nodeps", "--output",
                          "PATH,TYPE,PKNAME", source, capture=True))["blockdevices"]
    if len(info) != 1 or info[0]["type"] != "part" or not info[0]["pkname"]:
        raise ValueError("Cannot identify the existing ESP partition")
    disk = info[0]["pkname"]
    if not disk.startswith("/dev/"):
        disk = "/dev/" + disk
    if not re.fullmatch(r"/dev/[A-Za-z0-9_-]+", disk):
        raise ValueError("Invalid ESP parent device")
    number = (Path("/sys/class/block") / source.name / "partition").read_text().strip()
    if not number.isdigit() or not 0 < int(number) <= 4096:
        raise ValueError("Invalid ESP partition number")
    uuid = run("blkid", "-s", "PARTUUID", "-o", "value", source, capture=True).strip().lower()
    table = json.loads(run("sfdisk", "--json", disk, capture=True))["partitiontable"]
    parts = [p for p in table.get("partitions", []) if p["node"] == str(source)]
    if (table.get("label") != "gpt" or len(parts) != 1 or not UUID_PATTERN.fullmatch(uuid)
            or parts[0].get("uuid", "").lower() != uuid
            or parts[0].get("type", "").lower() != "c12a7328-f81f-11d2-ba4b-00a0c93ec93b"):
        raise ValueError("ESP partition identity does not match the GPT")
    return disk, number, uuid


def install_boot(target, payload, profile):
    """Deploy ARM64 GRUB into its own EFI directory and preserve BootOrder."""
    target, payload = Path(target), Path(payload)
    manifest = verify_payload(payload)
    abi = manifest["abi"]
    root, esp_record = target_mounts(target)
    esp = target / "boot/efi"
    if (esp / "EFI").exists() and any(p.name.lower() == "lexrarch" for p in (esp / "EFI").iterdir()):
        raise ValueError("EFI/LexrArch already exists; refusing to replace an installation")
    if shutil.disk_usage(esp).free < 32 << 20:
        raise ValueError("The shared ESP needs at least 32 MiB free")
    require_uefi()
    disk, number, partuuid = esp_identity(esp_record["source"])
    before_files = efi_snapshot(esp)
    before_order, before_entries = efi_entries(run("efibootmgr", "--verbose", capture=True))
    if any(BOOT_LABEL in entry or BOOT_LOADER.lower() in entry.lower() for entry in before_entries.values()):
        raise ValueError("A Lexr Arch firmware entry already exists")
    for tree in PROFILE_TREES.values():
        copy_asset(target / f"usr/lib/firmware/{abi}/device-tree/qcom/{tree}", target, f"boot/dtb-{abi}/{tree}")
    grub = confined(target, "boot/grub")
    grub.mkdir(parents=True, exist_ok=True)
    (grub / "grub.cfg").write_text(grub_config(payload, profile, root["uuid"]))
    run("arch-chroot", target, "grub-script-check", "/boot/grub/grub.cfg")
    run("arch-chroot", target, "grub-install", "--target=arm64-efi", "--efi-directory=/boot/efi",
        "--boot-directory=/boot", "--bootloader-id=LexrArch", "--no-nvram",
        "--modules=part_gpt ext2 search search_fs_uuid fdt linux normal configfile font gfxterm all_video efifwsetup")
    binary = confined(target, "boot/efi/EFI/LexrArch/grubaa64.efi", regular=True)
    header = binary.read_bytes()
    pe = int.from_bytes(header[60:64], "little")
    if header[:2] != b"MZ" or header[pe:pe+6] != b"PE\0\0\x64\xaa":
        raise ValueError("Installed GRUB is not an ARM64 EFI executable")
    if before_files != efi_snapshot(esp):
        raise ValueError("Existing EFI files changed during GRUB installation")
    run("efibootmgr", "--create-only", "--disk", disk, "--part", number,
        "--label", BOOT_LABEL, "--loader", BOOT_LOADER)
    after_order, after_entries = efi_entries(run("efibootmgr", "--verbose", capture=True))
    added = set(after_entries) - set(before_entries)
    if before_order != after_order or any(after_entries.get(k) != v for k, v in before_entries.items()) or len(added) != 1:
        raise ValueError("Firmware verification failed; preserve the USB and inspect efibootmgr")
    boot_number = added.pop()
    entry = after_entries[boot_number]
    if BOOT_LABEL not in entry or BOOT_LOADER.lower() not in entry.lower().replace("/", "\\") or partuuid not in entry.lower():
        raise ValueError("Firmware entry does not identify the new GRUB loader")
    receipt = {"schema": 1, "abi": abi, "profile": profile, "root_uuid": root["uuid"],
               "esp_partuuid": partuuid, "boot_number": boot_number,
               "boot_order": before_order, "efi_files": before_files, "grub_sha256": digest(binary)}
    (target / "etc/lexr/install-receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
    print(f"GRUB installed. Other EFI files and BootOrder are unchanged. Select '{BOOT_LABEL}' in firmware.")
    print(f"For a one-time test after leaving the installer: sudo efibootmgr --bootnext {boot_number}")


def verify_install(target, payload, profile):
    """Reject an incomplete installed boot chain before Archinstall reports success."""
    target, payload = Path(target), Path(payload)
    manifest = verify_payload(payload)
    abi = manifest["abi"]
    root, esp = target_mounts(target)
    receipt = json.loads(confined(target, "etc/lexr/install-receipt.json", regular=True).read_text())
    if (receipt["abi"], receipt["profile"], receipt["root_uuid"]) != (abi, profile, root["uuid"]):
        raise ValueError("Installed boot receipt does not match the selected target")
    check_runtime(target, payload, abi)
    if digest(target / "boot/efi/EFI/LexrArch/grubaa64.efi") != receipt["grub_sha256"]:
        raise ValueError("Installed EFI loader changed")
    if (target / "boot/grub/grub.cfg").read_text() != grub_config(payload, profile, root["uuid"]):
        raise ValueError("Installed GRUB menu changed")
    for tree in PROFILE_TREES.values():
        if digest(target / f"boot/dtb-{abi}/{tree}") != digest(target / f"usr/lib/firmware/{abi}/device-tree/qcom/{tree}"):
            raise ValueError("Installed DTB differs from the kernel package")
    entries = run("arch-chroot", target, "lsinitcpio", "-l", f"/boot/initramfs-{abi}.img", capture=True).splitlines()
    if any("hooks/archiso" in p for p in entries) or not any(p.endswith("usr/bin/busybox") for p in entries):
        raise ValueError("Installed initramfs is missing or contains live-media discovery")
    for name in FIRMWARE:
        if not any(p.endswith("usr/lib/firmware/" + name) for p in entries):
            raise ValueError(f"Installed initramfs is missing firmware: {name}")
    fstab = (target / "etc/fstab").read_text()
    active = [line.split() for line in fstab.splitlines() if line.strip() and not line.lstrip().startswith("#")]
    if not any(len(row) > 2 and row[0] == "UUID=" + root["uuid"] and row[1:3] == ["/", "ext4"] for row in active):
        raise ValueError("Installed fstab does not identify the root filesystem")
    if not esp["uuid"] or not any(len(row) > 2 and row[0].lower() == ("UUID=" + esp["uuid"]).lower() and row[1:3] == ["/boot/efi", "vfat"] for row in active):
        raise ValueError("Installed fstab is missing the shared ESP")
    if (target / "etc/sudoers.d/lexr-live").exists() or (target / "etc/systemd/system/getty@tty1.service.d/autologin.conf").exists():
        raise ValueError("Live login policy leaked into installed system")
    if efi_snapshot(target / "boot/efi") != receipt["efi_files"]:
        raise ValueError("Other EFI files changed after bootloader installation")
    order, entries = efi_entries(run("efibootmgr", "--verbose", capture=True))
    if order != receipt["boot_order"] or receipt["boot_number"] not in entries:
        raise ValueError("Firmware boot entry or BootOrder changed")
    print("Verified installed Surface kernel, DTBs, initramfs, GRUB and fstab. Hardware reboot remains to be tested.")
