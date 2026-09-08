"""Regression tests for preservation of other operating systems during install."""

import copy
import unittest

import policy


def fixture():
    """Describe a shared ESP, occupied OS partition and 20 GiB of free space."""
    table = {"label": "gpt", "id": "disk-uuid", "device": "/dev/nvme0n1", "unit": "sectors",
             "firstlba": 34, "lastlba": 100000000, "sectorsize": 512,
             "partitions": [
                 {"node": "/dev/nvme0n1p1", "start": 2048, "size": 524288, "type": policy.ESP_GUID, "uuid": "esp-uuid"},
                 {"node": "/dev/nvme0n1p2", "start": 526336, "size": 8388608, "type": "windows-guid", "uuid": "os-uuid"}]}
    devices = [{"path": "/dev/nvme0n1", "type": "disk", "ro": False, "mountpoints": [None], "children": [
        {"path": "/dev/nvme0n1p1", "type": "part", "ro": False, "fstype": "vfat", "mountpoints": [None]},
        {"path": "/dev/nvme0n1p2", "type": "part", "ro": False, "fstype": "ntfs", "mountpoints": [None]}]}]
    parts = []
    for old in table["partitions"]:
        esp = old["node"].endswith("p1")
        parts.append({"status": "existing", "start": old["start"] * 512, "size": old["size"] * 512,
                      "path": old["node"], "partuuid": old["uuid"], "fs": "fat32" if esp else "ntfs",
                      "mount": "/boot/efi" if esp else None, "efi": esp,
                      "flags": ["ESP"] if esp else [], "options": [], "subvolumes": False})
    parts.append({"status": "create", "start": 10 << 30, "size": 20 << 30, "path": None,
                  "partuuid": None, "fs": "ext4", "mount": "/", "efi": False,
                  "flags": [], "options": [], "subvolumes": False})
    return {"layout": "manual_partitioning", "wipe": False, "device": "/dev/nvme0n1",
            "encrypted": False, "lvm": False, "partitions": parts}, table, devices


class StoragePolicyTests(unittest.TestCase):
    """Unsafe plans must fail while all operations remain read-only."""

    def test_new_root_preserves_shared_esp_and_other_os(self):
        """The intended manual multi-boot plan is accepted without mutation."""
        args = fixture()
        before = copy.deepcopy(args)
        root, esp = policy.validate_plan(*args)
        self.assertEqual(root["mount"], "/")
        self.assertEqual(esp["path"], "/dev/nvme0n1p1")
        self.assertEqual(args, before)

    def test_unsafe_plan_variants(self):
        """Reject destructive defaults, foreign edits, overlap and unsupported layouts."""
        mutations = {
            "wipe": lambda p,t,d: p.update(wipe=True),
            "automatic": lambda p,t,d: p.update(layout="default_layout"),
            "encryption": lambda p,t,d: p.update(encrypted=True),
            "lvm": lambda p,t,d: p.update(lvm=True),
            "format ESP": lambda p,t,d: p["partitions"][0].update(status="modify"),
            "delete OS": lambda p,t,d: p["partitions"][1].update(status="delete"),
            "mount OS": lambda p,t,d: p["partitions"][1].update(mount="/home"),
            "overlap": lambda p,t,d: p["partitions"][2].update(start=1 << 30),
            "omitted OS": lambda p,t,d: p["partitions"].pop(1),
            "stale UUID": lambda p,t,d: p["partitions"][0].update(partuuid="replacement"),
            "stale geometry": lambda p,t,d: p["partitions"][1].update(start=0),
            "too small": lambda p,t,d: p["partitions"][2].update(size=8 << 30),
            "non ext4": lambda p,t,d: p["partitions"][2].update(fs="btrfs"),
            "outside disk": lambda p,t,d: p["partitions"][2].update(size=100 << 30),
            "active live disk": lambda p,t,d: d[0]["children"][1].update(mountpoints=["/run/archiso/bootmnt"]),
            "active swap": lambda p,t,d: d[0]["children"][1].update(mountpoints=["[SWAP]"]),
            "non GPT": lambda p,t,d: t.update(label="dos"),
            "missing geometry": lambda p,t,d: t.pop("firstlba"),
            "readonly": lambda p,t,d: d[0].update(ro=True),
            "wrong ESP GUID": lambda p,t,d: t["partitions"][0].update(type="other"),
            "path injection": lambda p,t,d: p.update(device="/dev/sda; reboot"),
        }
        for name, mutation in mutations.items():
            with self.subTest(name=name):
                args = fixture()
                mutation(*args)
                with self.assertRaises(ValueError):
                    policy.validate_plan(*args)

    def test_post_creation_checks_preserve_foreign_records(self):
        """Even a changed partition label or GUID must be detected after creation."""
        _, before, _ = fixture()
        after = copy.deepcopy(before)
        after["partitions"].append({"node": "/dev/nvme0n1p3", "start": 20971520, "size": 41943040})
        policy.preserved_partitions(before, after)
        after["partitions"][1]["uuid"] = "changed"
        with self.assertRaises(ValueError):
            policy.preserved_partitions(before, after)

    def test_arm_packages_without_replacement_kernel(self):
        """Normal terminal or optional desktop packages remain available."""
        policy.validate_packages(["base", "archlinuxarm-keyring", "networkmanager", "mesa", "plasma-desktop"])
        for package in ("linux", "linux-aarch64", "lexr-kernel-sp11", "nvidia-open", "lib32-mesa", "--overwrite", "base;reboot"):
            with self.subTest(package=package), self.assertRaises(ValueError):
                policy.validate_packages([package])


if __name__ == "__main__":
    unittest.main()
