"""Exercise adapter ordering against the actual packaged Archinstall API."""

import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import guided as adapter
from test_policy import fixture


class ArgumentTests(unittest.TestCase):
    """Extension flags must be rejected before the upstream parser loads code."""

    def test_executable_extensions_are_rejected_early(self):
        """No plugin, script or remote config can bypass the guided integration."""
        for arguments in (["--plugin", "danger.py"], ["--plugin-url=https://example.invalid"],
                          ["--script", "other"], ["--skip-boot"], ["--config-url", "https://example.invalid"]):
            with self.subTest(arguments=arguments), self.assertRaises(ValueError):
                adapter.check_arguments(arguments)
        adapter.check_arguments(["--dry-run", "--silent"])

    def test_saved_configuration_requires_explicit_surface_kernel(self):
        """An omitted kernel must not silently acquire upstream's stock default."""
        for config in ({}, {"kernels": ["linux"]}):
            with patch.object(Path, "read_text", return_value=json.dumps(config)), self.assertRaises(ValueError):
                adapter.check_arguments(["--config", "saved.json"])
        with patch.object(Path, "read_text", return_value='{"kernels":["lexr-kernel-sp11"]}'):
            adapter.check_arguments(["--config=saved.json"])

    def test_unsafe_mountpoint_is_rejected_before_target_mounts(self):
        """Reject the live root, symlinks and nested mounts of another filesystem."""
        for path in ("/", "/home", "/mnt/../home"):
            with self.subTest(path=path), self.assertRaises(ValueError):
                adapter.check_mountpoint(Path(path))
        with patch.object(adapter.surface, "run", return_value=json.dumps({"filesystems": [{"target": "/mnt/home"}]})):
            with self.assertRaises(ValueError):
                adapter.check_mountpoint(Path("/mnt"))


@unittest.skipUnless(importlib.util.find_spec("archinstall"), "run API tests in the pinned ARM64 test root")
class ArchinstallAPITests(unittest.TestCase):
    """Use real 4.4 classes with only device operations replaced by test doubles."""

    @classmethod
    def setUpClass(cls):
        """Build a realistic guided configuration without any mounted target."""
        from archinstall.lib.args import ArchConfig
        from archinstall.lib.models.bootloader import Bootloader, BootloaderConfiguration
        from archinstall.lib.models.device import DiskLayoutConfiguration, DiskLayoutType
        cls.config = ArchConfig(kernels=["lexr-kernel-sp11"], bootloader_config=BootloaderConfiguration(Bootloader.Grub, removable=False))
        cls.config.disk_config = DiskLayoutConfiguration(DiskLayoutType.Manual)
        cls.handler = SimpleNamespace(config=cls.config, args=SimpleNamespace(dry_run=True, silent=True, offline=False, verbose=False))
        cls.guided, cls.validate = adapter.configure_adapter(cls.handler, "surface-pro-11-x1e-oled")

    def test_constructor_never_straps_generic_or_local_kernel(self):
        """The local kernel is absent from repository requests even at defaults."""
        with patch("archinstall.lib.installer.accessibility_tools_in_use", return_value=False):
            installer = self.guided.Installer(Path("/mnt"), self.config.disk_config, kernels=self.config.kernels)
        self.assertIn("archlinuxarm-keyring", installer._base_packages)
        self.assertNotIn("linux", installer._base_packages)
        self.assertNotIn("lexr-kernel-sp11", installer._base_packages)
        with patch("archinstall.lib.pacman.pacman.Pacman.strap") as strap, self.assertRaises(ValueError):
            installer.pacman.strap(["linux-aarch64"])
        strap.assert_not_called()

    def test_kernel_and_grub_then_final_verification_order(self):
        """The Surface payload precedes GRUB, and fstab precedes final verification."""
        from archinstall.lib.installer import Installer
        from archinstall.lib.models.bootloader import Bootloader
        events = []
        with patch("archinstall.lib.installer.accessibility_tools_in_use", return_value=False):
            installer = self.guided.Installer(Path("/mnt"), self.config.disk_config, kernels=self.config.kernels)
        with patch.object(Installer, "minimal_installation", side_effect=lambda **kw: events.append(("base", kw["mkinitcpio"]))), \
                patch.object(adapter.surface, "install_kernel", side_effect=lambda *a: events.append("kernel")), \
                patch.object(installer.pacman, "strap", side_effect=lambda *a: events.append("grub packages")), \
                patch.object(adapter.surface, "install_boot", side_effect=lambda *a: events.append("boot")), \
                patch.object(Installer, "genfstab", side_effect=lambda *a: events.append("fstab")), \
                patch.object(adapter.surface, "verify_install", side_effect=lambda *a: events.append("verify")):
            installer.minimal_installation(mkinitcpio=True)
            installer.add_bootloader(Bootloader.Grub)
            installer.genfstab()
        self.assertEqual(events, [("base", False), "kernel", "grub packages", "boot", "fstab", "verify"])
        self.assertEqual(installer._helper_flags["bootloader"], "grub")

    def test_dry_run_rejects_filesystem_operations(self):
        """A direct call cannot accidentally format partitions during a preview."""
        from archinstall.lib.disk.filesystem import FilesystemHandler
        with patch.object(FilesystemHandler, "perform_filesystem_operations") as operations:
            with self.assertRaises(ValueError):
                self.guided.FilesystemHandler(self.config.disk_config).perform_filesystem_operations()
        operations.assert_not_called()

    def test_menu_has_fixed_platform_choices_without_desktop_default(self):
        """The real menu accepts string kernel identities and read-only settings."""
        with patch("archinstall.lib.hardware.SysInfo.has_uefi", return_value=True):
            menu = self.guided.GlobalMenu(self.config)
        for key in ("kernels", "bootloader_config", "mirror_config"):
            self.assertTrue(menu._item_group.find_by_key(key).read_only)
        self.assertIsNone(menu._item_group.find_by_key("profile_config").value)
        menu._prev_kernel(menu._item_group.find_by_key("kernels"))


if __name__ == "__main__":
    unittest.main()
