"""Exercise Surface callbacks against the separately packaged Archinstall API."""

import copy
import asyncio
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace
import tempfile
import unittest
from uuid import UUID
from unittest.mock import patch

import guided as adapter


class ArgumentTests(unittest.TestCase):
    def test_guided_entry_point_is_retained(self):
        for arguments in (["--plugin", "replacement.py"], ["--plugin-url=https://example.invalid"],
                          ["--script", "other"], ["--skip-boot"]):
            with self.subTest(arguments=arguments), self.assertRaises(ValueError):
                adapter.check_arguments(arguments)
        adapter.check_arguments(["--dry-run", "--silent", "--config", "saved.json"])

    def test_external_code_is_rejected_before_profile_parsing(self):
        for config in ({"profile_config": {"profile": {"path": "/tmp/custom.py"}}},
                       {"profile_config": {"profile": {"path": "https://example.invalid/profile.py"}}},
                       {"custom_commands": ["install-something"]}, {"script": "other"}):
            with self.subTest(config=config), self.assertRaises(ValueError):
                adapter.validate_raw_config(config)
        adapter.validate_raw_config({"profile_config": {"profile": {"main": "Minimal"}}})

    def test_effective_repository_and_host_architecture_must_match(self):
        def query(args, **kwargs):
            if args[1:] == ["Architecture"]:
                return "aarch64\n"
            if args[1:] == ["--repo-list"]:
                return "core\nextra\nalarm\naur\n"
            return f"https://ca.us.mirror.archlinuxarm.org/aarch64/{args[2]}\n"
        with patch.object(adapter.platform, "machine", return_value="aarch64"), \
                patch.object(adapter.subprocess, "check_output", side_effect=query):
            adapter.validate_repositories()
            for output in ("x86_64\n", "aarch64\nmultilib\n"):
                with patch.object(adapter.subprocess, "check_output", return_value=output), self.assertRaises(ValueError):
                    adapter.validate_repositories()
        with patch.object(adapter.platform, "machine", return_value="x86_64"), self.assertRaises(ValueError):
            adapter.validate_repositories()


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
        self.repository_check = patch.object(adapter, "validate_repositories").start()
        self.package_check = patch.object(adapter, "check_packages").start()
        self.addCleanup(patch.stopall)

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

    def test_mesa_round_trip_and_old_pc_graphics_configs_are_rejected(self):
        from archinstall.lib.models.profile import ProfileConfiguration
        from archinstall.lib.profile.profiles_handler import profile_handler
        from archinstall.lib.hardware import GfxDriver
        desktop = profile_handler.get_profile_by_name("Desktop")
        config = ProfileConfiguration(desktop, adapter.SurfaceGraphics.Mesa)
        self.assertEqual(ProfileConfiguration.parse_arg(config.json()).gfx_driver, adapter.SurfaceGraphics.Mesa)
        for driver in GfxDriver:
            with self.subTest(driver=driver), self.assertRaises(ValueError):
                ProfileConfiguration.parse_arg({**config.json(), "gfx_driver": driver.value})

    def test_desktop_providers_are_explicit_in_preflight_and_every_transaction(self):
        from archinstall.lib.models.profile import ProfileConfiguration
        from archinstall.lib.profile.profiles_handler import profile_handler
        desktop = profile_handler.get_profile_by_name("Desktop")
        desktop.current_selection = [profile_handler.get_profile_by_name("Cosmic")]
        self.config.profile_config = ProfileConfiguration(desktop, adapter.SurfaceGraphics.Mesa)
        self.config.packages = ["htop"]
        self.assertTrue({"cosmic", "mesa", "vulkan-freedreno", "htop"} <= set(adapter.profile_packages(self.config)))
        for request in (["base"], ["cosmic"], ["pipewire"], ["cosmic-greeter"]):
            with self.subTest(request=request):
                result = self.plugin.on_pacstrap(request)
                self.assertTrue({"mesa", "vulkan-freedreno"} <= set(result))
                self.package_check.assert_called_with(result)

    def test_profile_menu_uses_mesa_after_each_selection_and_can_reset(self):
        from archinstall.lib.models.profile import ProfileConfiguration
        from archinstall.lib.profile import profile_menu
        from archinstall.lib.profile.profiles_handler import profile_handler
        menu = self.guided.GlobalMenu(self.config)
        async def exercise(submenu):
            item = submenu._item_group.find_by_key("gfx_driver")
            self.assertTrue(item.read_only)
            self.assertIsNone(item.action)
            desktop = profile_handler.get_profile_by_name("Desktop")
            desktop.current_selection = [profile_handler.get_profile_by_name("Cosmic")]
            with patch.object(profile_menu, "select_profile", return_value=desktop):
                await submenu._select_profile(None)
            self.assertEqual(item.value, adapter.SurfaceGraphics.Mesa)
            with patch.object(profile_menu, "select_profile", return_value=None):
                await submenu._select_profile(desktop)
            self.assertIsNone(item.value)
            return ProfileConfiguration()
        with patch.object(profile_menu.ProfileMenu, "show", exercise):
            asyncio.run(menu._select_profile(None))

    def test_saved_config_is_checked_before_upstream_imports_code(self):
        from archinstall.lib.args import ArchConfigHandler, ArchConfig
        raw = {"profile_config": {"profile": {"path": "https://example.invalid/custom.py"}}}
        with patch.object(ArchConfigHandler, "_parse_config", return_value=raw), \
                patch.object(ArchConfig, "from_config") as parse, \
                patch("sys.argv", ["archinstall", "--dry-run"]), self.assertRaises(ValueError):
            adapter.config_handler_type()()
        parse.assert_not_called()

    def test_pc_graphics_commands_and_package_errors_stop_before_formatter(self):
        from archinstall.lib.models.profile import ProfileConfiguration
        from archinstall.lib.hardware import GfxDriver
        for change in (lambda c: setattr(c, "custom_commands", ["anything"]),
                       lambda c: setattr(c, "profile_config", ProfileConfiguration(gfx_driver=GfxDriver.AllOpenSource)),
                       lambda c: setattr(c, "packages", ["intel-media-driver"]),
                       lambda c: setattr(c, "packages", ["missing-arm-package"])):
            with self.subTest(change=change):
                config = copy.deepcopy(self.config)
                change(config)
                self.handler.config = config
                self.handler.args.dry_run = False
                self.package_check.side_effect = ValueError("missing ARM package")
                with patch.object(self.guided, "MirrorListHandler"), \
                        patch.object(type(config), "write_debug"), patch.object(type(config), "save"), \
                        patch.object(self.guided, "FilesystemHandler") as formatter, \
                        patch.object(self.guided, "perform_installation") as install:
                    self.guided.main(self.handler)
                formatter.assert_not_called()
                install.assert_not_called()

    def test_online_install_does_not_fetch_x86_mirrors(self):
        with patch("archinstall.lib.mirror.mirror_handler.fetch_data_from_url") as fetch:
            mirrors = self.guided.MirrorListHandler(offline=False)
            mirrors.get_mirror_regions()
        self.assertTrue(mirrors.offline)
        fetch.assert_not_called()

    def test_gpt_root_type_is_arm64_without_replacing_formatter(self):
        from archinstall.lib.disk import device_handler
        from archinstall.lib.models.device import PartitionGUID
        root = self.config.disk_config.device_modifications[0].partitions[1]
        disk = SimpleNamespace(type="gpt", device=SimpleNamespace(optimalAlignedConstraint=None), addPartition=lambda **kw: None)
        block = SimpleNamespace(disk=disk, device_info=SimpleNamespace(sector_size=root.start.sector_size))
        with patch.object(device_handler, "Geometry"), patch.object(device_handler, "FileSystem"), \
                patch.object(device_handler, "Partition") as partition:
            partition.return_value.path = "/dev/loop0p2"
            device_handler.DeviceHandler._setup_partition(SimpleNamespace(), root, block, disk, False)
        self.assertEqual(partition.return_value.type_uuid, UUID(adapter.ARM64_ROOT_GUID).bytes)
        self.assertEqual(str(UUID(PartitionGUID.LINUX_ROOT_X86_64.value)), "4f68bce3-e8cd-4db1-96e7-fbcaf984b709")

    def test_native_parted_creates_arm64_root_and_preserves_other_partition(self):
        import parted
        from archinstall.lib.disk.device_handler import DeviceHandler
        from archinstall.lib.models.device import Size, SectorSize, Unit
        # libparted accepts a sparse regular file. No host block device, mount,
        # firmware variable or filesystem formatting is involved in this test.
        with tempfile.TemporaryDirectory() as directory:
            image = Path(directory) / "gpt.img"
            with image.open("wb") as stream:
                stream.truncate(128 * 1024 * 1024)
            device = parted.getDevice(str(image))
            disk = parted.freshDisk(device, "gpt")
            geometry = parted.Geometry(device=device, start=2048, length=16384)
            other = parted.Partition(disk=disk, type=parted.PARTITION_NORMAL, geometry=geometry)
            disk.addPartition(other, constraint=parted.Constraint(exactGeom=geometry))
            before = (other.geometry.start, other.geometry.length, other.type_uuid)
            root = self.config.disk_config.device_modifications[0].partitions[1]
            root.start = Size(16, Unit.MiB, SectorSize(512, Unit.B))
            root.length = Size(32, Unit.MiB, SectorSize(512, Unit.B))
            block = SimpleNamespace(disk=disk, device_info=SimpleNamespace(sector_size=root.start.sector_size))
            DeviceHandler._setup_partition(SimpleNamespace(), root, block, disk, False)
            disk.commitToDevice()
            self.assertEqual((other.geometry.start, other.geometry.length, other.type_uuid), before)
            records = json.loads(adapter.subprocess.check_output(["sfdisk", "--json", str(image)], text=True))["partitiontable"]["partitions"]
            self.assertEqual(records[1]["type"].lower(), adapter.ARM64_ROOT_GUID)
            self.assertEqual(records[0]["start"], 2048)
            self.assertEqual(records[0]["size"], 16384)

    def test_package_policy_rejects_pc_hardware_generic_kernels_and_argument_overrides(self):
        for name in ("intel-media-driver", "xf86-video-ati", "amd-ucode", "linux", "linux-aarch64", "lib32-mesa", "--config", "custom/mesa", "/tmp/pkg.tar.zst"):
            with self.subTest(name=name), self.assertRaises(ValueError):
                adapter.validate_package_names([name])
        adapter.validate_package_names(["mesa", "vulkan-freedreno", "cosmic", "archlinuxarm-keyring"])

    def test_package_resolver_rejects_x86_dependency_and_group_members(self):
        # Restore the real checker; every record emitted by pacman's group and
        # dependency resolution is checked, not only the requested package name.
        patch.stopall()
        for output in ("mesa x86_64\n", "intel-media-driver aarch64\n", "bad metadata record\n"):
            with self.subTest(output=output), patch.object(adapter.subprocess, "run", return_value=SimpleNamespace(
                    returncode=0, stdout=output, stderr="")), self.assertRaises(ValueError):
                adapter.check_packages(["desktop-group"])
        with patch.object(adapter.subprocess, "run", return_value=SimpleNamespace(
                returncode=0, stdout="mesa aarch64\nxorgproto any\n", stderr="")) as resolve:
            adapter.check_packages(["mesa"])
        self.assertIn("--print", resolve.call_args.args[0])
        self.assertNotIn("--refresh", resolve.call_args.args[0])


if __name__ == "__main__":
    unittest.main()
