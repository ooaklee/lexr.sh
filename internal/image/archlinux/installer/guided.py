"""Archinstall's guided flow with hooks for the Surface kernel and ARM64 boot.

Archinstall owns partitioning, formatting, accounts, profiles and networking.
This adapter only supplies platform defaults and the Surface boot hand-off.
"""

import importlib.metadata
import os
from pathlib import Path
import subprocess
import sys
from types import SimpleNamespace

import target as surface

PAYLOAD = Path("/usr/share/lexr/arch-media/sp11")
MIRRORLIST = "Server = https://ca.us.mirror.archlinuxarm.org/$arch/$repo\n"
KERNEL = "lexr-kernel-sp11"


def check_arguments(arguments):
    """Keep the guided entry point and its Surface hooks when parsing options."""
    for argument in arguments:
        if argument.split("=", 1)[0] in {"--plugin", "--plugin-url", "--script", "--skip-boot"}:
            raise ValueError("Use the bundled guided entry point without replacement scripts or plugins")


def validate_platform(config):
    """Check boot compatibility, without implementing a partitioning policy."""
    from archinstall.lib.models.bootloader import Bootloader
    from archinstall.lib.models.device import DiskLayoutType, EncryptionType, FilesystemType, ModificationStatus

    boot, disk = config.bootloader_config, config.disk_config
    if not boot or boot.bootloader != Bootloader.Grub or boot.uki or boot.removable or boot.plymouth:
        raise ValueError("Select GRUB without UKI, removable fallback or Plymouth for this Surface image")
    if config.kernels != [KERNEL] or config.mirror_config:
        raise ValueError("Keep the Surface kernel and the image's Arch Linux ARM repositories")
    if config.script:
        raise ValueError("Use the guided installation entry point")
    if not disk or disk.config_type == DiskLayoutType.Pre_mount:
        raise ValueError("Select the installation partitions in Archinstall")
    if disk.lvm_config or (disk.disk_encryption and disk.disk_encryption.encryption_type != EncryptionType.NO_ENCRYPTION):
        raise ValueError("The Surface boot payload currently supports plain ext4 without LVM or encryption")
    parts = [p for m in disk.device_modifications for p in m.partitions if p.status != ModificationStatus.DELETE]
    roots = [p for p in parts if p.mountpoint == Path("/")]
    esps = [p for p in parts if p.mountpoint == Path("/boot/efi")]
    if len(roots) != 1 or roots[0].fs_type != FilesystemType.EXT4:
        raise ValueError("Select an ext4 root mounted at /")
    if len(esps) != 1 or not esps[0].is_efi() or not esps[0].fs_type or not esps[0].fs_type.is_fat():
        raise ValueError("Mount the FAT EFI System Partition at /boot/efi")
    esp_disk = next(m for m in disk.device_modifications if esps[0] in m.partitions)
    if not esp_disk.wipe and esp_disk.device.disk.type != "gpt":
        raise ValueError("The Surface firmware entry requires an ESP on a GPT disk")
    if any(p.mountpoint == Path("/boot") for p in parts):
        raise ValueError("Keep /boot on the ext4 root for the Surface kernel and DTBs")


class SurfacePlugin:
    """Use upstream plugin callbacks instead of replacing installer classes."""

    def __init__(self, profile):
        self.profile = profile
        self.installed_targets = set()

    def on_pacstrap(self, packages):
        """Install the local kernel separately; keep ARM repository signing keys."""
        # Upstream ignores an empty replacement list, so retain a real package
        # even if the only request was the offline Surface kernel placeholder.
        return list(dict.fromkeys([p for p in packages if p not in (KERNEL, "archlinux-keyring")] + ["archlinuxarm-keyring"]))

    def on_mkinitcpio(self, installation):
        """Install the kernel before the first initramfs, then rebuild its preset."""
        if installation.target not in self.installed_targets:
            surface.install_kernel(installation.target, PAYLOAD)
            self.installed_targets.add(installation.target)
        else:
            surface.run("arch-chroot", installation.target, "mkinitcpio", "-p", "lexr-sp11")
        return True

    def on_add_bootloader(self, installation):
        """Supply the arm64-efi target, matching DTB and dedicated Surface entry."""
        if installation.target not in self.installed_targets:
            raise ValueError("The Surface kernel must be installed before GRUB")
        installation.add_additional_packages(["grub", "efibootmgr"])
        surface.install_boot(installation.target, PAYLOAD, self.profile)
        installation._helper_flags["bootloader"] = "grub"
        return True

    def on_genfstab(self, installation):
        """Check the Surface hand-off before the normal completion dialog."""
        surface.verify_install(installation.target, PAYLOAD, self.profile)


def configure_adapter(handler, profile):
    """Register Surface hooks and add compatibility feedback to the stock menu."""
    from archinstall.scripts import guided
    from archinstall.lib.global_menu import GlobalMenu
    from archinstall.lib.bootloader.utils import validate_bootloader_layout
    from archinstall.lib.pacman.pacman import Pacman
    from archinstall.lib.plugins import plugins

    def checked_layout(boot, disk):
        """Use the same pre-confirmation check for interactive and saved configs."""
        failure = validate_bootloader_layout(boot, disk)
        if failure:
            return failure
        try:
            validate_platform(handler.config)
        except ValueError as error:
            return SimpleNamespace(description=str(error))
        return None

    class SurfaceMenu(GlobalMenu):
        """Only kernel and mirror selection differ from the standard guided menu."""

        def _get_menu_options(self):
            items = super()._get_menu_options()
            for item in items:
                if item.key in {"kernels", "mirror_config"}:
                    item.text = "Kernel (Lexr Surface Pro 11)" if item.key == "kernels" else "Mirrors (Arch Linux ARM)"
                    item.action, item.read_only = None, True
                    item.value = getattr(self._arch_config, item.key)
            return items

        def _validate_bootloader(self):
            self.sync_all_to_config()
            if failure := super()._validate_bootloader():
                return failure
            if failure := checked_layout(self._arch_config.bootloader_config, self._arch_config.disk_config):
                return failure.description
            return None

    def arm_keyring():
        """The upstream error recovery hard-codes the x86 Arch signing keyring."""
        surface.run("pacman-key", "--init")
        surface.run("pacman-key", "--populate", "archlinuxarm")

    plugins["lexr-surface"] = SurfacePlugin(profile)
    # There is no upstream configuration-validation or keyring-recovery hook.
    # Keep these small version-tested adaptations; the formatter and Installer
    # classes, account, network and profile handlers remain upstream-owned.
    Pacman._reinit_keyring = staticmethod(arm_keyring)
    guided.GlobalMenu = SurfaceMenu
    guided.validate_bootloader_layout = checked_layout
    guided.check_version_upgrade = lambda: None
    return guided, validate_platform


def main():
    """Start Archinstall with Surface defaults and no preselected desktop."""
    check_arguments(sys.argv[1:])
    if importlib.metadata.version("archinstall") != "4.4":
        raise ValueError("Use this image's bundled Archinstall 4.4; rebuild Lexr to update its reviewed dependency")
    from archinstall.lib.args import ArchConfigHandler
    from archinstall.lib.models.bootloader import Bootloader, BootloaderConfiguration
    from archinstall.lib.models.network import NetworkConfiguration, NicType

    class SurfaceConfigHandler(ArchConfigHandler):
        """Require exact option names for the entry-point check above."""

        def _define_arguments(self):
            parser = super()._define_arguments()
            parser.allow_abbrev = False
            return parser

    handler = SurfaceConfigHandler()
    if os.geteuid() != 0:
        raise ValueError("Run sudo archinstall from the live session")
    args, config = handler.args, handler.config
    if (args.silent and not args.dry_run) or args.skip_boot or args.offline or args.command:
        raise ValueError("Use the interactive online guided flow; --dry-run may use a saved configuration")
    if not args.config:
        config.kernels = [KERNEL]
        config.bootloader_config = BootloaderConfiguration(Bootloader.Grub, uki=False, removable=False)
    if config.network_config is None:
        config.network_config = NetworkConfiguration(NicType.NM)
    servers = [line.strip() for line in Path("/etc/pacman.d/mirrorlist").read_text().splitlines()
               if line.strip() and not line.lstrip().startswith("#")]
    if servers != [MIRRORLIST.strip()]:
        raise ValueError("Restore the image's Arch Linux ARM mirror configuration before installing")
    surface.verify_payload(PAYLOAD)
    profile = surface.detect_profile()
    surface.require_uefi()
    args.skip_wkd, args.skip_version_check, args.skip_wifi_check = True, True, True
    if args.dry_run:
        args.no_pkg_lookups = True
    else:
        surface.run("pacman-key", "--init")
        surface.run("pacman-key", "--populate", "archlinuxarm")
    guided, _ = configure_adapter(handler, profile)
    print("Lexr Surface kernel and GRUB integration. Archinstall controls disk changes; review its confirmation carefully.")
    guided.main(handler)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, KeyError, IndexError, subprocess.CalledProcessError) as error:
        print(f"Lexr Arch installer: {error}", file=sys.stderr)
        sys.exit(1)
