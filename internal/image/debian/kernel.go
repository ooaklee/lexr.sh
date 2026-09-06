// Package debian shares offline package registration for Debian-family images.
package debian

import (
	"context"
	"fmt"

	"github.com/ooaklee/lexr.sh/internal/kernel"
	"github.com/ooaklee/lexr.sh/internal/platform"
)

// InstallKernelPackages registers the exact runtime packages and, only for an
// external-required bundle, its manifest-declared generic boot-support package.
// Kernel hooks are suppressed only while the image and modules are configured;
// the support package is installed after those directories are restored so its
// package-owned hooks remain consistent with dpkg's database.
func InstallKernelPackages(ctx context.Context, docker *platform.Docker, image, workspace, volume string, bundle kernel.Bundle) error {
	imagePackage, ok := bundle.Package(kernel.RoleImage)
	if !ok {
		return errorsForMissingKernelRole(kernel.RoleImage)
	}
	modulesPackage, ok := bundle.Package(kernel.RoleModules)
	if !ok {
		return errorsForMissingKernelRole(kernel.RoleModules)
	}
	supportArchive := ""
	if bundle.EffectiveDTBDelivery == kernel.DTBDeliveryExternalRequired {
		supportPackage, found := bundle.Package(kernel.RoleBootSupport)
		if !found {
			return errorsForMissingKernelRole(kernel.RoleBootSupport)
		}
		supportArchive = "/work/kernel/" + supportPackage.Name
	} else if _, found := bundle.Package(kernel.RoleBootSupport); found {
		return fmt.Errorf("embedded kernel bundle unexpectedly contains a %s package", kernel.RoleBootSupport)
	}

	const script = `root=/linux-work/rootfs
abi=$1
version=$2
modules_archive=$3
image_archive=$4
support_archive=$5
modules_package="linux-modules-$abi"
image_package="linux-image-$abi"
support_package=lexr-kernel-boot-support

verify_archive() {
	archive=$1
	expected_package=$2
	expected_architecture=$3
	actual_package=$(dpkg-deb --field "$archive" Package)
	actual_version=$(dpkg-deb --field "$archive" Version)
	actual_architecture=$(dpkg-deb --field "$archive" Architecture)
	[ "$actual_package" = "$expected_package" ] || {
		echo "kernel archive $archive declares package $actual_package, expected $expected_package" >&2
		exit 65
	}
	[ "$actual_version" = "$version" ] || {
		echo "kernel archive $archive declares version $actual_version, expected $version" >&2
		exit 65
	}
	[ "$actual_architecture" = "$expected_architecture" ] || {
		echo "kernel archive $archive declares architecture $actual_architecture, expected $expected_architecture" >&2
		exit 65
	}
}

verify_archive "$modules_archive" "$modules_package" arm64
verify_archive "$image_archive" "$image_package" arm64
if [ -n "$support_archive" ]; then
	verify_archive "$support_archive" "$support_package" all
fi

backup=/linux-work/dpkg-offline-backup
[ ! -e "$backup" ] || {
	echo "refusing to reuse an existing offline dpkg backup" >&2
	exit 73
}
mkdir -m 0700 "$backup"

restore_hook_directories() {
	for phase in preinst.d postinst.d; do
		if [ -e "$backup/$phase" ]; then
			rm -rf -- "$root/etc/kernel/$phase"
			mv "$backup/$phase" "$root/etc/kernel/$phase"
		elif [ -e "$backup/$phase.absent" ]; then
			rm -rf -- "$root/etc/kernel/$phase"
			rm -f -- "$backup/$phase.absent"
		fi
	done
}

restore_statoverride() {
	if [ -e "$backup/statoverride" ]; then
		rm -f -- "$root/var/lib/dpkg/statoverride"
		mv "$backup/statoverride" "$root/var/lib/dpkg/statoverride"
	elif [ -e "$backup/statoverride.absent" ]; then
		rm -f -- "$root/var/lib/dpkg/statoverride" "$backup/statoverride.absent"
	fi
}

restore_offline_state() {
	status=$?
	restore_hook_directories
	restore_statoverride
	rm -rf -- "$backup"
	trap - EXIT HUP INT TERM
	exit "$status"
}
trap restore_offline_state EXIT HUP INT TERM

for phase in preinst.d postinst.d; do
	if [ -e "$root/etc/kernel/$phase" ]; then
		mv "$root/etc/kernel/$phase" "$backup/$phase"
	else
		: > "$backup/$phase.absent"
	fi
	mkdir -p "$root/etc/kernel/$phase"
done

# A layered Debian-family root may name accounts supplied by upper layers. Hide it only while the selected packages are installed, then
# restore the original bytes even when dpkg or an interrupt stops the build.
if [ -e "$root/var/lib/dpkg/statoverride" ]; then
	mv "$root/var/lib/dpkg/statoverride" "$backup/statoverride"
else
	: > "$backup/statoverride.absent"
fi
: > "$root/var/lib/dpkg/statoverride"

# Offline kernel hooks may expect mounted target devices or create a live
# initramfs. Register the runtime packages first, then restore the real hook
# directories before installing the package which owns Lexr's lifecycle hook.
dpkg --root="$root" --install "$modules_archive" "$image_archive"
restore_hook_directories
if [ -n "$support_archive" ]; then
	dpkg --root="$root" --install "$support_archive"
fi

chroot "$root" dpkg-query --show --showformat='${Package}\t${Version}\t${Architecture}\t${db:Status-Status}\n' \
	"$modules_package" "$image_package" > /linux-work/kernel-package-status
expected_modules=$(printf '%s\t%s\tarm64\tinstalled' "$modules_package" "$version")
expected_image=$(printf '%s\t%s\tarm64\tinstalled' "$image_package" "$version")
grep -Fx "$expected_modules" /linux-work/kernel-package-status >/dev/null
grep -Fx "$expected_image" /linux-work/kernel-package-status >/dev/null
if [ -n "$support_archive" ]; then
	chroot "$root" dpkg-query --show --showformat='${Package}\t${Version}\t${Architecture}\t${db:Status-Status}\n' \
		"$support_package" > /linux-work/boot-support-package-status
	expected_support=$(printf '%s\t%s\tall\tinstalled' "$support_package" "$version")
	grep -Fx "$expected_support" /linux-work/boot-support-package-status >/dev/null
fi

restore_statoverride
rm -rf -- "$backup"
trap - EXIT HUP INT TERM
`
	if err := docker.RunInWorkspaceVolume(ctx, image, workspace, volume,
		"bash", "-ceu", script, "lexr-install-kernel", bundle.ABI, bundle.Version,
		"/work/kernel/"+modulesPackage.Name, "/work/kernel/"+imagePackage.Name, supportArchive); err != nil {
		return fmt.Errorf("register custom kernel packages in remastered root: %w", err)
	}
	return nil
}

// errorsForMissingKernelRole returns a consistent error for an incomplete
// kernel bundle passed to the installed-system hand-off.
func errorsForMissingKernelRole(role kernel.PackageRole) error {
	return fmt.Errorf("kernel bundle has no %s package", role)
}
