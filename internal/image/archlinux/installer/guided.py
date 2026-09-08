"""Archinstall 4.4 guided flow with the Lexr Surface Pro 11 integration.

Upstream Archinstall remains a separately packaged, unmodified dependency.
The storage policy is checked again immediately before its first mutation.
"""

import importlib.metadata
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from types import SimpleNamespace

import policy
import target as surface

PAYLOAD = Path("/usr/share/lexr/arch-media/sp11")
MIRRORLIST = "Server = https://ca.us.mirror.archlinuxarm.org/$arch/$repo\n"


def check_arguments(arguments):
    """Reject executable extensions before Archinstall parses or imports them."""
    rejected = {"--plugin", "--plugin-url", "--script", "--config-url", "--creds-url", "--skip-boot", "share-log"}
    for argument in arguments:
        if argument.split("=", 1)[0] in rejected:
            raise ValueError("Use the bundled guided installer without external plugins, scripts or remote configuration")
    for index, argument in enumerate(arguments):
        if argument == "--config" or argument.startswith("--config="):
            name = argument.split("=", 1)[1] if "=" in argument else arguments[index + 1]
            config = json.loads(Path(name).read_text())
            if config.get("script") or config.get("custom_commands") or ((config.get("profile_config") or {}).get("profile") or {}).get("path"):
                raise ValueError("External installer code and custom post-install commands are not supported")
            if config.get("kernels") != ["lexr-kernel-sp11"]:
                raise ValueError("Saved configuration must select the Lexr Surface kernel")


def disk_state(device):
    """Read the real GPT and mount state without modifying the selected disk."""
    if not policy.DEVICE.fullmatch(device):
        raise ValueError("Invalid installation device")
    table = json.loads(surface.run("sfdisk", "--json", device, capture=True))["partitiontable"]
    devices = json.loads(surface.run("lsblk", "--json", "--paths", "--output",
                                     "PATH,TYPE,FSTYPE,MOUNTPOINTS,RO", device, capture=True))["blockdevices"]
    return table, devices


def require_boot_environment():
    """Fail before partitioning when the Surface UEFI path cannot be installed."""
    surface.require_uefi()
    _, entries = surface.efi_entries(surface.run("efibootmgr", "--verbose", capture=True))
    if any(surface.BOOT_LABEL in entry or surface.BOOT_LOADER.lower() in entry.lower() for entry in entries.values()):
        raise ValueError("A Lexr Arch firmware entry already exists; review it before creating another installation")


def inspect_esp(device):
    """Check space and name collisions on the existing ESP using a read-only mount."""
    with tempfile.TemporaryDirectory(prefix="lexr-esp-") as directory:
        surface.run("mount", "-t", "vfat", "-o", "ro,nosuid,nodev,noexec", device, directory)
        try:
            esp = Path(directory)
            if shutil.disk_usage(esp).free < 32 << 20:
                raise ValueError("The existing EFI partition needs at least 32 MiB free")
            if (esp / "EFI").exists() and any(p.name.lower() == "lexrarch" for p in (esp / "EFI").iterdir()):
                raise ValueError("EFI/LexrArch already exists; this installer creates a new installation")
        finally:
            surface.run("umount", directory)


def check_mountpoint(mountpoint):
    """Keep target mounting away from the live system or another mounted disk."""
    path = Path(mountpoint)
    if path != Path("/mnt") or path.resolve() != path:
        raise ValueError("Use the standard /mnt installation mountpoint")
    mounts = json.loads(surface.run("findmnt", "--json", "--list", "--output", "TARGET", capture=True))["filesystems"]
    if any(row["target"] == "/mnt" or row["target"].startswith("/mnt/") for row in mounts):
        raise ValueError("Unmount filesystems at or beneath /mnt before installing")
    if path.exists() and (not path.is_dir() or any(path.iterdir())):
        raise ValueError("The /mnt installation directory must be empty")


def configure_adapter(handler, profile):
    """Bind the reviewed Archinstall interfaces to the Surface implementation."""
    from archinstall.scripts import guided
    from archinstall.lib.global_menu import GlobalMenu
    from archinstall.lib.installer import Installer
    from archinstall.lib.disk.filesystem import FilesystemHandler
    from archinstall.lib.models.device import Unit, EncryptionType
    from archinstall.lib.models.bootloader import Bootloader
    from archinstall.lib.models.network import NicType
    from archinstall.lib.pacman.pacman import Pacman
    from archinstall.lib.hardware import GfxDriver, GfxPackage
    from archinstall.lib.plugins import plugins
    from archinstall.lib.profile.profile_menu import ProfileMenu
    from archinstall.lib.profile.profiles_handler import profile_handler
    from archinstall.lib.profile import profile_menu

    accepted = {}

    def plan_from_config(disk):
        """Convert the real 4.4 models into the independently tested policy data."""
        if not disk or len(disk.device_modifications) != 1:
            raise ValueError("Choose one GPT disk containing the existing ESP and unallocated root space")
        if disk.mountpoint is not None:
            raise ValueError("Use Manual Partitioning with the standard /mnt target")
        mod = disk.device_modifications[0]
        return {"layout": disk.config_type.value, "device": str(mod.device_path), "wipe": mod.wipe,
                "encrypted": bool(disk.disk_encryption and disk.disk_encryption.encryption_type != EncryptionType.NO_ENCRYPTION),
                "lvm": bool(disk.lvm_config),
                "partitions": [{"status": p.status.value, "start": p.start.convert(Unit.B).value,
                                "size": p.length.convert(Unit.B).value, "mount": str(p.mountpoint) if p.mountpoint else None,
                                "fs": p.fs_type.value if p.fs_type else None, "path": str(p.dev_path) if p.dev_path else None,
                                "partuuid": p.partuuid, "efi": p.is_efi(), "flags": [f.name for f in p.flags],
                                "options": p.mount_options, "subvolumes": bool(p.btrfs_subvols)} for p in mod.partitions]}

    def validate_config(config):
        """Apply platform policy to both menu selections and loaded configuration."""
        boot = config.bootloader_config
        if not boot or boot.bootloader != Bootloader.Grub or boot.uki or boot.removable or boot.plymouth:
            raise ValueError("Use Lexr GRUB without UKI, removable fallback or Plymouth")
        if config.kernels != ["lexr-kernel-sp11"] or config.mirror_config:
            raise ValueError("Use the bundled Surface kernel and Arch Linux ARM mirror")
        if config.custom_commands or config.script:
            raise ValueError("Custom installer commands are not supported")
        if config.profile_config and config.profile_config.gfx_driver not in (None, GfxDriver.AllOpenSource):
            raise ValueError("Use the Surface Adreno/Mesa graphics choice")
        policy.validate_packages(config.packages or [])
        plan = plan_from_config(config.disk_config)
        table, devices = disk_state(plan["device"])
        policy.validate_plan(plan, table, devices)
        return plan, table

    class SurfaceMenu(GlobalMenu):
        """Keep normal account/profile choices while fixing platform settings."""

        def _get_menu_options(self):
            """Make kernel, GRUB and ARM repository choices visible but immutable."""
            items = super()._get_menu_options()
            labels = {"kernels": "Kernel (Lexr Surface Pro 11)", "bootloader_config": "Bootloader (Lexr GRUB)",
                      "mirror_config": "Mirrors (Arch Linux ARM)"}
            for item in items:
                if item.key in labels:
                    item.text, item.action, item.read_only = labels[item.key], None, True
                    item.value = getattr(self._arch_config, item.key)
                    if item.key == "mirror_config":
                        item.preview_action = lambda _: "Arch Linux ARM HTTPS mirror; x86 repositories are not used."
            return items

        def _validate_bootloader(self):
            """Show a concrete storage/platform error in the normal Install preview."""
            self.sync_all_to_config()
            try:
                validate_config(self._arch_config)
            except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
                return str(error)
            return None

    class SurfaceProfiles(ProfileMenu):
        """Keep optional environments while using the device's Mesa graphics stack."""

        def _define_menu_options(self):
            """Disable irrelevant GPU vendor selection on this Surface image."""
            items = super()._define_menu_options()
            for item in items:
                if item.key == "gfx_driver":
                    item.text, item.action, item.read_only = "Graphics (Surface Adreno/Mesa)", None, True
            return items

    class SurfacePacman(Pacman):
        """Use ARM signing keys and reject incompatible package selections."""

        @staticmethod
        def _reinit_keyring():
            """Initialise only the Arch Linux ARM trust database."""
            surface.run("pacman-key", "--init")
            surface.run("pacman-key", "--populate", "archlinuxarm")

        def strap(self, packages):
            """Check every package request, including those generated by profiles."""
            policy.validate_packages([packages] if isinstance(packages, str) else packages)
            if plugins:
                raise ValueError("External Archinstall plugins are not supported by this integration")
            return super().strap(packages)

    class SurfaceFilesystem(FilesystemHandler):
        """Recheck the selected disk after confirmation and before partitioning."""

        def perform_filesystem_operations(self):
            """Permit only one new root; verify all existing records afterwards."""
            if handler.args.dry_run:
                raise ValueError("Dry run cannot modify filesystems")
            surface.verify_payload(PAYLOAD)
            if surface.detect_profile() != profile:
                raise ValueError("Surface identity changed")
            require_boot_environment()
            check_mountpoint(handler.args.mountpoint)
            plan, before = validate_config(handler.config)
            if accepted.get("table") != before or accepted.get("plan") != plan:
                raise ValueError("The disk layout changed after review; restart the installer")
            _, esp = policy.validate_plan(plan, *disk_state(plan["device"]))
            inspect_esp(esp["path"])
            # Nothing remains mounted on this disk; upstream cannot unmount
            # another running OS. It can format only the new CREATE partition.
            super().perform_filesystem_operations()
            after, _ = disk_state(plan["device"])
            policy.preserved_partitions(before, after)

    class SurfaceInstaller(Installer):
        """Replace only the target kernel and bootloader stages of Archinstall."""

        def __init__(self, *args, **kwargs):
            """Strip every kernel from pacstrap; the local package is installed later."""
            super().__init__(*args, **kwargs)
            self._base_packages = [p for p in self._base_packages if p not in self.kernels]
            self._base_packages.append("archlinuxarm-keyring")
            # The pinned 4.4 guided flow passes silent as a keyword argument.
            self.pacman = SurfacePacman(self.target, kwargs.get("silent", False))

        def minimal_installation(self, *args, **kwargs):
            """Strap a fresh root, then install the coherent offline Surface payload."""
            kwargs["mkinitcpio"] = False
            super().minimal_installation(*args, **kwargs)
            surface.install_kernel(self.target, PAYLOAD)

        def mkinitcpio(self, flags):
            """Keep the installed Surface hook/config when profiles request a rebuild."""
            abi = surface.verify_payload(PAYLOAD)["abi"]
            surface.run("arch-chroot", self.target, "mkinitcpio", "-c", "/etc/lexr/mkinitcpio-installed.conf",
                        "-k", abi, "-g", f"/boot/initramfs-{abi}.img")
            return True

        def add_bootloader(self, bootloader, uki_enabled=False, removable=False, plymouth=None):
            """Install only the dedicated ARM64 GRUB path, never upstream x86 defaults."""
            if bootloader != Bootloader.Grub or uki_enabled or removable or plymouth:
                raise ValueError("Only the Lexr GRUB boot configuration is supported")
            self.pacman.strap(["grub", "efibootmgr"])
            surface.install_boot(self.target, PAYLOAD, profile)
            self._helper_flags["bootloader"] = "grub"

        def copy_iso_network_config(self, enable_services=False):
            """Honour Copy ISO networking for NetworkManager when explicitly selected."""
            self.pacman.strap(["networkmanager", "wpa_supplicant"])
            source = Path("/etc/NetworkManager/system-connections")
            destination = self.target / "etc/NetworkManager/system-connections"
            destination.mkdir(parents=True, exist_ok=True)
            for path in source.glob("*.nmconnection"):
                if path.is_file() and not path.is_symlink():
                    shutil.copyfile(path, destination / path.name)
                    (destination / path.name).chmod(0o600)
            if enable_services:
                self.enable_service("NetworkManager.service")
            return True

        def genfstab(self, flags="-pU"):
            """Complete checks before the normal success/reboot dialog can appear."""
            network = handler.config.network_config
            if network and network.type in (NicType.ISO, NicType.NM, NicType.NM_IWD):
                resolv = self.target / "etc/resolv.conf"
                resolv.unlink(missing_ok=True)
                resolv.symlink_to("/run/NetworkManager/resolv.conf")
            super().genfstab(flags)
            surface.verify_install(self.target, PAYLOAD, profile)

    def checked_layout(boot, disk):
        """Check the final plan before dry-run exit or the confirmation screen."""
        try:
            plan, table = validate_config(handler.config)
            accepted.update(plan=plan, table=table)
        except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
            return SimpleNamespace(description=str(error))
        return None

    def surface_gfx(session, driver):
        """Install Mesa for Adreno when a user chooses an optional graphical profile."""
        if driver != GfxDriver.AllOpenSource:
            raise ValueError("Select Surface Adreno/Mesa graphics")
        session.add_additional_packages(["mesa"])

    original_gfx = GfxDriver.gfx_packages

    def gfx_packages(driver):
        """Make the generic open-source preview reflect the actual ARM package set."""
        return [GfxPackage.Mesa] if driver == GfxDriver.AllOpenSource else original_gfx(driver)

    guided.GlobalMenu = SurfaceMenu
    profile_menu.ProfileMenu = SurfaceProfiles
    guided.Installer = SurfaceInstaller
    guided.FilesystemHandler = SurfaceFilesystem
    guided.validate_bootloader_layout = checked_layout
    guided.check_version_upgrade = lambda: None
    profile_handler.install_gfx_driver = surface_gfx
    GfxDriver.gfx_packages = gfx_packages
    return guided, validate_config


def main():
    """Start the familiar guided flow, leaving dry-run free of device writes."""
    check_arguments(sys.argv[1:])
    if importlib.metadata.version("archinstall") != "4.4":
        raise ValueError("This image requires its bundled Archinstall 4.4; rebuild Lexr before upgrading the installer")
    from archinstall.lib.args import ArchConfigHandler
    from archinstall.lib.models.bootloader import Bootloader, BootloaderConfiguration
    from archinstall.lib.models.network import NetworkConfiguration, NicType
    class SurfaceConfigHandler(ArchConfigHandler):
        """Do not let abbreviated options bypass the extension preflight."""

        def _define_arguments(self):
            """Use exact upstream option names, including in saved-config flows."""
            parser = super()._define_arguments()
            parser.allow_abbrev = False
            return parser

    handler = SurfaceConfigHandler()
    if os.geteuid() != 0:
        raise ValueError("Run sudo archinstall from the live session")
    args, config = handler.args, handler.config
    if (args.silent and not args.dry_run) or args.skip_boot or args.offline or args.command:
        raise ValueError("Use the interactive online guided flow; --dry-run may use a saved configuration")
    check_mountpoint(args.mountpoint)
    if not args.config:
        config.kernels = ["lexr-kernel-sp11"]
        config.bootloader_config = BootloaderConfiguration(Bootloader.Grub, uki=False, removable=False)
    if config.network_config is None:
        config.network_config = NetworkConfiguration(NicType.NM)
    if Path("/etc/pacman.d/mirrorlist").read_text() != MIRRORLIST:
        raise ValueError("Restore the image's Arch Linux ARM mirror configuration before installing")
    surface.verify_payload(PAYLOAD)
    profile = surface.detect_profile()
    require_boot_environment()
    args.skip_wkd, args.skip_version_check, args.skip_wifi_check = True, True, True
    if args.dry_run:
        args.no_pkg_lookups = True
    else:
        surface.run("pacman-key", "--init")
        surface.run("pacman-key", "--populate", "archlinuxarm")
        surface.run("pacman", "-Sy", "--noconfirm")
    guided, _ = configure_adapter(handler, profile)
    print("Lexr Surface installer: manual ext4 root + existing ESP, ARM64 GRUB, custom kernel. No desktop is preselected.")
    guided.main(handler)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, KeyError, IndexError, subprocess.CalledProcessError) as error:
        print(f"Lexr Arch installer: {error}", file=sys.stderr)
        sys.exit(1)
