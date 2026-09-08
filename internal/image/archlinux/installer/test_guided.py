"""Exercise Surface callbacks against the separately packaged Archinstall API."""

import copy
import importlib.util
from pathlib import Path
from types import SimpleNamespace
import tempfile
import unittest
from unittest.mock import patch

import guided as adapter


class ArgumentTests(unittest.TestCase):
    def test_guided_entry_point_is_retained(self):
        for arguments in (["--plugin", "replacement.py"], ["--plugin-url=https://example.invalid"],
                          ["--script", "other"], ["--skip-boot"]):
            with self.subTest(arguments=arguments), self.assertRaises(ValueError):
                adapter.check_arguments(arguments)
        adapter.check_arguments(["--dry-run", "--silent", "--config", "saved.json"])


@unittest.skipUnless(importlib.util.find_spec("archinstall"), "run API tests in the pinned ARM64 test root")
class ArchinstallAPITests(unittest.TestCase):
    """Use real installer dispatch, replacing commands that would touch disks."""

    def setUp(self):
        from archinstall.lib.args import ArchConfig
        from archinstall.lib.models.bootloader import Bootloader, BootloaderConfiguration
        from archinstall.lib.models.device import (DiskLayoutConfiguration, DiskLayoutType, DeviceModification,
            PartitionModification, ModificationStatus, PartitionType, Size, SectorSize, Unit, FilesystemType, PartitionFlag)
        from archinstall.lib.plugins import plugins
        self.addCleanup(plugins.clear)
        esp = PartitionModification(ModificationStatus.EXIST, PartitionType.PRIMARY, Size(1, Unit.MiB, SectorSize(512, Unit.B)),
            Size(256, Unit.MiB, SectorSize(512, Unit.B)), FilesystemType.FAT32, Path("/boot/efi"),
            flags=[PartitionFlag.ESP, PartitionFlag.BOOT], dev_path=Path("/dev/nvme0n1p1"))
        root = PartitionModification(ModificationStatus.CREATE, PartitionType.PRIMARY, Size(10, Unit.GiB, SectorSize(512, Unit.B)),
            Size(20, Unit.GiB, SectorSize(512, Unit.B)), FilesystemType.EXT4, Path("/"))
        self.config = ArchConfig(kernels=[adapter.KERNEL], bootloader_config=BootloaderConfiguration(Bootloader.Grub, removable=False))
        self.config.disk_config = DiskLayoutConfiguration(DiskLayoutType.Manual,
            device_modifications=[DeviceModification(SimpleNamespace(disk=SimpleNamespace(type="gpt")), False, [esp, root])])
        self.handler = SimpleNamespace(config=self.config,
            args=SimpleNamespace(dry_run=True, silent=True, offline=True, verbose=False))
        self.guided, _ = adapter.configure_adapter(self.handler, "surface-pro-11-x1e-oled")
        self.plugin = plugins["lexr-surface"]

    def installer(self, directory):
        with patch("archinstall.lib.installer.accessibility_tools_in_use", return_value=False):
            return self.guided.Installer(Path(directory), self.config.disk_config, kernels=self.config.kernels)

    def test_stock_storage_account_profile_and_network_implementations_remain(self):
        from archinstall.lib.installer import Installer
        from archinstall.lib.disk.filesystem import FilesystemHandler
        from archinstall.lib.network.network_handler import install_network_config
        from archinstall.lib.profile.profiles_handler import profile_handler
        self.assertIs(self.guided.Installer, Installer)
        self.assertIs(self.guided.FilesystemHandler, FilesystemHandler)
        self.assertIs(self.guided.install_network_config, install_network_config)
        self.assertIs(self.guided.profile_handler, profile_handler)
        self.assertNotIn("copy_iso_network_config", adapter.SurfacePlugin.__dict__)

    def test_native_pacstrap_callback_excludes_local_kernel_even_when_only_request(self):
        from archinstall.lib.pacman.pacman import Pacman
        installer = self.installer("/mnt")
        with patch.object(Pacman, "sync"), patch.object(Pacman, "ask") as execute:
            installer.pacman.strap(installer._base_packages)
            command = execute.call_args.args[3]
            self.assertNotIn(adapter.KERNEL, command)
            self.assertIn("archlinuxarm-keyring", command)
            installer.pacman.strap([adapter.KERNEL])
            command = execute.call_args.args[3]
            self.assertNotIn(adapter.KERNEL, command)
            self.assertIn("archlinuxarm-keyring", command)

    def test_real_hook_dispatch_installs_kernel_once_then_boot_and_final_check(self):
        from archinstall.lib.models.bootloader import Bootloader
        events = []
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "etc").mkdir()
            installer = self.installer(root)
            def verify(*args):
                self.assertIn("generated UUID fstab", (root / "etc/fstab").read_text())
                events.append("verify")
            with patch.object(adapter.surface, "install_kernel", side_effect=lambda *a: events.append("kernel")), \
                    patch.object(adapter.surface, "run", side_effect=lambda *a: events.append("rebuild")), \
                    patch.object(installer, "add_additional_packages", side_effect=lambda *a: events.append("grub packages")), \
                    patch.object(adapter.surface, "install_boot", side_effect=lambda *a: events.append("boot")), \
                    patch("archinstall.lib.installer.SysCommand") as command, \
                    patch.object(adapter.surface, "verify_install", side_effect=verify):
                command.return_value.output.return_value = b"# generated UUID fstab\n"
                self.assertTrue(installer.mkinitcpio(["-P"]))
                self.assertTrue(installer.mkinitcpio(["-P"]))
                installer.add_bootloader(Bootloader.Grub)
                installer.genfstab()
            self.assertEqual(events, ["kernel", "rebuild", "grub packages", "boot", "verify"])
            self.assertEqual(installer._helper_flags["bootloader"], "grub")

    def test_kernel_failure_cannot_mark_target_installed(self):
        installer = self.installer("/mnt")
        with patch.object(adapter.surface, "install_kernel", side_effect=ValueError("bad payload")):
            with self.assertRaises(ValueError):
                installer.mkinitcpio(["-P"])
        self.assertFalse(self.plugin.installed_targets)
        with self.assertRaises(ValueError), patch.object(adapter.surface, "install_boot") as boot:
            self.plugin.on_add_bootloader(installer)
        boot.assert_not_called()

    def test_only_platform_menu_fields_are_fixed_and_no_desktop_default(self):
        with patch("archinstall.lib.hardware.SysInfo.has_uefi", return_value=True):
            menu = self.guided.GlobalMenu(self.config)
        for key in ("kernels", "mirror_config"):
            self.assertTrue(menu._item_group.find_by_key(key).read_only)
        for key in ("bootloader_config", "disk_config", "auth_config", "profile_config", "network_config"):
            self.assertFalse(menu._item_group.find_by_key(key).read_only)
        self.assertIsNone(menu._item_group.find_by_key("profile_config").value)
        self.assertIsNone(menu._validate_bootloader())

    def test_boot_compatibility_does_not_reimplement_partition_policy(self):
        from archinstall.lib.models.device import ModificationStatus
        # The user's reviewed choice may format an existing root or create a
        # fresh ESP. Lexr does not inspect/rewrite geometry or forbid that choice.
        disk = self.config.disk_config
        disk.device_modifications[0].wipe = True
        root = disk.device_modifications[0].partitions[1]
        root.status, root.dev_path = ModificationStatus.MODIFY, Path("/dev/nvme0n1p3")
        self.assertIsNone(adapter.validate_platform(self.config))

    def test_unsupported_boot_choices_fail_before_upstream_formatter(self):
        from archinstall.lib.models.bootloader import Bootloader
        from archinstall.lib.models.device import FilesystemType
        cases = [lambda c: setattr(c.disk_config.device_modifications[0].device.disk, "type", "msdos"),
                 lambda c: setattr(c.bootloader_config, "bootloader", Bootloader.Systemd),
                 lambda c: setattr(c.bootloader_config, "uki", True),
                 lambda c: setattr(c, "kernels", ["linux"]),
                 lambda c: setattr(c.disk_config.device_modifications[0].partitions[1], "fs_type", FilesystemType.BTRFS),
                 lambda c: setattr(c.disk_config.device_modifications[0].partitions[0], "mountpoint", Path("/boot"))]
        for change in cases:
            with self.subTest(change=change):
                config = copy.deepcopy(self.config)
                change(config)
                self.handler.config = config
                # Exercise the real guided main: it must return before creating
                # a formatter even for a saved configuration / silent path.
                self.handler.args.dry_run = False
                with patch.object(self.guided, "MirrorListHandler"), \
                        patch.object(type(config), "write_debug"), patch.object(type(config), "save"), \
                        patch.object(self.guided, "FilesystemHandler") as formatter, \
                        patch.object(self.guided, "perform_installation") as install:
                    self.guided.main(self.handler)
                formatter.assert_not_called()
                install.assert_not_called()

    def test_native_dry_run_stops_before_formatter(self):
        with patch.object(self.guided, "MirrorListHandler"), \
                patch.object(type(self.config), "write_debug"), patch.object(type(self.config), "save"), \
                patch.object(self.guided, "FilesystemHandler") as formatter, \
                patch.object(self.guided, "perform_installation") as install:
            self.guided.main(self.handler)
        formatter.assert_not_called()
        install.assert_not_called()


if __name__ == "__main__":
    unittest.main()
