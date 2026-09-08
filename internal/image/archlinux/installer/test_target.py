"""Check payload trust and EFI preservation without touching real devices."""

import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import target


def payload_fixture(base):
    """Create small, digest-bound assets for exercising pre-write validation."""
    payload = base / "sp11"
    abi = "7.2.0-jg-0sp11v23-qcom-x1e"
    runtime = {f"boot/vmlinuz-{abi}": {"sha256": "a" * 64, "size": 10},
               **{f"usr/lib/firmware/{abi}/device-tree/qcom/{tree}": {"sha256": "b" * 64, "size": 10}
                  for tree in target.PROFILE_TREES.values()}}
    for name in target.payload_paths():
        path = payload / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(runtime) if name == "installer/runtime.json" else "fixture\n")
    manifest = {"schema": 1, "abi": abi, "profiles": target.PROFILE_TREES,
                "files": {name: {"sha256": target.digest(payload / name), "size": (payload / name).stat().st_size}
                          for name in target.payload_paths()}}
    (payload / "installer/payload.json").write_text(json.dumps(manifest))
    return payload, manifest


class PayloadTests(unittest.TestCase):
    """Invalid offline inputs must be rejected before any target command runs."""

    def test_changed_kernel_is_rejected_before_commands(self):
        """Never install an archive which differs from the image's receipt."""
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory).resolve()
            payload, _ = payload_fixture(base)
            target.verify_payload(payload)
            (payload / "lexr-kernel-sp11.pkg.tar.gz").write_text("changed")
            with patch.object(target, "run") as run, self.assertRaises(ValueError):
                target.install_kernel(base / "target", payload)
            run.assert_not_called()

    def test_traversal_missing_assets_and_symlinks(self):
        """Neither manifest paths nor symlinked directories can escape the payload."""
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory).resolve()
            payload, manifest = payload_fixture(base)
            path = payload / "installer/payload.json"
            for malicious in ("../outside", "/etc/passwd"):
                bad = copy.deepcopy(manifest)
                bad["files"][malicious] = bad["files"].pop("installer/installed.conf")
                path.write_text(json.dumps(bad))
                with self.assertRaises(ValueError):
                    target.verify_payload(payload)
            path.write_text(json.dumps(manifest))
            original = payload / "installer/firmware"
            original.rename(base / "firmware")
            original.symlink_to(base / "firmware", target_is_directory=True)
            with self.assertRaises(ValueError):
                target.verify_payload(payload)

    def test_kernel_transaction_survives_arch_chroot_tmpfs(self):
        """Stage outside /tmp and remove the unsigned transaction config afterwards."""
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory).resolve()
            payload, _ = payload_fixture(base)
            root = base / "root"
            (root / "etc").mkdir(parents=True)
            persistent = root / "etc/pacman.conf"
            persistent.write_text("[options]\nSigLevel = Required\n")
            commands = []

            def native_command(*args, **kwargs):
                """Model arch-chroot's hidden /tmp without executing target code."""
                commands.append(args)
                if "--config" in args:
                    name = args[args.index("--config") + 1]
                    self.assertTrue(name.startswith("/var/tmp/lexr-kernel-"))
                    self.assertTrue((root / name.lstrip("/")).is_file())
                    self.assertTrue((root / str(args[-1]).lstrip("/")).is_file())

            with patch.object(target, "target_mounts"), patch.object(target, "check_runtime"), patch.object(target, "run", side_effect=native_command):
                target.install_kernel(root, payload)
            self.assertTrue(any("--config" in args for args in commands))
            self.assertEqual(list((root / "var/tmp").iterdir()), [])
            self.assertEqual(persistent.read_text(), "[options]\nSigLevel = Required\n")
            self.assertFalse((root / "etc/mkinitcpio.conf").exists())
            self.assertIn("/etc/lexr/mkinitcpio-installed.conf", (root / "etc/mkinitcpio.d/lexr-sp11.preset").read_text())

    def test_exact_mounts_and_readonly_refusal(self):
        """Do not mistake a directory on the live root for the new mounted root."""
        with patch.object(target, "run", return_value=json.dumps({"filesystems": [{"target": "/"}]})):
            with self.assertRaises(ValueError):
                target.mount_record(Path("/mnt"))
        good = {"source": "/dev/nvme0n1p7", "fstype": "ext4", "options": "rw,relatime",
                "uuid": "12345678-1234-1234-1234-123456789abc"}
        esp = {"source": "/dev/nvme0n1p1", "fstype": "vfat", "options": "ro,relatime"}
        with patch.object(target, "mount_record", side_effect=[good, esp]), self.assertRaises(ValueError):
            target.target_mounts(Path("/mnt"))

    def test_efi_snapshot_excludes_only_lexr_directory(self):
        """Windows, Ubuntu and the removable fallback remain protected."""
        with tempfile.TemporaryDirectory() as directory:
            esp = Path(directory).resolve()
            for name in ("EFI/Microsoft/Boot/bootmgfw.efi", "EFI/ubuntu/grubaa64.efi", "EFI/BOOT/BOOTAA64.EFI", "EFI/LexrArch/grubaa64.efi"):
                path = esp / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(name.encode())
            before = target.efi_snapshot(esp)
            self.assertEqual(len(before), 3)
            (esp / "EFI/LexrArch/grubaa64.efi").write_text("new own loader")
            self.assertEqual(before, target.efi_snapshot(esp))
            (esp / "EFI/BOOT/BOOTAA64.EFI").write_text("bad replacement")
            self.assertNotEqual(before, target.efi_snapshot(esp))

    def test_efi_output_preserves_all_existing_entries(self):
        """Parse active/inactive boot records without losing device paths."""
        text = "BootCurrent: 0003\nTimeout: 0 seconds\nBootOrder: 0001,0003\nBoot0001* Windows Boot Manager\tHD(1,GPT,abc)/File(\\EFI\\Microsoft\\Boot\\bootmgfw.efi)\nBoot0003  ubuntu\tHD(1,GPT,abc)/File(\\EFI\\ubuntu\\grubaa64.efi)\n"
        order, entries = target.efi_entries(text)
        self.assertEqual(order, "0001,0003")
        self.assertEqual(set(entries), {"0001", "0003"})
        self.assertIn("Microsoft", entries["0001"])
        with self.assertRaises(ValueError):
            target.efi_entries("EFI variables are not supported on this system")


if __name__ == "__main__":
    unittest.main()
