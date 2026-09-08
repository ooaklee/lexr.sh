"""Pure validation for the first supported, non-destructive Arch install layout."""

import re

ESP_GUID = "c12a7328-f81f-11d2-ba4b-00a0c93ec93b"
DEVICE = re.compile(r"/dev/(?:nvme[0-9]+n[0-9]+(?:p[0-9]+)?|sd[a-z]+[0-9]*|mmcblk[0-9]+(?:p[0-9]+)?)\Z")


def validate_packages(packages):
    """Refuse packages that replace the Surface kernel or select x86 drivers."""
    for package in packages:
        if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9@+_.-]*", package):
            raise ValueError("Select package names from the ARM repositories")
        if (package in {"linux", "linux-lts", "linux-zen", "linux-hardened", "linux-aarch64", "lexr-kernel-sp11"}
                or (package.startswith("linux-") and not package.startswith("linux-firmware")
                    and package not in {"linux-api-headers", "linux-tools", "linux-docs", "linux-atm"})
                or package.startswith(("nvidia", "lib32-"))):
            raise ValueError(f"{package} cannot replace the bundled Surface kernel or ARM graphics stack")


def validate_plan(plan, table, devices):
    """Validate a UI plan against fresh GPT and lsblk data without disk writes.

    Only CREATE for one new root and EXIST for every original partition are
    admitted. Existing OS partitions must never be edited, deleted or mounted.
    """
    if plan["layout"] != "manual_partitioning" or plan["wipe"]:
        raise ValueError("Choose Manual Partitioning; whole-disk layouts are not supported")
    if plan["encrypted"] or plan["lvm"]:
        raise ValueError("This installer currently supports unencrypted ext4 without LVM")
    disk = plan["device"]
    if not DEVICE.fullmatch(disk) or table.get("device") != disk or table.get("label") != "gpt":
        raise ValueError("Choose an existing GPT disk identified by its real device path")
    if not table.get("id") or not devices or devices[0].get("path") != disk or devices[0].get("type") != "disk":
        raise ValueError("Cannot verify target disk identity")
    rows = {}

    def collect(row):
        """Reject active/stacked devices before Archinstall can unmount anything."""
        if row.get("mountpoints") and any(row["mountpoints"]):
            raise ValueError("Unmount the target disk's filesystems first; do not select the live USB")
        if row.get("ro") or row.get("type") not in {"disk", "part"}:
            raise ValueError("Read-only, encrypted and stacked block devices are not supported")
        rows[row["path"]] = row
        for child in row.get("children", []):
            collect(child)

    collect(devices[0])
    existing = {p["node"]: p for p in table.get("partitions", [])}
    if not existing or len(existing) != len(table["partitions"]):
        raise ValueError("Cannot verify the existing partition table")
    sector = table.get("sectorsize")
    if sector not in {512, 4096} or table.get("unit") != "sectors" or not table.get("firstlba") or not table.get("lastlba"):
        raise ValueError("Cannot verify GPT geometry")
    lower, upper = table["firstlba"] * sector, (table["lastlba"] + 1) * sector
    seen = set()
    roots, esps = [], []
    for part in plan["partitions"]:
        status, mount = part["status"], part["mount"]
        if part["options"] or part["subvolumes"]:
            raise ValueError("Use default mount options without subvolumes")
        if status not in {"existing", "create"}:
            raise ValueError("Do not format, resize or delete an existing partition")
        start, size = part["start"], part["size"]
        if not isinstance(start, int) or not isinstance(size, int) or size <= 0 or start < lower or start + size > upper:
            raise ValueError("Partition is outside the disk's usable GPT space")
        if start % sector or size % sector:
            raise ValueError("Partition boundaries are not sector aligned")
        if status == "create":
            if mount != "/" or part["fs"] != "ext4" or part["path"] or part["efi"] or part["flags"]:
                raise ValueError("Create only a new ext4 root mounted at /, with no boot flags")
            if size < 16 << 30:
                raise ValueError("Allocate at least 16 GiB of free space for the new root")
            for old in existing.values():
                old_start, old_end = old["start"] * sector, (old["start"] + old["size"]) * sector
                if start < old_end and old_start < start + size:
                    raise ValueError("New root overlaps an existing partition; prepare unallocated space first")
            roots.append(part)
            continue
        path = part["path"]
        if path not in existing or path in seen or not DEVICE.fullmatch(path):
            raise ValueError("Missing or repeated existing partition identity")
        old = existing[path]
        if (start, size) != (old["start"] * sector, old["size"] * sector):
            raise ValueError("Existing partition geometry changed; reload the disk layout")
        if part["partuuid"] and part["partuuid"].lower() != old.get("uuid", "").lower():
            raise ValueError("Existing partition identity changed; reload the disk layout")
        if path not in rows:
            raise ValueError("Partition disappeared during validation")
        seen.add(path)
        if mount == "/boot/efi":
            if not part["efi"] or part["fs"] not in {"fat32", "vfat"} or old.get("type", "").lower() != ESP_GUID or rows[path].get("fstype") != "vfat":
                raise ValueError("Reuse the existing FAT EFI System Partition at /boot/efi without formatting")
            esps.append(part)
        elif mount is not None:
            raise ValueError("Leave every other existing partition unmounted")
    if set(existing) != seen:
        raise ValueError("Keep every existing partition in the plan without modifications")
    if len(roots) != 1 or len(esps) != 1:
        raise ValueError("Select one new ext4 root at / and one existing ESP at /boot/efi")
    return roots[0], esps[0]


def preserved_partitions(before, after):
    """Verify that creating the root did not change any existing GPT record."""
    for key in ("label", "id", "device", "unit", "firstlba", "lastlba", "sectorsize"):
        if before.get(key) != after.get(key):
            raise ValueError("The target disk identity or GPT geometry changed")
    records = {p["node"]: p for p in after["partitions"]}
    if len(records) != len(before["partitions"]) + 1:
        raise ValueError("Unexpected partition count after root creation")
    for part in before["partitions"]:
        if records.get(part["node"]) != part:
            raise ValueError("An existing partition changed during installation")
