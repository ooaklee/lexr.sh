"""Archinstall's guided flow with hooks for the Surface kernel and ARM64 boot.

Archinstall owns partitioning, formatting, accounts, profiles and networking.
This adapter only supplies platform defaults and the Surface boot hand-off.
"""

import importlib.metadata
from enum import Enum
import os
from pathlib import Path
import platform
import re
import subprocess
import sys
from types import SimpleNamespace
from uuid import UUID

import target as surface

PAYLOAD = Path("/usr/share/lexr/arch-media/sp11")
MIRRORLIST = "Server = https://ca.us.mirror.archlinuxarm.org/$arch/$repo\n"
KERNEL = "lexr-kernel-sp11"
ARM64_ROOT_GUID = "b921b045-1df0-41c3-af44-4c6f280d3fae"


class SurfaceGraphicsPackage(Enum):
    Mesa = "mesa"
    Vulkan = "vulkan-freedreno"


class SurfaceGraphics(Enum):
    """Implement the upstream graphics interface for the Surface's Adreno GPU."""

    Mesa = "Qualcomm Adreno (Mesa)"

    @classmethod
    def _missing_(cls, value):
        raise ValueError("This saved graphics choice is unsupported on Surface; select Qualcomm Adreno (Mesa) in a new configuration")

    def gfx_packages(self):
        # Resolve virtual opengl-driver/vulkan-driver dependencies to Adreno
        # providers, rather than pacman's first (potentially NVIDIA) choice.
        return list(SurfaceGraphicsPackage)

    def packages_text(self):
        return "Installed packages: mesa, vulkan-freedreno"

    def is_nvidia_proprietary(self):
        return False


def configure_types():
    """Adapt two constants in the pinned API, retaining its parser/formatter."""
    from archinstall.lib.models import profile
    from archinstall.lib.disk import device_handler
    # Saved configs round-trip the same Mesa choice shown by the menu. PC GPU
    # choices fail enum parsing instead of being silently translated to Mesa.
    profile.GfxDriver = SurfaceGraphics
    # Archinstall 4.4 refers to this x86-named constant when creating a root.
    # Substitute only the formatter's module-local reference, not disk geometry
    # or the global Enum. UUID.bytes is the upstream pyparted representation.
    device_handler.PartitionGUID = SimpleNamespace(LINUX_ROOT_X86_64=UUID(ARM64_ROOT_GUID))


def validate_raw_config(config):
    """Reject executable extensions BEFORE upstream imports a custom profile."""
    if any(config.get(key) for key in ("custom_commands", "script", "plugin", "plugin_url")):
        raise ValueError("Use bundled profiles without custom commands, scripts or plugins in the Surface installer")
    profile = (config.get("profile_config") or {}).get("profile") or {}
    if profile.get("path"):
        raise ValueError("Use a bundled profile; external profile files and URLs are not reviewed for ARM64")


def config_handler_type():
    from archinstall.lib.args import ArchConfigHandler

    class SurfaceConfigHandler(ArchConfigHandler):
        def _define_arguments(self):
            parser = super()._define_arguments()
            parser.allow_abbrev = False
            return parser

        def _parse_config(self):
            config = super()._parse_config()
            validate_raw_config(config)
            return config

    return SurfaceConfigHandler


def validate_repositories():
    """Check effective pacman settings, including Includes and per-repo servers."""
    def query(*args):
        return subprocess.check_output(["pacman-conf", *args], text=True).splitlines()

    if platform.machine() != "aarch64" or query("Architecture") != ["aarch64"]:
        raise ValueError("The Surface installer requires an AArch64 host and AArch64 pacman configuration")
    repos = query("--repo-list")
    if repos != ["core", "extra", "alarm", "aur"] or any(
            query("--repo", repo, "Server") != [f"https://ca.us.mirror.archlinuxarm.org/aarch64/{repo}"]
            for repo in repos):
        raise ValueError("Restore the image's Arch Linux ARM repositories before installing")


def validate_package_names(packages):
    """Reject platform packages even if an ARM repository happens to ship them."""
    from archinstall.lib.hardware import GfxPackage
    from archinstall.lib.models.package_types import Kernel
    blocked = {p.value for p in GfxPackage if p != GfxPackage.Mesa}
    blocked.update(k.value for k in Kernel)
    blocked.update({"linux-aarch64", "linux-aarch64-headers", "intel-ucode", "amd-ucode", "archlinux-keyring"})
    for name in packages:
        if not isinstance(name, str) or not re.fullmatch(r"[a-zA-Z0-9@_+][a-zA-Z0-9@._+\-]*", name):
            raise ValueError(f"Use an ARM repository package name, not a file, option or repository override: {name!r}")
        if name in blocked or name.endswith("-ucode") or name.startswith(("lib32-", "nvidia", "xf86-video-")):
            raise ValueError(f"{name} is outside this Surface image's kernel/graphics support; use the Surface kernel and Mesa")


def check_packages(packages):
    """Resolve with pacman without installing; check dependencies and groups too."""
    packages = sorted(set(packages))
    validate_package_names(packages)
    if not packages:
        return
    result = subprocess.run(["pacman", "--sync", "--print", "--noconfirm", "--print-format", "%n %a", "--", *packages],
                            text=True, capture_output=True)
    if result.returncode:
        raise ValueError("ARM package check failed before installation:\n" + result.stderr.strip())
    if not result.stdout.strip():
        raise ValueError("ARM package check returned no package metadata")
    for line in result.stdout.splitlines():
        fields = line.split()
        if len(fields) != 2 or fields[1] not in {"aarch64", "any"}:
            raise ValueError(f"Unexpected ARM package metadata: {line}")
        validate_package_names([fields[0]])


def profile_packages(config):
    """Read upstream's selected profile data, without duplicating its recipes."""
    from archinstall.default_profiles.profile import CustomSetting
    packages = list(config.packages)
    profile_config = config.profile_config
    if profile_config:
        pending = [profile_config.profile] if profile_config.profile else []
        while pending:
            profile = pending.pop()
            packages.extend(profile.packages)
            pending.extend(profile.current_selection or [])
            # The only bundled profile setting that selects extra packages.
            seat = profile.custom_settings.get(CustomSetting.SeatAccess)
            if seat:
                if seat not in {"seatd", "polkit"}:
                    raise ValueError("Select seatd or polkit for the profile's seat access")
                packages.append(seat)
        if profile_config.gfx_driver:
            packages.extend(p.value for p in profile_config.gfx_driver.gfx_packages())
    return packages


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
    if config.script or config.custom_commands:
        raise ValueError("Use the bundled guided flow without custom commands")
    if config.profile_config and config.profile_config.gfx_driver not in (None, SurfaceGraphics.Mesa):
        raise ValueError("Select Qualcomm Adreno (Mesa) graphics for the Surface Pro 11")
    validate_package_names(profile_packages(config))
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

    def __init__(self, profile, config):
        self.profile = profile
        self.config = config
        self.installed_targets = set()

    def on_pacstrap(self, packages):
        """Install the local kernel separately; keep ARM repository signing keys."""
        # Upstream ignores an empty replacement list, so retain a real package
        # even if the only request was the offline Surface kernel placeholder.
        packages = list(dict.fromkeys([p for p in packages if p not in (KERNEL, "archlinux-keyring")] + ["archlinuxarm-keyring"]))
        if self.config.profile_config and self.config.profile_config.gfx_driver == SurfaceGraphics.Mesa:
            # pacstrap resolves against the target, while preflight uses the
            # live database. Explicit providers keep both resolutions consistent,
            # including early app transactions before upstream's graphics step.
            packages = list(dict.fromkeys(packages + [p.value for p in SurfaceGraphicsPackage]))
        # Guard every transaction, including later application/profile hooks.
        # This is an architecture check, not a promise that downloads cannot fail.
        validate_repositories()
        check_packages(packages)
        return packages

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
    from archinstall.lib.profile.profile_menu import ProfileMenu
    from archinstall.lib.mirror.mirror_handler import MirrorListHandler

    configure_types()

    class SurfaceProfileMenu(ProfileMenu):
        def _define_menu_options(self):
            items = super()._define_menu_options()
            for item in items:
                if item.key == "gfx_driver":
                    item.action, item.read_only = None, True
            return items

        async def _select_profile(self, preset):
            profile = await super()._select_profile(preset)
            self._item_group.find_by_key("gfx_driver").value = (
                SurfaceGraphics.Mesa if profile and profile.is_graphic_driver_supported() else None)
            return profile

    class ARMMirrorListHandler(MirrorListHandler):
        def __init__(self, *args, **kwargs):
            # Keep online package installation, but never query x86 mirror status
            # or run upstream's x86_64 mirror speed test.
            kwargs["offline"] = True
            super().__init__(*args, **kwargs)

    def checked_layout(boot, disk):
        """Use the same pre-confirmation check for interactive and saved configs."""
        failure = validate_bootloader_layout(boot, disk)
        if failure:
            return failure
        try:
            validate_platform(handler.config)
            validate_repositories()
            check_packages(profile_packages(handler.config))
        except ValueError as error:
            return SimpleNamespace(description=str(error))
        return None

    class SurfaceMenu(GlobalMenu):
        """Fix platform choices while retaining upstream installation menus."""

        async def _select_profile(self, current_profile):
            return await SurfaceProfileMenu(preset=current_profile).show()

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

    plugins["lexr-surface"] = SurfacePlugin(profile, handler.config)
    # There is no upstream configuration-validation or keyring-recovery hook.
    # Keep these small version-tested adaptations; the formatter and Installer
    # classes, account, network and profile handlers remain upstream-owned.
    Pacman._reinit_keyring = staticmethod(arm_keyring)
    guided.GlobalMenu = SurfaceMenu
    guided.MirrorListHandler = ARMMirrorListHandler
    guided.validate_bootloader_layout = checked_layout
    guided.check_version_upgrade = lambda: None
    return guided, validate_platform


def main():
    """Start Archinstall with Surface defaults and no preselected desktop."""
    check_arguments(sys.argv[1:])
    if importlib.metadata.version("archinstall") != "4.4":
        raise ValueError("Use this image's bundled Archinstall 4.4; rebuild Lexr to update its reviewed dependency")
    from archinstall.lib.models.bootloader import Bootloader, BootloaderConfiguration
    from archinstall.lib.models.network import NetworkConfiguration, NicType

    if os.geteuid() != 0:
        raise ValueError("Run sudo archinstall from the live session")
    configure_types()
    handler = config_handler_type()()
    args, config = handler.args, handler.config
    if (args.silent and not args.dry_run) or args.skip_boot or args.offline or args.command:
        raise ValueError("Use the interactive online guided flow; --dry-run may use a saved configuration")
    if not args.config and not args.config_url:
        config.kernels = [KERNEL]
        config.bootloader_config = BootloaderConfiguration(Bootloader.Grub, uki=False, removable=False)
    if config.network_config is None:
        config.network_config = NetworkConfiguration(NicType.NM)
    validate_repositories()
    surface.verify_payload(PAYLOAD)
    profile = surface.detect_profile()
    surface.require_uefi()
    args.skip_wkd, args.skip_version_check, args.skip_wifi_check = True, True, True
    if args.dry_run:
        args.no_pkg_lookups = True
    else:
        surface.run("pacman-key", "--init")
        surface.run("pacman-key", "--populate", "archlinuxarm")
        surface.run("pacman", "--sync", "--refresh", "--noconfirm")
    guided, _ = configure_adapter(handler, profile)
    print("Lexr Surface kernel and GRUB integration. Archinstall controls disk changes; review its confirmation carefully.")
    guided.main(handler)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, KeyError, IndexError, subprocess.CalledProcessError) as error:
        print(f"Lexr Arch installer: {error}", file=sys.stderr)
        sys.exit(1)
